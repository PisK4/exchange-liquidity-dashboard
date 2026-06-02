package listing

import (
	"context"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/config"
)

// edgexListedPlatformName matches the key used in config/listed_universe.yaml
// for the edgeX platform. Listed-universe lookups are by base asset
// (case-insensitive); ListedUniverse handles the upper-casing
// internally so we pass canonical_symbol through as-is.
const edgexListedPlatformName = "edgeX"

// platformDisplayNames maps internal short names to the labels we
// surface on the decision card's Market Status block. PRD §5.2 uses
// "Binance Futures" rather than "binance"; this map is the single
// source of truth so the renderer and the operator-facing log
// messages stay in sync.
var platformDisplayNames = map[string]string{
	"binance":     "Binance Futures",
	"bybit":       "Bybit Linear",
	"okx":         "OKX SWAP",
	"bitget":      "Bitget USDT-Futures",
	"mexc":        "MEXC Contract",
	"hyperliquid": "Hyperliquid Perp",
	"gate":        "Gate Futures",
	"bingx":       "BingX Futures",
	"lighter":     "Lighter",
	"edgeX":       "edgeX",
}

// platformPriority controls the order in which platforms appear on
// the Market Status block. Lower = higher priority. Binance leads
// (PRD §1.1: highest priority listing signal); after the seven CEXs
// come the DEXs. Unknown platforms fall through to a high sentinel
// so they still render but at the bottom.
var platformPriority = map[string]int{
	"binance":     0,
	"bybit":       1,
	"okx":         2,
	"bitget":      3,
	"mexc":        4,
	"hyperliquid": 5,
	"gate":        6,
	"bingx":       7,
	"lighter":     8,
	"edgeX":       99,
}

// statusLabelByEnum maps StatusNormalized + SourceKind to the
// Chinese label rendered on the card. The combination matters because
// "active + api" → "Perp LIVE" but "active + announcement" never
// happens (announcements are always pre_listing-y); keeping the
// matrix explicit makes the locale change reviewable in one place.
func statusLabelByEnum(status, sourceKind string) string {
	switch status {
	case StatusActive:
		return "Perp LIVE"
	case StatusPreListing:
		if sourceKind == "announcement" {
			return "公告刚发布"
		}
		return "pre-listing"
	case StatusPaused:
		return "暂停交易"
	case StatusDelisted:
		return "已下架"
	default:
		return "状态未知"
	}
}

func marketStatusLabel(ms PlatformMarketStatus) string {
	label := statusLabelByEnum(ms.Status, ms.SourceKind)
	if ms.Status != StatusPaused && ms.Status != StatusDelisted {
		return label
	}
	raw := strings.TrimSpace(ms.StatusRaw)
	if raw == "" {
		return label + "（当前状态）"
	}
	return label + "（当前状态 · API: " + raw + "）"
}

// BuildMarketStatusLoader adapts a *Repository into the
// MarketStatusLoader contract. The returned function fans the raw
// repository rows into per-platform PlatformMarketStatus entries:
//   - one entry per (platform); when both an API snapshot and an
//     announcement exist for the same platform the API source wins
//     (it is the stronger evidence) but SourceKind is upgraded to
//     "both" so the renderer can colour the bullet accordingly.
//   - empty input → empty output. The renderer renders a grey
//     fallback bullet in that case.
//   - results sorted by (platformPriority asc, OccurredAt desc).
func BuildMarketStatusLoader(repo *Repository) func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
	if repo == nil {
		return func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
			return nil, nil
		}
	}
	return func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
		raw, err := repo.LoadMarketStatusByCanonical(ctx, canonical)
		if err != nil {
			return nil, err
		}
		return foldMarketStatusRows(raw), nil
	}
}

