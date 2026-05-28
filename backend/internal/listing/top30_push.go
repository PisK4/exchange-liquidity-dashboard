package listing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/config"
)

// Top30RowForPush is the per-row projection of t_top30_snapshot used
// by the push producer. The CoinGecko / Top30 collector writes the
// raw snapshot; this struct narrows it down to fields the listing
// delivery worker actually consumes.
type Top30RowForPush struct {
	Platform        string
	Symbol          string
	Rank            int
	Volume24HUSD    float64
	CoverageCount   int
	EdgexListed     *bool
	SuggestedAction string
	SnapshotTS      time.Time
}

// Top30PlatformEvidence is one entry inside the per-symbol push
// event. Sorted by Rank ascending so the rendered card surfaces the
// strongest platform first.
type Top30PlatformEvidence struct {
	Platform     string  `json:"platform"`
	Rank         int     `json:"rank"`
	Volume24HUSD float64 `json:"volume_24h_usd"`
}

// Top30PushEvent is the aggregated payload that becomes a single
// outbox row + Lark card. Display symbol is the t_top30_snapshot
// symbol verbatim so the rendered card matches what operators see on
// the Top30 tab.
type Top30PushEvent struct {
	Symbol       string                  `json:"symbol"`
	Action       string                  `json:"action"`
	MaxCoverage  int                     `json:"max_coverage"`
	Platforms    []Top30PlatformEvidence `json:"platforms"`
	DashboardURL string                  `json:"dashboard_url"`
	SnapshotDate string                  `json:"snapshot_date"`
	DedupeKey    string                  `json:"dedupe_key"`
}

// Eligible Top30 push actions. Anything outside this set is treated
// as a non-actionable row and never produces a push.
var top30PushActions = map[string]struct{}{
	"优先上架": {},
	"评估上架": {},
}

// BuildTop30PushEvents groups eligible rows by display symbol and
// action, sorts platforms by rank, and computes the dedupe key. The
// caller decides what to do with the events (DB insert + outbox).
func BuildTop30PushEvents(rows []Top30RowForPush, day time.Time) []Top30PushEvent {
	type key struct{ symbol, action string }
	groups := make(map[key]*Top30PushEvent)
	order := make([]key, 0)
	dayKey := day.UTC().Format("2006-01-02")
	for _, row := range rows {
		if row.EdgexListed == nil || *row.EdgexListed {
			continue
		}
		if _, ok := top30PushActions[row.SuggestedAction]; !ok {
			continue
		}
		k := key{row.Symbol, row.SuggestedAction}
		ev, ok := groups[k]
		if !ok {
			ev = &Top30PushEvent{
				Symbol:       row.Symbol,
				Action:       row.SuggestedAction,
				SnapshotDate: dayKey,
				DedupeKey:    fmt.Sprintf("top30_hot_gap|%s|%s|%s", row.Symbol, row.SuggestedAction, dayKey),
			}
			groups[k] = ev
			order = append(order, k)
		}
		if row.CoverageCount > ev.MaxCoverage {
			ev.MaxCoverage = row.CoverageCount
		}
		ev.Platforms = append(ev.Platforms, Top30PlatformEvidence{
			Platform:     row.Platform,
			Rank:         row.Rank,
			Volume24HUSD: row.Volume24HUSD,
		})
	}
	out := make([]Top30PushEvent, 0, len(order))
	for _, k := range order {
		ev := groups[k]
		sort.Slice(ev.Platforms, func(i, j int) bool { return ev.Platforms[i].Rank < ev.Platforms[j].Rank })
		out = append(out, *ev)
	}
	return out
}

