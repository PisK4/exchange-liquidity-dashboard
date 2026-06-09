// Package listing implements the Listing Agent P1 backend detection
// main link plus the Top30 hot-gap delivery outbox.
//
// The architecture lives in:
//
//	architecture/方案设计/EdgeX运营/Listing/2026-05-27-Listing-Agent-P1-主链路方案设计.md
//
// and the implementation plan in:
//
//	architecture/方案设计/EdgeX运营/Listing/2026-05-27-Listing-Agent-P1-后端检测与Top30推送-实现计划.md
//
// Both documents take precedence over inline comments below.
package listing

import (
	"encoding/json"
	"time"
)

// Signal types observed by the Listing Agent. The first three drive
// P1a; manual_seed is reserved for the future runbook tooling but its
// enum value already lives here so callers do not have to special-case
// future signal kinds.
const (
	SignalInstrumentDiff      = "instrument_diff"
	SignalAnnouncementListing = "announcement_listing"
	SignalTop30HotGap         = "top30_hot_gap"
	SignalTop30Divergence     = "top30_divergence"
	SignalManualSeed          = "manual_seed"
)

// Diff subtypes emitted by the instrument poller.
const (
	DiffNewSymbol          = "new_symbol"
	DiffStatusChanged      = "status_changed"
	DiffDelisted           = "delisted"
	DiffRelisted           = "relisted"
	DiffListingTimeChanged = "listing_time_changed"
	DiffMetadataChanged    = "metadata_changed"
)

// Announcement signal subtypes. Only canonical perp listings drive
// candidate creation; everything else is parsed for the audit trail.
const (
	AnnouncementPerpListing = "perp_listing_announcement"
	AnnouncementPreMarket   = "pre_market_announcement"
	AnnouncementStockPerp   = "stock_perp_announcement"
	AnnouncementSpotListing = "spot_listing_announcement"
	AnnouncementIrrelevant  = "irrelevant_announcement"
	AnnouncementParseFailed = "parse_failed"
)

// Status normalisation across all instrument sources. MEXC unknown
// state maps to StatusUnknown rather than being optimistically promoted
// to StatusActive.
const (
	StatusActive     = "active"
	StatusPreListing = "pre_listing"
	StatusPaused     = "paused"
	StatusDelisted   = "delisted"
	StatusUnknown    = "unknown"
)

// Confidence levels used by signals and candidates.
const (
	ConfidenceLow        = "low"
	ConfidenceMedium     = "medium"
	ConfidenceMediumHigh = "medium_high"
	ConfidenceHigh       = "high"
)

// Evidence kinds describe how a candidate became known. They are also
// the input to the recommendation gate.
const (
	EvidenceAnnouncementAndAPI     = "announcement_and_api"
	EvidenceInstrumentDiffOnly     = "instrument_diff_only"
	EvidenceAnnouncementPendingAPI = "announcement_pending_api"
	EvidenceTop30Only              = "top30_only"
	EvidenceManualSeed             = "manual_seed"
)

// Lifecycle status enum (see §6.5).
const (
	LifecycleObserved                  = "observed"
	LifecycleAnnouncedPendingAPI       = "announced_pending_api_confirmation"
	LifecycleAPIDetectedNoAnnouncement = "api_detected_no_announcement"
	LifecycleConfirmedListingCandidate = "confirmed_listing_candidate"
	LifecycleAlreadyListed             = "already_listed"
)

// Recommendation enum + operator-facing labels (see §23.5).
const (
	RecommendationPreAssessment  = "pre_assessment"
	RecommendationPrepareListing = "prepare_listing"
	RecommendationWatch          = "watch"
	RecommendationRecordOnly     = "record_only"
	RecommendationNoAction       = "no_action"
)

// RecommendationLabels maps stable enums to the Chinese display label
// rendered in the read-only API.
var RecommendationLabels = map[string]string{
	RecommendationPreAssessment:  "进入预评估",
	RecommendationPrepareListing: "准备上线",
	RecommendationWatch:          "进入观察",
	RecommendationRecordOnly:     "仅记录",
	RecommendationNoAction:       "无需动作",
}

// LifecycleStatusLabels mirrors RecommendationLabels for lifecycle
// status enums so the read-only API surfaces both machine + human
// fields without forcing the frontend to maintain its own map.
var LifecycleStatusLabels = map[string]string{
	LifecycleObserved:                  "观察中",
	LifecycleAnnouncedPendingAPI:       "公告待 API 确认",
	LifecycleAPIDetectedNoAnnouncement: "API 已发现待公告确认",
	LifecycleConfirmedListingCandidate: "已确认候选",
	LifecycleAlreadyListed:             "竞品已历史上线",
}

// Outbox lifecycle states.
const (
	OutboxStatusPending  = "pending"
	OutboxStatusSent     = "sent"
	OutboxStatusRetry    = "retry"
	OutboxStatusFailed   = "failed"
	OutboxStatusDisabled = "disabled"
)

