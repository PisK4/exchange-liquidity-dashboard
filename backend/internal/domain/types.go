package domain

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	StatusComplete            = "complete"
	StatusPartial             = "partial"
	StatusAggregatedOrderbook = "aggregated_orderbook"
	StatusWSLimitedDepth      = "ws_limited_depth"
	StatusStale               = "stale"
	StatusUnsupported         = "unsupported"
	StatusError               = "error"
	StatusInsufficientHistory = "insufficient_history"

	// HistoryInsufficient is a legacy alias kept for backwards compatibility.
	// New code should prefer StatusInsufficientHistory.
	HistoryInsufficient = StatusInsufficientHistory

	ReasonAPILevelCap = "api_level_cap"
	ReasonSparseBook  = "sparse_book"
	ReasonUnknown     = "unknown"

	ReasonFeedTruncation         = "feed_truncation"
	ReasonMaxPrecisionShortfall  = "max_precision_shortfall"
	ReasonMonotonicityLowerBound = "monotonicity_lower_bound"

	PolicyRawStrict          = "raw_strict"
	PolicyAggregatedStrict   = "aggregated_strict"
	PolicyLooseLowerBound    = "loose_lower_bound"
	PolicyLooseGroupedApprox = "loose_grouped_approx"

	FreshnessLive    = "live"
	FreshnessDelayed = "delayed"

	SourceRawOrderbook        = "raw_orderbook"
	SourceAggregatedOrderbook = "aggregated_orderbook"
	SourceWSLocalBook         = "ws_local_book"
	SourceWSLimitedDepth      = "ws_limited_depth"

	DataSourceCoinGecko         = "coingecko"
	DataSourceCoinGeckoBackfill = "coingecko_backfill"
	DataSourceNative            = "native"
	DataSourceNativeBackfill    = "native_backfill"
)

type SymbolSub struct {
	DisplaySymbol  string `json:"display_symbol"`
	DisplayName    string `json:"display_name"`
	Canonical      string `json:"canonical"`
	AssetCategory  string `json:"asset_category"`
	MarketSurface  string `json:"market_surface"`
	InstrumentKind string `json:"instrument_kind"`
	Lineage        string `json:"lineage,omitempty"`
	Platform       string `json:"platform"`
	APISymbol      string `json:"api_symbol"`
	BaseAsset      string `json:"base_asset"`
	QuoteAsset     string `json:"quote_asset"`
	SettleAsset    string `json:"settle_asset"`
	SourceEndpoint string `json:"source_endpoint"`

	ContractID       string  `json:"contract_id,omitempty"`
	MarketID         *int    `json:"market_id,omitempty"`
	ContractSize     float64 `json:"contract_size,omitempty"`
	QuantoMultiplier float64 `json:"quanto_multiplier,omitempty"`
	APILevelCap      int     `json:"api_level_cap,omitempty"`
	FrontendURL      string  `json:"frontend_url,omitempty"`
	URLVerified      bool    `json:"url_verified,omitempty"`
	CatalogStatus    string  `json:"catalog_status,omitempty"`
}

const (
	AssetCategoryCrypto    = "crypto"
	AssetCategoryCommodity = "commodity"
	AssetCategoryStock     = "stock"
	AssetCategoryIndexETF  = "index_etf"
)

// DefaultDisplayName composes the canonical-USD display label used across the
// UI when symbol_mapping.yaml does not specify display_name explicitly.
func DefaultDisplayName(canonical string) string {
	if canonical == "" {
		return ""
	}
	return canonical + "-USD"
}

type Level struct {
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
}

type BookView struct {
	SourceID             string            `json:"source_id"`
	Source               string            `json:"depth_source"`
	SourceEndpoint       string            `json:"source_endpoint"`
	Bids                 []Level           `json:"bids,omitempty"`
	Asks                 []Level           `json:"asks,omitempty"`
	SequenceID           uint64            `json:"sequence_id,omitempty"`
	SnapshotTS           time.Time         `json:"snapshot_ts"`
	ReceivedTS           time.Time         `json:"received_ts,omitempty"`
	AggregationParams    map[string]string `json:"aggregation_params,omitempty"`
	APILevelCap          int               `json:"api_level_cap,omitempty"`
	StepUSD              float64           `json:"step_usd,omitempty"`
	ResolutionPct        float64           `json:"resolution_pct,omitempty"`
	UnofficialUIEndpoint bool              `json:"unofficial_ui_endpoint,omitempty"`
	PolicyAcceptance     string            `json:"policy_acceptance,omitempty"`
}