// RenderTop30PostMessage returns the JSON body the Lark / Feishu
// webhook expects. Only msg_type=post is used; rich-text content
// includes the display symbol, the recommended action, coverage
// count, every platform's rank/volume, and the dashboard URL.
func RenderTop30PostMessage(ev Top30PushEvent) ([]byte, error) {
	headline := fmt.Sprintf("%s · %s", ev.Symbol, ev.Action)
	subtitle := fmt.Sprintf("Coverage: %d 平台  快照: %s", ev.MaxCoverage, ev.SnapshotDate)
	var platformLines [][]map[string]any
	for _, p := range ev.Platforms {
		platformLines = append(platformLines, []map[string]any{
			{"tag": "text", "text": fmt.Sprintf("%s · rank #%d · 24h $%s", p.Platform, p.Rank, humanUSD(p.Volume24HUSD))},
		})
	}
	if ev.DashboardURL != "" {
		platformLines = append(platformLines, []map[string]any{
			{"tag": "a", "text": "查看 Top30 →", "href": ev.DashboardURL},
		})
	}
	content := append([][]map[string]any{
		{{"tag": "text", "text": subtitle}},
	}, platformLines...)
	body := map[string]any{
		"msg_type": "post",
		"content": map[string]any{
			"post": map[string]any{
				"zh_cn": map[string]any{
					"title":   headline,
					"content": content,
				},
			},
		},
	}
	return json.Marshal(body)
}

