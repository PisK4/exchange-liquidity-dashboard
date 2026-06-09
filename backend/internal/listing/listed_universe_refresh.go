package listing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"edgex-ops-intelligence/backend/internal/config"
)

// ListedUniverseRefreshArgs bundles every value RefreshListedUniverseFromSnapshots
// needs. The struct shape keeps the call site readable and lets the
// engine pass the same args twice in a row (cold-start sync + first
// tick) without re-listing positional parameters.
type ListedUniverseRefreshArgs struct {
	SeedPath          string
	RuntimePath       string
	FreshWindow       time.Duration
	ShrinkFloor       float64 // default 0.5; clamped at 0..1
	CoveredPlatforms  []string
	PlatformOverrides map[string]config.ListedUniverseRefreshPlatformPolicy
	Now               time.Time      // pinned for tests; zero means use repo clock
	Metrics           MetricRecorder // optional; defaults to NopMetrics
}

// ListedUniverseRefreshResult is the operator-facing summary the
// engine logs after every refresh tick. The Per-{Perp,Spot}Reconciled
// fields are kept distinct so a regression on either surface is
// observable without diffing total counts.
type ListedUniverseRefreshResult struct {
	PlatformsFromDB      []string
	PlatformsFromSeed    []string
	TotalBases           int
	CandidatesReconciled int
	PerpReconciled       int
	SpotReconciled       int
}

type listedUniversePlatformDecision struct {
	UseDB   bool
	Message string
	Context listedUniverseRefreshContext
}

type listedUniverseRefreshContext struct {
	SchemaVersion                     int            `json:"schema_version"`
	Platform                          string         `json:"platform"`
	Decision                          string         `json:"decision"`
	Reason                            string         `json:"reason"`
	BaselineType                      string         `json:"baseline_type"`
	BaselineCount                     int            `json:"baseline_count"`
	PreviousSuccessDBFreshActiveCount int            `json:"previous_success_db_fresh_active_count,omitempty"`
	DBFreshActiveCount                int            `json:"db_fresh_active_count"`
	SeedCount                         int            `json:"seed_count"`
	Threshold                         int            `json:"threshold"`
	RatioToBaseline                   float64        `json:"ratio_to_baseline"`
	ShrinkFloor                       float64        `json:"shrink_floor"`
	BootstrapBaseline                 string         `json:"bootstrap_baseline,omitempty"`
	BootstrapMinCount                 int            `json:"bootstrap_min_count,omitempty"`
	FreshWindowSeconds                int64          `json:"fresh_window_seconds"`
	SurfaceCounts                     map[string]int `json:"surface_counts,omitempty"`
	SeedOnlySample                    []string       `json:"seed_only_sample,omitempty"`
	DBOnlySample                      []string       `json:"db_only_sample,omitempty"`
}

