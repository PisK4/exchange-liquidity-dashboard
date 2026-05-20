// build-catalog crawls every supported exchange's public catalog endpoint,
// writes raw per-market dumps to backend/docs/raw-instruments/<platform>-<market>/<YYYY-MM-DD>.json,
// and emits a filtered config/instrument_catalog.yaml with one entry per
// (platform, canonical) in symbol_mapping.yaml.
//
// Designed to be run by hand on a monthly cadence:
//
//	cd backend && make catalog
//
// CI may also call it in dry-run mode to detect drift:
//
//	cd backend && make catalog-diff
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/adapter"
	"edgex-dashboard/backend/internal/config"

	"gopkg.in/yaml.v3"
)

type cliFlags struct {
	whitelistPath string
	catalogPath   string
	rawDir        string
	dryRun        bool
	diffAgainst   string
	timeout       time.Duration
	platforms     string
	allowPartial  bool
	rawOnly       bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "build-catalog: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var f cliFlags
	flag.StringVar(&f.whitelistPath, "whitelist", "../config/symbol_mapping.yaml", "path to symbol_mapping.yaml (canonical whitelist)")
	flag.StringVar(&f.catalogPath, "output", "../config/instrument_catalog.yaml", "path to write instrument_catalog.yaml")
	flag.StringVar(&f.rawDir, "raw-dir", "docs/raw-instruments", "directory to write per-market raw json dumps under")
	flag.BoolVar(&f.dryRun, "dry-run", false, "do not write files; print summary to stdout (and diff if --diff-against)")
	flag.StringVar(&f.diffAgainst, "diff-against", "", "in dry-run, exit non-zero if generated catalog differs from this file")
	flag.DurationVar(&f.timeout, "timeout", 60*time.Second, "per-platform fetch timeout")
	flag.StringVar(&f.platforms, "platforms", "binance,okx,bybit,bitget,bingx,mexc,gate,hyperliquid,lighter,edgeX", "comma-separated platform list")
	flag.BoolVar(&f.allowPartial, "allow-partial", false, "write catalog yaml even if some platforms failed (default: refuse, to avoid silently shrinking the catalog)")
	flag.BoolVar(&f.rawOnly, "raw-only", false, "write raw dumps only; do not touch instrument_catalog.yaml (useful for partial-network audit refreshes)")
	flag.Parse()

	wl, err := loadSymbolWhitelist(f.whitelistPath)
	if err != nil {
		return fmt.Errorf("load whitelist: %w", err)
	}
	if len(wl.symbols) == 0 {
		return fmt.Errorf("whitelist %s has no symbols", f.whitelistPath)
	}

	now := time.Now().UTC()
	dateStamp := now.Format("2006-01-02")

	results := make(map[string]adapter.CatalogResult)
	var errs []string
	for _, platform := range strings.Split(f.platforms, ",") {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		ad := adapter.New(platform, f.timeout)
		ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
		res, err := ad.FetchInstruments(ctx)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", platform, err))
			continue
		}
		results[platform] = res
	}

	catalog := buildCatalog(now, wl, results)

	if f.dryRun {
		buf, err := yaml.Marshal(catalog)
		if err != nil {
			return err
		}
		if f.diffAgainst != "" {
			existing, err := os.ReadFile(f.diffAgainst)
			if err != nil {
				return fmt.Errorf("read diff-against: %w", err)
			}
			if shouldSkipCatalogDiff(errs) {
				fmt.Fprintln(os.Stderr, "warnings (dry-run, not fatal):")
				for _, e := range errs {
					fmt.Fprintln(os.Stderr, "  "+e)
				}
				fmt.Fprintln(os.Stderr, "catalog diff skipped because dry-run catalog is partial")
			} else {
				diff, equal := compareYAMLIgnoringTimestamp(existing, buf)
				if !equal {
					fmt.Fprintln(os.Stderr, diff)
					return fmt.Errorf("catalog drift detected vs %s", f.diffAgainst)
				}
				fmt.Println("catalog has no semantic drift vs", f.diffAgainst)
			}
		} else {
			os.Stdout.Write(buf)
		}
		if len(errs) > 0 && f.diffAgainst == "" {
			fmt.Fprintln(os.Stderr, "warnings (dry-run, not fatal):")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "  "+e)
			}
		}
		return nil
	}

	// Non-dry-run path. Raw dumps for successful platforms are written
	// first; catalog yaml writing is gated on either full coverage or an
	// explicit --allow-partial. --raw-only skips catalog writing entirely.
	if err := writeRawDumps(f.rawDir, dateStamp, results); err != nil {
		return fmt.Errorf("write raw dumps: %w", err)
	}
	fmt.Printf("wrote raw dumps under %s/<platform>-<market>/%s.json (%d platform(s) succeeded)\n",
		f.rawDir, dateStamp, len(results))

	if f.rawOnly {
		if len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "warnings (--raw-only, catalog yaml left untouched):")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "  "+e)
			}
		}
		return nil
	}

	if len(errs) > 0 && !f.allowPartial {
		fmt.Fprintln(os.Stderr, "refusing to write catalog yaml because the following platforms failed:")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  "+e)
		}
		fmt.Fprintln(os.Stderr, "rerun with --allow-partial to overwrite catalog yaml anyway, --raw-only to keep yaml intact, or fix connectivity and retry")
		return fmt.Errorf("%d platform(s) failed; catalog not written", len(errs))
	}
	if err := writeCatalogYAML(f.catalogPath, catalog); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	fmt.Printf("wrote %s (%d platforms, %d entries)\n",
		f.catalogPath, len(catalog.Platforms), countCatalogEntries(catalog))
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "warnings (catalog written with --allow-partial):")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  "+e)
		}
	}
	return nil
}

