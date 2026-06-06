// verify-frontend-urls is the operator-facing tool that probes every
// frontend_url declared in instrument_catalog.yaml and writes a YAML
// report describing reachability per (platform, canonical) entry.
//
// The report is purely informational: it does NOT block the release
// pipeline. Use it to surface broken links so the catalog can be
// patched (or url_verified flipped on) in a follow-up commit.
//
// Probe strategy per URL:
//  1. HEAD with a 5s timeout. Many exchange CDN edges respond to HEAD.
//  2. If HEAD returns >= 400 or errors, fall back to a streaming GET
//     and read the first byte. We do not buffer the whole body so
//     large pages (300KB+ for Binance) finish in ~one RTT.
//
// Honors --proxy for environments behind an HTTP egress proxy.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"edgex-ops-intelligence/backend/internal/config"

	"gopkg.in/yaml.v3"
)

const (
	defaultProbeTimeout = 5 * time.Second
	defaultParallelism  = 4
)

type cliFlags struct {
	catalogPath string
	reportPath  string
	proxy       string
	timeout     time.Duration
	parallelism int
	apply       bool
}

// probeResult is the per-URL record persisted in the report yaml. The
// shape is intentionally narrow and stable so future tooling (e.g. a
// dashboard widget that surfaces stale links) can consume it without
// schema gymnastics.
type probeResult struct {
	Platform   string `yaml:"platform" json:"platform"`
	Canonical  string `yaml:"canonical" json:"canonical"`
	URL        string `yaml:"url" json:"url"`
	OK         bool   `yaml:"ok" json:"ok"`
	Method     string `yaml:"method" json:"method"`
	HTTPStatus int    `yaml:"http_status" json:"http_status"`
	LatencyMS  int64  `yaml:"latency_ms" json:"latency_ms"`
	Error      string `yaml:"error,omitempty" json:"error,omitempty"`
}

type probeReport struct {
	GeneratedAt string        `yaml:"generated_at" json:"generated_at"`
	OKCount     int           `yaml:"ok_count" json:"ok_count"`
	FailCount   int           `yaml:"fail_count" json:"fail_count"`
	TotalCount  int           `yaml:"total_count" json:"total_count"`
	Results     []probeResult `yaml:"results" json:"results"`
}

func main() {
	flags := parseFlags()
	if err := run(flags); err != nil {
		fmt.Fprintln(os.Stderr, "verify-frontend-urls: "+err.Error())
		os.Exit(1)
	}
}

func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.catalogPath, "catalog", "../config/instrument_catalog.yaml", "path to instrument_catalog.yaml")
	flag.StringVar(&f.reportPath, "report", "../config/url_verification_report.yaml", "path to write the verification report")
	flag.StringVar(&f.proxy, "proxy", "", "optional HTTP/HTTPS proxy URL (e.g. http://127.0.0.1:7897)")
	flag.DurationVar(&f.timeout, "timeout", defaultProbeTimeout, "per-URL probe timeout")
	flag.IntVar(&f.parallelism, "parallelism", defaultParallelism, "concurrent probes")
	flag.BoolVar(&f.apply, "apply", false, "when true, also patch url_verified=true into the catalog for entries that responded OK")
	flag.Parse()
	return f
}

func run(f cliFlags) error {
	body, err := os.ReadFile(f.catalogPath)
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}
	var cat config.Catalog
	if err := yaml.Unmarshal(body, &cat); err != nil {
		return fmt.Errorf("unmarshal catalog: %w", err)
	}

	client, err := buildHTTPClient(f.proxy, f.timeout)
	if err != nil {
		return err
	}

	tasks := collectProbeTasks(cat)
	results := probeAll(client, tasks, f.parallelism, f.timeout)

	report := probeReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		TotalCount:  len(results),
		Results:     results,
	}
	for _, r := range results {
		if r.OK {
			report.OKCount++
		} else {
			report.FailCount++
		}
	}
	if err := writeReport(f.reportPath, report); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	log.Printf("probe complete: total=%d ok=%d fail=%d report=%s",
		report.TotalCount, report.OKCount, report.FailCount, f.reportPath)

	if f.apply {
		if err := applyVerified(f.catalogPath, cat, results); err != nil {
			return fmt.Errorf("apply url_verified flags: %w", err)
		}
	}
	return nil
}

