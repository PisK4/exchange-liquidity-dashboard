package activity

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type ProducerStore interface {
	ListOutboxCandidateEvents(ctx context.Context, limit int) ([]ActivityEvent, error)
	InsertActivityOutbox(ctx context.Context, row DeliveryOutbox) error
}

type ProducerConfig struct {
	WebhookURL          string
	DecisionTokenSecret string
	DashboardBaseURL    string
	MaxPerTick          int
	Now                 func() time.Time
}

type ProducerResult struct {
	Candidates            int
	EventAlerts           int
	ReviewRequired        int
	OutboxRows            int
	DisabledNoWebhook     int
	DisabledMissingSecret int
}

func ProduceOutbox(ctx context.Context, store ProducerStore, cfg ProducerConfig) (ProducerResult, error) {
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	limit := cfg.MaxPerTick
	if limit <= 0 {
		limit = 10
	}
	events, err := store.ListOutboxCandidateEvents(ctx, limit)
	if err != nil {
		return ProducerResult{}, err
	}
	now := cfg.Now().UTC()
	res := ProducerResult{Candidates: len(events)}
	for _, ev := range events {
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
				Summary:             ev.ContentText,
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
			if strings.TrimSpace(cfg.WebhookURL) == "" {
				status = DeliveryStatusDisabledNoWebhook
				res.DisabledNoWebhook++
			}
		}
		nextAttempt := now
		row := DeliveryOutbox{
			EventType:     eventType,
			DedupeKey:     BuildOutboxDedupeKey(eventType, ev.ID, ev.EventVersion, ""),
			TargetChannel: DeliveryChannelLarkActivity,
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
