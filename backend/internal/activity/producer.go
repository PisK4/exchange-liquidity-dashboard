package activity

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type ProducerStore interface {
	ListOutboxCandidateEvents(ctx context.Context, limit int) ([]ActivityEvent, error)
	ListOutboxCandidateEventsBySource(ctx context.Context, platform, sourceGroup string, limit int) ([]ActivityEvent, error)
	InsertActivityOutbox(ctx context.Context, row DeliveryOutbox) error
}

type SourceDeliveryPolicy struct {
	Platform        string
	SourceGroup     string
	Enabled         bool
	TargetChannel   string
	WebhookURL      string
	MaxPerTick      int
	SendSpacing     time.Duration
	AutoPushEnabled bool
}

type ProducerConfig struct {
	WebhookURL                     string
	WebhookURLByChannel            map[string]string
	DecisionTokenSecret            string
	EventAgeCutoff                 time.Duration
	SuppressInitialSnapshot        bool
	RequireSourceTimeForAutoPush   bool
	SuppressMissingTimeOnBootstrap bool
	DashboardBaseURL               string
	MaxPerTick                     int
	SourcePolicies                 []SourceDeliveryPolicy
	Now                            func() time.Time
}

type ProducerResult struct {
	Candidates            int
	EventAlerts           int
	ReviewRequired        int
	OutboxRows            int
	DisabledNoWebhook     int
	DisabledMissingSecret int
	SuppressedHistorical  int
	SuppressedBootstrap   int
	SuppressedMissingTime int
}

func ProduceOutbox(ctx context.Context, store ProducerStore, cfg ProducerConfig) (ProducerResult, error) {
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	limit := cfg.MaxPerTick
	if limit <= 0 {
		limit = 10
	}
	events, err := listOutboxCandidates(ctx, store, cfg, limit)
	if err != nil {
		return ProducerResult{}, err
	}
	now := cfg.Now().UTC()
	res := ProducerResult{Candidates: len(events)}
	for _, ev := range events {
		if suppressed, reason := suppressActivityEventForProducer(ev, cfg, now); suppressed {
			switch reason {
			case "historical_event_age_cutoff":
				res.SuppressedHistorical++
			case "missing_source_time_bootstrap":
				res.SuppressedMissingTime++
			default:
				res.SuppressedBootstrap++
			}
			continue
		}
		policy := sourceDeliveryPolicyFor(cfg, ev)
		if !policy.Enabled {
			continue
		}
		eventType := DeliveryEventEventAlert
		if ev.NeedsHumanReview && ev.ReviewStatus != ReviewApproved {
			eventType = DeliveryEventReviewRequired
			res.ReviewRequired++
		} else {
			res.EventAlerts++
		}
		status := DeliveryStatusPending
		var payload []byte
		if strings.TrimSpace(cfg.DecisionTokenSecret) == "" {
			status = DeliveryStatusDisabledMissingSecret
			payload = []byte(`{}`)
			res.DisabledMissingSecret++
		} else {
			summary := strings.TrimSpace(ev.ContentText)
			if summary == "" {
				summary = strings.TrimSpace(ev.Title)
			}
			card := ActivityEventCard{
				EventID:             ev.ID,
				EventVersion:        ev.EventVersion,
				ContentHash:         ev.ContentHash,
				Platform:            ev.Platform,
				SourceGroup:         ev.SourceGroup,
				FetchMode:           fetchModeFromSourceContext(ev),
				SourceHealth:        SourceStatusOK,
				Title:               ev.Title,
				ActivityType:        ev.ActivityType,
				Summary:             summary,
				SourceURL:           ev.SourceURL,
				DedupeKey:           ev.DedupeKey,
				TriggerTime:         now,
				DecisionBaseURL:     cfg.DashboardBaseURL,
				DecisionTokenSecret: cfg.DecisionTokenSecret,
				ConfirmedRichLines:  richLines(ev),
				CandidateLines:      parserWarningLines(ev),
				ReviewReasons:       reviewReasons(ev),
			}
			if eventType == DeliveryEventReviewRequired {
				payload, err = RenderActivityReviewRequiredPostMessage(card)
			} else {
				payload, err = RenderActivityEventAlertPostMessage(card)
			}
			if err != nil {
				return res, err
			}
			if strings.TrimSpace(webhookURLForPolicy(cfg, policy)) == "" {
				status = DeliveryStatusDisabledNoWebhook
				res.DisabledNoWebhook++
			}
		}
		nextAttempt := now
		targetChannel := strings.TrimSpace(policy.TargetChannel)
		if targetChannel == "" {
			targetChannel = DeliveryChannelLarkActivity
		}
		row := DeliveryOutbox{
			EventType:     eventType,
			EventID:       ev.ID,
			EventVersion:  ev.EventVersion,
			DedupeKey:     BuildOutboxDedupeKey(eventType, ev.ID, ev.EventVersion, ""),
			TargetChannel: targetChannel,
			Status:        status,
			AttemptCount:  0,
			MaxAttempts:   5,
			NextAttemptAt: &nextAttempt,
			PayloadJSON:   payload,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := store.InsertActivityOutbox(ctx, row); err != nil {
			return res, err
		}
		res.OutboxRows++
	}
	return res, nil
}

func suppressActivityEventForProducer(ev ActivityEvent, cfg ProducerConfig, now time.Time) (bool, string) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	eventTime, hasSourceTime := effectiveActivityEventTime(ev)
	if cfg.EventAgeCutoff > 0 && hasSourceTime && !eventTime.After(now.Add(-cfg.EventAgeCutoff)) {
		return true, "historical_event_age_cutoff"
	}
	if cfg.RequireSourceTimeForAutoPush && ev.AutoPushAllowed && !hasSourceTime && ev.ReviewStatus != ReviewApproved {
		return true, "missing_source_time"
	}
	if cfg.SuppressInitialSnapshot {
		if ev.SourceBootstrapCompletedAt == nil || ev.SourceProducerWatermarkAt == nil {
			if cfg.SuppressMissingTimeOnBootstrap && !hasSourceTime {
				return true, "missing_source_time_bootstrap"
			}
			return true, "bootstrap_incomplete"
		}
		if !eventTime.IsZero() && !eventTime.After(ev.SourceProducerWatermarkAt.UTC()) {
			return true, "before_source_producer_watermark"
		}
	}
	return false, ""
}

