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

type OrderBookSnapshot struct {
	Platform           string    `json:"platform"`
	DisplaySymbol      string    `json:"display_symbol"`
	SourceEndpoint     string    `json:"source_endpoint"`
	SnapshotTS         time.Time `json:"snapshot_ts"`
	Bids               []Level   `json:"bids,omitempty"`
	Asks               []Level   `json:"asks,omitempty"`
	DepthStatus        string    `json:"depth_status"`
	PartialReason      string    `json:"partial_reason,omitempty"`
	Error              string    `json:"error,omitempty"`
	LevelsReturned     int       `json:"levels_returned"`
	APILevelCap        int       `json:"api_level_cap"`
	FarthestDistancePC float64   `json:"farthest_distance_pct"`
}

type DepthMetrics struct {
	BidUSD   float64 `json:"bid_usd"`
	AskUSD   float64 `json:"ask_usd"`
	TotalUSD float64 `json:"total_usd"`
}

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
	BuySlippageBP        map[string]float64      `json:"buy_slippage_bp"`
	SellSlippageBP       map[string]float64      `json:"sell_slippage_bp"`
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
	Action         string    `json:"suggested_action,omitempty"`
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
