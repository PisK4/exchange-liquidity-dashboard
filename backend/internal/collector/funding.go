package collector

import (
	"math"
	"strings"
)

// Funding rate semantics across the 10 platforms covered by the dashboard.
//
// CoinGecko's /derivatives endpoint exposes one funding_rate field per
// ticker, but does NOT normalise across platforms — each value is reported
// in the platform's native settlement period. Cross-platform comparison
// therefore requires we know each platform's period and re-express every
// rate in a common unit before stacking it on the same axis.
//
// We pick 8h equivalent because:
//   - Most CEX perp products settle every 8h, so 8h is the canonical mental
//     model for operators reading the dashboard.
//   - It avoids inflating the visual magnitude of 1h-settling venues
//     (Hyperliquid / Lighter) which would otherwise look like flatlines on
//     the cross-platform bar chart.
//
// CoinGecko's funding_rate field is already expressed in percent units
// (i.e. 0.003164 means 0.003164% per native period, not 0.3164%). Confirmed
// empirically by sampling BTC perps from all 10 venues on 2026-05-25:
// magnitudes match published exchange ranges only when treated as percent.
//
// Settlement period sources (every constant in fundingPeriodHours must
// point to one of these):
//
//   - Binance / OKX / Bybit / MEXC / BingX  — industry-standard 8h
//   - Bitget / Gate                          — 4h or 8h per contract;
//                                              for V1 default symbols
//                                              (BTC/ETH/SOL) all confirmed
//                                              8h via raw-instruments
//                                              fundInterval / funding_interval
//                                              (see backend/docs/raw-instruments/
//                                              bitget-usdt-futures and
//                                              gate-futures-usdt). Long-tail
//                                              symbols with 4h settlement
//                                              are a follow-up — see TODO
//                                              below for the per-contract
//                                              override path.
//   - Hyperliquid                            — 1h. Source:
//                                              docs.hyperliquid.xyz/trading/funding
//   - Lighter                                — 1h. Source:
//                                              docs.lighter.xyz/trading/funding
//                                              ("Funding payments occur at
//                                              each hour mark")
//   - edgeX V1 / V2                          — 4h. Source: every contract
//                                              in raw-instruments/edgeX-perp-v{1,2}
//                                              carries fundingRateIntervalMin=240.
//
// TODO(long-tail symbols): when the dashboard expands beyond
// BTC/ETH/SOL, look up the per-symbol funding interval from the raw
// instrument catalog for Bitget/Gate contracts (they expose fundInterval
// and funding_interval per symbol). The current V1 platform-level
// constant is correct for the configured V1 symbols and is the simplest
// implementation that does not silently drift for other symbols (we
// detect the override via IsKnownPeriod once that catalog lookup lands).

// fundingPeriodHours maps an internal platform identifier to its native
// funding settlement period in hours. Internal platform names are the
// canonical lower-case identifiers used in runtime.yaml (binance, okx,
// bybit, ...) plus edgeX (case-preserving) as defined in
// config/runtime.yaml.platforms.
var fundingPeriodHours = map[string]int{
	"binance":     8,
	"okx":         8,
	"bybit":       8,
	"bitget":      8,
	"bingx":       8,
	"mexc":        8,
	"gate":        8,
	"hyperliquid": 1,
	"lighter":     1,
	"edgeX":       4,
}

// FundingPeriodHours returns the platform's native funding settlement
// period in hours. Lookup is case-sensitive on the canonical form first
// then falls back to a case-insensitive match so callers that hold an
// upper-cased display name (e.g. "EDGEX") still resolve.
//
// Returns (0, false) for unknown platforms; callers MUST treat the
// missing case as funding_rate_status = unsupported and skip
// normalisation. We deliberately do not return a default 8h: an unknown
// platform usually signals a config drift bug and silently assuming 8h
// would mask it.
func FundingPeriodHours(platform string) (int, bool) {
	if hours, ok := fundingPeriodHours[platform]; ok {
		return hours, true
	}
	trimmed := strings.TrimSpace(platform)
	for k, v := range fundingPeriodHours {
		if strings.EqualFold(k, trimmed) {
			return v, true
		}
	}
	return 0, false
}

// NormalizeTo8h converts a native-period funding rate to its 8-hour
// equivalent. rate is expressed in percent units (1.0 == 1%, matching
// CoinGecko's emitted format). periodHours must be one of {1, 4, 8}; any
// other value returns NaN so callers can detect the misuse explicitly.
//
// For periodHours <= 0 we also return NaN: the only valid invocation path
// runs the value through FundingPeriodHours first, which returns a
// positive integer or signals unsupported.
func NormalizeTo8h(rate float64, periodHours int) float64 {
	if periodHours <= 0 {
		return math.NaN()
	}
	switch periodHours {
	case 1, 4, 8:
		return rate * float64(8) / float64(periodHours)
	default:
		return math.NaN()
	}
}

// fundingSanityThreshold8h is the maximum absolute 8h-equivalent funding
// rate we treat as physically plausible. 0.5% per 8h ≈ 5500% APR which is
// already an order of magnitude above the worst legitimate readings ever
// observed on the listed venues; anything above is overwhelmingly likely
// to be a CoinGecko unit drift (e.g. one venue switching from percent to
// decimal in an upstream adapter release).
//
// This is intentionally LOOSE rather than tight: clamping at the worst
// real observation (~0.01% per 8h ≈ 100% APR) would cause an honest
// volatile-market reading to be discarded as noise. The threshold's job is
// to catch obvious unit / sign errors, not to enforce a business rule.
const fundingSanityThreshold8h = 0.5

// SanityCheckRate8h returns true iff the 8h-equivalent rate falls within
// the plausibility band (±fundingSanityThreshold8h). NaN / Inf values fail
// the check so a normalisation miss cannot accidentally write a poisonous
// value into the funding sample store.
func SanityCheckRate8h(rate8h float64) bool {
	if math.IsNaN(rate8h) || math.IsInf(rate8h, 0) {
		return false
	}
	if math.Abs(rate8h) > fundingSanityThreshold8h {
		return false
	}
	return true
}

// IsKnownPeriod is a convenience wrapper around FundingPeriodHours for the
// common case where the caller only needs the boolean. Equivalent to
// `_, ok := FundingPeriodHours(platform); ok`.
func IsKnownPeriod(platform string) bool {
	_, ok := FundingPeriodHours(platform)
	return ok
}