// SourceState status enum (see §15 t_listing_source_state).
const (
	SourceStatusOK            = "ok"
	SourceStatusStale         = "stale"
	SourceStatusError         = "error"
	SourceStatusSchemaDrift   = "schema_drift"
	SourceStatusLeaseSkipped  = "lease_skipped"
	SourceStatusDisabledUntil = "disabled_until"
	SourceStatusFailClosed    = "fail_closed"
)

// SourceType enum used by t_listing_source_state.
const (
	SourceTypeInstrument   = "instrument_diff"
	SourceTypeAnnouncement = "announcement"
	SourceTypeTop30Push    = "top30_push"
	SourceTypeDelivery     = "delivery"
)

// DeliveryEventTypes used in t_listing_delivery_outbox.event_type.
const (
	DeliveryEventTop30HotGap               = "top30_hot_gap"
	DeliveryEventTop30DivergenceCEXOnly    = "top30_divergence_cex_only"
	DeliveryEventTop30DivergenceDEXOnly    = "top30_divergence_dex_only"
	DeliveryEventTop30DivergenceHeavyGap   = "top30_divergence_heavy_gap"
	DeliveryEventTop30DivergenceBothHotGap = "top30_divergence_both_hot_gap"
	// Liquidity-alert event types (P3 Dashboard #10/#11). These go
	// into a separate Lark group (cfg.Alert.Webhooks.Liquidity) so
	// the operator can mute/forward them independently from listing
	// announcements.
	DeliveryEventLiquidityLag = "liquidity_lag"
	DeliveryEventWorstDepth   = "worst_depth"
)

// DivergenceCategoryKey identifies the category of a divergence push
// card. It is the third path-component of the divergence dedupe key
// (`top30_divergence|{category}|YYYY-MM-DD`) and the suffix on the
// outbox event_type values above. Stable across the wire even when
// the operator-facing label changes.
const (
	DivergenceCategoryCEXOnly    = "cex_only"
	DivergenceCategoryDEXOnly    = "dex_only"
	DivergenceCategoryHeavyGap   = "heavy_gap"
	DivergenceCategoryBothHotGap = "both_hot_gap"
)

// DeliveryChannel enum stored in t_listing_delivery_outbox.target_channel.
const (
	DeliveryChannelLarkTop30     = "lark_top30"
	DeliveryChannelLarkLiquidity = "lark_liquidity"
)

// Candidate is the read model of t_listing_candidate.
type Candidate struct {
	ID                   int64
	CanonicalSymbol      string
	DisplaySymbol        string
	MarketSurface        string
	InstrumentKind       string
	LifecycleStatus      string
	LifecycleStatusLabel string
	EvidenceKind         string
	ConfidenceLevel      string
	BusinessScore        *float64
	BusinessScoreVersion string
	Recommendation       string
	RecommendationLabel  string
	SourcePlatforms      []string
	Top30Enrichment      json.RawMessage
	FirstObservedAt      time.Time
	LastObservedAt       time.Time
}

// SignalObservation is the read/write model of t_listing_signal_observation.
type SignalObservation struct {
	ID               int64
	SignalType       string
	SignalSubtype    string
	SourcePlatform   string
	MarketType       string
	APISymbol        string
	APIMarketID      string
	CanonicalSymbol  string
	DisplaySymbol    string
	BaseAsset        string
	QuoteAsset       string
	SettleAsset      string
	MarketSurface    string
	InstrumentKind   string
	StatusRaw        string
	StatusNormalized string
	Confidence       string
	ObservedAt       time.Time
	SourceSnapshotTS *time.Time
	PublishedAt      *time.Time
	ListingTimeTS    *time.Time
	SourceEndpoint   string
	SourceURL        string
	Fingerprint      string
	PayloadJSON      json.RawMessage
	RawPayloadJSON   json.RawMessage
	RawPayloadHash   string
	FusedAt          *time.Time
}

// SourceState mirrors t_listing_source_state.
type SourceState struct {
	SourceKey             string
	SourceType            string
	Platform              string
	Status                string
	LastSuccessAt         *time.Time
	LastErrorAt           *time.Time
	ConsecutiveErrorCount int
	SchemaDriftCount      int
	DisabledUntil         *time.Time
	LastError             string
	SourceContextJSON     json.RawMessage
	UpdatedAt             time.Time
}

// DeliveryOutbox mirrors t_listing_delivery_outbox.
type DeliveryOutbox struct {
	ID            int64
	EventType     string
	DedupeKey     string
	TargetChannel string
	Status        string
	AttemptCount  int
	MaxAttempts   int
	NextAttemptAt *time.Time
	PayloadJSON   json.RawMessage
	LastError     string
	SentAt        *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastAttempt   *DeliveryAttempt
}

// Action dispatch enums. dispatch_type identifies which downstream
// owner reacts to the row; target_channel is which Lark group (or
// internal channel) receives the matching notification.
const (
	DispatchTypeListingOps = "listing_ops"
	DispatchTypeMM         = "mm"
	DispatchTypeWatchlist  = "watchlist"
	DispatchTypeIgnore     = "ignore"
)

