package listing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
)

// ErrFusionFailClosed is returned when fusion cannot run because the
// listed universe is unavailable. The engine logs this as a normal
// outcome and increments source health, but no candidates are
// produced.
var ErrFusionFailClosed = errors.New("listing fusion fail-closed")

// FusionDeps wires the moving parts fusion needs. Tests inject fake
// universe loaders; production wiring uses
// config.LoadListedUniverse from the dashboard runtime path.
type FusionDeps struct {
	LoadUniverse func() (*config.ListedUniverse, error)
	Now          func() time.Time
	// SignalBatchSize bounds the number of unfused signals processed
	// per run; defaults to 500.
	SignalBatchSize int
	// HistoricalListingGracePeriod is the freshness window used to
	// distinguish exchange-side new listings from late discovery of
	// markets that were already active long before we observed them.
	HistoricalListingGracePeriod time.Duration
}

// FusionResult is what one fusion run produces. The engine writes
// these counts onto its RunSummary.
type FusionResult struct {
	Signals    int
	Candidates int
	// SkippedAggregator counts signals whose signal_type is an
	// aggregator marker (top30_hot_gap / top30_divergence) rather
	// than a candidate-bearing source (instrument_diff /
	// announcement_listing). Such signals are marked fused so the
	// next tick ignores them, but they NEVER produce a candidate.
	SkippedAggregator int
	// SkippedObservationOnly counts instrument_diff signals whose
	// signal_subtype is observation-only (metadata_changed,
	// delisted, status_changed→{paused,inactive,delisted}). They are
	// still marked fused so the unfused queue drains, but they do
	// NOT upsert into t_listing_candidate. See
	// isCandidatePromotingInstrumentDiff and the 2026-06-01 incident
	// where Gate/BingX/Lighter raw_json contained time-varying
	// market data fields that flipped metadata_changed every tick
	// and surfaced as bogus "New Perp Listing Detected" Lark cards
	// for already-listed tokens like GLM.
	SkippedObservationOnly int
	// SkippedHistorical counts candidate-shaped signals whose exchange-side
	// listing timestamp is older than the configured freshness gate. They are
	// preserved in t_listing_signal_observation for audit/catalog use but are
	// marked fused without creating or updating candidates.
	SkippedHistorical int
	FailClosed        string
}

// isCandidateBearingSignal reports whether a signal type carries
// real market-existence evidence that should fuse into a candidate.
// Top30 signals are aggregator markers used for delivery dedupe and
// MUST NOT generate candidates — they would surface as mojibake
// "CEX_ONLY" / "DEX_ONLY" rows that pollute t_listing_candidate
// and ultimately the Lark decision-card group.
func isCandidateBearingSignal(signalType string) bool {
	switch signalType {
	case SignalInstrumentDiff, SignalAnnouncementListing:
		return true
	}
	return false
}

// isCandidatePromotingInstrumentDiff reports whether an
// instrument_diff signal's subtype should elevate to a
// t_listing_candidate row (and thereby a Lark decision card).
//
// Only events that semantically describe "a new perpetual / spot
// listing has appeared on this venue" qualify:
//   - new_symbol            (instrument first observed)
//   - listing_time_changed  (scheduled listing time moved)
//   - status_changed where status_to ∈ {active, pre_listing}
//
// All other subtypes (metadata_changed, delisted, status_changed →
// paused/inactive/delisted) are observation-only: they may be useful
// for forensic analysis and DB-first CatalogResolver reads, but they
// are NOT "新上市候选" decisions and must not surface in the Lark
// listing group. See the 2026-06-01 hash-noise incident root-cause
// for why this gate exists.
func isCandidatePromotingInstrumentDiff(s SignalObservation) bool {
	switch s.SignalSubtype {
	case DiffNewSymbol, DiffListingTimeChanged:
		return true
	case DiffStatusChanged:
		return statusChangedPromotes(s.PayloadJSON)
	}
	return false
}

