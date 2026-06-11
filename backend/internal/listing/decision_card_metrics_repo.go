package listing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DecisionCardMetricSourceLiveReference = "live_reference"
	DecisionCardMetricSourceDBSnapshot    = "db_snapshot"
)

// SnapshotMetricOptions controls local DB snapshot fallback for decision-card
// metrics. DepthTierPct is formatted with the same MySQL tier label used by
// the collector (for example 0.001 -> "0.10%").
type SnapshotMetricOptions struct {
	DepthTierPct       float64
	StaleAfter         time.Duration
	ReferencePlatforms []string
	Now                func() time.Time
}

// LoadLatestDepthEvidence returns the most liquid fresh snapshot for one
// canonical + market surface + tier. It first narrows to each platform's latest
// eligible snapshot, then picks the largest total_usd winner for the compact
// card row.
func (r *Repository) LoadLatestDepthEvidence(ctx context.Context, canonical, marketSurface, tier string, staleAfter time.Duration, now time.Time, platforms []string) (*DepthEvidence, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	canonical = strings.ToUpper(strings.TrimSpace(canonical))
	marketSurface = strings.TrimSpace(marketSurface)
	tier = strings.TrimSpace(tier)
	if canonical == "" || marketSurface == "" || tier == "" {
		return nil, errors.New("canonical, market surface and tier are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	cutoff := now.Add(-staleAfter)
	platforms = normalizeDepthReferencePlatforms(platforms)

	innerPlatformFilter, innerPlatformArgs := sqlPlatformFilter("platform", platforms)
	outerPlatformFilter, outerPlatformArgs := sqlPlatformFilter("s.platform", platforms)
	query := `SELECT s.platform, COALESCE(s.total_usd, 0), s.tier, s.snapshot_ts
  FROM t_orderbook_snapshot s
  JOIN (
    SELECT platform, MAX(snapshot_ts) AS snapshot_ts
      FROM t_orderbook_snapshot
     WHERE canonical_symbol = ?
       AND market_surface = ?
       AND tier = ?
       AND snapshot_ts >= ?
       AND depth_status IN ('complete','partial','aggregated_orderbook','ws_limited_depth')` + innerPlatformFilter + `
     GROUP BY platform
  ) latest
    ON latest.platform = s.platform
   AND latest.snapshot_ts = s.snapshot_ts
 WHERE s.canonical_symbol = ?
   AND s.market_surface = ?
   AND s.tier = ?
   AND s.snapshot_ts >= ?
   AND s.depth_status IN ('complete','partial','aggregated_orderbook','ws_limited_depth')` + outerPlatformFilter + `
 ORDER BY COALESCE(s.total_usd, 0) DESC, s.platform ASC
 LIMIT 1`
	args := []any{canonical, marketSurface, tier, cutoff}
	args = append(args, innerPlatformArgs...)
	args = append(args, canonical, marketSurface, tier, cutoff)
	args = append(args, outerPlatformArgs...)

	var ev DepthEvidence
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&ev.Platform, &ev.USDValue, &ev.Tier, &ev.SnapshotTS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load latest %s depth snapshot for %s: %w", marketSurface, canonical, err)
	}
	if ev.USDValue <= 0 {
		return nil, nil
	}
	ev.Source = DecisionCardMetricSourceDBSnapshot
	return &ev, nil
}

// LoadLatestSpotVolumeEvidence sums the latest fresh spot-only 24h volume per
// platform for a canonical. It intentionally excludes perp rows so the DB
// fallback does not change the meaning of the "Spot 24h Vol" card label.
func (r *Repository) LoadLatestSpotVolumeEvidence(ctx context.Context, canonical string, staleAfter time.Duration, now time.Time, platforms []string) (*VolumeEvidence, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	canonical = strings.ToUpper(strings.TrimSpace(canonical))
	if canonical == "" {
		return nil, errors.New("canonical is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	cutoff := now.Add(-staleAfter)
	platforms = normalizeDepthReferencePlatforms(platforms)
	innerPlatformFilter, innerPlatformArgs := sqlPlatformFilter("platform", platforms)
	outerPlatformFilter, outerPlatformArgs := sqlPlatformFilter("s.platform", platforms)
	query := `SELECT COALESCE(SUM(platform_volume), 0), MAX(snapshot_ts), COUNT(*)
  FROM (
    SELECT s.platform,
           MAX(COALESCE(s.volume_24h_usd, 0)) AS platform_volume,
           MAX(s.snapshot_ts) AS snapshot_ts
      FROM t_symbol_volume_snapshot s
      JOIN (
        SELECT platform, MAX(snapshot_ts) AS snapshot_ts
          FROM t_symbol_volume_snapshot
         WHERE canonical_symbol = ?
           AND market_surface = 'spot'
           AND snapshot_ts >= ?
           AND status IN ('complete','partial')` + innerPlatformFilter + `
         GROUP BY platform
      ) latest
        ON latest.platform = s.platform
       AND latest.snapshot_ts = s.snapshot_ts
     WHERE s.canonical_symbol = ?
       AND s.market_surface = 'spot'
       AND s.snapshot_ts >= ?
       AND s.status IN ('complete','partial')` + outerPlatformFilter + `
     GROUP BY s.platform
  ) per_platform`
	args := []any{canonical, cutoff}
	args = append(args, innerPlatformArgs...)
	args = append(args, canonical, cutoff)
	args = append(args, outerPlatformArgs...)

	var (
		usd        sql.NullFloat64
		snapshotTS sql.NullTime
		count      int
	)
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&usd, &snapshotTS, &count); err != nil {
		return nil, fmt.Errorf("load latest spot volume snapshot for %s: %w", canonical, err)
	}
	if count == 0 || !usd.Valid || usd.Float64 <= 0 {
		return nil, nil
	}
	out := &VolumeEvidence{
		USDValue:      usd.Float64,
		Source:        DecisionCardMetricSourceDBSnapshot,
		PlatformCount: count,
	}
	if snapshotTS.Valid {
		out.SnapshotTS = snapshotTS.Time
	}
	return out, nil
}

func BuildSnapshotDepthFetcher(repo *Repository, opts SnapshotMetricOptions) func(ctx context.Context, canonical string, sourcePlatforms []string) (*DepthEvidence, *DepthEvidence, error) {
	if repo == nil {
		return nil
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	tier := mysqlTierLabel(defaultFloat(opts.DepthTierPct, 0.001))
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	refs := normalizeDepthReferencePlatforms(opts.ReferencePlatforms)
	return func(ctx context.Context, canonical string, _ []string) (*DepthEvidence, *DepthEvidence, error) {
		now := opts.Now()
		spot, spotErr := repo.LoadLatestDepthEvidence(ctx, canonical, string(DepthKindSpot), tier, staleAfter, now, refs)
		perp, perpErr := repo.LoadLatestDepthEvidence(ctx, canonical, string(DepthKindPerp), tier, staleAfter, now, refs)
		return spot, perp, joinErrors(spotErr, perpErr)
	}
}

func BuildSnapshotSpotVolumeFetcher(repo *Repository, opts SnapshotMetricOptions) func(ctx context.Context, canonical string, sourcePlatforms []string) (*VolumeEvidence, error) {
	if repo == nil {
		return nil
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	return func(ctx context.Context, canonical string, _ []string) (*VolumeEvidence, error) {
		return repo.LoadLatestSpotVolumeEvidence(ctx, canonical, staleAfter, opts.Now(), nil)
	}
}

func BuildFallbackDepthFetcher(primary, fallback func(ctx context.Context, canonical string, sourcePlatforms []string) (*DepthEvidence, *DepthEvidence, error)) func(ctx context.Context, canonical string, sourcePlatforms []string) (*DepthEvidence, *DepthEvidence, error) {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	return func(ctx context.Context, canonical string, sourcePlatforms []string) (*DepthEvidence, *DepthEvidence, error) {
		spot, perp, primaryErr := primary(ctx, canonical, sourcePlatforms)
		stampDepthSource(spot, DecisionCardMetricSourceLiveReference)
		stampDepthSource(perp, DecisionCardMetricSourceLiveReference)
		if spot != nil && perp != nil && primaryErr == nil {
			return spot, perp, nil
		}
		fbSpot, fbPerp, fallbackErr := fallback(ctx, canonical, sourcePlatforms)
		if spot == nil {
			spot = fbSpot
		}
		if perp == nil {
			perp = fbPerp
		}
		if spot != nil && perp != nil {
			return spot, perp, fallbackErr
		}
		return spot, perp, joinErrors(primaryErr, fallbackErr)
	}
}

func sqlPlatformFilter(column string, platforms []string) (string, []any) {
	if len(platforms) == 0 {
		return "", nil
	}
	placeholders := make([]string, 0, len(platforms))
	args := make([]any, 0, len(platforms))
	for _, p := range platforms {
		placeholders = append(placeholders, "?")
		args = append(args, p)
	}
	return " AND " + column + " IN (" + strings.Join(placeholders, ",") + ")", args
}

func defaultFloat(v, fallback float64) float64 {
	if v > 0 {
		return v
	}
	return fallback
}

func stampDepthSource(ev *DepthEvidence, source string) {
	if ev != nil && ev.Source == "" {
		ev.Source = source
	}
}

func joinErrors(errs ...error) error {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}
