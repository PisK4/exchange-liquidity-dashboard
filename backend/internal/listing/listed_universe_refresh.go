package listing

import (
	"context"
	"errors"
	"fmt"
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
	SeedPath         string
	RuntimePath      string
	FreshWindow      time.Duration
	ShrinkFloor      float64 // default 0.5; clamped at 0..1
	CoveredPlatforms []string
	Now              time.Time      // pinned for tests; zero means use repo clock
	Metrics          MetricRecorder // optional; defaults to NopMetrics
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

	// (2) DB-derived bases.
	dbRows, err := repo.QueryActiveListedBases(ctx, args.FreshWindow)
	if err != nil {
		return ListedUniverseRefreshResult{}, fmt.Errorf("query active bases: %w", err)
	}

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
		if len(seedBases) > 0 && len(dbBases) < int(args.ShrinkFloor*float64(len(seedBases))) {
			out.Platforms[platform] = config.ListedPlatform{BaseAssets: append([]string(nil), seedBases...)}
			res.PlatformsFromSeed = append(res.PlatformsFromSeed, platform)
			args.Metrics.Inc("listed_universe_shrink_fallback_total", platform)
			recordShrinkFloorError(ctx, repo, platform, len(dbBases), len(seedBases), args.ShrinkFloor, now)
			continue
		}
		out.Platforms[platform] = config.ListedPlatform{BaseAssets: dbBases}
		res.PlatformsFromDB = append(res.PlatformsFromDB, platform)
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

// recordShrinkFloorError surfaces the safety-net trip on
// t_listing_source_state.last_error so operators can see it next
// to the rest of the source health. The platform key is the same
// one PollWithSourceHealth uses for the underlying instrument source
// so the row lines up in /admin/listing/sources.
func recordShrinkFloorError(ctx context.Context, repo *Repository, platform string, dbCount, seedCount int, floor float64, now time.Time) {
	if repo == nil {
		return
	}
	msg := fmt.Sprintf("listed_universe shrink_floor triggered: db=%d seed=%d floor=%.2f", dbCount, seedCount, floor)
	_ = repo.UpsertSourceState(ctx, SourceState{
		SourceKey:   fmt.Sprintf("listing/listed_universe/%s", platform),
		SourceType:  "listed_universe_refresh",
		Platform:    platform,
		Status:      "schema_drift",
		LastErrorAt: &now,
		LastError:   msg,
		UpdatedAt:   now,
	})
}
