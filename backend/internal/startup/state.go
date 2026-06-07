package startup

import (
	"sync"
	"time"
)

const (
	StateDisabled  = "disabled"
	StatePending   = "pending"
	StateStarting  = "starting"
	StateRunning   = "running"
	StateScheduled = "scheduled"
	StateReady     = "ready"
	StateComplete  = "complete"
	StateFailed    = "failed"
	StateSkipped   = "skipped"
	StateTimeout   = "timeout"
	StatePartial   = "partial"

	ReadinessPolicyCachedOrCollected = "cached_or_collected"
)

// WarmCacheSummary captures whether the process restored enough persisted
// dashboard state to serve traffic while live collectors warm in the background.
type WarmCacheSummary struct {
	PlatformSnapshots    int  `json:"platform_snapshots"`
	VolumeSnapshots      int  `json:"volume_snapshots"`
	Top30Rows            int  `json:"top30_rows"`
	CollectionStatusRows int  `json:"collection_status_rows"`
	HasUsableData        bool `json:"has_usable_data"`
}

type TaskSnapshot struct {
	State       string     `json:"state"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastError   string     `json:"last_error,omitempty"`
	DurationMS  int64      `json:"duration_ms,omitempty"`
}

type ProviderSnapshot struct {
	Enabled        bool       `json:"enabled"`
	State          string     `json:"state"`
	ExpectedCount  int        `json:"expected_count,omitempty"`
	ReadyCount     int        `json:"ready_count,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastError      string     `json:"last_error,omitempty"`
	SoftDependency bool       `json:"soft_dependency"`
}