// foldMarketStatusRows is the pure projection from per-source raw
// rows to per-platform PlatformMarketStatus. Pulled out for testing.
func foldMarketStatusRows(raw []MarketStatusRow) []PlatformMarketStatus {
	type accumulator struct {
		status PlatformMarketStatus
		hasAPI bool
		hasAnn bool
	}
	by := make(map[string]*accumulator, len(raw))
	order := make([]string, 0, len(raw))
	for _, r := range raw {
		acc, ok := by[r.Platform]
		if !ok {
			acc = &accumulator{status: PlatformMarketStatus{
				Platform:    r.Platform,
				DisplayName: displayNameForPlatform(r.Platform),
			}}
			by[r.Platform] = acc
			order = append(order, r.Platform)
		}
		var occurredAt time.Time
		switch r.SourceKind {
		case "api":
			acc.hasAPI = true
			effectiveStatus := r.StatusNormalized
			// Prefer the listing_time_ts when present (true exchange-
			// declared listing moment), otherwise fall back to
			// last_seen_at which is when we last polled the row.
			if r.ListingTimeTS != nil {
				occurredAt = *r.ListingTimeTS
				if r.StatusNormalized == StatusActive && r.ListingTimeTS.After(r.LastSeenAt) {
					effectiveStatus = StatusPreListing
				}
			} else {
				occurredAt = r.LastSeenAt
			}
			if occurredAt.After(acc.status.OccurredAt) || acc.status.OccurredAt.IsZero() {
				acc.status.Status = effectiveStatus
				acc.status.StatusRaw = r.StatusRaw
				acc.status.OccurredAt = occurredAt
			}
		case "announcement":
			acc.hasAnn = true
			if r.PublishedAt != nil {
				occurredAt = *r.PublishedAt
			} else {
				occurredAt = r.LastSeenAt
			}
			// API source wins for the headline status, but the
			// announcement timestamp can still bump OccurredAt if it
			// is fresher and there is no API row yet.
			if !acc.hasAPI {
				acc.status.Status = StatusPreListing
				if occurredAt.After(acc.status.OccurredAt) || acc.status.OccurredAt.IsZero() {
					acc.status.OccurredAt = occurredAt
				}
			}
		}
	}
	out := make([]PlatformMarketStatus, 0, len(order))
	for _, p := range order {
		acc := by[p]
		switch {
		case acc.hasAPI && acc.hasAnn:
			acc.status.SourceKind = "both"
		case acc.hasAPI:
			acc.status.SourceKind = "api"
		case acc.hasAnn:
			acc.status.SourceKind = "announcement"
		}
		acc.status.StatusLabel = marketStatusLabel(acc.status)
		out = append(out, acc.status)
	}
	sortMarketStatuses(out)
	return out
}

func displayNameForPlatform(platform string) string {
	if name, ok := platformDisplayNames[platform]; ok {
		return name
	}
	return platform
}

func priorityForPlatform(platform string) int {
	if p, ok := platformPriority[platform]; ok {
		return p
	}
	return 50
}

func sortMarketStatuses(in []PlatformMarketStatus) {
	// Insertion sort: input is tiny (≤ 10) and we want stable
	// ordering when two platforms share priority.
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 {
			pi := priorityForPlatform(in[j].Platform)
			pj := priorityForPlatform(in[j-1].Platform)
			if pi < pj {
				in[j], in[j-1] = in[j-1], in[j]
				j--
				continue
			}
			if pi == pj && in[j].OccurredAt.After(in[j-1].OccurredAt) {
				in[j], in[j-1] = in[j-1], in[j]
				j--
				continue
			}
			break
		}
	}
}

// BuildEdgexListedLookup adapts a *config.ListedUniverse into the
// EdgexListedLookup contract on DecisionCardEnrichDeps.
//
// Three-state semantics, matching the §"Three-state edgex_listed"
// convention used by the Top30 alert pipeline:
//
//   - universe loaded + edgeX has base assets → returns (IsListed,
//     true). "false" here is meaningful ("definitely not listed").
//   - universe nil or universe.Loaded()==false → returns (false,
//     false). "unknown" so the renderer can degrade gracefully
//     (display "?" instead of a misleading "未上线").
//   - universe loaded but edgeX platform missing → returns (false,
//     false). Same reasoning: we cannot prove the negative so we
//     refuse to assert it.
func BuildEdgexListedLookup(universe *config.ListedUniverse) func(canonical string) (bool, bool) {
	if universe == nil || !universe.Loaded() {
		return func(string) (bool, bool) { return false, false }
	}
	edgexBases := universe.BaseAssets(edgexListedPlatformName)
	if len(edgexBases) == 0 {
		return func(string) (bool, bool) { return false, false }
	}
	return func(canonical string) (bool, bool) {
		return universe.IsListed(edgexListedPlatformName, canonical), true
	}
}