const (
	DispatchChannelLarkListingOps = "lark_listing_ops"
	DispatchChannelLarkMM         = "lark_mm"
	DispatchChannelInternal       = "internal"
)

const (
	DispatchStatusPending   = "pending"
	DispatchStatusCompleted = "completed"
	DispatchStatusFailed    = "failed"
)

// Watchlist status enum.
const (
	WatchStatusObserving = "observing"
	WatchStatusListed    = "listed"
	WatchStatusDropped   = "dropped"
)

// Action dispatch notification event types written into the delivery
// outbox so listing-ops / MM groups receive a matching Lark card.
const (
	DeliveryEventListingActionListingOps = "listing_action_listing_ops"
	DeliveryEventListingActionContactMM  = "listing_action_contact_mm"
)

// ActionDispatchRecord mirrors t_listing_action_dispatch.
type ActionDispatchRecord struct {
	ID            int64
	CandidateID   int64
	DecisionID    int64
	DispatchType  string
	TargetChannel string
	Status        string
	OutboxID      *int64
	PayloadJSON   json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// WatchlistEntry mirrors t_listing_watchlist.
type WatchlistEntry struct {
	ID               int64
	CandidateID      int64
	CanonicalSymbol  string
	MarketSurface    string
	InstrumentKind   string
	WatchStatus      string
	WatchReason      string
	SourceDecisionID int64
	WatchStartedAt   time.Time
	PayloadJSON      json.RawMessage
}

// DecisionRecord mirrors t_listing_decision. The struct is the write
// model used by the Lark callback API; the unique key on
// (candidate_id, operator_open_id, action, callback_ts) protects
// against double-click bursts. CallbackTS is truncated to seconds
// before insert so the unique key cannot be defeated by
// sub-millisecond differences between two card clicks.
type DecisionRecord struct {
	ID                  int64
	CandidateID         int64
	CardID              string
	MessageID           string
	OperatorOpenID      string
	Action              string
	Reason              string
	SignatureVerified   bool
	CallbackPayloadJSON json.RawMessage
	CallbackTS          time.Time
	CreatedAt           time.Time
}

// DeliveryAttempt mirrors t_listing_delivery_attempt.
type DeliveryAttempt struct {
	ID           int64
	OutboxID     int64
	AttemptNo    int
	Status       string
	HTTPStatus   *int
	ErrorMessage string
	AttemptedAt  time.Time
	ResponseBody string
	LatencyMS    int64
}

// CandidateFilter narrows the list returned by Repository.ListCandidates.
type CandidateFilter struct {
	Limit        int
	Status       string
	EvidenceKind string
	Platform     string
	Symbol       string
}

// DeliveryFilter narrows the list returned by Repository.ListDeliveries.
type DeliveryFilter struct {
	Limit     int
	EventType string
	Status    string
}

// CandidateUpsert is the write-side projection used by Repository
// callers. It is intentionally narrower than Candidate so callers do
// not have to populate fields that the database manages.
type CandidateUpsert struct {
	CanonicalSymbol      string
	DisplaySymbol        string
	MarketSurface        string
	InstrumentKind       string
	LifecycleStatus      string
	LifecycleStatusLabel string
	EvidenceKind         string
	ConfidenceLevel      string
	BusinessScore        *float64
	BusinessScoreVersion string
	Recommendation       string
	RecommendationLabel  string
	SourcePlatforms      []string
	Top30Enrichment      json.RawMessage
	ObservedAt           time.Time
}

// InstrumentSnapshot mirrors one row of t_listing_instrument_snapshot.
// It is the per-platform `prev` view the instrument Diff consumes;
// the poller loads it before fetching new payload, computes the
// diff, then writes the new view through UpsertInstrumentSnapshot.
//
// The struct intentionally embeds the columns the migration owns
// (first/previous/last_seen_at + raw_json + raw_json_hash) so callers
// can rebuild the production audit trail without a second lookup.
type InstrumentSnapshot struct {
	ID                   int64
	Platform             string
	MarketType           string
	APISymbol            string
	APIMarketID          string
	DisplaySymbol        string
	CanonicalSymbol      string
	BaseAsset            string
	QuoteAsset           string
	SettleAsset          string
	MarketSurface        string
	InstrumentKind       string
	ContractType         string
	StatusRaw            string
	StatusNormalized     string
	StatusFieldName      string
	ListingTimeTS        *time.Time
	ListingTimeFieldName string
	DelistFlag           bool
	FirstSeenAt          time.Time
	PreviousSeenAt       *time.Time
	LastSeenAt           time.Time
	RawJSON              json.RawMessage
	// StableHash is the projection-based hash stored in DB column
	// raw_json_hash. The DB column name is unchanged for schema
	// compatibility; only the Go field name reflects the new
	// projection semantics (see instrument.NormalizedInstrument).
	StableHash        string
	NormalizerVersion string
}
