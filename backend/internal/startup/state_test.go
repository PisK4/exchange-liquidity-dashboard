package startup

import (
	"errors"
	"testing"
)

func TestReadinessUsesWarmCacheBeforeInitialCollection(t *testing.T) {
	state := New("all")
	state.MarkAPIListening()
	decision := state.Readiness()
	if decision.OK || decision.Reason != "collector_warming_up" {
		t.Fatalf("readiness before cache/collection = %+v", decision)
	}

	state.SetWarmCache(WarmCacheSummary{PlatformSnapshots: 1, HasUsableData: true})
	decision = state.Readiness()
	if !decision.OK || decision.Reason != "warm_cache_available" {
		t.Fatalf("readiness with warm cache = %+v", decision)
	}
	if got := state.Snapshot().Phase; got != "serving_warm_cache" {
		t.Fatalf("phase = %q, want serving_warm_cache", got)
	}
}

func TestReadinessAllowsTrafficAfterInitialCollectionCompletes(t *testing.T) {
	state := New("all")
	state.MarkAPIListening()
	state.MarkInitialCollectionStarted()
	if decision := state.Readiness(); decision.OK {
		t.Fatalf("running initial collection without cache must not be ready: %+v", decision)
	}

	state.MarkInitialCollectionCompleted(errors.New("partial upstream errors"))
	decision := state.Readiness()
	if !decision.OK || decision.Reason != "initial_collection_completed" {
		t.Fatalf("readiness after collection terminal state = %+v", decision)
	}
	if got := state.Snapshot().InitialCollection.State; got != StateFailed {
		t.Fatalf("initial collection state = %q, want failed", got)
	}
}

func TestSnapshotIsIsolatedFromCallerMutation(t *testing.T) {
	state := New("all")
	state.MarkWorker("listing", StateRunning, nil)

	snap := state.Snapshot()
	snap.Workers["listing"] = TaskSnapshot{State: StateFailed}
	if got := state.Snapshot().Workers["listing"].State; got != StateRunning {
		t.Fatalf("snapshot mutation leaked into state: %q", got)
	}
}

func TestLighterProgressAndTimeoutAreSoftDependency(t *testing.T) {
	state := New("all")
	state.MarkLighterStarted(3)
	state.MarkLighterProgress(1, 3)
	if got := state.Snapshot().LighterWS.State; got != StatePartial {
		t.Fatalf("lighter state = %q, want partial", got)
	}
	state.MarkLighterTimeout(0, 3)
	snap := state.Snapshot()
	if snap.LighterWS.State != StateTimeout || !snap.LighterWS.SoftDependency {
		t.Fatalf("lighter timeout snapshot = %+v", snap.LighterWS)
	}
}