func buildHTTPClient(proxy string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("parse --proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout * 2, // belt-and-braces over the per-request ctx
	}, nil
}

type probeTask struct {
	platform  string
	canonical string
	url       string
}

func collectProbeTasks(cat config.Catalog) []probeTask {
	var tasks []probeTask
	for platform, byCanon := range cat.Platforms {
		for canonical, sym := range byCanon {
			if sym.FrontendURL == "" {
				continue
			}
			tasks = append(tasks, probeTask{platform: platform, canonical: canonical, url: sym.FrontendURL})
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].platform != tasks[j].platform {
			return tasks[i].platform < tasks[j].platform
		}
		return tasks[i].canonical < tasks[j].canonical
	})
	return tasks
}

func probeAll(client *http.Client, tasks []probeTask, parallelism int, timeout time.Duration) []probeResult {
	if parallelism <= 0 {
		parallelism = defaultParallelism
	}
	out := make([]probeResult, len(tasks))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, task probeTask) {
			defer wg.Done()
			defer func() { <-sem }()
			out[idx] = probeOne(client, task, timeout)
		}(i, t)
	}
	wg.Wait()
	return out
}

// probeOne issues HEAD then falls back to a streaming GET. The
// fallback is necessary because some CDN/edge configs return 405 or
// 403 on HEAD even when GET works (binance, OKX both have edge cases
// like this).
func probeOne(client *http.Client, t probeTask, timeout time.Duration) probeResult {
	res := probeResult{Platform: t.platform, Canonical: t.canonical, URL: t.url}

	if r := tryProbe(client, http.MethodHead, t.url, timeout, false); r.OK || r.HTTPStatus >= 200 && r.HTTPStatus < 400 {
		res.Method = http.MethodHead
		res.OK = r.OK
		res.HTTPStatus = r.HTTPStatus
		res.LatencyMS = r.LatencyMS
		res.Error = r.Error
		if res.OK {
			return res
		}
	}
	r := tryProbe(client, http.MethodGet, t.url, timeout, true)
	res.Method = http.MethodGet
	res.OK = r.OK
	res.HTTPStatus = r.HTTPStatus
	res.LatencyMS = r.LatencyMS
	if !r.OK {
		res.Error = r.Error
	} else {
		res.Error = ""
	}
	return res
}

type singleProbeResult struct {
	OK         bool
	HTTPStatus int
	LatencyMS  int64
	Error      string
}

func tryProbe(client *http.Client, method, target string, timeout time.Duration, drainFirstByte bool) singleProbeResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return singleProbeResult{Error: err.Error()}
	}
	req.Header.Set("User-Agent", "edgex-ops-intelligence-url-verifier/1.0")
	begin := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return singleProbeResult{Error: err.Error(), LatencyMS: time.Since(begin).Milliseconds()}
	}
	defer resp.Body.Close()
	if drainFirstByte {
		// Read at most one byte so we exercise the response stream
		// without buffering large bodies.
		buf := make([]byte, 1)
		_, _ = io.ReadFull(resp.Body, buf)
	}
	out := singleProbeResult{
		HTTPStatus: resp.StatusCode,
		LatencyMS:  time.Since(begin).Milliseconds(),
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 400,
	}
	if !out.OK {
		out.Error = fmt.Sprintf("status=%d", resp.StatusCode)
	}
	return out
}

func writeReport(path string, r probeReport) error {
	body, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func applyVerified(catalogPath string, cat config.Catalog, results []probeResult) error {
	verified := map[string]bool{}
	for _, r := range results {
		if r.OK {
			verified[r.Platform+"|"+r.Canonical+"|"+r.URL] = true
		}
	}
	patched := 0
	for platform, byCanon := range cat.Platforms {
		for canonical, sym := range byCanon {
			key := platform + "|" + canonical + "|" + sym.FrontendURL
			if verified[key] && !sym.URLVerified {
				sym.URLVerified = true
				cat.Platforms[platform][canonical] = sym
				patched++
			}
		}
	}
	if patched == 0 {
		log.Printf("apply: no entries needed flag flip")
		return nil
	}
	body, err := yaml.Marshal(cat)
	if err != nil {
		return err
	}
	if err := os.WriteFile(catalogPath, body, 0o644); err != nil {
		return err
	}
	log.Printf("apply: flipped url_verified=true on %d entries; rewrote %s", patched, catalogPath)
	return nil
}