type OrderBookSnapshot struct {
	Platform           string              `json:"platform"`
	DisplaySymbol      string              `json:"display_symbol"`
	SourceEndpoint     string              `json:"source_endpoint"`
	SnapshotTS         time.Time           `json:"snapshot_ts"`
	Bids               []Level             `json:"bids,omitempty"`
	Asks               []Level             `json:"asks,omitempty"`
	DepthStatus        string              `json:"depth_status"`
	PartialReason      string              `json:"partial_reason,omitempty"`
	Error              string              `json:"error,omitempty"`
	LevelsReturned     int                 `json:"levels_returned"`
	BidLevelsReturned  int                 `json:"bid_levels_returned,omitempty"`
	AskLevelsReturned  int                 `json:"ask_levels_returned,omitempty"`
	APILevelCap        int                 `json:"api_level_cap"`
	FarthestDistancePC float64             `json:"farthest_distance_pct"`
	FarthestBidPct     float64             `json:"farthest_bid_pct,omitempty"`
	FarthestAskPct     float64             `json:"farthest_ask_pct,omitempty"`
	DepthSource        string              `json:"depth_source,omitempty"`
	SourceID           string              `json:"source_id,omitempty"`
	SourceBooks        map[string]BookView `json:"-"`
}

type TierDepthMetrics struct {
	BidUSD               float64           `json:"bid_usd"`
	AskUSD               float64           `json:"ask_usd"`
	TotalUSD             float64           `json:"total_usd"`
	DepthStatus          string            `json:"depth_status,omitempty"`
	PartialReason        string            `json:"partial_reason,omitempty"`
	DepthSource          string            `json:"depth_source,omitempty"`
	SourceID             string            `json:"source_id,omitempty"`
	SourceEndpoint       string            `json:"source_endpoint,omitempty"`
	LevelsReturned       int               `json:"levels_returned,omitempty"`
	BidLevelsReturned    int               `json:"bid_levels_returned,omitempty"`
	AskLevelsReturned    int               `json:"ask_levels_returned,omitempty"`
	APILevelCap          int               `json:"api_level_cap,omitempty"`
	FarthestBidPct       float64           `json:"farthest_bid_pct,omitempty"`
	FarthestAskPct       float64           `json:"farthest_ask_pct,omitempty"`
	FarthestDistancePct  float64           `json:"farthest_distance_pct,omitempty"`
	AggregationParams    map[string]string `json:"aggregation_params,omitempty"`
	StrictComplete       bool              `json:"strict_complete"`
	DisplayAvailable     bool              `json:"display_available"`
	PolicyAcceptance     string            `json:"policy_acceptance,omitempty"`
	PhysicalLimit        bool              `json:"physical_limit,omitempty"`
	UnofficialUIEndpoint bool              `json:"unofficial_ui_endpoint,omitempty"`
}

type DepthMetrics = TierDepthMetrics

type PlatformSnapshot struct {
	Platform             string                  `json:"platform"`
	DisplaySymbol        string                  `json:"display_symbol"`
	SnapshotTS           time.Time               `json:"snapshot_ts"`
	SourceEndpoint       string                  `json:"source_endpoint"`
	DepthStatus          string                  `json:"depth_status"`
	PartialReason        string                  `json:"partial_reason,omitempty"`
	Error                string                  `json:"error,omitempty"`
	DataFreshness        string                  `json:"data_freshness,omitempty"`
	LastCollectionStatus string                  `json:"last_collection_status,omitempty"`
	LastCollectionError  string                  `json:"last_collection_error,omitempty"`
	LastCollectionTS     *time.Time              `json:"last_collection_ts,omitempty"`
	MidPrice             float64                 `json:"mid_price,omitempty"`
	SpreadBP             float64                 `json:"spread_bp,omitempty"`
	Imbalance            float64                 `json:"imbalance_pct,omitempty"`
	DepthByTier          map[string]DepthMetrics `json:"depth_by_tier"`
	VsMedianByTier       map[string]float64      `json:"vs_median_by_tier,omitempty"`
	Rank01               int                     `json:"rank_0_1,omitempty"`
	DepthStatusLabel     string                  `json:"depth_status_label,omitempty"`
	BuySlippageBP        map[string]float64      `json:"buy_slippage_bp"`
	SellSlippageBP       map[string]float64      `json:"sell_slippage_bp"`
	WorstSlippageBP      map[string]float64      `json:"worst_slippage_bp,omitempty"`
	Verdict              string                  `json:"verdict,omitempty"`
}

