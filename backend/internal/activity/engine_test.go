package activity

import (
	"context"
	"testing"
	"time"
)

type fakeEngineStore struct {
	fakeProducerStore
	fakeDeliveryStore
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

func TestEngineRunOnceAcquiresLeaseProducesAndDrains(t *testing.T) {
	store := &fakeEngineStore{leaseOK: true}
	store.fakeProducerStore.events = []ActivityEvent{{
		ID: 1, Platform: "binance", SourceGroup: "cms_article_detail", Title: "Launchpool",
		ContentHash: "hash", DedupeKey: "binance|cms|1", EventVersion: 1, AutoPushAllowed: true, ReviewStatus: ReviewPending,
	}}
	store.fakeDeliveryStore.rows = []DeliveryOutbox{{ID: 11, PayloadJSON: []byte(`{"msg_type":"interactive"}`), Status: DeliveryStatusPending}}
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
