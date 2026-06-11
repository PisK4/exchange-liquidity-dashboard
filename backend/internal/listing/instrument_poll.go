package listing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// SignalingMode controls whether the instrument poll driver emits
// fusion-relevant signals on top of the snapshot write. The default
// (empty string == SignalingModeFull) preserves Phase 4 semantics
// for the six head CEX pollers. SignalingModeSnapshotOnly is the
// dynamic-discovery default for surfaces whose presence in the
// snapshot table is interesting (CatalogResolver DB-first,
// listed_universe refresh) but whose own diff events MUST NOT feed
// the listing decision loop (e.g. edgeX's own 3 surfaces, spec F5).
type SignalingMode string

const (
	SignalingModeFull         SignalingMode = "full"
	SignalingModeSnapshotOnly SignalingMode = "snapshot_only"
)

// InstrumentSource describes one (platform, market_type) endpoint
// pair the listing agent polls. The Fetch closure isolates the HTTP
// + normalisation work so tests can inject fixture instruments and
// the engine can compose real adapters at wiring time.
//
// SignalingMode is consulted by RunInstrumentPoll to decide whether
// to InsertSignal on a diff event; the snapshot upsert path is
// unconditional so CatalogResolver and the universe refresh job
// always see the latest data regardless of signaling mode.
type InstrumentSource struct {
	Platform      string
	MarketType    string
	SourceURL     string
	SourceKey     string
	SignalingMode SignalingMode
	Fetch         func(ctx context.Context) ([]instrument.NormalizedInstrument, error)
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
//
// Logger is consulted when a per-instrument best-effort failure
// happens (InsertSignal / UpsertInstrumentSnapshot). The driver must
// not abort the whole tick because of a single bad row — a stale
// last_seen_at on the remaining 755 binance contracts is how a single
// poisonous symbol crashes the dashboard runtime listed_universe (see
// the 2026-06-01 root-cause: fingerprint VARCHAR(96) overflow caused
// InsertSignal to silently miss → return error → cascading abort of
// every snapshot upsert in the same tick).
type InstrumentPollDeps struct {
	Now            func() time.Time
	Logger         *log.Logger
	SymbolResolver SymbolIdentityResolver
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
	if deps.Logger == nil {
		deps.Logger = log.Default()
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
	for i := range instruments {
		instruments[i] = ApplyInstrumentSymbolIdentity(instruments[i], deps.SymbolResolver)
	}
	res.Fetched = len(instruments)
	now := deps.Now()

	// Per-instrument processing is best-effort: a single poisoned row
	// (signal-insert silent fail, transient deadlock, malformed prev
	// row) must NOT abort the whole tick — otherwise the remaining
	// instruments lose their snapshot last_seen_at bump and fall out
	// of QueryActiveListedBases' freshness window, draining the
	// dashboard runtime listed_universe to an empty platform.
	for _, curr := range instruments {
		var prevSnap *InstrumentSnapshot
		if hasBaseline {
			ps, loadErr := repo.LatestInstrumentSnapshotByKey(ctx, src.Platform, src.MarketType, curr.APISymbol)
			if loadErr != nil {
				deps.Logger.Printf("listing instrument poll: load prev %s/%s/%s: %v (continuing tick)",
					src.Platform, src.MarketType, curr.APISymbol, loadErr)
				continue
			}
			prevSnap = ps
			var prev *instrument.NormalizedInstrument
			if prevSnap != nil {
				tmp := snapshotToNormalized(*prevSnap)
				prev = &tmp
			}
			// Normalizer-version rollover guard: when prev snapshot
			// was written by an older normalizer version (different
			// hash recipe), the StableHash values are not directly
			// comparable. Skipping Diff for one tick lets the
			// snapshot upsert below replace the row with the new
			// recipe; subsequent ticks then compare like for like.
			// Without this guard, the v1→v2 cutover would produce a
			// one-shot metadata_changed firehose across every
			// instrument on every platform.
			versionRollover := prevSnap != nil && prevSnap.NormalizerVersion != instrument.NormalizerVersion
			if versionRollover {
				deps.Logger.Printf("listing instrument poll: normalizer rolled %s->%s for %s/%s/%s (diff skipped this tick)",
					prevSnap.NormalizerVersion, instrument.NormalizerVersion,
					src.Platform, src.MarketType, curr.APISymbol)
			}
			if src.SignalingMode != SignalingModeSnapshotOnly && !versionRollover {
				events := instrument.Diff(prev, curr, true)
				for _, ev := range events {
					signal := buildInstrumentDiffSignal(src, curr, ev, now)
					if _, _, sigErr := repo.InsertSignal(ctx, signal); sigErr != nil {
						// Soft-fail: log and keep going so the snapshot
						// upsert below still happens. Without this guard
						// one bad fingerprint poisoned the entire poll.
						deps.Logger.Printf("listing instrument poll: insert signal %s/%s/%s: %v (continuing tick)",
							src.Platform, curr.APISymbol, ev.Subtype, sigErr)
						continue
					}
					res.SignalsEmitted++
					res.DiffSubtypes[ev.Subtype]++
				}
			}
		}

		snap := normalizedToSnapshot(curr, now)
		if prevSnap != nil {
			snap.FirstSeenAt = prevSnap.FirstSeenAt
		}
		if upErr := repo.UpsertInstrumentSnapshot(ctx, snap); upErr != nil {
			deps.Logger.Printf("listing instrument poll: upsert snapshot %s/%s: %v (continuing tick)",
				src.Platform, curr.APISymbol, upErr)
			continue
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
		StableHash:           n.StableHash,
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
		StableHash:           s.StableHash,
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
		"stable_hash_from":   ev.StableHashFrom,
		"stable_hash_to":     ev.StableHashTo,
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

// instrumentDiffFingerprint returns the deterministic per-event row
// key used to deduplicate t_listing_signal_observation inserts.
//
// We sha256-hash the full component tuple instead of concatenating it
// verbatim because two of the components are themselves 64-char sha256
// hex digests (StableHashFrom / StableHashTo): the plaintext form
// blew the column's VARCHAR(96) ceiling and caused INSERT IGNORE to
// silently drop / truncate, which the resolve-by-fingerprint SELECT
// then turned into "no rows in result set" and propagated up the call
// stack as a permanent poll failure (see 2026-06-01 root-cause). The
// sha256 form is 80 chars total ("instrument_diff:" + 64 hex) so it
// always fits and is future-proof against further metadata growth.
func instrumentDiffFingerprint(platform, marketType, apiSymbol string, ev instrument.DiffEvent) string {
	payload := strings.Join([]string{
		platform,
		marketType,
		apiSymbol,
		ev.Subtype,
		ev.StableHashFrom,
		ev.StableHashTo,
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return "instrument_diff:" + hex.EncodeToString(sum[:])
}
