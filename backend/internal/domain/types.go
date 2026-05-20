package domain

import "time"

const (
	StatusComplete            = "complete"
	StatusPartial             = "partial"
	StatusAggregatedOrderbook = "aggregated_orderbook"
	StatusWSLimitedDepth      = "ws_limited_depth"
	StatusStale               = "stale"
	StatusUnsupported         = "unsupported"
	StatusError               = "error"

	ReasonAPILevelCap = "api_level_cap"
	ReasonSparseBook  = "sparse_book"
	ReasonUnknown     = "unknown"

	HistoryInsufficient = "insufficient_history"

	FreshnessLive    = "live"
	FreshnessDelayed = "delayed"

	SourceRawOrderbook        = "raw_orderbook"
	SourceAggregatedOrderbook = "aggregated_orderbook"
	SourceWSLocalBook         = "ws_local_book"
	SourceWSLimitedDepth      = "ws_limited_depth"
)

type SymbolSub struct {
	DisplaySymbol  string `json:"display_symbol"`
	Canonical      string `json:"canonical"`
	MarketSurface  string `json:"market_surface"`
	InstrumentKind string `json:"instrument_kind"`
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

type Level struct {
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
}

type BookView struct {
	SourceID          string            `json:"source_id"`
	Source            string            `json:"depth_source"`
	SourceEndpoint    string            `json:"source_endpoint"`
	Bids              []Level           `json:"bids,omitempty"`
	Asks              []Level           `json:"asks,omitempty"`
	SequenceID        uint64            `json:"sequence_id,omitempty"`
	SnapshotTS        time.Time         `json:"snapshot_ts"`
	ReceivedTS        time.Time         `json:"received_ts,omitempty"`
	AggregationParams map[string]string `json:"aggregation_params,omitempty"`
	APILevelCap       int               `json:"api_level_cap,omitempty"`
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
	BidUSD              float64           `json:"bid_usd"`
	AskUSD              float64           `json:"ask_usd"`
	TotalUSD            float64           `json:"total_usd"`
	DepthStatus         string            `json:"depth_status,omitempty"`
	PartialReason       string            `json:"partial_reason,omitempty"`
	DepthSource         string            `json:"depth_source,omitempty"`
	SourceID            string            `json:"source_id,omitempty"`
	SourceEndpoint      string            `json:"source_endpoint,omitempty"`
	LevelsReturned      int               `json:"levels_returned,omitempty"`
	BidLevelsReturned   int               `json:"bid_levels_returned,omitempty"`
	AskLevelsReturned   int               `json:"ask_levels_returned,omitempty"`
	APILevelCap         int               `json:"api_level_cap,omitempty"`
	FarthestBidPct      float64           `json:"farthest_bid_pct,omitempty"`
	FarthestAskPct      float64           `json:"farthest_ask_pct,omitempty"`
	FarthestDistancePct float64           `json:"farthest_distance_pct,omitempty"`
	AggregationParams   map[string]string `json:"aggregation_params,omitempty"`
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
	Error          string    `json:"error,omitempty"`
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