type VolumeSnapshot struct {
	Platform       string    `json:"platform"`
	DisplaySymbol  string    `json:"display_symbol,omitempty"`
	SnapshotTS     time.Time `json:"snapshot_ts"`
	SourceEndpoint string    `json:"source_endpoint"`
	Volume24HUSD   float64   `json:"volume_24h_usd"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
}

type Top30Row struct {
	Rank           int       `json:"rank"`
	Platform       string    `json:"platform"`
	Symbol         string    `json:"symbol"`
	Volume24HUSD   float64   `json:"volume_24h_usd"`
	Volume7DUSD    *float64  `json:"volume_7d_usd,omitempty"`
	Delta7DPct     *float64  `json:"delta_7d_pct,omitempty"`
	Volume7DStatus string    `json:"volume_7d_status"`
	Delta7DStatus  string    `json:"delta_7d_status"`
	EdgexListed    bool      `json:"edgex_listed"`
	ListedStatus   string    `json:"edgex_listed_status,omitempty"`
	CoverageCount  int       `json:"competitor_top30_coverage"`
	CoverageStatus string    `json:"competitor_top30_coverage_status,omitempty"`
	Action         string    `json:"suggested_action,omitempty"`
	ActionStatus   string    `json:"suggested_action_status,omitempty"`
	SnapshotTS     time.Time `json:"snapshot_ts"`
	SourceEndpoint string    `json:"source_endpoint"`
	Status         string    `json:"status"`
	DataSource     string    `json:"data_source,omitempty"`
	Error          string    `json:"error,omitempty"`
}

// PlatformVolumeAggregate captures one platform-level 24h volume / OI reading
// for use in the Share(24h) view. SnapshotTS is the time at which the upstream
// source produced this aggregate.
type PlatformVolumeAggregate struct {
	Platform        string    `json:"platform"`
	SnapshotTS      time.Time `json:"snapshot_ts"`
	Volume24HUSD    float64   `json:"volume_24h_usd"`
	OpenInterestUSD float64   `json:"open_interest_usd,omitempty"`
	DataSource      string    `json:"data_source"`
	SourceEndpoint  string    `json:"source_endpoint,omitempty"`
	Status          string    `json:"status"`
}

// DailyVolumeAggregate is one row of per-day platform (and optionally per-symbol)
// 24h volume, used to build 7d/30d windows for Share() and Top30. Volume24HUSD is
// always stored as raw USD; MEXC×0.4 / Gate×0.5 discounts are applied only at
// query time via indicators.AdjustedVolume().
type DailyVolumeAggregate struct {
	Day            time.Time `json:"day"`
	Platform       string    `json:"platform"`
	DisplaySymbol  string    `json:"display_symbol,omitempty"`
	Volume24HUSD   float64   `json:"volume_24h_usd"`
	DataSource     string    `json:"data_source"`
	SourceEndpoint string    `json:"source_endpoint,omitempty"`
	Status         string    `json:"status"`
	SnapshotTS     time.Time `json:"snapshot_ts,omitempty"`
}

// DerivativesPlatformMeta carries the two-tier mapping required by the
// CoinGecko derivatives endpoints: exchange_id is used for
// /derivatives/exchanges/{id} requests, while market_name is the display name
// emitted as tickers[].market in the /derivatives response and is used to
// filter the global response down to our 9 target competitors.
type DerivativesPlatformMeta struct {
	Platform   string `json:"platform"`
	ExchangeID string `json:"exchange_id"`
	MarketName string `json:"market_name"`
}

type CollectionStatus struct {
	Platform       string    `json:"platform"`
	DisplaySymbol  string    `json:"display_symbol,omitempty"`
	Collector      string    `json:"collector"`
	SourceEndpoint string    `json:"source_endpoint"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	SnapshotTS     time.Time `json:"snapshot_ts"`
	LatencyMS      int64     `json:"latency_ms"`
}

func NormalizePlatformSnapshot(row *PlatformSnapshot) {
	if row == nil {
		return
	}
	for tier, depth := range row.DepthByTier {
		DeriveDepthMetricsDefaults(row.DepthStatus, &depth)
		row.DepthByTier[tier] = depth
	}
	EnforceTierMonotonicity(row)
}

