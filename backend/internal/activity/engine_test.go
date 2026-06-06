package activity

import (
	"context"
	"testing"
	"time"
)

type fakeEngineStore struct {
	fakeProducerStore
	fakeDeliveryStore
	fakeIngestionStore
	leaseOK       bool
	acquiredLease string
	releasedLease string
}

func (f *fakeEngineStore) AcquireActivityLease(ctx context.Context, leaseName, ownerID string, ttl time.Duration) (bool, error) {
	f.acquiredLease = leaseName
	return f.leaseOK, nil
}

func (f *fakeEngineStore) ReleaseActivityLease(ctx context.Context, leaseName, ownerID string) error {
	f.releasedLease = leaseName
	return nil
}

func (f *fakeEngineStore) UpsertActivityEvent(ctx context.Context, event ActivityEvent) (int64, bool, error) {
	id, inserted, err := f.fakeIngestionStore.UpsertActivityEvent(ctx, event)
	if err != nil {
		return 0, false, err
	}
	event.ID = id
	f.fakeProducerStore.events = append(f.fakeProducerStore.events, event)
	return id, inserted, nil
}

func (f *fakeEngineStore) InsertActivityOutbox(ctx context.Context, row DeliveryOutbox) error {
	if row.ID == 0 {
		row.ID = int64(len(f.fakeDeliveryStore.rows) + 1)
	}
	f.fakeProducerStore.outbox = append(f.fakeProducerStore.outbox, row)
	f.fakeDeliveryStore.rows = append(f.fakeDeliveryStore.rows, row)
	return nil
}

func TestEngineRunOnceAcquiresLeaseProducesAndDrains(t *testing.T) {
	store := &fakeEngineStore{leaseOK: true}
	store.fakeProducerStore.events = []ActivityEvent{{
		ID: 1, Platform: "binance", SourceGroup: "cms_article_detail", Title: "Launchpool",
		ContentHash: "hash", DedupeKey: "binance|cms|1", EventVersion: 1, AutoPushAllowed: true, ReviewStatus: ReviewPending,
	}}
	engine := NewEngine(store, EngineConfig{
		Enabled:             true,
		OwnerID:             "test-owner",
		WorkerLeaseTTL:      time.Minute,
		WebhookURL:          "",
		DecisionTokenSecret: "secret",
		MaxPerTick:          5,
	})
	sum, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err=%v", err)
	}
	if !sum.LeaseAcquired || sum.Producer.OutboxRows != 1 || sum.Delivery.Disabled != 1 {
		t.Fatalf("summary=%+v", sum)
	}
	if store.acquiredLease != "activity:run_once" || store.releasedLease != "activity:run_once" {
		t.Fatalf("lease acquire=%q release=%q", store.acquiredLease, store.releasedLease)
	}
}

func TestEngineRunOnceSkipsWhenLeaseNotAcquired(t *testing.T) {
	store := &fakeEngineStore{leaseOK: false}
	engine := NewEngine(store, EngineConfig{Enabled: true, OwnerID: "test-owner", WorkerLeaseTTL: time.Minute})
	sum, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err=%v", err)
	}
	if sum.LeaseAcquired || sum.Producer.OutboxRows != 0 {
		t.Fatalf("summary=%+v", sum)
	}
}

func TestEngineRunOnceIngestsBeforeProducingOutbox(t *testing.T) {
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	store := &fakeEngineStore{leaseOK: true}
	payload := []byte(`{"id":"gate-515","title":"Gate Launchpool CTR","summary":"Stake to earn CTR","url":"https://gate.example/launchpool/515","activity_type":"launchpool","symbols":["CTR"]}`)
	engine := NewEngine(store, EngineConfig{
		Enabled:             true,
		OwnerID:             "test-owner",
		WorkerLeaseTTL:      time.Minute,
		WebhookURL:          "",
		DecisionTokenSecret: "secret",
		DashboardBaseURL:    "https://dashboard.example/activity",
		MaxPerTick:          5,
		Sources: []SourceConfig{{
			Platform:        "gate",
			SourceGroup:     "launchpool_project_list",
			SourceURL:       "https://gate.example/api/launchpool",
			FetchMode:       "utls_proxy_json",
			Enabled:         true,
			AutoPushEnabled: true,
		}},
		Fetch: func(ctx context.Context, req FetchRequest) (FetchResult, error) {
			return FetchResult{
				Platform:    req.Platform,
				SourceGroup: req.SourceGroup,
				SourceURL:   req.URL,
				FetchMode:   req.FetchMode,
				Payload:     payload,
				PayloadHash: "payload-hash",
				ContentHash: "content-hash",
				HTTPStatus:  200,
				ContentType: "application/json",
				FetchedAt:   now,
			}, nil
		},
		Parse: func(ctx context.Context, doc RawDocument) ([]ActivityEvent, error) {
			return []ActivityEvent{{
				Platform:         doc.Platform,
				SourceGroup:      doc.SourceGroup,
				SourceExternalID: "gate-515",
				SourceURL:        "https://gate.example/launchpool/515",
				Title:            "Gate Launchpool CTR",
				ActivityType:     "launchpool",
				ContentText:      "Stake to earn CTR",
				ContentHash:      "content-hash",
				DedupeKey:        BuildEventDedupeKey(doc.Platform, doc.SourceGroup, "gate-515", ""),
				AutoPushAllowed:  true,
				ReviewStatus:     ReviewPending,
				EventVersion:     1,
				ParserVersion:    "test-parser",
			}}, nil
		},
	}, WithEngineNow(func() time.Time { return now }))
	sum, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err=%v", err)
	}
	if sum.Ingestion.Events != 1 {
		t.Fatalf("summary=%+v", sum)
	}
	if len(store.fakeProducerStore.events) != 1 {
		t.Fatalf("producer did not see ingested events: %+v", store.fakeProducerStore.events)
	}
	if sum.Producer.OutboxRows != 1 || sum.Delivery.Disabled != 1 {
		t.Fatalf("summary=%+v", sum)
	}
}
