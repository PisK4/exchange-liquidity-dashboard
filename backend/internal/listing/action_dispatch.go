package listing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DispatchDeps wires the action dispatcher's runtime knobs. Now is
// injectable for tests; the rest of the policy lives in stable
// constants so the function stays a pure router from action enum
// to write set.
type DispatchDeps struct {
	Now func() time.Time
}

// DispatchResult is the per-decision summary the callback API
// returns alongside the decision_id.
type DispatchResult struct {
	DispatchID  int64
	WatchlistID int64
	OutboxRows  int
}

// DispatchDecisionAction fans out one DecisionRecord to its
// downstream side effects according to spec §Phase 2:
//
//   - prepare_listing → write action_dispatch + notify listing-ops
//     Lark group (outbox row).
//   - enter_watchlist → write action_dispatch + upsert watchlist
//     entry. No notification (watch is a silent self-service).
//   - contact_mm     → write action_dispatch + notify MM Lark
//     channel.
//   - ignore         → write action_dispatch only. The cooldown
//     gate in ProduceDecisionCards is what reads back this row;
//     no further notification is needed.
//
// All writes are sequenced in the same transactionless flow as the
// rest of the listing pipeline. The dispatcher does NOT roll back
// partial state on error: callers should observe DispatchResult to
// see which side of the action made it through.
func DispatchDecisionAction(ctx context.Context, repo *Repository, dec DecisionRecord, cand Candidate, deps DispatchDeps) (DispatchResult, error) {
	if repo == nil {
		return DispatchResult{}, errors.New("dispatch: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	now := deps.Now()

	dispatchType, targetChannel, notifyEventType := routeAction(dec.Action)
	payload := map[string]any{
		"candidate_id":     dec.CandidateID,
		"decision_id":      dec.ID,
		"action":           dec.Action,
		"operator_open_id": dec.OperatorOpenID,
		"canonical_symbol": cand.CanonicalSymbol,
		"display_symbol":   cand.DisplaySymbol,
		"reason":           dec.Reason,
	}
	payloadJSON, _ := json.Marshal(payload)

	dispatchID, err := repo.InsertActionDispatch(ctx, ActionDispatchRecord{
		CandidateID:   dec.CandidateID,
		DecisionID:    dec.ID,
		DispatchType:  dispatchType,
		TargetChannel: targetChannel,
		Status:        DispatchStatusPending,
		PayloadJSON:   payloadJSON,
	})
	if err != nil {
		return DispatchResult{}, fmt.Errorf("insert action dispatch: %w", err)
	}
	res := DispatchResult{DispatchID: dispatchID}

	switch dec.Action {
	case DecisionActionEnterWatchlist:
		entry := WatchlistEntry{
			CandidateID:      dec.CandidateID,
			CanonicalSymbol:  cand.CanonicalSymbol,
			MarketSurface:    cand.MarketSurface,
			InstrumentKind:   cand.InstrumentKind,
			WatchStatus:      WatchStatusObserving,
			WatchReason:      dec.Reason,
			SourceDecisionID: dec.ID,
			WatchStartedAt:   now,
			PayloadJSON:      payloadJSON,
		}
		id, err := repo.UpsertWatchlist(ctx, entry)
		if err != nil {
			return res, fmt.Errorf("upsert watchlist: %w", err)
		}
		res.WatchlistID = id
	case DecisionActionPrepareListing, DecisionActionContactMM:
		dedupe := fmt.Sprintf("%s|%d|%d", dec.Action, dec.CandidateID, dec.ID)
		if err := repo.insertOutbox(ctx, DeliveryOutbox{
			EventType:     notifyEventType,
			DedupeKey:     dedupe,
			TargetChannel: targetChannel,
			Status:        OutboxStatusPending,
			MaxAttempts:   5,
			PayloadJSON:   payloadJSON,
			NextAttemptAt: ptrTime(now),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return res, fmt.Errorf("insert dispatch outbox: %w", err)
		}
		res.OutboxRows = 1
	case DecisionActionIgnore:
		// Audit only — the cooldown gate in ProduceDecisionCards is
		// the consumer of the row.
	default:
		return res, fmt.Errorf("dispatch: unknown action %q", dec.Action)
	}
	return res, nil
}

// RepoDispatcher adapts a *Repository so it satisfies the
// api.DecisionDispatcher interface: it loads the matching candidate
// from MySQL and forwards to DispatchDecisionAction. Engine wiring
// constructs one of these via NewRepoDispatcher and passes it
// through WithListingDispatch.
type RepoDispatcher struct {
	Repo *Repository
	Now  func() time.Time
}

// NewRepoDispatcher returns an api.DecisionDispatcher-compatible
// adapter. Callers may leave Now nil; the adapter defaults to UTC
// time.Now.
func NewRepoDispatcher(repo *Repository, now func() time.Time) *RepoDispatcher {
	return &RepoDispatcher{Repo: repo, Now: now}
}

// DispatchDecision loads the candidate by id and forwards to
// DispatchDecisionAction. Missing candidate returns an error so the
// callback handler can surface a 500 rather than silently no-op a
// click for a deleted candidate.
func (d *RepoDispatcher) DispatchDecision(ctx context.Context, dec DecisionRecord) (DispatchResult, error) {
	if d == nil || d.Repo == nil {
		return DispatchResult{}, errors.New("dispatch: repo dispatcher not configured")
	}
	cand, err := d.Repo.GetCandidate(ctx, dec.CandidateID)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("get candidate %d: %w", dec.CandidateID, err)
	}
	deps := DispatchDeps{Now: d.Now}
	return DispatchDecisionAction(ctx, d.Repo, dec, cand, deps)
}

func routeAction(action string) (dispatchType, targetChannel, notifyEventType string) {
	switch action {
	case DecisionActionPrepareListing:
		return DispatchTypeListingOps, DispatchChannelLarkListingOps, DeliveryEventListingActionListingOps
	case DecisionActionEnterWatchlist:
		return DispatchTypeWatchlist, DispatchChannelInternal, ""
	case DecisionActionContactMM:
		return DispatchTypeMM, DispatchChannelLarkMM, DeliveryEventListingActionContactMM
	case DecisionActionIgnore:
		return DispatchTypeIgnore, DispatchChannelInternal, ""
	}
	return action, DispatchChannelInternal, ""
}