// statusChangedPromotes inspects the diff payload's status_to field.
// Only transitions INTO an active / pre-listing state qualify as a
// listing decision; transitions OUT (active → paused / delisted) are
// the opposite of a listing event.
func statusChangedPromotes(payload json.RawMessage) bool {
	if len(payload) == 0 {
		return false
	}
	var p struct {
		StatusTo string `json:"status_to"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return false
	}
	switch p.StatusTo {
	case "active", "pre_listing":
		return true
	}
	return false
}

// FuseSignals reads the next batch of unfused signals and groups them
// by (canonical_symbol, market_surface, instrument_kind). Per group
// the function derives evidence kind, runs the scoring gate, upserts
// the candidate, links every signal, and marks each signal fused.
//
// Fail-closed rules:
//  1. Universe failed to load or no edgeX base assets => skip the
//     entire run, return ErrFusionFailClosed.
//
// The function intentionally does not modify config.LoadListedUniverse;
// callers thread their own loader so the Top30 collector keeps its
// existing fail-open behaviour while listing fusion is fail-closed.
func FuseSignals(ctx context.Context, repo *Repository, deps FusionDeps) (FusionResult, error) {
	if repo == nil {
		return FusionResult{}, errors.New("listing fusion: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.SignalBatchSize <= 0 {
		deps.SignalBatchSize = 500
	}
	if deps.HistoricalListingGracePeriod <= 0 {
		deps.HistoricalListingGracePeriod = 48 * time.Hour
	}
	universe, err := deps.LoadUniverse()
	if err != nil {
		return FusionResult{FailClosed: "universe_load_error"}, fmt.Errorf("%w: %v", ErrFusionFailClosed, err)
	}
	if universe == nil || !universe.Loaded() || len(universe.BaseAssets("edgeX")) == 0 {
		return FusionResult{FailClosed: "universe_not_loaded"}, ErrFusionFailClosed
	}

	signals, err := repo.ListUnfusedSignals(ctx, deps.SignalBatchSize)
	if err != nil {
		return FusionResult{}, fmt.Errorf("list unfused signals: %w", err)
	}
	if len(signals) == 0 {
		return FusionResult{}, nil
	}

	type groupKey struct{ canonical, surface, kind string }
	type group struct {
		key                groupKey
		signals            []SignalObservation
		display            string
		firstSeen          time.Time
		lastSeen           time.Time
		platforms          map[string]struct{}
		scoringPlatforms   map[string]struct{}
		hasFreshInstrument bool
		hasAnnouncement    bool
	}
	groups := make(map[groupKey]*group)
	order := make([]groupKey, 0)
	aggregatorIDs := make([]int64, 0)
	observationOnlyIDs := make([]int64, 0)
	historicalIDs := make([]int64, 0)
	for _, s := range signals {
		if !isCandidateBearingSignal(s.SignalType) {
			aggregatorIDs = append(aggregatorIDs, s.ID)
			continue
		}
		// Subtype gate: instrument_diff signals whose subtype does
		// not semantically describe "new listing" are observation-
		// only. We still mark them fused so the unfused queue drains
		// but they do NOT upsert into t_listing_candidate.
		if s.SignalType == SignalInstrumentDiff && !isCandidatePromotingInstrumentDiff(s) {
			observationOnlyIDs = append(observationOnlyIDs, s.ID)
			continue
		}
		if isNonListingTargetSignal(s) {
			observationOnlyIDs = append(observationOnlyIDs, s.ID)
			continue
		}
		if isHistoricalListingSignal(s, deps.HistoricalListingGracePeriod) {
			historicalIDs = append(historicalIDs, s.ID)
			continue
		}
		key := groupKey{strings.ToUpper(s.CanonicalSymbol), s.MarketSurface, s.InstrumentKind}
		g, ok := groups[key]
		if !ok {
			g = &group{key: key, platforms: make(map[string]struct{}), scoringPlatforms: make(map[string]struct{})}
			groups[key] = g
			order = append(order, key)
		}
		g.signals = append(g.signals, s)
		if g.display == "" {
			g.display = s.DisplaySymbol
		}
		if g.firstSeen.IsZero() || s.ObservedAt.Before(g.firstSeen) {
			g.firstSeen = s.ObservedAt
		}
		if s.ObservedAt.After(g.lastSeen) {
			g.lastSeen = s.ObservedAt
		}
		platform := strings.ToLower(s.SourcePlatform)
		if platform != "" {
			g.platforms[platform] = struct{}{}
		}
		switch s.SignalType {
		case SignalInstrumentDiff:
			g.hasFreshInstrument = true
			if platform != "" {
				g.scoringPlatforms[platform] = struct{}{}
			}
		case SignalAnnouncementListing:
			g.hasAnnouncement = true
			if platform != "" {
				g.scoringPlatforms[platform] = struct{}{}
			}
		}
	}

	now := deps.Now()
	out := FusionResult{}
	for _, key := range order {
		g := groups[key]
		evidence := EvidenceInstrumentDiffOnly
		switch {
		case g.hasAnnouncement && g.hasFreshInstrument:
			evidence = EvidenceAnnouncementAndAPI
		case g.hasAnnouncement && !g.hasFreshInstrument:
			evidence = EvidenceAnnouncementPendingAPI
		}
		platforms := keysSorted(g.scoringPlatforms)
		if len(platforms) == 0 {
			platforms = keysSorted(g.platforms)
		}
		isListed := false
		for _, base := range universe.BaseAssets("edgeX") {
			if strings.EqualFold(base, g.key.canonical) {
				isListed = true
				break
			}
		}
		score := ScoreCandidate(ScoreInput{
			Platforms:      platforms,
			EvidenceKind:   evidence,
			EdgexListed:    isListed,
			MarketSurface:  g.key.surface,
			InstrumentKind: g.key.kind,
		})
		lifecycleStatus, lifecycleLabel := deriveLifecycle(evidence, isListed, score.LifecycleStatus, score.LifecycleStatusLabel)
		upsert := CandidateUpsert{
			CanonicalSymbol:      g.key.canonical,
			DisplaySymbol:        g.display,
			MarketSurface:        g.key.surface,
			InstrumentKind:       g.key.kind,
			LifecycleStatus:      lifecycleStatus,
			LifecycleStatusLabel: lifecycleLabel,
			EvidenceKind:         evidence,
			ConfidenceLevel:      score.ConfidenceLevel,
			BusinessScore:        score.BusinessScore,
			BusinessScoreVersion: score.BusinessScoreVersion,
			Recommendation:       score.Recommendation,
			RecommendationLabel:  score.RecommendationLabel,
			SourcePlatforms:      platforms,
			ObservedAt:           g.lastSeen,
		}
		candidateID, err := repo.UpsertCandidate(ctx, upsert)
		if err != nil {
			return out, fmt.Errorf("upsert candidate %s: %w", g.key.canonical, err)
		}
		for _, s := range g.signals {
			if err := repo.LinkCandidateSignal(ctx, candidateID, s.ID); err != nil {
				return out, fmt.Errorf("link candidate %d signal %d: %w", candidateID, s.ID, err)
			}
		}
		for _, s := range g.signals {
			if err := repo.MarkSignalFused(ctx, s.ID, now); err != nil {
				return out, fmt.Errorf("mark signal fused %d: %w", s.ID, err)
			}
		}
		out.Candidates++
		out.Signals += len(g.signals)
	}
	// Drain aggregator signals: they must NOT remain in fused_at IS
	// NULL state or the next tick re-reads + re-skips them forever,
	// growing the unfused queue and slowing the listing engine.
	for _, id := range aggregatorIDs {
		if err := repo.MarkSignalFused(ctx, id, now); err != nil {
			return out, fmt.Errorf("mark aggregator signal fused %d: %w", id, err)
		}
		out.SkippedAggregator++
	}
	// Drain observation-only instrument_diff signals (metadata_changed,
	// delisted, status_changed→paused, etc.). Same reasoning as
	// aggregator drain: mark fused or the unfused queue grows
	// unboundedly. These signals are valuable for forensic SQL but
	// must not surface as Lark decision cards.
	for _, id := range observationOnlyIDs {
		if err := repo.MarkSignalFused(ctx, id, now); err != nil {
			return out, fmt.Errorf("mark observation-only signal fused %d: %w", id, err)
		}
		out.SkippedObservationOnly++
	}
	// Drain historical listing evidence separately so operators can distinguish
	// stale exchange launch/open times from generic observation-only diffs.
	for _, id := range historicalIDs {
		if err := repo.MarkSignalFused(ctx, id, now); err != nil {
			return out, fmt.Errorf("mark historical listing signal fused %d: %w", id, err)
		}
		out.SkippedHistorical++
	}
	return out, nil
}

func isHistoricalListingSignal(s SignalObservation, grace time.Duration) bool {
	if grace <= 0 || s.ListingTimeTS == nil || s.ObservedAt.IsZero() {
		return false
	}
	if !isCandidatePromotingSignal(s) {
		return false
	}
	return !s.ListingTimeTS.After(s.ObservedAt.Add(-grace))
}

func isCandidatePromotingSignal(s SignalObservation) bool {
	switch s.SignalType {
	case SignalInstrumentDiff:
		return isCandidatePromotingInstrumentDiff(s)
	case SignalAnnouncementListing:
		return true
	}
	return false
}

// deriveLifecycle picks the lifecycle_status enum for the candidate
// row, preferring the score-derived AlreadyListed override when
// edgeX already lists the asset.
func deriveLifecycle(evidence string, edgexListed bool, scoreLifecycle, scoreLabel string) (string, string) {
	if scoreLifecycle != "" {
		return scoreLifecycle, scoreLabel
	}
	if edgexListed {
		return LifecycleAlreadyListed, LifecycleStatusLabels[LifecycleAlreadyListed]
	}
	switch evidence {
	case EvidenceAnnouncementAndAPI:
		return LifecycleConfirmedListingCandidate, LifecycleStatusLabels[LifecycleConfirmedListingCandidate]
	case EvidenceAnnouncementPendingAPI:
		return LifecycleAnnouncedPendingAPI, LifecycleStatusLabels[LifecycleAnnouncedPendingAPI]
	case EvidenceInstrumentDiffOnly:
		return LifecycleAPIDetectedNoAnnouncement, LifecycleStatusLabels[LifecycleAPIDetectedNoAnnouncement]
	}
	return LifecycleObserved, LifecycleStatusLabels[LifecycleObserved]
}

func keysSorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// deterministic order across runs and across stable assertions
	if len(out) > 1 {
		// inline sort.Strings via manual swap to avoid extra imports
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j-1] > out[j]; j-- {
				out[j-1], out[j] = out[j], out[j-1]
			}
		}
	}
	return out
}
