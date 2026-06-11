package listing

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestLoadLatestDepthEvidenceUsesReferencePlatformIndexShape(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-30 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.platform, COALESCE(s.total_usd, 0), s.tier, s.snapshot_ts")).
		WithArgs("ABC", "spot", "0.10%", cutoff, "binance", "okx", "ABC", "spot", "0.10%", cutoff, "binance", "okx").
		WillReturnRows(sqlmock.NewRows([]string{"platform", "total_usd", "tier", "snapshot_ts"}).
			AddRow("binance", 580000.0, "0.10%", now))

	got, err := repo.LoadLatestDepthEvidence(context.Background(), " abc ", "spot", "0.10%", 30*time.Minute, now, []string{"okx", "binance", "BINANCE"})
	if err != nil {
		t.Fatalf("LoadLatestDepthEvidence err = %v", err)
	}
	if got == nil || got.Platform != "binance" || got.USDValue != 580000 || got.Tier != "0.10%" || got.Source != DecisionCardMetricSourceDBSnapshot {
		t.Fatalf("depth evidence = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestLoadLatestDepthEvidenceReturnsNilWhenNoFreshSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.platform, COALESCE(s.total_usd, 0), s.tier, s.snapshot_ts")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "total_usd", "tier", "snapshot_ts"}))

	got, err := repo.LoadLatestDepthEvidence(context.Background(), "ABC", "perp", "0.10%", 30*time.Minute, now, nil)
	if err != nil {
		t.Fatalf("LoadLatestDepthEvidence err = %v", err)
	}
	if got != nil {
		t.Fatalf("depth evidence = %+v, want nil", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestLoadLatestSpotVolumeEvidenceUsesSpotOnlySnapshots(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-30 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(SUM(platform_volume), 0), MAX(snapshot_ts), COUNT(*)")).
		WithArgs("ABC", cutoff, "ABC", cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"volume", "snapshot_ts", "count"}).
			AddRow(35000000.0, now, 2))

	got, err := repo.LoadLatestSpotVolumeEvidence(context.Background(), "abc", 30*time.Minute, now, nil)
	if err != nil {
		t.Fatalf("LoadLatestSpotVolumeEvidence err = %v", err)
	}
	if got == nil || got.USDValue != 35000000 || got.PlatformCount != 2 || got.Source != DecisionCardMetricSourceDBSnapshot {
		t.Fatalf("volume evidence = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBuildFallbackDepthFetcherFillsMissingSideFromSnapshot(t *testing.T) {
	primary := func(ctx context.Context, canonical string, sourcePlatforms []string) (*DepthEvidence, *DepthEvidence, error) {
		return &DepthEvidence{Platform: "binance", USDValue: 580000, Tier: "0.10%"}, nil, ErrDepthUnavailable
	}
	fallback := func(ctx context.Context, canonical string, sourcePlatforms []string) (*DepthEvidence, *DepthEvidence, error) {
		return nil, &DepthEvidence{Platform: "binance", USDValue: 1200000, Tier: "0.10%", Source: DecisionCardMetricSourceDBSnapshot}, nil
	}
	merged := BuildFallbackDepthFetcher(primary, fallback)
	spot, perp, err := merged(context.Background(), "ABC", []string{"bybit"})
	if err != nil {
		t.Fatalf("merged depth err = %v", err)
	}
	if spot == nil || spot.Source != DecisionCardMetricSourceLiveReference || spot.USDValue != 580000 {
		t.Fatalf("spot = %+v", spot)
	}
	if perp == nil || perp.Source != DecisionCardMetricSourceDBSnapshot || perp.USDValue != 1200000 {
		t.Fatalf("perp = %+v", perp)
	}
}