func humanUSD(v float64) string {
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", v/1_000_000_000)
	case v >= 1_000_000:
		return fmt.Sprintf("%.2fM", v/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.2fK", v/1_000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// ProduceTop30Push is the public producer entry point. It loads the
// latest Top30 snapshot rows, fail-closes when stale or when the
// listed universe is unavailable, materialises Top30PushEvents, and
// writes the matching signal + outbox rows.
type Top30Deps struct {
	LoadUniverse  func() (*config.ListedUniverse, error)
	Now           func() time.Time
	DashboardBase string
	WebhookURL    string
	MaxAttempts   int
	StaleAfter    time.Duration
}

type Top30PushResult struct {
	Events     int
	Signals    int
	OutboxRows int
	FailClosed string
}

func ProduceTop30Push(ctx context.Context, repo *Repository, deps Top30Deps) (Top30PushResult, error) {
	if repo == nil {
		return Top30PushResult{}, errors.New("listing top30 push: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	universe, err := deps.LoadUniverse()
	if err != nil {
		return Top30PushResult{FailClosed: "universe_load_error"}, fmt.Errorf("load universe: %w", err)
	}
	if universe == nil || !universe.Loaded() || len(universe.BaseAssets("edgeX")) == 0 {
		return Top30PushResult{FailClosed: "universe_not_loaded"}, nil
	}
	now := deps.Now()
	rows, latest, err := repo.loadTop30LatestRows(ctx)
	if err != nil {
		return Top30PushResult{}, fmt.Errorf("load top30 rows: %w", err)
	}
	if latest.IsZero() {
		return Top30PushResult{FailClosed: "no_snapshot"}, nil
	}
	stale := deps.StaleAfter
	if stale <= 0 {
		stale = 30 * time.Minute
	}
	if now.Sub(latest) > stale {
		return Top30PushResult{FailClosed: "snapshot_stale"}, nil
	}
	events := BuildTop30PushEvents(rows, latest)
	for _, ev := range events {
		signalPayload, err := json.Marshal(ev)
		if err != nil {
			return Top30PushResult{Events: len(events)}, fmt.Errorf("marshal event: %w", err)
		}
		outboxPayload, err := RenderTop30PostMessage(ev)
		if err != nil {
			return Top30PushResult{Events: len(events)}, fmt.Errorf("render top30 post message: %w", err)
		}
		signal := SignalObservation{
			SignalType:      SignalTop30HotGap,
			SignalSubtype:   ev.Action,
			SourcePlatform:  "top30",
			CanonicalSymbol: strings.ToUpper(extractBase(ev.Symbol)),
			DisplaySymbol:   ev.Symbol,
			MarketSurface:   "perp",
			InstrumentKind:  "canonical",
			ObservedAt:      now,
			Fingerprint:     fmt.Sprintf("top30_hot_gap|%s|%s|%s", ev.Symbol, ev.Action, ev.SnapshotDate),
			PayloadJSON:     signalPayload,
		}
		if _, _, err := repo.InsertSignal(ctx, signal); err != nil {
			return Top30PushResult{}, fmt.Errorf("insert top30 signal: %w", err)
		}
		var status string
		if strings.TrimSpace(deps.WebhookURL) == "" {
			status = OutboxStatusDisabled
		} else {
			status = OutboxStatusPending
		}
		maxAttempts := deps.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}
		if err := repo.insertOutbox(ctx, DeliveryOutbox{
			EventType:     DeliveryEventTop30HotGap,
			DedupeKey:     ev.DedupeKey,
			TargetChannel: DeliveryChannelLarkTop30,
			Status:        status,
			MaxAttempts:   maxAttempts,
			PayloadJSON:   outboxPayload,
			NextAttemptAt: ptrTime(now),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return Top30PushResult{}, fmt.Errorf("insert outbox: %w", err)
		}
	}
	return Top30PushResult{Events: len(events), Signals: len(events), OutboxRows: len(events)}, nil
}

func extractBase(displaySymbol string) string {
	s := strings.TrimSpace(displaySymbol)
	if idx := strings.Index(s, "-"); idx > 0 {
		return s[:idx]
	}
	if idx := strings.Index(s, "/"); idx > 0 {
		return s[:idx]
	}
	if idx := strings.IndexAny(s, " ("); idx > 0 {
		return s[:idx]
	}
	return s
}

func ptrTime(t time.Time) *time.Time { return &t }

// loadTop30LatestRows returns every row tied to the most recent
// snapshot_ts across t_top30_snapshot, plus that timestamp.
func (r *Repository) loadTop30LatestRows(ctx context.Context) ([]Top30RowForPush, time.Time, error) {
	if r.db == nil {
		return nil, time.Time{}, errors.New("listing top30: no db attached")
	}
	var latest sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT MAX(snapshot_ts) FROM t_top30_snapshot`).Scan(&latest); err != nil {
		return nil, time.Time{}, err
	}
	if !latest.Valid {
		return nil, time.Time{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT platform, symbol, rank_no, COALESCE(volume_24h_usd, 0),
		         COALESCE(coverage_count, 0), edgex_listed, suggested_action, snapshot_ts
		    FROM t_top30_snapshot
		   WHERE snapshot_ts = ?`, latest.Time)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var out []Top30RowForPush
	for rows.Next() {
		var row Top30RowForPush
		var listed sql.NullBool
		var action sql.NullString
		var ts time.Time
		if err := rows.Scan(&row.Platform, &row.Symbol, &row.Rank, &row.Volume24HUSD,
			&row.CoverageCount, &listed, &action, &ts); err != nil {
			return nil, time.Time{}, err
		}
		if listed.Valid {
			v := listed.Bool
			row.EdgexListed = &v
		}
		row.SuggestedAction = action.String
		row.SnapshotTS = ts
		out = append(out, row)
	}
	return out, latest.Time, rows.Err()
}

// insertOutbox writes one outbox row. The unique key on dedupe_key
// gives natural idempotency, so we use INSERT IGNORE: re-runs in the
// same UTC day are no-ops.
func (r *Repository) insertOutbox(ctx context.Context, o DeliveryOutbox) error {
	if r.db == nil {
		return errors.New("listing top30: no db attached")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT IGNORE INTO t_listing_delivery_outbox
		   (event_type, dedupe_key, target_channel, status,
		    attempt_count, max_attempts, next_attempt_at, payload_json, last_error,
		    sent_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.EventType, o.DedupeKey, o.TargetChannel, o.Status,
		o.AttemptCount, o.MaxAttempts, nullTimePtr(o.NextAttemptAt),
		[]byte(o.PayloadJSON), nullString(o.LastError),
		nullTimePtr(o.SentAt), o.CreatedAt, o.UpdatedAt,
	)
	return err
}