func shouldSkipCatalogDiff(errs []string) bool {
	return len(errs) > 0
}

type symbolWhitelist struct {
	symbols   []whitelistEntry
	platforms []string
}

type whitelistEntry struct {
	displaySymbol string
	canonical     string
	marketSurface string
}

type whitelistFile struct {
	Symbols []struct {
		DisplaySymbol string `yaml:"display_symbol"`
		Canonical     string `yaml:"canonical"`
		MarketSurface string `yaml:"market_surface"`
	} `yaml:"symbols"`
	Platforms []string `yaml:"platforms"`
}

func loadSymbolWhitelist(path string) (symbolWhitelist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return symbolWhitelist{}, err
	}
	var wf whitelistFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return symbolWhitelist{}, err
	}
	wl := symbolWhitelist{platforms: wf.Platforms}
	for _, s := range wf.Symbols {
		wl.symbols = append(wl.symbols, whitelistEntry{
			displaySymbol: s.DisplaySymbol,
			canonical:     s.Canonical,
			marketSurface: s.MarketSurface,
		})
	}
	return wl, nil
}

func writeRawDumps(rootDir, dateStamp string, results map[string]adapter.CatalogResult) error {
	for platform, res := range results {
		for _, m := range res.Markets {
			dir := filepath.Join(rootDir, platform+"-"+m.MarketType)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path := filepath.Join(dir, dateStamp+".json")
			if err := os.WriteFile(path, []byte(m.RawJSON), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildCatalog(now time.Time, wl symbolWhitelist, results map[string]adapter.CatalogResult) config.Catalog {
	cat := config.Catalog{
		SchemaVersion: 1,
		GeneratedAt:   now.Format(time.RFC3339),
		GeneratedBy:   "backend/scripts/build-catalog",
		Platforms:     map[string]map[string]config.CatalogSymbol{},
	}
	for _, s := range wl.symbols {
		cat.CanonicalWhitelist = append(cat.CanonicalWhitelist, config.CatalogWhitelistEntry{
			Canonical:     s.canonical,
			MarketSurface: s.marketSurface,
			Quote:         "USDT",
			Confidence:    "confirmed",
		})
	}

	for _, platform := range wl.platforms {
		res, ok := results[platform]
		if !ok {
			continue
		}
		marketType := perpMarketTypeFor(platform)
		var market adapter.MarketDump
		found := false
		for _, m := range res.Markets {
			if m.MarketType == marketType {
				market = m
				found = true
				break
			}
		}
		if !found {
			continue
		}
		expectedQuote := expectedQuoteFor(platform)
		platformMap := map[string]config.CatalogSymbol{}
		for _, s := range wl.symbols {
			inst, ok := matchInstrument(market.Instruments, s.canonical, expectedQuote, platform)
			if !ok {
				continue
			}
			url, _ := frontendURL(platform, marketType, inst.BaseAsset, inst.QuoteAsset, inst.APISymbol)
			sym := config.CatalogSymbol{
				APISymbol:        inst.APISymbol,
				BaseAsset:        inst.BaseAsset,
				QuoteAsset:       inst.QuoteAsset,
				SettleAsset:      defaultSettle(inst, expectedQuote),
				APILevelCap:      apiLevelCapDefault(platform),
				ContractID:       inst.ContractID,
				ContractSize:     inst.ContractSize,
				QuantoMultiplier: inst.QuantoMultiplier,
				SourceEndpoint:   sourceEndpointFor(platform),
				CatalogStatus:    inst.Status,
				FrontendURL:      url,
				URLVerified:      false,
			}
			if platform == "lighter" {
				v := inst.MarketID
				sym.MarketID = &v
			}
			platformMap[s.canonical] = sym
		}
		if len(platformMap) > 0 {
			cat.Platforms[platform] = platformMap
		}
	}
	return cat
}

// matchInstrument finds the canonical instrument in a market's parsed
// instrument list. We use base==canonical && quote==expectedQuote; ties broken
// by preferring the shortest api_symbol (e.g. "BTCUSDT" over "BTCUSDT_240329").
func matchInstrument(insts []adapter.Instrument, canonical, expectedQuote, platform string) (adapter.Instrument, bool) {
	candidates := make([]adapter.Instrument, 0, 4)
	for _, inst := range insts {
		if !strings.EqualFold(inst.BaseAsset, canonical) {
			continue
		}
		if expectedQuote != "" && !strings.EqualFold(inst.QuoteAsset, expectedQuote) {
			continue
		}
		if isExpiringContract(inst.APISymbol) {
			continue
		}
		candidates = append(candidates, inst)
	}
	if len(candidates) == 0 {
		return adapter.Instrument{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i].APISymbol) < len(candidates[j].APISymbol)
	})
	return candidates[0], true
}

// isExpiringContract heuristic skips obvious dated futures symbols like
// BTCUSDT_240329, BTC-USDT-241227. Pure perp symbols never contain dates.
func isExpiringContract(sym string) bool {
	for _, sep := range []string{"_", "-"} {
		parts := strings.Split(sym, sep)
		if len(parts) < 2 {
			continue
		}
		last := parts[len(parts)-1]
		if len(last) == 6 || len(last) == 8 {
			allDigits := true
			for _, r := range last {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return true
			}
		}
	}
	return false
}

func defaultSettle(inst adapter.Instrument, expectedQuote string) string {
	if inst.SettleAsset != "" {
		return inst.SettleAsset
	}
	return expectedQuote
}

func sourceEndpointFor(platform string) string {
	switch platform {
	case "binance":
		return "https://fapi.binance.com/fapi/v1/depth"
	case "okx":
		return "https://www.okx.com/api/v5/market/books-full"
	case "bybit":
		return "https://api.bybit.com/v5/market/orderbook"
	case "bitget":
		return "https://api.bitget.com/api/v2/mix/market/orderbook"
	case "bingx":
		return "https://open-api.bingx.com/openApi/swap/v2/quote/depth"
	case "mexc":
		return "https://contract.mexc.com/api/v1/contract/depth"
	case "gate":
		return "https://api.gateio.ws/api/v4/futures/usdt/order_book"
	case "hyperliquid":
		return "https://api.hyperliquid.xyz/info"
	case "edgeX":
		return "https://pro.edgex.exchange"
	case "lighter":
		return "https://mainnet.zklighter.elliot.ai/api/v1/orderBooks"
	}
	return ""
}

func writeCatalogYAML(path string, cat config.Catalog) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".instrument_catalog.*.yaml")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	header := "# instrument_catalog.yaml\n" +
		"# DO NOT edit api_symbol / contract_id / market_id / contract_size /\n" +
		"# quanto_multiplier by hand. Regenerate via: cd backend && make catalog\n" +
		"# Front-end URLs (frontend_url) and url_verified flag MAY be edited by hand\n" +
		"# after clicking through each link to confirm the trading pair exists.\n"
	if _, err := io.WriteString(tmp, header); err != nil {
		return err
	}
	enc := yaml.NewEncoder(tmp)
	enc.SetIndent(2)
	if err := enc.Encode(cat); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func countCatalogEntries(cat config.Catalog) int {
	n := 0
	for _, m := range cat.Platforms {
		n += len(m)
	}
	return n
}

// compareYAMLIgnoringTimestamp returns (humanReadableDiff, equal) — equality
// is decided after stripping `generated_at:` lines so monthly regeneration
// doesn't trip CI on the timestamp alone.
func compareYAMLIgnoringTimestamp(a, b []byte) (string, bool) {
	stripA, errA := normalizeCatalogYAML(a)
	stripB, errB := normalizeCatalogYAML(b)
	if errA != nil || errB != nil {
		stripA = stripGeneratedAt(string(a))
		stripB = stripGeneratedAt(string(b))
	}
	if stripA == stripB {
		return "", true
	}
	var diff strings.Builder
	diff.WriteString("catalog YAML differs (excluding generated_at):\n")
	for i, line := range linesOf(stripA) {
		other := ""
		if i < len(linesOf(stripB)) {
			other = linesOf(stripB)[i]
		}
		if line != other {
			fmt.Fprintf(&diff, "  L%d: have %q\n        want %q\n", i+1, other, line)
		}
	}
	return diff.String(), false
}

func normalizeCatalogYAML(raw []byte) (string, error) {
	var parsed any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	removeMapKey(parsed, "generated_at")
	if !reflect.ValueOf(parsed).IsValid() {
		return "", nil
	}
	normalized, err := yaml.Marshal(parsed)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func removeMapKey(value any, key string) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, key)
		for _, child := range typed {
			removeMapKey(child, key)
		}
	case []any:
		for _, child := range typed {
			removeMapKey(child, key)
		}
	}
}

func stripGeneratedAt(s string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "generated_at:") {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func linesOf(s string) []string { return strings.Split(s, "\n") }