// BuildEdgexListedLookupLoader produces a lookup closure that resolves
// the listed universe on every call (spec §F1). The returned closure
// is safe to keep installed on DecisionCardEnrichDeps forever — each
// candidate enrichment pulls a fresh universe so a refresh job that
// just rewrote runtime yaml becomes visible at the next decision-card
// tick without re-wiring deps.
//
// loader may be nil; in that case the returned closure behaves like
// the legacy BuildEdgexListedLookup(nil) and always reports unknown.
func BuildEdgexListedLookupLoader(loader func() *config.ListedUniverse) func(canonical string) (bool, bool) {
	if loader == nil {
		return func(string) (bool, bool) { return false, false }
	}
	return func(canonical string) (bool, bool) {
		return BuildEdgexListedLookup(loader())(canonical)
	}
}

// PlatformMarketStatus is one entry in the decision card's
// "Market Status" block (PRD §5: 各平台状态/时序). It tells the
// operator at a glance which platforms already have the perp live,
// which only have a fresh announcement, and which are paused or
// unknown.
type PlatformMarketStatus struct {
	// Platform is the internal short name (binance / bybit / okx / ...).
	Platform string
	// DisplayName is the human-friendly label rendered on the card
	// (e.g. "Binance Futures"). When empty the renderer falls back to
	// the capitalised Platform.
	DisplayName string
	// Status is the StatusNormalized enum (active / pre_listing /
	// paused / unknown) — stable across renderer tweaks.
	Status string
	// StatusLabel is the Chinese label rendered to operators
	// ("Perp LIVE" / "公告刚发布" / "pre-listing" / ...). The
	// enrichment layer derives it from Status + SourceKind so the
	// renderer never has to touch the locale map.
	StatusLabel string
	// StatusRaw preserves the exchange-native status value for
	// paused/delisted context labels on operator cards.
	StatusRaw string
	// SourceKind explains where the status came from:
	//   - "api"          → t_listing_instrument_snapshot row
	//   - "announcement" → t_listing_announcement row
	//   - "both"         → both sources agree on the canonical
	SourceKind string
	// OccurredAt is the most recent timestamp we can attribute to
	// this platform's status (listing_time_ts or
	// announcement.published_at, whichever is newer).
	OccurredAt time.Time
}

// DepthEvidence is one platform's depth measurement at one tier.
// The renderer surfaces a single "winning" platform per market kind
// (spot vs perp) so the card stays compact; the enrichment layer
// picks the platform with the largest USD value.
type DepthEvidence struct {
	Platform string
	USDValue float64
	// Tier is the depth tier as a percentage of mid-price, e.g.
	// "2pct" or "5pct". V1 records whatever tier the adapter
	// returned without normalisation; operators read it next to
	// the USD value.
	Tier string
}

// DecisionCardEnrichment is the bundle of extra data assembled by
// EnrichCandidateForDecisionCard right before the card is rendered.
// Every field is best-effort: a failure on one source must not
// poison the rest of the card, so the producer records the failure
// in EnrichErrors and renders the remaining fields normally.
type DecisionCardEnrichment struct {
	// EdgexListed is true when the canonical is already on edgeX
	// (per listed_universe). EdgexListedKnown distinguishes
	// "explicitly false" (do show "未上线") from "unknown / lookup
	// disabled" (do show "?") so the renderer can pick the right
	// label without ambiguity.
	EdgexListed      bool
	EdgexListedKnown bool

	// MarketStatuses is sorted by (priority of source platform,
	// OccurredAt desc). Empty slice → renderer surfaces a single
	// grey bullet "无平台状态记录".
	MarketStatuses []PlatformMarketStatus

	// MarketCapUSD is from CoinGecko /coins/markets. Nil → renderer
	// shows "n/a".
	MarketCapUSD *float64
	// Spot24hVolumeUSD is from the same CoinGecko call.
	Spot24hVolumeUSD *float64
	// CoinGeckoID is the resolved id used for the lookup; surfaced
	// in EnrichErrors when the id resolved but the market call
	// failed, so operators can audit which coin we asked about.
	CoinGeckoID string

	// SpotDepth and PerpDepth point to the largest USD value across
	// adapters for the candidate's canonical. Nil → renderer shows
	// "不可用".
	SpotDepth *DepthEvidence
	PerpDepth *DepthEvidence

	// EnrichErrors lists per-source failures keyed by short tag
	// ("coingecko" / "depth:binance" / "market_status" / ...) so the
	// renderer can surface a compact footer note for transparency.
	EnrichErrors []string
}

