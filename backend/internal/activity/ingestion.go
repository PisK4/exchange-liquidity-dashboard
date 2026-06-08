package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SourceConfig struct {
	Platform               string
	SourceGroup            string
	SourceType             string
	SourceURL              string
	FetchMode              string
	PollInterval           time.Duration
	Enabled                bool
	AutoPushEnabled        bool
	RequiresProxy          bool
	RequiresBrowserContext bool
	RequiresLogin          bool
	Personalized           bool
	Headers                map[string]string
}

type FetchRequest struct {
	URL         string
	Platform    string
	SourceGroup string
	FetchMode   string
	Headers     map[string]string
}

type FetchResult struct {
	Platform     string
	SourceGroup  string
	SourceURL    string
	FetchMode    string
	Payload      []byte
	PayloadHash  string
	ContentHash  string
	HTTPStatus   int
	ContentType  string
	FetchedAt    time.Time
	ElapsedMS    int64
	AttemptCount int
	ProxyUsed    bool
}

type FetchMetadata struct {
	SourceURL        string
	FetchMode        string
	ElapsedMS        int64
	AttemptCount     int
	ProxyUsed        bool
	LastErrorMessage string
}

type FetchError struct {
	Err      error
	Metadata FetchMetadata
}

func (e *FetchError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *FetchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type RawDocument struct {
	Platform      string
	SourceGroup   string
	SourceURL     string
	FetchMode     string
	Payload       []byte
	RequiresLogin bool
	Personalized  bool
}

type IngestionStore interface {
	LoadActivitySourceState(ctx context.Context, sourceKey string) (SourceState, bool, error)
	UpsertActivitySourceState(ctx context.Context, state SourceState) error
	UpsertRawEvidence(ctx context.Context, row RawEvidence) (int64, error)
	UpsertActivityEvent(ctx context.Context, event ActivityEvent) (int64, bool, error)
}

type FetchFunc func(context.Context, FetchRequest) (FetchResult, error)
type ParseFunc func(context.Context, RawDocument) ([]ActivityEvent, error)

type IngestionDeps struct {
	Sources []SourceConfig
	Fetch   FetchFunc
	Parse   ParseFunc
	Now     func() time.Time
}

type IngestionResult struct {
	Sources          int
	Fetched          int
	RawEvidence      int
	Events           int
	SourceErrors     int
	ParserErrors     int
	SkippedSources   int
	UnchangedSources int
}

const defaultActivitySourcePollInterval = 30 * time.Minute

type ActivitySourcePollDecision struct {
	ShouldPoll bool
	Reason     string
}

func ShouldPollActivitySource(src SourceConfig, state SourceState, hasState bool, now time.Time) ActivitySourcePollDecision {
	if !src.Enabled {
		return ActivitySourcePollDecision{Reason: "disabled_config"}
	}
	if hasState && state.DisabledUntil != nil && now.Before(state.DisabledUntil.UTC()) {
		return ActivitySourcePollDecision{Reason: "disabled_until"}
	}
	if hasState && state.LastCheckedAt != nil && !state.LastCheckedAt.IsZero() {
		interval := src.PollInterval
		if interval <= 0 && state.PollIntervalSeconds > 0 {
			interval = time.Duration(state.PollIntervalSeconds) * time.Second
		}
		if interval <= 0 {
			interval = defaultActivitySourcePollInterval
		}
		if now.Before(state.LastCheckedAt.UTC().Add(interval)) {
			return ActivitySourcePollDecision{Reason: "poll_interval"}
		}
	}
	return ActivitySourcePollDecision{ShouldPoll: true, Reason: "due"}
}

func IngestSources(ctx context.Context, store IngestionStore, deps IngestionDeps) (IngestionResult, error) {
	if store == nil {
		return IngestionResult{}, errors.New("activity ingestion: store is nil")
	}
	if deps.Fetch == nil {
		return IngestionResult{}, errors.New("activity ingestion: fetch func is nil")
	}
	if deps.Parse == nil {
		return IngestionResult{}, errors.New("activity ingestion: parse func is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	now := deps.Now().UTC()
	res := IngestionResult{}
	for _, src := range deps.Sources {
		if !src.Enabled {
			res.SkippedSources++
			continue
		}
		sourceKey := BuildSourceKey(src.Platform, src.SourceGroup, src.FetchMode)
		state, hasState, err := store.LoadActivitySourceState(ctx, sourceKey)
		if err != nil {
			return res, err
		}
		decision := ShouldPollActivitySource(src, state, hasState, now)
		if !decision.ShouldPoll {
			res.SkippedSources++
			continue
		}
		if strings.TrimSpace(src.SourceURL) == "" {
			res.SourceErrors++
			state := buildSourceState(src, now, nil, SourceStatusDegraded, "missing_source_url", 0, "")
			if err := store.UpsertActivitySourceState(ctx, state); err != nil {
				return res, err
			}
			continue
		}
		res.Sources++
		fetched, err := deps.Fetch(ctx, FetchRequest{
			URL:         src.SourceURL,
			Platform:    src.Platform,
			SourceGroup: src.SourceGroup,
			FetchMode:   src.FetchMode,
			Headers:     src.Headers,
		})
		if err != nil {
			res.SourceErrors++
			state := buildSourceState(src, now, nil, SourceStatusDegraded, classifyError(err), 0, "")
			state.SourceContextJSON = fetchErrorSourceContext(src, err)
			if upErr := store.UpsertActivitySourceState(ctx, state); upErr != nil {
				return res, upErr
			}
			continue
		}
		res.Fetched++
		payloadHash := fetched.PayloadHash
		if payloadHash == "" {
			payloadHash = hashBytes(fetched.Payload)
		}
		contentHash := fetched.ContentHash
		if contentHash == "" {
			contentHash = payloadHash
		}
		httpStatus := fetched.HTTPStatus
		if httpStatus >= 200 && httpStatus < 300 && hasState && strings.TrimSpace(state.LastContentHash) != "" && state.LastContentHash == contentHash {
			res.UnchangedSources++
			state := buildSourceState(src, now, &httpStatus, SourceStatusOK, "", 0, contentHash)
			state.SourceContextJSON = fetchResultSourceContext(src, fetched)
			successAt := now
			state.LastSuccessAt = &successAt
			if err := store.UpsertActivitySourceState(ctx, state); err != nil {
				return res, err
			}
			continue
		}
		meta, _ := json.Marshal(fetchResultRawMeta(src, fetched, payloadHash))
		rawID, err := store.UpsertRawEvidence(ctx, RawEvidence{
			SourceKey:    sourceKey,
			Platform:     src.Platform,
			SourceGroup:  src.SourceGroup,
			SourceURL:    src.SourceURL,
			FetchMode:    src.FetchMode,
			PayloadText:  string(fetched.Payload),
			PayloadHash:  payloadHash,
			ContentHash:  contentHash,
			FetchedAt:    fetched.FetchedAt,
			ResponseMeta: json.RawMessage(meta),
		})
		if err != nil {
			return res, err
		}
		res.RawEvidence++
		if httpStatus < 200 || httpStatus >= 300 {
			res.SourceErrors++
			state := buildSourceState(src, now, &httpStatus, SourceStatusDegraded, fmt.Sprintf("http_%d", httpStatus), 1, contentHash)
			state.SourceContextJSON = fetchResultSourceContext(src, fetched)
			if err := store.UpsertActivitySourceState(ctx, state); err != nil {
				return res, err
			}
			continue
		}
		events, err := deps.Parse(ctx, RawDocument{
			Platform:      src.Platform,
			SourceGroup:   src.SourceGroup,
			SourceURL:     src.SourceURL,
			FetchMode:     src.FetchMode,
			Payload:       fetched.Payload,
			RequiresLogin: src.RequiresLogin,
			Personalized:  src.Personalized,
		})
		if err != nil {
			res.ParserErrors++
			state := buildSourceState(src, now, &httpStatus, SourceStatusDegraded, "parser_error", 1, contentHash)
			state.SourceContextJSON = fetchResultSourceContext(src, fetched)
			if upErr := store.UpsertActivitySourceState(ctx, state); upErr != nil {
				return res, upErr
			}
			continue
		}
		for _, ev := range events {
			ev.RawEvidenceID = rawID
			applySourcePolicy(&ev, src)
			if ev.DedupeKey == "" {
				ev.DedupeKey = BuildEventDedupeKey(ev.Platform, ev.SourceGroup, ev.SourceExternalID, ev.SourceURL)
			}
			if ev.ContentHash == "" {
				ev.ContentHash = contentHash
			}
			if ev.EventVersion <= 0 {
				ev.EventVersion = 1
			}
			if ev.ReviewStatus == "" {
				ev.ReviewStatus = ReviewPending
			}
			if ev.ParserVersion == "" {
				ev.ParserVersion = "activity-parser-v1"
			}
			if ev.ActivityType == "" {
				ev.ActivityType = "unknown"
			}
			if ev.Title == "" {
				continue
			}
			_, _, err = store.UpsertActivityEvent(ctx, ev)
			if err != nil {
				return res, err
			}
			res.Events++
		}
		state = buildSourceState(src, now, &httpStatus, SourceStatusOK, "", 1, contentHash)
		state.SourceContextJSON = fetchResultSourceContext(src, fetched)
		state.EventCount = len(events)
		successAt := now
		state.LastSuccessAt = &successAt
		if err := store.UpsertActivitySourceState(ctx, state); err != nil {
			return res, err
		}
	}
	return res, nil
}

func BuildSourceKey(platform, sourceGroup, fetchMode string) string {
	return strings.ToLower(strings.TrimSpace(platform)) + "|" + strings.TrimSpace(sourceGroup) + "|" + strings.TrimSpace(fetchMode)
}

func buildSourceState(src SourceConfig, now time.Time, httpStatus *int, status, errorKind string, sampleCount int, contentHash string) SourceState {
	intervalSeconds := int(src.PollInterval.Seconds())
	if intervalSeconds <= 0 {
		intervalSeconds = int(defaultActivitySourcePollInterval.Seconds())
	}
	checkedAt := now
	return SourceState{
		Platform:               strings.ToLower(strings.TrimSpace(src.Platform)),
		SourceGroup:            src.SourceGroup,
		SourceType:             src.SourceType,
		SourceURL:              src.SourceURL,
		SourceKey:              BuildSourceKey(src.Platform, src.SourceGroup, src.FetchMode),
		FetchMode:              src.FetchMode,
		EvidenceQuality:        evidenceQualityForFetchMode(src.FetchMode),
		Enabled:                src.Enabled,
		AutoPushEnabled:        src.AutoPushEnabled,
		RequiresProxy:          src.RequiresProxy,
		RequiresBrowserContext: src.RequiresBrowserContext,
		RequiresLogin:          src.RequiresLogin,
		Personalized:           src.Personalized,
		SourceStatus:           status,
		LastHTTPStatus:         httpStatus,
		LastErrorKind:          errorKind,
		LastContentHash:        contentHash,
		SampleCount:            sampleCount,
		PollIntervalSeconds:    intervalSeconds,
		LastCheckedAt:          &checkedAt,
		UpdatedAt:              now,
	}
}

func applySourcePolicy(ev *ActivityEvent, src SourceConfig) {
	ev.Platform = strings.ToLower(strings.TrimSpace(ev.Platform))
	if ev.Platform == "" {
		ev.Platform = strings.ToLower(strings.TrimSpace(src.Platform))
	}
	if ev.SourceGroup == "" {
		ev.SourceGroup = src.SourceGroup
	}
	if ev.SourceURL == "" {
		ev.SourceURL = src.SourceURL
	}
	if src.RequiresLogin || src.Personalized || src.RequiresBrowserContext {
		ev.NeedsHumanReview = true
		ev.AutoPushAllowed = false
	}
	if !src.AutoPushEnabled {
		ev.AutoPushAllowed = false
	}
}

func evidenceQualityForFetchMode(fetchMode string) string {
	switch fetchMode {
	case "http_direct", "http_direct_json", "utls_proxy_json":
		return "api_json"
	case "markdown_doc":
		return "markdown_doc"
	case "utls_html", "utls_proxy_html", "http_with_browser_headers":
		return "html_page"
	default:
		return "unknown"
	}
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "tls"):
		return "tls_error"
	default:
		return "fetch_error"
	}
}

func hashBytes(payload []byte) string {
	return PrepareRawEvidencePayload(string(payload), MaxRawPayloadBytes).Hash
}

func fetchResultRawMeta(src SourceConfig, fetched FetchResult, payloadHash string) map[string]any {
	meta := fetchResultMeta(src, fetched)
	meta["http_status"] = fetched.HTTPStatus
	meta["content_type"] = fetched.ContentType
	meta["payload_hash"] = payloadHash
	return meta
}

func fetchResultSourceContext(src SourceConfig, fetched FetchResult) json.RawMessage {
	encoded, _ := json.Marshal(fetchResultMeta(src, fetched))
	return json.RawMessage(encoded)
}

func fetchResultMeta(src SourceConfig, fetched FetchResult) map[string]any {
	return map[string]any{
		"source_url":    firstNonEmptyString(fetched.SourceURL, src.SourceURL),
		"fetch_mode":    firstNonEmptyString(fetched.FetchMode, src.FetchMode),
		"elapsed_ms":    fetched.ElapsedMS,
		"attempt_count": normalizeAttemptCount(fetched.AttemptCount),
		"proxy_used":    fetched.ProxyUsed,
	}
}

func fetchErrorSourceContext(src SourceConfig, err error) json.RawMessage {
	metadata := FetchMetadata{
		SourceURL:        src.SourceURL,
		FetchMode:        src.FetchMode,
		AttemptCount:     1,
		LastErrorMessage: "",
	}
	if err != nil {
		metadata.LastErrorMessage = err.Error()
	}
	var fetchErr *FetchError
	if errors.As(err, &fetchErr) {
		metadata = fetchErr.Metadata
		metadata.SourceURL = firstNonEmptyString(metadata.SourceURL, src.SourceURL)
		metadata.FetchMode = firstNonEmptyString(metadata.FetchMode, src.FetchMode)
		metadata.AttemptCount = normalizeAttemptCount(metadata.AttemptCount)
		if metadata.LastErrorMessage == "" && fetchErr.Err != nil {
			metadata.LastErrorMessage = fetchErr.Err.Error()
		}
	}
	encoded, _ := json.Marshal(map[string]any{
		"source_url":         metadata.SourceURL,
		"fetch_mode":         metadata.FetchMode,
		"elapsed_ms":         metadata.ElapsedMS,
		"attempt_count":      normalizeAttemptCount(metadata.AttemptCount),
		"proxy_used":         metadata.ProxyUsed,
		"last_error_message": metadata.LastErrorMessage,
	})
	return json.RawMessage(encoded)
}

func normalizeAttemptCount(attempts int) int {
	if attempts <= 0 {
		return 1
	}
	return attempts
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
