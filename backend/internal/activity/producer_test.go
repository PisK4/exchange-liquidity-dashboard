package activity

import (
	"context"
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