// RefreshListedUniverseFromSnapshots regenerates the runtime
// listed_universe.yaml from t_listing_instrument_snapshot. It is the
// dynamic-discovery counterpart of the monthly `make catalog` step.
//
// Behaviour (spec §B):
//
//   - For each platform in args.CoveredPlatforms, derive the active
//     base-asset set from the snapshot table.
//   - If the DB-derived list collapses below ShrinkFloor * seed_size,
//     keep the seed list, record a source-state error, and bump the
//     listed_universe_shrink_fallback_total counter.
//   - Pass-through platforms that exist in seed but are not covered
//     so seed-only deployments stay valid.
//   - Write the resulting yaml atomically (temp + rename) so a
//     reader never observes a half-written file.
//   - Reconcile candidates: for the edgeX surface, flip every
//     candidate whose canonical is in the DB-derived set to
//     lifecycle_status='already_listed'. The call is scoped per
//     surface so spot candidates are not closed when only perp
//     listed.
func RefreshListedUniverseFromSnapshots(ctx context.Context, repo *Repository, args ListedUniverseRefreshArgs) (ListedUniverseRefreshResult, error) {
	if repo == nil {
		return ListedUniverseRefreshResult{}, errors.New("refresh listed universe: repo is nil")
	}
	if args.RuntimePath == "" {
		return ListedUniverseRefreshResult{}, errors.New("refresh listed universe: runtime path required")
	}
	if args.Metrics == nil {
		args.Metrics = NopMetrics{}
	}
	if args.ShrinkFloor <= 0 || args.ShrinkFloor > 1 {
		args.ShrinkFloor = 0.5
	}
	now := args.Now
	if now.IsZero() {
		now = repo.now()
	}

	// (1) Seed — optional fallback only.
	seed, seedErr := config.LoadListedUniverse(args.SeedPath)
	if seedErr != nil && !os.IsNotExist(seedErr) {
		// A malformed seed is suspicious enough to abort; an absent
		// seed is normal in a brand-new deployment.
		return ListedUniverseRefreshResult{}, fmt.Errorf("load seed: %w", seedErr)
	}
	if seed == nil {
		seed = &config.ListedUniverse{Platforms: map[string]config.ListedPlatform{}}
	}
	//region debug-be6994 listed-universe refresh start
	debugListedUniverseLog("H3,H5", "backend/internal/listing/listed_universe_refresh.go:seed_loaded", "listed universe refresh inputs loaded", map[string]any{
		"freshWindowSeconds": int64(args.FreshWindow.Seconds()),
		"shrinkFloor":        args.ShrinkFloor,
		"coveredPlatforms":   args.CoveredPlatforms,
		"seedPlatformCount":  len(seed.Platforms),
	})
	//endregion

	// (2) DB-derived bases.
	dbRows, err := repo.QueryActiveListedBases(ctx, args.FreshWindow)
	if err != nil {
		return ListedUniverseRefreshResult{}, fmt.Errorf("query active bases: %w", err)
	}
	debugListedUniverseSnapshotBreakdown(ctx, repo, args.CoveredPlatforms, now.Add(-args.FreshWindow))

	// (3) Merge per platform with shrink-floor safety net.
	out := config.ListedUniverse{
		SchemaVersion: 1,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		GeneratedBy:   "listing-agent/refresh",
		Platforms:     map[string]config.ListedPlatform{},
	}
	res := ListedUniverseRefreshResult{}
	for _, platform := range args.CoveredPlatforms {
		dbBases := dedupSortedBases(rowsForPlatform(dbRows, platform))
		seedBases := seed.BaseAssets(platform)
		previousState, loadErr := repo.LoadSourceState(ctx, listedUniverseSourceKey(platform))
		if loadErr != nil {
			return res, fmt.Errorf("load listed universe source state %s: %w", platform, loadErr)
		}
		decision := decideListedUniversePlatform(platform, dbBases, seedBases, dbRows, previousState, args)
		//region debug-be6994 listed-universe platform decision
		debugListedUniverseLog("H1,H2,H3,H4,H5", "backend/internal/listing/listed_universe_refresh.go:platform_decision", "listed universe platform merge decision", map[string]any{
			"platform":           platform,
			"seedCount":          len(seedBases),
			"dbFreshActiveCount": len(dbBases),
			"threshold":          decision.Context.Threshold,
			"fallback":           !decision.UseDB,
			"baselineType":       decision.Context.BaselineType,
			"reason":             decision.Context.Reason,
			"surfaceCounts":      surfaceCountsForPlatform(dbRows, platform),
			"seedOnlySample":     sampleMissingBases(seedBases, dbBases, 15),
			"dbOnlySample":       sampleMissingBases(dbBases, seedBases, 15),
		})
		//endregion
		if !decision.UseDB {
			out.Platforms[platform] = config.ListedPlatform{BaseAssets: append([]string(nil), seedBases...)}
			res.PlatformsFromSeed = append(res.PlatformsFromSeed, platform)
			args.Metrics.Inc("listed_universe_shrink_fallback_total", platform)
			recordShrinkFloorError(ctx, repo, platform, decision.Message, decision.Context, now)
			continue
		}
		out.Platforms[platform] = config.ListedPlatform{BaseAssets: dbBases}
		res.PlatformsFromDB = append(res.PlatformsFromDB, platform)
		recordListedUniverseRefreshOK(ctx, repo, platform, decision.Context, now)
	}
	// (4) Pass-through seed-only platforms.
	for name, plat := range seed.Platforms {
		if _, ok := out.Platforms[name]; ok {
			continue
		}
		out.Platforms[name] = config.ListedPlatform{BaseAssets: append([]string(nil), plat.BaseAssets...)}
	}
	for _, plat := range out.Platforms {
		res.TotalBases += len(plat.BaseAssets)
	}

	// (5) Atomic write.
	if err := writeListedUniverseAtomic(args.RuntimePath, out); err != nil {
		return res, fmt.Errorf("write runtime yaml: %w", err)
	}

	// (6) Reconcile candidates (edgeX surface only — that is the
	//     only platform whose universe drives the candidate state
	//     machine).
	perpBases := basesFor(dbRows, "edgeX", "perp")
	spotBases := basesFor(dbRows, "edgeX", "spot")
	nPerp, perpErr := repo.BulkMarkCandidatesAlreadyListed(ctx, perpBases, "perp", now)
	if perpErr != nil {
		return res, fmt.Errorf("bulk mark perp: %w", perpErr)
	}
	res.PerpReconciled = nPerp
	nSpot, spotErr := repo.BulkMarkCandidatesAlreadyListed(ctx, spotBases, "spot", now)
	if spotErr != nil {
		return res, fmt.Errorf("bulk mark spot: %w", spotErr)
	}
	res.SpotReconciled = nSpot
	res.CandidatesReconciled = nPerp + nSpot
	args.Metrics.Add("listing_already_listed_reconciled_total", float64(res.CandidatesReconciled))
	return res, nil
}

