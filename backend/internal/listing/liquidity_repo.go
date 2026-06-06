package listing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing/liquidity"
)

// LoadFreshDepthMatrix returns the latest depth snapshot row per
// (platform, display_symbol) at the requested tier, filtered to
// rows whose snapshot_ts is newer than now-staleAfter and whose
// depth_status is one of the "displayable" set
// (complete | partial | aggregated_orderbook | ws_limited_depth) —
// the same definition `enrichQualityVsMedianRows` already uses for
// the competitor median in the live dashboard, kept consistent so
// the liquidity alert never disagrees with the UI rendering.
//
// Output shape: map[canonicalSymbol]map[lowerCasePlatform]row. The
// canonical fold is done in-process via cfg.CanonicalIndex so callers
// can mock the resolver (the storage layer never persists canonical
// for orderbook snapshots).
//
// IMPORTANT: tier is the MySQL string label, e.g. "0.10%" for 0.001.
// The producer translates LiquidityAlertConfig.DepthTierPct using the
// same `fmt.Sprintf("%.2f%%", pct*100)` formula collector.go uses on
// write, so the read path always lands on rows that were actually
// persisted.
func (r *Repository) LoadFreshDepthMatrix(
	ctx context.Context,
	tier string,
	staleAfter time.Duration,
	now time.Time,
	index *config.CanonicalIndex,
) (map[string]map[string]liquidity.PlatformDepthRow, error) {
	if r.db == nil {
		return nil, errors.New("liquidity: no db attached")
	}
	if strings.TrimSpace(tier) == "" {
		return nil, errors.New("liquidity: empty tier label")
	}
	cutoff := now.Add(-staleAfter)
	query := `
SELECT s.platform, s.display_symbol, s.snapshot_ts,
       COALESCE(s.bid_usd, 0), COALESCE(s.ask_usd, 0), COALESCE(s.total_usd, 0)
  FROM t_orderbook_snapshot s
  JOIN (
    SELECT platform, display_symbol, MAX(snapshot_ts) AS snapshot_ts
      FROM t_orderbook_snapshot
     WHERE tier = ?
       AND snapshot_ts >= ?
       AND depth_status IN ('complete','partial','aggregated_orderbook','ws_limited_depth')
     GROUP BY platform, display_symbol
  ) latest
    ON latest.platform = s.platform
   AND latest.display_symbol = s.display_symbol
   AND latest.snapshot_ts = s.snapshot_ts
 WHERE s.tier = ?
   AND s.depth_status IN ('complete','partial','aggregated_orderbook','ws_limited_depth')
`
	rows, err := r.db.QueryContext(ctx, query, tier, cutoff, tier)
	if err != nil {
		return nil, fmt.Errorf("query depth matrix: %w", err)
	}
	defer rows.Close()
	out := make(map[string]map[string]liquidity.PlatformDepthRow)
	for rows.Next() {
		var (
			platform, displaySymbol string
			snapshotTS              time.Time
			bid, ask, total         float64
		)
		if err := rows.Scan(&platform, &displaySymbol, &snapshotTS, &bid, &ask, &total); err != nil {
			return nil, fmt.Errorf("scan depth matrix row: %w", err)
		}
		base := extractBase(displaySymbol)
		if base == "" {
			continue
		}
		canonical := strings.ToUpper(base)
		if index != nil {
			if resolved := index.Resolve(platform, base); strings.TrimSpace(resolved) != "" {
				canonical = resolved
			}
		}
		canonical = strings.ToUpper(canonical)
		key := strings.ToLower(strings.TrimSpace(platform))
		perPlatform := out[canonical]
		if perPlatform == nil {
			perPlatform = make(map[string]liquidity.PlatformDepthRow)
			out[canonical] = perPlatform
		}
		// If two display symbols map to the same canonical on the
		// same platform (rare; e.g. perp vs spot both visible), keep
		// the one with the larger total depth. The alert should reflect
		// the most-liquid surface the operator actually trades.
		if existing, ok := perPlatform[key]; ok && existing.DepthUSD >= total {
			continue
		}
		perPlatform[key] = liquidity.PlatformDepthRow{
			Platform:      key,
			DisplaySymbol: displaySymbol,
			Tier:          tier,
			DepthUSD:      total,
			BidUSD:        bid,
			AskUSD:        ask,
			SnapshotTS:    snapshotTS,
		}
	}
	return out, rows.Err()
}

