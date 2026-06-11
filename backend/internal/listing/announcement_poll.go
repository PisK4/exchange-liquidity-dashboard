package listing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/announcement"
)

// AnnouncementSource describes one CMS feed the listing agent polls.
// Fetch returns the raw payload list (one entry per announcement) and
// Parse converts each raw payload into a normalised announcement —
// keeping the two responsibilities separate so tests can drive the
// parser directly without an HTTP fake and the driver can drive any
// platform with the same orchestration logic.
type AnnouncementSource struct {
	Platform  string
	SourceURL string
	SourceKey string
	Fetch     func(ctx context.Context) ([]json.RawMessage, error)
	Parse     func(raw json.RawMessage) (announcement.ParsedAnnouncement, error)
}

// AnnouncementPollResult summarises one tick. SignalsEmitted reports
// the count of t_listing_signal_observation rows written (warm path
// only); ParseSkips counts announcements the parser classified as
// audit-only (spot, pre-market, irrelevant) so operators can spot a
// CMS feed that suddenly emits zero perp announcements.
type AnnouncementPollResult struct {
	Platform         string
	Baseline         bool
	Fetched          int
	Announcements    int
	SignalsEmitted   int
	ParseSkips       int
	ParseSkipReasons map[string]int
}

func (r *AnnouncementPollResult) recordParseSkip(reason string) {
	r.ParseSkips++
	if r.ParseSkipReasons == nil {
		r.ParseSkipReasons = make(map[string]int)
	}
	if reason == "" {
		reason = announcement.SkipReasonAuditOnly
	}
	r.ParseSkipReasons[reason]++
}

// AnnouncementPollDeps wires the clock; the production poller passes
// a UTC time.Now wrapper, tests inject a fixed clock so observed_at
// and the audit timestamps are deterministic.
//
// Logger is consulted on per-announcement / per-symbol best-effort
// failures so a single bad fingerprint or transient write error does
// NOT abort the rest of the tick — see the parallel comment on
// InstrumentPollDeps for the failure mode this guards against.
type AnnouncementPollDeps struct {
	Now            func() time.Time
	Logger         *log.Logger
	SymbolResolver SymbolIdentityResolver
}

// RunAnnouncementPoll executes one pass over the announcement source.
//
// Contract per 2026-05-29-listing-agent.md §Phase 1:
//
//   - HasAnnouncementBaseline=false (cold start) → upsert parents only,
//     ZERO signals. A fresh deploy never re-posts historical
//     announcements as new perp candidates.
//   - HasAnnouncementBaseline=true → for every parsed announcement,
//     decide via HasAnnouncementForExternalID whether the parent has
//     already been seen. Already-seen announcements still upsert
//     (idempotent ON DUPLICATE KEY) but skip the symbol/signal
//     fan-out; unseen announcements emit one signal per parsed
//     symbol (spot/pre-market/irrelevant get zero symbols by
//     construction in classifyTitle).
//   - Fetch / Parse errors are returned verbatim so the source-health
//     wrapper can classify them; the driver writes nothing for the
//     offending announcement.
//
// The driver does NOT touch t_listing_source_state; that is the
// wrapper's job (PollWithSourceHealth) so each layer's tests stay
// narrow.
func RunAnnouncementPoll(ctx context.Context, repo *Repository, src AnnouncementSource, deps AnnouncementPollDeps) (AnnouncementPollResult, error) {
	if repo == nil {
		return AnnouncementPollResult{}, errors.New("announcement poll: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.Logger == nil {
		deps.Logger = log.Default()
	}
	if src.Platform == "" {
		return AnnouncementPollResult{}, errors.New("announcement poll: source missing platform")
	}
	if src.Fetch == nil || src.Parse == nil {
		return AnnouncementPollResult{}, errors.New("announcement poll: Fetch and Parse required")
	}

	res := AnnouncementPollResult{Platform: src.Platform}

	hasBaseline, err := repo.HasAnnouncementBaseline(ctx, src.Platform)
	if err != nil {
		return res, fmt.Errorf("has announcement baseline: %w", err)
	}
	res.Baseline = !hasBaseline

	raws, err := src.Fetch(ctx)
	if err != nil {
		return res, fmt.Errorf("fetch %s: %w", src.Platform, err)
	}
	res.Fetched = len(raws)
	now := deps.Now()

	// Per-announcement processing is best-effort for transient /
	// per-item failures, but schema-drift errors must still bubble up
	// so the source-health wrapper can flip the source to
	// schema_drift status (the dashboard signal that the parser
	// itself is broken).
	for _, raw := range raws {
		parsed, parseErr := src.Parse(raw)
		if parseErr != nil {
			var drift *announcement.SchemaDriftError
			if errors.As(parseErr, &drift) {
				return res, fmt.Errorf("parse %s: %w", src.Platform, parseErr)
			}
			deps.Logger.Printf("listing announcement poll: parse %s: %v (skipping item)", src.Platform, parseErr)
			continue
		}
		for i := range parsed.Symbols {
			parsed.Symbols[i] = ApplyAnnouncementSymbolIdentity(src.Platform, parsed.Symbols[i], deps.SymbolResolver)
		}

		// On warm path decide whether the parent is new BEFORE the
		// idempotent UpsertAnnouncement runs, so we can decide
		// whether to fan out to the child symbols + signal rows.
		alreadySeen := false
		if hasBaseline {
			seen, seenErr := repo.HasAnnouncementForExternalID(ctx, src.Platform, parsed.AnnouncementID)
			if seenErr != nil {
				deps.Logger.Printf("listing announcement poll: has announcement external id %s/%s: %v (skipping item)",
					src.Platform, parsed.AnnouncementID, seenErr)
				continue
			}
			alreadySeen = seen
		}

		announcementID, upErr := repo.UpsertAnnouncement(ctx, parsed)
		if upErr != nil {
			deps.Logger.Printf("listing announcement poll: upsert announcement %s/%s: %v (skipping item)",
				src.Platform, parsed.AnnouncementID, upErr)
			continue
		}
		res.Announcements++

		// Cold start writes parents only; warm + already-seen also
		// skips the fan-out. Only new warm-path announcements emit
		// signals.
		if !hasBaseline || alreadySeen {
			continue
		}
		if len(parsed.Symbols) == 0 {
			res.recordParseSkip(parsed.SkipReason())
			continue
		}

		rawPayload := append([]byte(nil), parsed.RawPayloadJSON...)
		for _, sym := range parsed.Symbols {
			if _, _, sigErr := repo.InsertAnnouncementSymbolAndSignal(ctx, announcementID, src.Platform, parsed.AnnouncementID, sym, rawPayload, now); sigErr != nil {
				deps.Logger.Printf("listing announcement poll: insert announcement signal %s/%s/%s: %v (continuing tick)",
					src.Platform, parsed.AnnouncementID, sym.CanonicalSymbol, sigErr)
				continue
			}
			res.SignalsEmitted++
		}
	}
	return res, nil
}