func rowsForPlatform(rows []PlatformBaseSurface, platform string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Platform == platform {
			out = append(out, r.BaseAsset)
		}
	}
	return out
}

func basesFor(rows []PlatformBaseSurface, platform, surface string) []string {
	out := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, r := range rows {
		if r.Platform != platform || r.MarketSurface != surface {
			continue
		}
		base := strings.ToUpper(strings.TrimSpace(r.BaseAsset))
		if base == "" {
			continue
		}
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	sort.Strings(out)
	return out
}

func dedupSortedBases(bases []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(bases))
	for _, b := range bases {
		b = strings.ToUpper(strings.TrimSpace(b))
		if b == "" {
			continue
		}
		if _, ok := seen[b]; ok {
			continue
		}
		seen[b] = struct{}{}
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

func surfaceCountsForPlatform(rows []PlatformBaseSurface, platform string) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		if r.Platform != platform {
			continue
		}
		surface := strings.TrimSpace(r.MarketSurface)
		if surface == "" {
			surface = "unknown"
		}
		out[surface]++
	}
	return out
}

func sampleMissingBases(left, right []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	rightSet := map[string]struct{}{}
	for _, item := range right {
		item = strings.ToUpper(strings.TrimSpace(item))
		if item != "" {
			rightSet[item] = struct{}{}
		}
	}
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, item := range left {
		item = strings.ToUpper(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := rightSet[item]; ok {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func decideListedUniversePlatform(platform string, dbBases, seedBases []string, dbRows []PlatformBaseSurface, previous *SourceState, args ListedUniverseRefreshArgs) listedUniversePlatformDecision {
	policy := args.PlatformOverrides[platform]
	floor := args.ShrinkFloor
	if policy.ShrinkFloor > 0 && policy.ShrinkFloor <= 1 {
		floor = policy.ShrinkFloor
	}
	bootstrap := strings.TrimSpace(policy.BootstrapBaseline)
	if bootstrap == "" {
		bootstrap = "seed_floor"
	}
	ctx := listedUniverseRefreshContext{
		SchemaVersion:      1,
		Platform:           platform,
		DBFreshActiveCount: len(dbBases),
		SeedCount:          len(seedBases),
		ShrinkFloor:        floor,
		BootstrapBaseline:  bootstrap,
		BootstrapMinCount:  policy.BootstrapMinCount,
		FreshWindowSeconds: int64(args.FreshWindow.Seconds()),
		SurfaceCounts:      surfaceCountsForPlatform(dbRows, platform),
		SeedOnlySample:     sampleMissingBases(seedBases, dbBases, 15),
		DBOnlySample:       sampleMissingBases(dbBases, seedBases, 15),
	}

	if previousCount := previousListedUniverseSuccessCount(previous); previousCount > 0 {
		ctx.BaselineType = "previous_success"
		ctx.BaselineCount = previousCount
		ctx.PreviousSuccessDBFreshActiveCount = previousCount
		ctx.Threshold = int(floor * float64(previousCount))
		ctx.RatioToBaseline = ratio(len(dbBases), previousCount)
		if len(dbBases) < ctx.Threshold {
			ctx.Decision = "fallback_seed"
			ctx.Reason = "runtime_shrink_below_previous_success"
			msg := fmt.Sprintf("listed_universe runtime shrink triggered: db=%d previous_success=%d floor=%.2f", len(dbBases), previousCount, floor)
			return listedUniversePlatformDecision{UseDB: false, Message: msg, Context: ctx}
		}
		ctx.Decision = "use_db"
		ctx.Reason = "within_previous_success_floor"
		return listedUniversePlatformDecision{UseDB: true, Context: ctx}
	}

	if bootstrap == "db_first" {
		ctx.BaselineType = "db_first_bootstrap"
		ctx.BaselineCount = policy.BootstrapMinCount
		ctx.Threshold = policy.BootstrapMinCount
		ctx.RatioToBaseline = ratio(len(dbBases), policy.BootstrapMinCount)
		if len(dbBases) < policy.BootstrapMinCount {
			ctx.Decision = "fallback_seed"
			ctx.Reason = "db_first_bootstrap_below_min_count"
			msg := fmt.Sprintf("listed_universe db_first bootstrap below minimum: db=%d min=%d", len(dbBases), policy.BootstrapMinCount)
			return listedUniversePlatformDecision{UseDB: false, Message: msg, Context: ctx}
		}
		ctx.Decision = "use_db"
		ctx.Reason = "db_first_bootstrap_min_count_met"
		return listedUniversePlatformDecision{UseDB: true, Context: ctx}
	}

	ctx.BaselineType = "seed_floor"
	ctx.BaselineCount = len(seedBases)
	ctx.Threshold = int(floor * float64(len(seedBases)))
	ctx.RatioToBaseline = ratio(len(dbBases), len(seedBases))
	if len(seedBases) > 0 && len(dbBases) < ctx.Threshold {
		ctx.Decision = "fallback_seed"
		ctx.Reason = "db_below_seed_floor"
		msg := fmt.Sprintf("listed_universe shrink_floor triggered: db=%d seed=%d floor=%.2f", len(dbBases), len(seedBases), floor)
		return listedUniversePlatformDecision{UseDB: false, Message: msg, Context: ctx}
	}
	ctx.Decision = "use_db"
	ctx.Reason = "seed_floor_met_or_seed_missing"
	return listedUniversePlatformDecision{UseDB: true, Context: ctx}
}

func previousListedUniverseSuccessCount(s *SourceState) int {
	if s == nil || s.Status != SourceStatusOK || len(s.SourceContextJSON) == 0 {
		return 0
	}
	var ctx listedUniverseRefreshContext
	if err := json.Unmarshal(s.SourceContextJSON, &ctx); err != nil {
		return 0
	}
	if ctx.DBFreshActiveCount > 0 {
		return ctx.DBFreshActiveCount
	}
	return ctx.PreviousSuccessDBFreshActiveCount
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func listedUniverseSourceKey(platform string) string {
	return fmt.Sprintf("listing/listed_universe/%s", platform)
}

func marshalListedUniverseContext(ctx listedUniverseRefreshContext) json.RawMessage {
	body, err := json.Marshal(ctx)
	if err != nil {
		return nil
	}
	return json.RawMessage(body)
}

func debugListedUniverseSnapshotBreakdown(ctx context.Context, repo *Repository, coveredPlatforms []string, cutoff time.Time) {
	if repo == nil || repo.db == nil || len(coveredPlatforms) == 0 {
		return
	}
	placeholders := make([]string, 0, len(coveredPlatforms))
	args := make([]any, 0, len(coveredPlatforms))
	for _, p := range coveredPlatforms {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, p)
	}
	if len(placeholders) == 0 {
		return
	}
	query := fmt.Sprintf(`SELECT platform,
	       COALESCE(market_surface, '') AS market_surface,
	       COALESCE(status_normalized, '') AS status_normalized,
	       COALESCE(instrument_kind, '') AS instrument_kind,
	       SUM(CASE WHEN last_seen_at >= ? THEN 1 ELSE 0 END) AS fresh_rows,
	       COUNT(*) AS total_rows,
	       COUNT(DISTINCT CASE WHEN last_seen_at >= ? THEN base_asset END) AS fresh_bases,
	       COUNT(DISTINCT base_asset) AS total_bases,
	       MIN(last_seen_at) AS min_seen_at,
	       MAX(last_seen_at) AS max_seen_at
	  FROM t_listing_instrument_snapshot
	 WHERE platform IN (%s)
	   AND COALESCE(base_asset, '') <> ''
	 GROUP BY platform, market_surface, status_normalized, instrument_kind
	 ORDER BY platform, market_surface, status_normalized, instrument_kind`, strings.Join(placeholders, ","))
	queryArgs := make([]any, 0, len(args)+2)
	queryArgs = append(queryArgs, cutoff, cutoff)
	queryArgs = append(queryArgs, args...)
	rows, err := repo.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		debugListedUniverseLog("H2,H3,H4", "backend/internal/listing/listed_universe_refresh.go:snapshot_breakdown_error", "listed universe snapshot breakdown query failed", map[string]any{
			"error": err.Error(),
		})
		return
	}
	defer rows.Close()
	type breakdownRow struct {
		Platform         string `json:"platform"`
		MarketSurface    string `json:"marketSurface"`
		StatusNormalized string `json:"statusNormalized"`
		InstrumentKind   string `json:"instrumentKind"`
		FreshRows        int64  `json:"freshRows"`
		TotalRows        int64  `json:"totalRows"`
		FreshBases       int64  `json:"freshBases"`
		TotalBases       int64  `json:"totalBases"`
		MinSeenAt        string `json:"minSeenAt"`
		MaxSeenAt        string `json:"maxSeenAt"`
	}
	breakdown := []breakdownRow{}
	for rows.Next() {
		var row breakdownRow
		var minSeenAt, maxSeenAt sql.NullTime
		if err := rows.Scan(&row.Platform, &row.MarketSurface, &row.StatusNormalized, &row.InstrumentKind, &row.FreshRows, &row.TotalRows, &row.FreshBases, &row.TotalBases, &minSeenAt, &maxSeenAt); err != nil {
			debugListedUniverseLog("H2,H3,H4", "backend/internal/listing/listed_universe_refresh.go:snapshot_breakdown_scan_error", "listed universe snapshot breakdown scan failed", map[string]any{
				"error": err.Error(),
			})
			return
		}
		if minSeenAt.Valid {
			row.MinSeenAt = minSeenAt.Time.UTC().Format(time.RFC3339)
		}
		if maxSeenAt.Valid {
			row.MaxSeenAt = maxSeenAt.Time.UTC().Format(time.RFC3339)
		}
		breakdown = append(breakdown, row)
	}
	if err := rows.Err(); err != nil {
		debugListedUniverseLog("H2,H3,H4", "backend/internal/listing/listed_universe_refresh.go:snapshot_breakdown_rows_error", "listed universe snapshot breakdown rows failed", map[string]any{
			"error": err.Error(),
		})
		return
	}
	//region debug-be6994 listed-universe snapshot breakdown
	debugListedUniverseLog("H2,H3,H4", "backend/internal/listing/listed_universe_refresh.go:snapshot_breakdown", "listed universe DB snapshot breakdown", map[string]any{
		"cutoff":    cutoff.UTC().Format(time.RFC3339),
		"breakdown": breakdown,
	})
	//endregion
}

func debugListedUniverseLog(hypothesisID, location, message string, data map[string]any) {
	entry := map[string]any{
		"sessionId":    "be6994",
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().UnixMilli(),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	//region debug-be6994 listed-universe docker stdout fallback
	// Docker containers cannot always write to the host debug path. Keep a stdout
	// fallback so runtime evidence is still visible in `docker logs`.
	fmt.Printf("debug-be6994 %s\n", string(line))
	//endregion
	debugPostListedUniverseLog(line)
	f, err := os.OpenFile("/Users/pis/workspace-intelligence/edgex-intelligence/.cursor/debug-be6994.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func debugPostListedUniverseLog(line []byte) {
	endpoints := []string{
		"http://127.0.0.1:7751/ingest/7e939475-49af-44d7-87cf-0aa77e4d521c",
		"http://host.docker.internal:7751/ingest/7e939475-49af-44d7-87cf-0aa77e4d521c",
	}
	client := http.Client{Timeout: 200 * time.Millisecond}
	for _, endpoint := range endpoints {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(line))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Debug-Session-Id", "be6994")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
	}
}

// writeListedUniverseAtomic writes the universe yaml via temp +
// rename so a concurrent reader never observes a partial file. The
// temp lives in the same directory as the target so the rename is a
// same-filesystem operation (cross-fs rename is non-atomic on most
// kernels).
func writeListedUniverseAtomic(path string, u config.ListedUniverse) error {
	if path == "" {
		return errors.New("write listed universe: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(&u)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// recordShrinkFloorError surfaces the safety-net trip on a synthetic
// listed_universe_refresh source row. It intentionally does not
// overwrite the underlying instrument source (for example bybit/linear)
// because shrink fallback is a derived-universe health signal rather
// than a parser/fetcher failure in the poll source itself.
func recordShrinkFloorError(ctx context.Context, repo *Repository, platform string, msg string, refreshCtx listedUniverseRefreshContext, now time.Time) {
	if repo == nil {
		return
	}
	_ = repo.UpsertSourceState(ctx, SourceState{
		SourceKey:         listedUniverseSourceKey(platform),
		SourceType:        "listed_universe_refresh",
		Platform:          platform,
		Status:            SourceStatusSchemaDrift,
		LastErrorAt:       &now,
		LastError:         msg,
		SourceContextJSON: marshalListedUniverseContext(refreshCtx),
		UpdatedAt:         now,
	})
}

func recordListedUniverseRefreshOK(ctx context.Context, repo *Repository, platform string, refreshCtx listedUniverseRefreshContext, now time.Time) {
	if repo == nil {
		return
	}
	_ = repo.UpsertSourceState(ctx, SourceState{
		SourceKey:         listedUniverseSourceKey(platform),
		SourceType:        "listed_universe_refresh",
		Platform:          platform,
		Status:            SourceStatusOK,
		LastSuccessAt:     &now,
		LastError:         "",
		SourceContextJSON: marshalListedUniverseContext(refreshCtx),
		UpdatedAt:         now,
	})
}
