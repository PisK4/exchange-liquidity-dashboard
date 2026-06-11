package listing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DepthMarketKind narrows the two depth varieties the decision card
// surfaces: spot vs perp. PRD §5.2 calls them out as two separate
// rows ("现货深度" and "合约深度"); we keep the same axis on the
// inner aggregator so the renderer can pick its winner per kind.
type DepthMarketKind string

const (
	DepthKindSpot DepthMarketKind = "spot"
	DepthKindPerp DepthMarketKind = "perp"
)

// PlatformDepthFetcher fetches the depth USD value at the
// canonical's "2%" tier (or whatever tier the production fetcher
// considers the right reference) for one (platform, kind) pair.
//
// Returning (value, tier, nil) signals success; tier is a free-form
// string the renderer surfaces alongside the USD figure
// (e.g. "2pct" / "5pct"). Returning ErrDepthUnavailable signals
// "this combination is unsupported / no data" — the aggregator
// silently drops it rather than recording an error. Any other error
// is collected and surfaced on EnrichErrors so operators can see why
// a row dropped out.
type PlatformDepthFetcher func(ctx context.Context, platform, canonical string, kind DepthMarketKind) (usd float64, tier string, err error)

// ErrDepthUnavailable is the sentinel error PlatformDepthFetcher
// returns when a (platform, kind) combination has no usable snapshot
// (e.g. the platform never supports spot trading, or the canonical
// has no instrument row on that platform yet). The aggregator
// suppresses these so they do not pollute EnrichErrors with noise.
var ErrDepthUnavailable = errors.New("depth unavailable")

// BuildDepthFetcher returns a DepthFetcher closure satisfying
// DecisionCardEnrichDeps.DepthFetcher. It fans `sources` out across
// PlatformDepthFetcher calls in parallel, with an overall deadline
// budget, and picks the (platform) with the largest USD value per
// market kind.
//
// Per-platform timeouts: each platform gets its own derived context
// with `perCallTimeout` deadline so a single slow adapter cannot eat
// the whole 3s budget. When perCallTimeout <= 0 the aggregator falls
// back to `overallBudget` for each call. When overallBudget <= 0 the
// caller is expected to provide a non-nil parent context whose
// deadline governs the whole call.
func BuildDepthFetcher(fetcher PlatformDepthFetcher, overallBudget, perCallTimeout time.Duration) func(ctx context.Context, canonical string, sources []string) (*DepthEvidence, *DepthEvidence, error) {
	if fetcher == nil {
		return func(context.Context, string, []string) (*DepthEvidence, *DepthEvidence, error) {
			return nil, nil, errors.New("depth fetcher not configured")
		}
	}
	return func(ctx context.Context, canonical string, sources []string) (*DepthEvidence, *DepthEvidence, error) {
		if canonical == "" {
			return nil, nil, errors.New("canonical required")
		}
		if len(sources) == 0 {
			return nil, nil, nil
		}
		// Stable ordering keeps tests deterministic when two
		// platforms tie on USD value.
		platforms := append([]string(nil), sources...)
		sort.Strings(platforms)

		if overallBudget > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, overallBudget)
			defer cancel()
		}

		type result struct {
			platform string
			kind     DepthMarketKind
			usd      float64
			tier     string
			err      error
		}

		jobs := make([]struct {
			platform string
			kind     DepthMarketKind
		}, 0, len(platforms)*2)
		for _, p := range platforms {
			for _, k := range []DepthMarketKind{DepthKindSpot, DepthKindPerp} {
				jobs = append(jobs, struct {
					platform string
					kind     DepthMarketKind
				}{p, k})
			}
		}
		out := make(chan result, len(jobs))
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Add(1)
			go func(platform string, kind DepthMarketKind) {
				defer wg.Done()
				callCtx := ctx
				if perCallTimeout > 0 {
					var cancel context.CancelFunc
					callCtx, cancel = context.WithTimeout(ctx, perCallTimeout)
					defer cancel()
				}
				usd, tier, err := fetcher(callCtx, platform, canonical, kind)
				out <- result{platform: platform, kind: kind, usd: usd, tier: tier, err: err}
			}(j.platform, j.kind)
		}
		go func() { wg.Wait(); close(out) }()

		var (
			spotBest *DepthEvidence
			perpBest *DepthEvidence
			errs     []string
		)
		for r := range out {
			if r.err != nil {
				if errors.Is(r.err, ErrDepthUnavailable) {
					continue
				}
				errs = append(errs, fmt.Sprintf("%s/%s: %v", r.platform, r.kind, r.err))
				continue
			}
			if r.usd <= 0 {
				continue
			}
			ev := DepthEvidence{Platform: r.platform, USDValue: r.usd, Tier: r.tier}
			switch r.kind {
			case DepthKindSpot:
				if spotBest == nil || ev.USDValue > spotBest.USDValue {
					tmp := ev
					spotBest = &tmp
				}
			case DepthKindPerp:
				if perpBest == nil || ev.USDValue > perpBest.USDValue {
					tmp := ev
					perpBest = &tmp
				}
			}
		}

		var aggregateErr error
		if len(errs) > 0 {
			aggregateErr = errors.New(joinStrings(errs, "; "))
		}
		return spotBest, perpBest, aggregateErr
	}
}

// BuildReferenceDepthFetcher pins decision-card depth checks to an explicit
// reference-venue set instead of reusing the candidate's source platforms.
// A Bybit-only listing signal can still be evaluated against Binance (or any
// configured reference venue), which is the operator-facing metric contract for
// the compact card rows.
func BuildReferenceDepthFetcher(fetcher PlatformDepthFetcher, referencePlatforms []string, overallBudget, perCallTimeout time.Duration) func(ctx context.Context, canonical string, sources []string) (*DepthEvidence, *DepthEvidence, error) {
	base := BuildDepthFetcher(fetcher, overallBudget, perCallTimeout)
	refs := normalizeDepthReferencePlatforms(referencePlatforms)
	return func(ctx context.Context, canonical string, _ []string) (*DepthEvidence, *DepthEvidence, error) {
		if len(refs) == 0 {
			return nil, nil, errors.New("depth reference platforms not configured")
		}
		spot, perp, err := base(ctx, canonical, append([]string(nil), refs...))
		stampDepthSource(spot, DecisionCardMetricSourceLiveReference)
		stampDepthSource(perp, DecisionCardMetricSourceLiveReference)
		return spot, perp, err
	}
}

func normalizeDepthReferencePlatforms(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func joinStrings(in []string, sep string) string {
	if len(in) == 0 {
		return ""
	}
	out := in[0]
	for _, s := range in[1:] {
		out += sep + s
	}
	return out
}