// HasMarketStatus reports whether any platform status row was
// successfully enriched. Used by the renderer to decide between the
// per-platform list and the grey fallback bullet.
func (e *DecisionCardEnrichment) HasMarketStatus() bool {
	return e != nil && len(e.MarketStatuses) > 0
}

// DecisionCardEnrichDeps wires the data sources EnrichCandidateForDecisionCard
// consults. Every field is optional — a nil dependency cleanly skips
// that source and records a soft EnrichErrors entry so the rest of
// the card is unaffected. This is what lets C3/C4/C5/C6 land
// independently: each commit injects a non-nil dependency, the rest
// stay nil until their commit lands.
type DecisionCardEnrichDeps struct {
	// Now is injected for deterministic tests; production uses
	// time.Now.UTC.
	Now func() time.Time
	// EdgexListedLookup answers "is `canonical` already on edgeX?"
	// Returns (listed, known). known=false signals "lookup disabled
	// or no data", which the renderer surfaces as "?".
	EdgexListedLookup func(canonical string) (bool, bool)
	// MarketStatusLoader joins t_listing_instrument_snapshot +
	// t_listing_announcement for a canonical and returns the
	// per-platform timeline.
	MarketStatusLoader func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error)
	// CoinGeckoFetcher returns (market_cap_usd, vol_24h_usd, id) for
	// the canonical. id may be empty if the lookup failed before
	// the markets call.
	CoinGeckoFetcher func(ctx context.Context, canonical string) (marketCapUSD, vol24hUSD *float64, cgID string, err error)
	// DepthFetcher returns (spotEvidence, perpEvidence). Each
	// pointer may be nil when no adapter produced a usable depth.
	// Implementation is expected to enforce its own per-platform
	// timeout (3s budget per spec) so EnrichCandidateForDecisionCard
	// does not need to manage it.
	DepthFetcher func(ctx context.Context, canonical string, sourcePlatforms []string) (spot, perp *DepthEvidence, err error)
}

// EnrichCandidateForDecisionCard runs every available enrichment
// source in sequence (none of them are CPU bound, so sequential keeps
// the code simple; the depth fetcher fans out internally). Every
// failure is logged into the returned EnrichErrors slice rather than
// short-circuiting — the goal is to render as much of the card as
// possible even when half the upstream sources are down.
func EnrichCandidateForDecisionCard(ctx context.Context, deps DecisionCardEnrichDeps, c Candidate) DecisionCardEnrichment {
	out := DecisionCardEnrichment{}

	if deps.EdgexListedLookup != nil {
		listed, known := deps.EdgexListedLookup(c.CanonicalSymbol)
		out.EdgexListed = listed
		out.EdgexListedKnown = known
	}

	if deps.MarketStatusLoader != nil {
		statuses, err := deps.MarketStatusLoader(ctx, c.CanonicalSymbol)
		if err != nil {
			out.EnrichErrors = append(out.EnrichErrors, "market_status: "+err.Error())
		} else {
			out.MarketStatuses = statuses
		}
	}

	if deps.CoinGeckoFetcher != nil {
		mcap, vol, cgID, err := deps.CoinGeckoFetcher(ctx, c.CanonicalSymbol)
		out.CoinGeckoID = cgID
		if err != nil {
			out.EnrichErrors = append(out.EnrichErrors, "coingecko: "+err.Error())
		} else {
			out.MarketCapUSD = mcap
			out.Spot24hVolumeUSD = vol
		}
	}

	if deps.DepthFetcher != nil {
		spot, perp, err := deps.DepthFetcher(ctx, c.CanonicalSymbol, c.SourcePlatforms)
		if err != nil {
			out.EnrichErrors = append(out.EnrichErrors, "depth: "+err.Error())
		}
		out.SpotDepth = spot
		out.PerpDepth = perp
	}

	return out
}
