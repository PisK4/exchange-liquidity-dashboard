package listing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"edgex-dashboard/backend/internal/listing/announcement"
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
	Platform       string
	Baseline       bool
	Fetched        int
	Announcements  int
	SignalsEmitted int
	ParseSkips     int
}

// AnnouncementPollDeps wires the clock; the production poller passes
// a UTC time.Now wrapper, tests inject a fixed clock so observed_at
// and the audit timestamps are deterministic.
type AnnouncementPollDeps struct {
	Now func() time.Time
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

	for _, raw := range raws {
		parsed, err := src.Parse(raw)
		if err != nil {
			return res, fmt.Errorf("parse %s: %w", src.Platform, err)
		}

		// On warm path decide whether the parent is new BEFORE the
		// idempotent UpsertAnnouncement runs, so we can decide
		// whether to fan out to the child symbols + signal rows.
		alreadySeen := false
		if hasBaseline {
			alreadySeen, err = repo.HasAnnouncementForExternalID(ctx, src.Platform, parsed.AnnouncementID)
			if err != nil {
				return res, fmt.Errorf("has announcement external id %s/%s: %w", src.Platform, parsed.AnnouncementID, err)
			}
		}

		announcementID, err := repo.UpsertAnnouncement(ctx, parsed)
		if err != nil {
			return res, fmt.Errorf("upsert announcement %s/%s: %w", src.Platform, parsed.AnnouncementID, err)
		}
		res.Announcements++

		// Cold start writes parents only; warm + already-seen also
		// skips the fan-out. Only new warm-path announcements emit
		// signals.
		if !hasBaseline || alreadySeen {
			continue
		}
		if len(parsed.Symbols) == 0 {
			res.ParseSkips++
			continue
		}

		rawPayload := append([]byte(nil), parsed.RawPayloadJSON...)
		for _, sym := range parsed.Symbols {
			_, _, err := repo.InsertAnnouncementSymbolAndSignal(ctx, announcementID, src.Platform, parsed.AnnouncementID, sym, rawPayload, now)
			if err != nil {
				return res, fmt.Errorf("insert announcement signal %s/%s/%s: %w", src.Platform, parsed.AnnouncementID, sym.CanonicalSymbol, err)
			}
			res.SignalsEmitted++
		}
	}
	return res, nil
}
