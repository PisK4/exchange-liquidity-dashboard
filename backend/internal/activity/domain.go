package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxRawPayloadBytes = 2 * 1024 * 1024

const (
	EventStatusActive   = "active"
	EventStatusEnded    = "ended"
	EventStatusArchived = "archived"
)

const (
	ReviewPending  = "pending"
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
	ReviewExpired  = "expired"
)

const (
	DecisionFollowNow       = "follow_now"
	DecisionBenchmarkWatch  = "benchmark_watch"
	DecisionDifferentiate   = "differentiate"
	DecisionNoFollow        = "no_follow"
	DecisionIgnoreDuplicate = "ignore_duplicate"
)

const (
	SourceStatusOK       = "ok"
	SourceStatusDegraded = "degraded"
	SourceStatusBlocked  = "blocked"
	SourceStatusDisabled = "disabled"
)

const (
	DeliveryStatusPending               = "pending"
	DeliveryStatusRetry                 = "retry"
	DeliveryStatusSent                  = "sent"
	DeliveryStatusFailed                = "failed"
	DeliveryStatusDisabledNoWebhook     = "disabled_no_webhook"
	DeliveryStatusDisabledMissingSecret = "disabled_missing_secret"
	DeliveryStatusMuted                 = "muted"
	DeliveryStatusRedrivePending        = "redrive_pending"
)

const (
	DeliveryEventDailyDigest       = "activity_daily_digest"
	DeliveryEventWeeklyDigest      = "activity_weekly_digest"
	DeliveryEventEventAlert        = "activity_event_alert"
	DeliveryEventEventUpdate       = "activity_event_update"
	DeliveryEventReviewRequired    = "activity_review_required"
	DeliveryEventSourceHealthAlert = "activity_source_health_alert"
)

const DeliveryChannelLarkActivity = "lark_activity"

func ReviewStatusForDecision(action string) (string, bool) {
	switch action {
	case DecisionFollowNow, DecisionBenchmarkWatch, DecisionDifferentiate, DecisionNoFollow:
		return ReviewApproved, true
	case DecisionIgnoreDuplicate:
		return ReviewRejected, true
	default:
		return "", false
	}
}

func BuildEventDedupeKey(platform, sourceGroup, externalID, sourceURL string) string {
	identity := strings.TrimSpace(externalID)
	if identity == "" {
		identity = canonicalURL(sourceURL)
	}
	return strings.ToLower(strings.TrimSpace(platform)) + "|" + strings.TrimSpace(sourceGroup) + "|" + identity
}

func BuildOutboxDedupeKey(eventType string, eventID int64, eventVersion int, digestKey string) string {
	parts := []string{eventType}
	if eventID > 0 {
		parts = append(parts, strconv.FormatInt(eventID, 10), "v"+strconv.Itoa(eventVersion))
	}
	if digestKey != "" {
		parts = append(parts, digestKey)
	}
	return strings.Join(parts, "|")
}

type RawPayload struct {
	PayloadText string
	Preview     string
	Hash        string
	SizeBytes   int64
	Truncated   bool
}

func PrepareRawEvidencePayload(payload string, limit int) RawPayload {
	if limit <= 0 {
		limit = MaxRawPayloadBytes
	}
	sum := sha256.Sum256([]byte(payload))
	out := RawPayload{
		Hash:      hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(payload)),
	}
	if len(payload) > limit {
		out.Truncated = true
		out.Preview = payload[:limit]
		return out
	}
	out.PayloadText = payload
	out.Preview = payload
	return out
}

type RawEvidence struct {
	ID               int64
	SourceKey        string
	Platform         string
	SourceGroup      string
	SourceURL        string
	FetchMode        string
	PayloadText      string
	PayloadPreview   string
	PayloadHash      string
	PayloadSizeBytes int64
	PayloadTruncated bool
	SchemaHash       string
	ContentHash      string
	FetchedAt        time.Time
	ResponseMeta     json.RawMessage
	FixtureRef       string
}

type ActivityEvent struct {
	ID                         int64
	RawEvidenceID              int64
	Platform                   string
	SourceGroup                string
	SourceExternalID           string
	SourceURL                  string
	Title                      string
	ActivityType               string
	TargetSymbols              []ActivityEventSymbol
	RewardPoolText             string
	RewardPoolUSDEstimate      *float64
	RewardPoolPrimaryToken     string
	RewardPoolParseConfidence  string
	HasRewardPool              bool
	StartTime                  *time.Time
	EndTime                    *time.Time
	PublishTime                *time.Time
	RawTimeText                string
	RawTimezoneHint            string
	TimeParseConfidence        string
	ContentText                string
	ContentHash                string
	DedupeKey                  string
	ConfidenceScore            float64
	NeedsHumanReview           bool
	AutoPushAllowed            bool
	EventStatus                string
	ReviewStatus               string
	OpsDecisionAction          string
	OpsDecisionStale           bool
	EventVersion               int
	ParserVersion              string
	SourceContextJSON          json.RawMessage
	ParserWarningsJSON         json.RawMessage
	RewardPoolsJSON            json.RawMessage
	TaskConditionsJSON         json.RawMessage
	EligibilityRulesJSON       json.RawMessage
	RichFieldsSummaryJSON      json.RawMessage
	SourceObservedAt           *time.Time
	SourceProducerWatermarkAt  *time.Time
	SourceBootstrapCompletedAt *time.Time
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

type ActivityEventSymbol struct {
	EventID         int64
	CanonicalSymbol string
	DisplaySymbol   string
	MarketSurface   string
	Role            string
	SortOrder       int
}

type DecisionRecord struct {
	EventID      int64
	EventVersion int
	ContentHash  string
	Action       string
	Reviewer     string
	Reason       string
}

type ReviewRecord struct {
	EventID  int64
	Action   string
	Reviewer string
	Reason   string
}

type SourceState struct {
	ID                     int64
	Platform               string
	SourceGroup            string
	SourceType             string
	SourceURL              string
	SourceKey              string
	FetchMode              string
	EvidenceQuality        string
	Enabled                bool
	PollIntervalSeconds    int
	AutoPushEnabled        bool
	RequiresProxy          bool
	RequiresBrowserContext bool
	RequiresLogin          bool
	Personalized           bool
	SourceStatus           string
	LastHTTPStatus         *int
	LastErrorKind          string
	LastSchemaHash         string
	LastContentHash        string
	SampleCount            int
	EventCount             int
	SourceContextJSON      json.RawMessage
	DisabledUntil          *time.Time
	LastCheckedAt          *time.Time
	LastSuccessAt          *time.Time
	ProducerWatermarkAt    *time.Time
	BootstrapCompletedAt   *time.Time
	UpdatedAt              time.Time
}

type DeliveryOutbox struct {
	ID            int64
	EventType     string
	EventID       int64
	EventVersion  int
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
}

type EventFilter struct {
	Platform        string
	ActivityType    string
	Status          string
	ReviewStatus    string
	AutoPushAllowed *bool
	From            *time.Time
	To              *time.Time
	Limit           int
	Cursor          int64
}

type DeliveryFilter struct {
	Status    string
	EventType string
	Limit     int
	Cursor    int64
}

func canonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "spm" {
			q.Del(key)
		}
	}
	keys := make([]string, 0, len(q))
	for key := range q {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	nq := url.Values{}
	for _, key := range keys {
		for _, val := range q[key] {
			nq.Add(key, val)
		}
	}
	u.RawQuery = nq.Encode()
	u.Fragment = ""
	return u.String()
}
