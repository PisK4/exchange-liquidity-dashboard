package activity

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeProducerStore struct {
	events  []ActivityEvent
	outbox  []DeliveryOutbox
	listErr error
}

func (f *fakeProducerStore) ListOutboxCandidateEvents(ctx context.Context, limit int) ([]ActivityEvent, error) {
	return f.events, f.listErr
}

func (f *fakeProducerStore) ListOutboxCandidateEventsBySource(ctx context.Context, platform, sourceGroup string, limit int) ([]ActivityEvent, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]ActivityEvent, 0, len(f.events))
	for _, ev := range f.events {
		if ev.Platform != platform || ev.SourceGroup != sourceGroup {
			continue
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeProducerStore) InsertActivityOutbox(ctx context.Context, row DeliveryOutbox) error {
	f.outbox = append(f.outbox, row)
	return nil
}

func TestProduceOutboxCreatesAlertAndReviewCardsInCollectOnly(t *testing.T) {
	now := time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	store := &fakeProducerStore{events: []ActivityEvent{
		{
			ID: 1, Platform: "binance", SourceGroup: "cms_article_detail", SourceURL: "https://binance.example/a",
			Title: "Binance Launchpool ABC", ActivityType: "launchpool", ContentText: "Stake BNB to earn ABC",
			ContentHash: "hash-1", DedupeKey: "binance|cms_article_detail|abc", EventVersion: 2,
			AutoPushAllowed: true, ReviewStatus: ReviewPending,
		},
		{
			ID: 2, Platform: "mexc", SourceGroup: "latest_events", SourceURL: "https://mexc.example/a",
			Title: "MEXC M-Day Futures", ActivityType: "futures_trading_competition", ContentText: "Reward candidate",
			ContentHash: "hash-2", DedupeKey: "mexc|latest_events|mday", EventVersion: 1,
			NeedsHumanReview: true, ReviewStatus: ReviewPending,
		},
	}}
	res, err := ProduceOutbox(context.Background(), store, ProducerConfig{
		WebhookURL:          "",
		DecisionTokenSecret: "secret",
		DashboardBaseURL:    "https://dashboard.example",
		MaxPerTick:          10,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("ProduceOutbox err=%v", err)
	}
	if res.OutboxRows != 2 || len(store.outbox) != 2 {
		t.Fatalf("result=%+v outbox=%+v", res, store.outbox)
	}
	if store.outbox[0].EventType != DeliveryEventEventAlert || store.outbox[0].Status != DeliveryStatusDisabledNoWebhook {
		t.Fatalf("alert outbox=%+v", store.outbox[0])
	}
	if store.outbox[1].EventType != DeliveryEventReviewRequired || store.outbox[1].Status != DeliveryStatusDisabledNoWebhook {
		t.Fatalf("review outbox=%+v", store.outbox[1])
	}
	if len(store.outbox[0].PayloadJSON) == 0 || len(store.outbox[1].PayloadJSON) == 0 {
		t.Fatalf("collect-only rows should keep previewable card payloads")
	}
	if store.outbox[0].EventID != 1 || store.outbox[0].EventVersion != 2 || store.outbox[0].TargetChannel != DeliveryChannelLarkActivity {
		t.Fatalf("alert outbox should keep event metadata/channel: %+v", store.outbox[0])
	}
}

func TestProduceOutboxBatchesCandidatesBySourcePolicy(t *testing.T) {
	store := &fakeProducerStore{events: []ActivityEvent{
		{
			ID: 1, Platform: "binance", SourceGroup: "cms_article_detail", Title: "Binance Launchpool",
			ContentHash: "hash-1", DedupeKey: "binance|cms_article_detail|1", EventVersion: 1,
			AutoPushAllowed: true, ReviewStatus: ReviewPending,
		},
		{
			ID: 2, Platform: "okx", SourceGroup: "help_announcement", Title: "OKX Campaign",
			ContentHash: "hash-2", DedupeKey: "okx|help_announcement|2", EventVersion: 1,
			AutoPushAllowed: true, ReviewStatus: ReviewPending,
		},
	}}
	res, err := ProduceOutbox(context.Background(), store, ProducerConfig{
		WebhookURL:          "https://example.invalid/default",
		DecisionTokenSecret: "secret",
		MaxPerTick:          10,
		SourcePolicies: []SourceDeliveryPolicy{
			{Platform: "binance", SourceGroup: "cms_article_detail", Enabled: true, AutoPushEnabled: true, TargetChannel: DeliveryChannelLarkActivity, MaxPerTick: 1},
		},
	})
	if err != nil {
		t.Fatalf("ProduceOutbox err=%v", err)
	}
	if res.OutboxRows != 1 || len(store.outbox) != 1 || store.outbox[0].EventID != 1 {
		t.Fatalf("source policy should only produce matching source: result=%+v outbox=%+v", res, store.outbox)
	}
}

func TestProduceOutboxUsesSourceChannelWebhookPolicy(t *testing.T) {
	store := &fakeProducerStore{events: []ActivityEvent{{
		ID: 3, Platform: "gate", SourceGroup: "launchpool_project_list", Title: "Gate Launchpool",
		ContentHash: "hash-3", DedupeKey: "gate|launchpool_project_list|3", EventVersion: 2,
		AutoPushAllowed: true, ReviewStatus: ReviewPending,
	}}}
	res, err := ProduceOutbox(context.Background(), store, ProducerConfig{
		DecisionTokenSecret: "secret",
		MaxPerTick:          10,
		SourcePolicies: []SourceDeliveryPolicy{{
			Platform: "gate", SourceGroup: "launchpool_project_list", Enabled: true, AutoPushEnabled: true,
			TargetChannel: "lark_activity_gate", WebhookURL: "https://example.invalid/gate", MaxPerTick: 10,
		}},
	})
	if err != nil {
		t.Fatalf("ProduceOutbox err=%v", err)
	}
	if res.OutboxRows != 1 || res.DisabledNoWebhook != 0 || store.outbox[0].TargetChannel != "lark_activity_gate" || store.outbox[0].Status != DeliveryStatusPending {
		t.Fatalf("source webhook/channel not honored: result=%+v outbox=%+v", res, store.outbox)
	}
}

func TestProduceOutboxDisablesDecisionCardsWhenSecretMissing(t *testing.T) {
	store := &fakeProducerStore{events: []ActivityEvent{{
		ID: 7, Platform: "gate", SourceGroup: "launchpool_project_list", Title: "Gate Launchpool",
		ContentHash: "hash", DedupeKey: "gate|launchpool|7", EventVersion: 1, AutoPushAllowed: true, ReviewStatus: ReviewPending,
	}}}
	res, err := ProduceOutbox(context.Background(), store, ProducerConfig{WebhookURL: "https://example.invalid", MaxPerTick: 10})
	if err != nil {
		t.Fatalf("ProduceOutbox err=%v", err)
	}
	if res.DisabledMissingSecret != 1 || store.outbox[0].Status != DeliveryStatusDisabledMissingSecret {
		t.Fatalf("result=%+v outbox=%+v", res, store.outbox)
	}
}

func TestProduceOutboxSkipsUnapprovedReviewRequiredFactFlow(t *testing.T) {
	store := &fakeProducerStore{events: []ActivityEvent{{
		ID: 9, Platform: "bybit", SourceGroup: "rewards_hub", Title: "Personalized reward",
		ContentHash: "hash", DedupeKey: "bybit|rewards|9", EventVersion: 1,
		NeedsHumanReview: true, AutoPushAllowed: false, ReviewStatus: ReviewPending,
	}}}
	res, err := ProduceOutbox(context.Background(), store, ProducerConfig{WebhookURL: "https://example.invalid", DecisionTokenSecret: "secret", MaxPerTick: 10})
	if err != nil {
		t.Fatalf("ProduceOutbox err=%v", err)
	}
	if res.ReviewRequired != 1 || store.outbox[0].EventType != DeliveryEventReviewRequired {
		t.Fatalf("result=%+v outbox=%+v", res, store.outbox)
	}
}

func TestProduceOutboxFallsBackSummaryToTitle(t *testing.T) {
	store := &fakeProducerStore{events: []ActivityEvent{{
		ID: 11, Platform: "lighter", SourceGroup: "incentive_docs", SourceURL: "https://lighter.example/docs",
		Title: "Lighter Points Program", ActivityType: "incentive_rule_snapshot",
		ContentHash: "hash", DedupeKey: "lighter|incentive|points", EventVersion: 1,
		AutoPushAllowed: true, ReviewStatus: ReviewPending,
	}}}
	_, err := ProduceOutbox(context.Background(), store, ProducerConfig{
		WebhookURL:          "https://example.invalid",
		DecisionTokenSecret: "secret",
		DashboardBaseURL:    "https://dashboard.example",
		MaxPerTick:          10,
	})
	if err != nil {
		t.Fatalf("ProduceOutbox err=%v", err)
	}
	payload := string(store.outbox[0].PayloadJSON)
	if !strings.Contains(payload, "Lighter Points Program") || strings.Contains(payload, "**内容**\\n-") {
		t.Fatalf("payload should fall back content to title: %s", payload)
	}
}
