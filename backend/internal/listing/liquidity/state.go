package liquidity

import (
	"fmt"
	"time"
)

// Action is the decision output of the state machine: what to do
// with one (kind, canonical) pair after evaluating this tick.
type Action int

const (
	// ActionSilent means: update last_evaluated_at + clear_streak,
	// but DO NOT enqueue a Lark push or change status.
	ActionSilent Action = iota
	// ActionFirstTrigger means: this is the first time (or the
	// first time after a clear) we see the trigger. Insert / promote
	// state row to active, bump severity_seq, and push a "first
	// trigger" card.
	ActionFirstTrigger
	// ActionReissue means: the alert has been active for
	// ≥ cfg.ReissueInterval since the last push. Push a "持续告警"
	// reissue card and refresh last_pushed_at.
	ActionReissue
	// ActionClear means: the trigger has been false for at least
	// cfg.ClearConsecutive consecutive evaluations. Flip the state
	// to cleared and push a "恢复" card.
	ActionClear
)

// Phase strings used in dedupe keys and outbox payload.
const (
	PhaseFirst   = "first"
	PhaseReissue = "reissue"
	PhaseClear   = "clear"
)

// ActionDecision is the per-(kind, canonical) decision payload. The
// caller passes NewState directly into UpsertAlertState; if Action is
// ActionSilent the caller still updates LastEvaluatedAt + ClearStreak
// so the state table reflects this tick.
type ActionDecision struct {
	Action    Action
	Phase     string // PhaseFirst / PhaseReissue / PhaseClear; "" for silent
	DedupeKey string // only populated for non-silent actions
	NewState  AlertState
}

// DecideAction is the pure state-machine. prev may be the zero
// AlertState (meaning "no row yet"); triggered indicates whether the
// candidate's threshold was crossed this tick. now is the evaluation
// timestamp.
//
// Contract:
//   - The function only returns DedupeKey + Phase for non-silent
//     actions. Callers MUST treat an empty DedupeKey as "do not
//     enqueue outbox row".
//   - NewState always reflects the post-tick state, including the
//     silent path (LastEvaluatedAt + ClearStreak update). The caller
//     is responsible for the UPSERT.
//   - cfg fields with non-positive values fall back to spec defaults
//     so DecideAction stays callable from unit tests with a partial
//     Config.
func DecideAction(prev AlertState, triggered bool, kind AlertKind, canonical string, cfg Config, now time.Time) ActionDecision {
	reissue := cfg.ReissueInterval
	if reissue <= 0 {
		reissue = 6 * time.Hour
	}
	clearConsecutive := cfg.ClearConsecutive
	if clearConsecutive <= 0 {
		clearConsecutive = 3
	}

	next := prev
	next.Kind = kind
	next.Canonical = canonical
	next.LastEvaluatedAt = now

	previouslyActive := prev.Status == StatusActive

	switch {
	case !previouslyActive && triggered:
		next.Status = StatusActive
		next.SeveritySeq = prev.SeveritySeq + 1
		if next.SeveritySeq <= 0 {
			next.SeveritySeq = 1
		}
		next.ReissueCount = 0
		next.ClearStreak = 0
		next.FirstTriggeredAt = now
		next.LastPushedAt = now
		return ActionDecision{
			Action:    ActionFirstTrigger,
			Phase:     PhaseFirst,
			DedupeKey: buildDedupeKey(kind, canonical, next.SeveritySeq, 0, PhaseFirst),
			NewState:  next,
		}

	case previouslyActive && triggered:
		// Active & still triggered. Either silent (cooldown) or reissue.
		next.ClearStreak = 0
		elapsed := now.Sub(prev.LastPushedAt)
		if !prev.LastPushedAt.IsZero() && elapsed < reissue {
			return ActionDecision{Action: ActionSilent, NewState: next}
		}
		next.ReissueCount = prev.ReissueCount + 1
		if next.ReissueCount <= 0 {
			next.ReissueCount = 1
		}
		next.LastPushedAt = now
		return ActionDecision{
			Action:    ActionReissue,
			Phase:     PhaseReissue,
			DedupeKey: buildDedupeKey(kind, canonical, next.SeveritySeq, next.ReissueCount, PhaseReissue),
			NewState:  next,
		}

	case previouslyActive && !triggered:
		next.ClearStreak = prev.ClearStreak + 1
		if next.ClearStreak >= clearConsecutive {
			next.Status = StatusCleared
			next.LastPushedAt = now
			return ActionDecision{
				Action:    ActionClear,
				Phase:     PhaseClear,
				DedupeKey: buildDedupeKey(kind, canonical, prev.SeveritySeq, 0, PhaseClear),
				NewState:  next,
			}
		}
		return ActionDecision{Action: ActionSilent, NewState: next}

	default:
		// !previouslyActive && !triggered → still silent. Reset clear
		// streak so cleared rows do not accumulate stale counters.
		next.ClearStreak = 0
		return ActionDecision{Action: ActionSilent, NewState: next}
	}
}

// buildDedupeKey shapes the outbox dedupe_key for one (kind,
// canonical, seq, phase) tuple. Spec §3.4 contract:
//
//	first   → "<kind>|<canonical>|seq<N>|first"
//	reissue → "<kind>|<canonical>|seq<N>|reissue<M>"
//	clear   → "<kind>|<canonical>|seq<N>|clear"
func buildDedupeKey(kind AlertKind, canonical string, seq int, reissueIdx int, phase string) string {
	switch phase {
	case PhaseFirst:
		return fmt.Sprintf("%s|%s|seq%d|first", kind, canonical, seq)
	case PhaseReissue:
		return fmt.Sprintf("%s|%s|seq%d|reissue%d", kind, canonical, seq, reissueIdx)
	case PhaseClear:
		return fmt.Sprintf("%s|%s|seq%d|clear", kind, canonical, seq)
	}
	return ""
}
