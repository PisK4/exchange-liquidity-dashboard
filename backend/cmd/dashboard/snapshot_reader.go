package main

import (
	"context"

	"edgex-dashboard/backend/internal/collector"
	"edgex-dashboard/backend/internal/listing"
)

// listingSnapshotReader adapts *listing.Repository to the narrow
// collector.SnapshotReader interface the CatalogResolver consumes.
// Keeping the adapter in cmd/dashboard (not in listing or collector)
// is what lets listing keep its rich domain.InstrumentSnapshot type
// without leaking it across package boundaries, and lets collector
// stay agnostic of the repository layer.
type listingSnapshotReader struct {
	repo *listing.Repository
}

func newListingSnapshotReader(repo *listing.Repository) *listingSnapshotReader {
	return &listingSnapshotReader{repo: repo}
}

// ListLatestInstrumentSnapshotsByPlatform forwards to the listing
// repository and projects each row to the collector's SnapshotRow
// shape. We deliberately drop columns the resolver does not consume
// (StatusRaw, listing_time_ts, ...) so the resolver does not start
// depending on listing-internal semantics.
func (r *listingSnapshotReader) ListLatestInstrumentSnapshotsByPlatform(ctx context.Context, platform string) ([]collector.SnapshotRow, error) {
	rows, err := r.repo.ListLatestInstrumentSnapshotsByPlatform(ctx, platform)
	if err != nil {
		return nil, err
	}
	out := make([]collector.SnapshotRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, collector.SnapshotRow{
			Platform:       row.Platform,
			MarketType:     row.MarketType,
			APISymbol:      row.APISymbol,
			APIMarketID:    row.APIMarketID,
			BaseAsset:      row.BaseAsset,
			MarketSurface:  row.MarketSurface,
			InstrumentKind: row.InstrumentKind,
			RawJSON:        []byte(row.RawJSON),
		})
	}
	return out, nil
}