// LoadAlertState fetches the current row for (kind, canonical), or
// returns (zero, false, nil) when no row exists. The caller treats
// the zero AlertState as "no prior history".
func (r *Repository) LoadAlertState(
	ctx context.Context,
	kind liquidity.AlertKind,
	canonical string,
) (liquidity.AlertState, bool, error) {
	if r.db == nil {
		return liquidity.AlertState{}, false, errors.New("liquidity: no db attached")
	}
	var (
		statusStr        string
		severitySeq      int
		reissueCount     int
		clearStreak      int
		firstTriggeredAt time.Time
		lastPushedAt     sql.NullTime
		lastEvaluatedAt  time.Time
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT status, severity_seq, reissue_count, clear_streak,
		        first_triggered_at, last_pushed_at, last_evaluated_at
		   FROM t_listing_alert_state
		  WHERE alert_kind = ? AND canonical_symbol = ?`,
		string(kind), canonical,
	).Scan(&statusStr, &severitySeq, &reissueCount, &clearStreak,
		&firstTriggeredAt, &lastPushedAt, &lastEvaluatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return liquidity.AlertState{}, false, nil
	}
	if err != nil {
		return liquidity.AlertState{}, false, err
	}
	state := liquidity.AlertState{
		Kind:             kind,
		Canonical:        canonical,
		Status:           liquidity.AlertStatus(statusStr),
		SeveritySeq:      severitySeq,
		ReissueCount:     reissueCount,
		ClearStreak:      clearStreak,
		FirstTriggeredAt: firstTriggeredAt,
		LastEvaluatedAt:  lastEvaluatedAt,
	}
	if lastPushedAt.Valid {
		state.LastPushedAt = lastPushedAt.Time
	}
	return state, true, nil
}

// UpsertAlertState writes the post-tick state. lastSeverityJSON is
// the JSON-encoded snapshot of the rendered card so an operator can
// diff successive pushes from MySQL alone. Use INSERT … ON DUPLICATE
// KEY UPDATE so the first-trigger path (no prior row) and the
// reissue/clear path (existing row) share one code path.
func (r *Repository) UpsertAlertState(
	ctx context.Context,
	state liquidity.AlertState,
	lastSeverityJSON json.RawMessage,
) error {
	if r.db == nil {
		return errors.New("liquidity: no db attached")
	}
	var lastPushedAt any
	if !state.LastPushedAt.IsZero() {
		lastPushedAt = state.LastPushedAt
	}
	var severityArg any
	if len(lastSeverityJSON) > 0 {
		severityArg = []byte(lastSeverityJSON)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_listing_alert_state
		   (alert_kind, canonical_symbol, status, severity_seq, reissue_count, clear_streak,
		    first_triggered_at, last_pushed_at, last_evaluated_at, last_severity_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   status = VALUES(status),
		   severity_seq = VALUES(severity_seq),
		   reissue_count = VALUES(reissue_count),
		   clear_streak = VALUES(clear_streak),
		   first_triggered_at = VALUES(first_triggered_at),
		   last_pushed_at = VALUES(last_pushed_at),
		   last_evaluated_at = VALUES(last_evaluated_at),
		   last_severity_json = VALUES(last_severity_json)`,
		string(state.Kind), state.Canonical, string(state.Status),
		state.SeveritySeq, state.ReissueCount, state.ClearStreak,
		state.FirstTriggeredAt, lastPushedAt, state.LastEvaluatedAt,
		severityArg,
	)
	return err
}

// ListActiveAlertStates returns every (kind, canonical) pair whose
// status is currently "active". The producer uses this to drive the
// clear-path: even when this tick's depth matrix doesn't include a
// canonical at all (e.g. snapshot fell out of the freshness window),
// the active alert still needs DecideAction called on it so the
// clear_streak counter advances.
func (r *Repository) ListActiveAlertStates(
	ctx context.Context,
	kinds []liquidity.AlertKind,
) ([]liquidity.AlertState, error) {
	if r.db == nil {
		return nil, errors.New("liquidity: no db attached")
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(kinds))
	args := make([]any, 0, len(kinds)+1)
	for _, k := range kinds {
		placeholders = append(placeholders, "?")
		args = append(args, string(k))
	}
	args = append(args, string(liquidity.StatusActive))
	rows, err := r.db.QueryContext(ctx,
		`SELECT alert_kind, canonical_symbol, status, severity_seq, reissue_count, clear_streak,
		        first_triggered_at, last_pushed_at, last_evaluated_at
		   FROM t_listing_alert_state
		  WHERE alert_kind IN (`+strings.Join(placeholders, ",")+`)
		    AND status = ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []liquidity.AlertState
	for rows.Next() {
		var (
			kindStr, canonical, statusStr     string
			severitySeq, reissueCount, streak int
			firstTriggered, lastEvaluated     time.Time
			lastPushed                        sql.NullTime
		)
		if err := rows.Scan(&kindStr, &canonical, &statusStr, &severitySeq, &reissueCount, &streak,
			&firstTriggered, &lastPushed, &lastEvaluated); err != nil {
			return nil, err
		}
		st := liquidity.AlertState{
			Kind:             liquidity.AlertKind(kindStr),
			Canonical:        canonical,
			Status:           liquidity.AlertStatus(statusStr),
			SeveritySeq:      severitySeq,
			ReissueCount:     reissueCount,
			ClearStreak:      streak,
			FirstTriggeredAt: firstTriggered,
			LastEvaluatedAt:  lastEvaluated,
		}
		if lastPushed.Valid {
			st.LastPushedAt = lastPushed.Time
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