type Snapshot struct {
	Role              string                  `json:"role"`
	StartedAt         time.Time               `json:"started_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
	Phase             string                  `json:"phase"`
	ReadinessPolicy   string                  `json:"readiness_policy"`
	ConfigLoaded      bool                    `json:"config_loaded"`
	MySQL             TaskSnapshot            `json:"mysql"`
	Migrations        TaskSnapshot            `json:"migrations"`
	LatestSnapshots   TaskSnapshot            `json:"latest_snapshots"`
	WarmCache         WarmCacheSummary        `json:"warm_cache"`
	APIListening      bool                    `json:"api_listening"`
	LighterWS         ProviderSnapshot        `json:"lighter_ws"`
	InitialCollection TaskSnapshot            `json:"initial_collection"`
	Workers           map[string]TaskSnapshot `json:"workers"`
}

type ReadinessDecision struct {
	OK                     bool             `json:"ok"`
	Policy                 string           `json:"policy"`
	Reason                 string           `json:"reason"`
	Phase                  string           `json:"phase"`
	WarmCache              WarmCacheSummary `json:"warm_cache"`
	InitialCollectionState string           `json:"initial_collection_state"`
}

type State struct {
	mu   sync.RWMutex
	snap Snapshot
}

func New(role string) *State {
	now := time.Now().UTC()
	return &State{snap: Snapshot{
		Role:            role,
		StartedAt:       now,
		UpdatedAt:       now,
		Phase:           "starting",
		ReadinessPolicy: ReadinessPolicyCachedOrCollected,
		MySQL:           newTask(StatePending, now),
		Migrations:      newTask(StatePending, now),
		LatestSnapshots: newTask(StatePending, now),
		LighterWS: ProviderSnapshot{
			Enabled:        false,
			State:          StateDisabled,
			UpdatedAt:      now,
			SoftDependency: true,
		},
		InitialCollection: newTask(StatePending, now),
		Workers:           map[string]TaskSnapshot{},
	}}
}

func newTask(state string, now time.Time) TaskSnapshot {
	return TaskSnapshot{State: state, UpdatedAt: now}
}

func (s *State) MarkConfigLoaded() {
	s.update(func(snap *Snapshot, now time.Time) { snap.ConfigLoaded = true })
}

func (s *State) MarkMySQLNotConfigured() {
	s.update(func(snap *Snapshot, now time.Time) { snap.MySQL = TaskSnapshot{State: StateSkipped, UpdatedAt: now} })
}

func (s *State) MarkMySQLConnected() {
	s.update(func(snap *Snapshot, now time.Time) { snap.MySQL = completedTask(snap.MySQL, now, nil) })
}

func (s *State) MarkMigrationsSkipped() {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.Migrations = TaskSnapshot{State: StateSkipped, UpdatedAt: now}
	})
}

func (s *State) MarkMigrationsApplied(err error) {
	s.update(func(snap *Snapshot, now time.Time) { snap.Migrations = completedTask(snap.Migrations, now, err) })
}

func (s *State) MarkLatestSnapshotsSkipped() {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.LatestSnapshots = TaskSnapshot{State: StateSkipped, UpdatedAt: now}
	})
}

func (s *State) MarkLatestSnapshotsLoading() {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.LatestSnapshots.State = StateRunning
		snap.LatestSnapshots.StartedAt = ptrTime(now)
		snap.LatestSnapshots.CompletedAt = nil
		snap.LatestSnapshots.UpdatedAt = now
		snap.LatestSnapshots.LastError = ""
		snap.LatestSnapshots.DurationMS = 0
	})
}

func (s *State) MarkLatestSnapshotsLoaded(err error) {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.LatestSnapshots = completedTask(snap.LatestSnapshots, now, err)
	})
}

func (s *State) SetWarmCache(summary WarmCacheSummary) {
	s.update(func(snap *Snapshot, now time.Time) { snap.WarmCache = summary })
}

func (s *State) MarkAPIListening() {
	s.update(func(snap *Snapshot, now time.Time) { snap.APIListening = true })
}

func (s *State) MarkLighterStarted(expected int) {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.LighterWS.Enabled = true
		snap.LighterWS.State = StateStarting
		snap.LighterWS.ExpectedCount = expected
		snap.LighterWS.ReadyCount = 0
		snap.LighterWS.StartedAt = ptrTime(now)
		snap.LighterWS.UpdatedAt = now
		snap.LighterWS.LastError = ""
		snap.LighterWS.SoftDependency = true
	})
}

func (s *State) MarkLighterProgress(ready, expected int) {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.LighterWS.Enabled = true
		snap.LighterWS.ExpectedCount = expected
		snap.LighterWS.ReadyCount = ready
		if expected > 0 && ready == expected {
			snap.LighterWS.State = StateReady
		} else if ready > 0 {
			snap.LighterWS.State = StatePartial
		} else {
			snap.LighterWS.State = StateStarting
		}
		snap.LighterWS.UpdatedAt = now
	})
}

func (s *State) MarkLighterTimeout(ready, expected int) {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.LighterWS.Enabled = true
		snap.LighterWS.ExpectedCount = expected
		snap.LighterWS.ReadyCount = ready
		snap.LighterWS.State = StateTimeout
		if ready > 0 {
			snap.LighterWS.State = StatePartial
		}
		snap.LighterWS.LastError = "lighter ws not fully ready before startup warmup"
		snap.LighterWS.UpdatedAt = now
	})
}

func (s *State) MarkInitialCollectionStarted() {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.InitialCollection.State = StateRunning
		snap.InitialCollection.StartedAt = ptrTime(now)
		snap.InitialCollection.CompletedAt = nil
		snap.InitialCollection.UpdatedAt = now
		snap.InitialCollection.LastError = ""
		snap.InitialCollection.DurationMS = 0
	})
}

func (s *State) MarkInitialCollectionCompleted(err error) {
	s.update(func(snap *Snapshot, now time.Time) {
		snap.InitialCollection = completedTask(snap.InitialCollection, now, err)
	})
}

func (s *State) MarkWorker(name, state string, err error) {
	if name == "" {
		return
	}
	s.update(func(snap *Snapshot, now time.Time) {
		task := snap.Workers[name]
		if task.State == "" {
			task = newTask(StatePending, now)
		}
		task.State = state
		if task.StartedAt == nil && (state == StateStarting || state == StateRunning || state == StateScheduled) {
			task.StartedAt = ptrTime(now)
		}
		if state == StateComplete || state == StateFailed || state == StateSkipped || state == StateDisabled {
			task.CompletedAt = ptrTime(now)
		}
		task.UpdatedAt = now
		if err != nil {
			task.LastError = err.Error()
		} else if state != StateFailed {
			task.LastError = ""
		}
		snap.Workers[name] = task
	})
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	out := cloneSnapshot(s.snap)
	s.mu.RUnlock()
	out.Phase = computePhase(out)
	return out
}

func (s *State) Readiness() ReadinessDecision { return s.Snapshot().ReadinessDecision() }

func (snap Snapshot) ReadinessDecision() ReadinessDecision {
	decision := ReadinessDecision{
		OK:                     true,
		Policy:                 snap.ReadinessPolicy,
		Reason:                 "startup_gate_not_required",
		Phase:                  computePhase(snap),
		WarmCache:              snap.WarmCache,
		InitialCollectionState: snap.InitialCollection.State,
	}
	if !requiresCollectorStartupGate(snap.Role) {
		return decision
	}
	if snap.WarmCache.HasUsableData {
		decision.Reason = "warm_cache_available"
		return decision
	}
	if taskTerminal(snap.InitialCollection.State) {
		decision.Reason = "initial_collection_completed"
		return decision
	}
	decision.OK = false
	decision.Reason = "collector_warming_up"
	return decision
}

func (s *State) update(fn func(*Snapshot, time.Time)) {
	now := time.Now().UTC()
	s.mu.Lock()
	fn(&s.snap, now)
	s.snap.UpdatedAt = now
	s.snap.Phase = computePhase(s.snap)
	s.mu.Unlock()
}

func completedTask(task TaskSnapshot, now time.Time, err error) TaskSnapshot {
	if task.StartedAt == nil {
		task.StartedAt = ptrTime(now)
	}
	task.CompletedAt = ptrTime(now)
	task.UpdatedAt = now
	if err != nil {
		task.State = StateFailed
		task.LastError = err.Error()
	} else {
		task.State = StateComplete
		task.LastError = ""
	}
	if task.StartedAt != nil {
		task.DurationMS = now.Sub(*task.StartedAt).Milliseconds()
	}
	return task
}

func computePhase(snap Snapshot) string {
	if !snap.APIListening {
		return "starting"
	}
	if requiresCollectorStartupGate(snap.Role) {
		if snap.WarmCache.HasUsableData && !taskTerminal(snap.InitialCollection.State) {
			return "serving_warm_cache"
		}
		if !snap.WarmCache.HasUsableData && !taskTerminal(snap.InitialCollection.State) {
			return "collector_warming_up"
		}
	}
	return "ready"
}

func requiresCollectorStartupGate(role string) bool { return role == "all" }

func taskTerminal(state string) bool {
	switch state {
	case StateComplete, StateFailed, StateSkipped:
		return true
	default:
		return false
	}
}

func ptrTime(t time.Time) *time.Time {
	out := t
	return &out
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.MySQL = cloneTask(in.MySQL)
	out.Migrations = cloneTask(in.Migrations)
	out.LatestSnapshots = cloneTask(in.LatestSnapshots)
	out.LighterWS = cloneProvider(in.LighterWS)
	out.InitialCollection = cloneTask(in.InitialCollection)
	out.Workers = make(map[string]TaskSnapshot, len(in.Workers))
	for k, v := range in.Workers {
		out.Workers[k] = cloneTask(v)
	}
	return out
}

func cloneProvider(in ProviderSnapshot) ProviderSnapshot {
	out := in
	if in.StartedAt != nil {
		out.StartedAt = ptrTime(*in.StartedAt)
	}
	return out
}

func cloneTask(in TaskSnapshot) TaskSnapshot {
	out := in
	if in.StartedAt != nil {
		out.StartedAt = ptrTime(*in.StartedAt)
	}
	if in.CompletedAt != nil {
		out.CompletedAt = ptrTime(*in.CompletedAt)
	}
	return out
}
