package listing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"edgex-dashboard/backend/internal/listing/instrument"
)

// InstrumentSource describes one (platform, market_type) endpoint
// pair the listing agent polls. The Fetch closure isolates the HTTP
// + normalisation work so tests can inject fixture instruments and
// the engine can compose real adapters at wiring time.
type InstrumentSource struct {
	Platform   string
	MarketType string
	SourceURL  string
	SourceKey  string
	Fetch      func(ctx context.Context) ([]instrument.NormalizedInstrument, error)
}

// InstrumentPollResult is a per-source summary the engine attaches to
// its tick log so operators can spot a source that suddenly stops
// emitting or a baseline that has not yet ratcheted on.
type InstrumentPollResult struct {
	Platform          string
	MarketType        string
	Baseline          bool
	Fetched           int
	SnapshotsUpserted int
	SignalsEmitted    int
	DiffSubtypes      map[string]int
}

// InstrumentPollDeps wires the moving parts the driver needs. The
// only required field is Now; tests pass a fixed clock so the
// signal observed_at and the snapshot last_seen_at are deterministic.
type InstrumentPollDeps struct {
	Now func() time.Time
}

// RunInstrumentPoll executes one full pass over the given source.
//
// Contract (see 2026-05-29-listing-agent.md §Phase 1):
//   - Cold start: HasInstrumentBaseline=false → write every fetched
//     instrument as a baseline snapshot, emit ZERO signals, return
//     Baseline=true. This prevents the cold-start firehose of
//     misclassified "new" listings.
//   - Warm path: every diff event produced by instrument.Diff becomes
//     one t_listing_signal_observation row via repo.InsertSignal,
//     keyed by a deterministic fingerprint so retries are idempotent.
//   - Fetch error: returned unchanged so the calling source-health
//     wrapper (Phase 1.3) can decide what to do; no DB writes happen
//     in the snapshot/signal tables when the upstream API failed.
//
// The driver does NOT manage t_listing_source_state on its own — that
// is the responsibility of the source-health wrapper Phase 1.3 will
// add, so each layer has a single concern and the unit tests for
// either layer stay narrow.
func RunInstrumentPoll(ctx context.Context, repo *Repository, src InstrumentSource, deps InstrumentPollDeps) (InstrumentPollResult, error) {
	if repo == nil {
		return InstrumentPollResult{}, errors.New("instrument poll: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if src.Platform == "" || src.MarketType == "" {
		return InstrumentPollResult{}, errors.New("instrument poll: source missing platform/market_type")
	}

	res := InstrumentPollResult{
		Platform:     src.Platform,
		MarketType:   src.MarketType,
		DiffSubtypes: map[string]int{},
	}

	hasBaseline, err := repo.HasInstrumentBaseline(ctx, src.Platform, src.MarketType)
	if err != nil {
		return res, fmt.Errorf("has baseline: %w", err)
	}
	res.Baseline = !hasBaseline

	instruments, err := src.Fetch(ctx)
	if err != nil {
		return res, fmt.Errorf("fetch %s/%s: %w", src.Platform, src.MarketType, err)
	}
	res.Fetched = len(instruments)
	now := deps.Now()

	for _, curr := range instruments {
		var prevSnap *InstrumentSnapshot
		if hasBaseline {
			prevSnap, err = repo.LatestInstrumentSnapshotByKey(ctx, src.Platform, src.MarketType, curr.APISymbol)
			if err != nil {
				return res, fmt.Errorf("load prev %s/%s/%s: %w", src.Platform, src.MarketType, curr.APISymbol, err)
			}
			var prev *instrument.NormalizedInstrument
			if prevSnap != nil {
				tmp := snapshotToNormalized(*prevSnap)
				prev = &tmp
			}
			events := instrument.Diff(prev, curr, true)
			for _, ev := range events {
				signal := buildInstrumentDiffSignal(src, curr, ev, now)
				if _, _, err := repo.InsertSignal(ctx, signal); err != nil {
					return res, fmt.Errorf("insert signal %s/%s: %w", curr.APISymbol, ev.Subtype, err)
				}
				res.SignalsEmitted++
				res.DiffSubtypes[ev.Subtype]++
			}
		}

		snap := normalizedToSnapshot(curr, now)
		if prevSnap != nil {
			snap.FirstSeenAt = prevSnap.FirstSeenAt
		}
		if err := repo.UpsertInstrumentSnapshot(ctx, snap); err != nil {
			return res, fmt.Errorf("upsert snapshot %s/%s: %w", src.Platform, curr.APISymbol, err)
		}
		res.SnapshotsUpserted++
	}
	return res, nil
}

func normalizedToSnapshot(n instrument.NormalizedInstrument, now time.Time) InstrumentSnapshot {
	return InstrumentSnapshot{
		Platform:             n.Platform,
		MarketType:           n.MarketType,
		APISymbol:            n.APISymbol,
		APIMarketID:          n.APIMarketID,
		DisplaySymbol:        n.DisplaySymbol,
		CanonicalSymbol:      n.CanonicalSymbol,
		BaseAsset:            n.BaseAsset,
		QuoteAsset:           n.QuoteAsset,
		SettleAsset:          n.SettleAsset,
		MarketSurface:        n.MarketSurface,
		InstrumentKind:       n.InstrumentKind,
		ContractType:         n.ContractType,
		StatusRaw:            n.StatusRaw,
		StatusNormalized:     n.StatusNormalized,
		StatusFieldName:      n.StatusFieldName,
		ListingTimeTS:        n.ListingTimeTS,
		ListingTimeFieldName: n.ListingTimeFieldName,
		DelistFlag:           n.DelistFlag,
		FirstSeenAt:          now,
		LastSeenAt:           now,
		RawJSON:              json.RawMessage(append([]byte(nil), n.RawJSON...)),
		RawJSONHash:          n.RawJSONHash,
		NormalizerVersion:    instrument.NormalizerVersion,
	}
}

func snapshotToNormalized(s InstrumentSnapshot) instrument.NormalizedInstrument {
	return instrument.NormalizedInstrument{
		Platform:             s.Platform,
		MarketType:           s.MarketType,
		APISymbol:            s.APISymbol,
		APIMarketID:          s.APIMarketID,
		DisplaySymbol:        s.DisplaySymbol,
		CanonicalSymbol:      s.CanonicalSymbol,
		BaseAsset:            s.BaseAsset,
		QuoteAsset:           s.QuoteAsset,
		SettleAsset:          s.SettleAsset,
		MarketSurface:        s.MarketSurface,
		InstrumentKind:       s.InstrumentKind,
		ContractType:         s.ContractType,
		StatusRaw:            s.StatusRaw,
		StatusNormalized:     s.StatusNormalized,
		StatusFieldName:      s.StatusFieldName,
		ListingTimeTS:        s.ListingTimeTS,
		ListingTimeFieldName: s.ListingTimeFieldName,
		DelistFlag:           s.DelistFlag,
		RawJSON:              json.RawMessage(append([]byte(nil), s.RawJSON...)),
		RawJSONHash:          s.RawJSONHash,
	}
}

func buildInstrumentDiffSignal(src InstrumentSource, curr instrument.NormalizedInstrument, ev instrument.DiffEvent, now time.Time) SignalObservation {
	listingTime := curr.ListingTimeTS
	fingerprint := instrumentDiffFingerprint(src.Platform, src.MarketType, curr.APISymbol, ev)
	rawCopy := append([]byte(nil), curr.RawJSON...)
	hash := sha256.Sum256(rawCopy)
	rawHash := hex.EncodeToString(hash[:])
	payload, _ := json.Marshal(map[string]any{
		"diff_subtype":       ev.Subtype,
		"status_from":        ev.StatusFrom,
		"status_to":          ev.StatusTo,
		"listing_time_from":  ev.ListingTimeFrom,
		"listing_time_to":    ev.ListingTimeTo,
		"raw_json_hash_from": ev.RawJSONHashFrom,
		"raw_json_hash_to":   ev.RawJSONHashTo,
		"normalizer_version": instrument.NormalizerVersion,
	})
	return SignalObservation{
		SignalType:       SignalInstrumentDiff,
		SignalSubtype:    ev.Subtype,
		SourcePlatform:   src.Platform,
		MarketType:       src.MarketType,
		APISymbol:        curr.APISymbol,
		APIMarketID:      curr.APIMarketID,
		CanonicalSymbol:  curr.CanonicalSymbol,
		DisplaySymbol:    curr.DisplaySymbol,
		BaseAsset:        curr.BaseAsset,
		QuoteAsset:       curr.QuoteAsset,
		SettleAsset:      curr.SettleAsset,
		MarketSurface:    curr.MarketSurface,
		InstrumentKind:   curr.InstrumentKind,
		StatusRaw:        curr.StatusRaw,
		StatusNormalized: curr.StatusNormalized,
		Confidence:       ConfidenceHigh,
		ObservedAt:       now,
		ListingTimeTS:    listingTime,
		SourceEndpoint:   src.SourceKey,
		SourceURL:        src.SourceURL,
		Fingerprint:      fingerprint,
		PayloadJSON:      payload,
		RawPayloadJSON:   json.RawMessage(rawCopy),
		RawPayloadHash:   rawHash,
	}
}

func instrumentDiffFingerprint(platform, marketType, apiSymbol string, ev instrument.DiffEvent) string {
	return fmt.Sprintf("instrument_diff|%s|%s|%s|%s|%s|%s",
		platform, marketType, apiSymbol, ev.Subtype, ev.RawJSONHashFrom, ev.RawJSONHashTo)
}