// EnforceTierMonotonicity clamps each tier's bid_usd and ask_usd to be at
// least as large as the previous (narrower) tier's. The cumulative depth
// inside ±N% must by definition include everything inside ±(N-Δ)%, but multi-
// endpoint adapters (e.g. bitget's /merge-depth, gate's /order_book with
// different `interval` query params) can return mutually inconsistent
// snapshots that violate this. When we detect a violation we treat the
// narrower tier's value as a verified lower bound for the wider tier and
// transfer it forward, downgrading the affected metric to partial /
// loose_lower_bound and tagging partial_reason with
// ReasonMonotonicityLowerBound so the UI surfaces it as approximate.
//
// Tiers that are not display-available (unsupported/stale/error/etc.) are
// skipped without resetting the running lower bound, so a missing middle
// tier still keeps the constraint between the surrounding tiers.
func EnforceTierMonotonicity(row *PlatformSnapshot) {
	if row == nil || len(row.DepthByTier) == 0 {
		return
	}
	tiers := make([]string, 0, len(row.DepthByTier))
	for tier := range row.DepthByTier {
		tiers = append(tiers, tier)
	}
	sort.Slice(tiers, func(i, j int) bool { return tierFraction(tiers[i]) < tierFraction(tiers[j]) })

	var prevBid, prevAsk float64
	havePrev := false
	for _, tier := range tiers {
		d, ok := row.DepthByTier[tier]
		if !ok {
			continue
		}
		if !d.DisplayAvailable {
			continue
		}
		corrected := false
		if havePrev && d.BidUSD+1e-9 < prevBid {
			d.BidUSD = prevBid
			corrected = true
		}
		if havePrev && d.AskUSD+1e-9 < prevAsk {
			d.AskUSD = prevAsk
			corrected = true
		}
		if corrected {
			d.TotalUSD = d.BidUSD + d.AskUSD
			d.StrictComplete = false
			if d.DepthStatus == StatusComplete || d.DepthStatus == StatusAggregatedOrderbook || d.DepthStatus == StatusWSLimitedDepth {
				d.DepthStatus = StatusPartial
			}
			if d.PolicyAcceptance == PolicyRawStrict || d.PolicyAcceptance == PolicyAggregatedStrict || d.PolicyAcceptance == "" {
				d.PolicyAcceptance = PolicyLooseLowerBound
			}
			d.PartialReason = appendPartialReason(d.PartialReason, ReasonMonotonicityLowerBound)
			row.DepthByTier[tier] = d
		}
		prevBid = d.BidUSD
		prevAsk = d.AskUSD
		havePrev = true
	}
}

func tierFraction(tier string) float64 {
	trimmed := strings.TrimSuffix(strings.TrimSpace(tier), "%")
	f, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0
	}
	return f
}

func appendPartialReason(existing, addition string) string {
	if existing == "" {
		return addition
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.TrimSpace(part) == addition {
			return existing
		}
	}
	return existing + "," + addition
}

func DeriveDepthMetricsDefaults(rowStatus string, metric *TierDepthMetrics) {
	if metric == nil {
		return
	}
	status := metric.DepthStatus
	if status == "" {
		status = rowStatus
		metric.DepthStatus = status
	}
	switch status {
	case StatusComplete:
		metric.StrictComplete = true
		metric.DisplayAvailable = true
		if metric.PolicyAcceptance == "" {
			metric.PolicyAcceptance = PolicyRawStrict
		}
	case StatusAggregatedOrderbook, StatusWSLimitedDepth:
		metric.StrictComplete = true
		metric.DisplayAvailable = true
		if metric.PolicyAcceptance == "" {
			metric.PolicyAcceptance = PolicyAggregatedStrict
		}
	case StatusPartial:
		metric.StrictComplete = false
		metric.DisplayAvailable = !metric.PhysicalLimit
		if metric.PolicyAcceptance == "" {
			metric.PolicyAcceptance = PolicyLooseLowerBound
		}
	case StatusStale, StatusUnsupported, StatusError, StatusInsufficientHistory:
		metric.StrictComplete = false
		metric.DisplayAvailable = false
	default:
		if metric.PolicyAcceptance == PolicyRawStrict || metric.PolicyAcceptance == PolicyAggregatedStrict {
			metric.StrictComplete = true
			metric.DisplayAvailable = true
		} else if metric.PolicyAcceptance == PolicyLooseLowerBound || metric.PolicyAcceptance == PolicyLooseGroupedApprox {
			metric.StrictComplete = false
			metric.DisplayAvailable = !metric.PhysicalLimit
		}
	}
}