func effectiveActivityEventTime(ev ActivityEvent) (time.Time, bool) {
	if ev.PublishTime != nil && !ev.PublishTime.IsZero() {
		return ev.PublishTime.UTC(), true
	}
	if ev.StartTime != nil && !ev.StartTime.IsZero() {
		return ev.StartTime.UTC(), true
	}
	if ev.SourceObservedAt != nil && !ev.SourceObservedAt.IsZero() {
		return ev.SourceObservedAt.UTC(), false
	}
	if !ev.CreatedAt.IsZero() {
		return ev.CreatedAt.UTC(), false
	}
	return time.Time{}, false
}

func listOutboxCandidates(ctx context.Context, store ProducerStore, cfg ProducerConfig, defaultLimit int) ([]ActivityEvent, error) {
	policies := enabledSourceDeliveryPolicies(cfg)
	if len(policies) == 0 {
		return store.ListOutboxCandidateEvents(ctx, defaultLimit)
	}
	out := []ActivityEvent{}
	seen := map[int64]bool{}
	for _, policy := range policies {
		limit := policy.MaxPerTick
		if limit <= 0 {
			limit = defaultLimit
		}
		events, err := store.ListOutboxCandidateEventsBySource(ctx, policy.Platform, policy.SourceGroup, limit)
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			if seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
			out = append(out, ev)
		}
	}
	return out, nil
}

func enabledSourceDeliveryPolicies(cfg ProducerConfig) []SourceDeliveryPolicy {
	out := []SourceDeliveryPolicy{}
	for _, policy := range cfg.SourcePolicies {
		if !policy.Enabled {
			continue
		}
		if strings.TrimSpace(policy.Platform) == "" || strings.TrimSpace(policy.SourceGroup) == "" {
			continue
		}
		out = append(out, policy)
	}
	return out
}

func sourceDeliveryPolicyFor(cfg ProducerConfig, ev ActivityEvent) SourceDeliveryPolicy {
	for _, policy := range cfg.SourcePolicies {
		if strings.EqualFold(strings.TrimSpace(policy.Platform), strings.TrimSpace(ev.Platform)) && strings.TrimSpace(policy.SourceGroup) == strings.TrimSpace(ev.SourceGroup) {
			if strings.TrimSpace(policy.TargetChannel) == "" {
				policy.TargetChannel = DeliveryChannelLarkActivity
			}
			return policy
		}
	}
	return SourceDeliveryPolicy{Enabled: true, Platform: ev.Platform, SourceGroup: ev.SourceGroup, TargetChannel: DeliveryChannelLarkActivity}
}

func webhookURLForPolicy(cfg ProducerConfig, policy SourceDeliveryPolicy) string {
	if strings.TrimSpace(policy.WebhookURL) != "" {
		return policy.WebhookURL
	}
	channel := strings.TrimSpace(policy.TargetChannel)
	if channel != "" && cfg.WebhookURLByChannel != nil {
		if got := strings.TrimSpace(cfg.WebhookURLByChannel[channel]); got != "" {
			return got
		}
	}
	if channel != "" && channel != DeliveryChannelLarkActivity {
		return ""
	}
	return cfg.WebhookURL
}

func fetchModeFromSourceContext(ev ActivityEvent) string {
	if len(ev.SourceContextJSON) == 0 {
		return "-"
	}
	var ctx map[string]any
	if err := json.Unmarshal(ev.SourceContextJSON, &ctx); err != nil {
		return "-"
	}
	if v, _ := ctx["fetch_mode"].(string); strings.TrimSpace(v) != "" {
		return v
	}
	return "-"
}

func richLines(ev ActivityEvent) []string {
	if ev.HasRewardPool && strings.TrimSpace(ev.RewardPoolText) != "" {
		return []string{"Rich: " + ev.RewardPoolText}
	}
	return nil
}

func parserWarningLines(ev ActivityEvent) []string {
	if len(ev.ParserWarningsJSON) == 0 {
		return nil
	}
	var warnings []string
	if err := json.Unmarshal(ev.ParserWarningsJSON, &warnings); err != nil {
		return nil
	}
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if strings.TrimSpace(warning) != "" {
			out = append(out, warning)
		}
	}
	return out
}

func reviewReasons(ev ActivityEvent) []string {
	lines := parserWarningLines(ev)
	if ev.NeedsHumanReview && len(lines) == 0 {
		return []string{"needs_human_review"}
	}
	return lines
}
