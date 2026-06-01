package collector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSnapshotReader is the test double the DB-first resolver tests
// drive. It records every call so assertions like "convention
// platforms MUST NOT query the DB" can be enforced.
type fakeSnapshotReader struct {
	calls atomic.Int32
	rows  map[string][]SnapshotRow
	err   error
}

func (f *fakeSnapshotReader) ListLatestInstrumentSnapshotsByPlatform(ctx context.Context, platform string) ([]SnapshotRow, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[platform], nil
}

func newDBResolver(t *testing.T, reader SnapshotReader, ttl time.Duration, clock func() time.Time) *CatalogResolver {
	t.Helper()
	r := NewCatalogResolverWithDB(t.TempDir(), reader, ttl, clock)
	return r
}

// TestResolveDBFirstHyperliquidUsesDBOverDump pins the §C contract:
// hyperliquid is one of the 4 DB-first platforms, so the resolver
// must consult SnapshotReader and SUCCEED without any file dump
// being present on disk.
func TestResolveDBFirstHyperliquidUsesDBOverDump(t *testing.T) {
	reader := &fakeSnapshotReader{
		rows: map[string][]SnapshotRow{
			"hyperliquid": {
				{Platform: "hyperliquid", APISymbol: "BTC", BaseAsset: "BTC", MarketSurface: "perp", InstrumentKind: "canonical"},
				{Platform: "hyperliquid", APISymbol: "kPEPE", BaseAsset: "KPEPE", MarketSurface: "perp", InstrumentKind: "canonical"},
			},
		},
	}
	r := newDBResolver(t, reader, 5*time.Minute, func() time.Time { return time.Unix(0, 0) })
	sub, err := r.Resolve("hyperliquid", "BTC", "BTC-USD (perp)")
	if err != nil {
		t.Fatalf("resolve hyperliquid/BTC: %v", err)
	}
	if sub.APISymbol != "BTC" {
		t.Fatalf("api_symbol=%q want BTC", sub.APISymbol)
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("expected 1 DB call, got %d", reader.calls.Load())
	}
}

// TestResolveDBFirstGatePreservesQuantoFromRawJSON proves the gate
// DB-first path extracts quanto_multiplier from raw_json (the
// snapshot row's only carrier for the field) and pipes it into the
// returned SymbolSub.
func TestResolveDBFirstGatePreservesQuantoFromRawJSON(t *testing.T) {
	reader := &fakeSnapshotReader{
		rows: map[string][]SnapshotRow{
			"gate": {
				{
					Platform: "gate", APISymbol: "BTC_USDT", BaseAsset: "BTC",
					MarketType: "usdt_futures", MarketSurface: "perp", InstrumentKind: "canonical",
					RawJSON: []byte(`{"name":"BTC_USDT","quanto_multiplier":"0.0001"}`),
				},
			},
		},
	}
	r := newDBResolver(t, reader, 5*time.Minute, time.Now)
	sub, err := r.Resolve("gate", "BTC", "")
	if err != nil {
		t.Fatalf("resolve gate/BTC: %v", err)
	}
	if sub.APISymbol != "BTC_USDT" {
		t.Fatalf("api_symbol=%q want BTC_USDT", sub.APISymbol)
	}
	if sub.QuantoMultiplier != 0.0001 {
		t.Fatalf("quanto_multiplier=%v want 0.0001", sub.QuantoMultiplier)
	}
}

// TestResolveDBFirstLighterPreservesMarketID asserts the lighter
// path uses api_market_id (the column populated by the normalizer)
// to populate the SymbolSub.MarketID pointer the candles adapter
// requires.
func TestResolveDBFirstLighterPreservesMarketID(t *testing.T) {
	reader := &fakeSnapshotReader{
		rows: map[string][]SnapshotRow{
			"lighter": {
				{
					Platform: "lighter", APISymbol: "BTC", BaseAsset: "BTC",
					APIMarketID: "1", MarketType: "perp", MarketSurface: "perp", InstrumentKind: "canonical",
				},
			},
		},
	}
	r := newDBResolver(t, reader, 5*time.Minute, time.Now)
	sub, err := r.Resolve("lighter", "BTC", "")
	if err != nil {
		t.Fatalf("resolve lighter/BTC: %v", err)
	}
	if sub.MarketID == nil || *sub.MarketID != 1 {
		t.Fatalf("market_id = %v, want 1", sub.MarketID)
	}
}

// TestResolveDBFirstEdgeXPreservesContractIDFromRawJSON pins the
// edgeX path: contract_id lives in raw_json (the normalizer keeps
// it there so a schema change does not need a migration).
func TestResolveDBFirstEdgeXPreservesContractIDFromRawJSON(t *testing.T) {
	reader := &fakeSnapshotReader{
		rows: map[string][]SnapshotRow{
			"edgeX": {
				{
					Platform: "edgeX", APISymbol: "BTCUSD", BaseAsset: "BTC",
					MarketType: "perp_v1", MarketSurface: "perp", InstrumentKind: "canonical",
					RawJSON: []byte(`{"contractId":"10000001","contractName":"BTCUSD","baseCoinId":"1000"}`),
				},
			},
		},
	}
	r := newDBResolver(t, reader, 5*time.Minute, time.Now)
	sub, err := r.Resolve("edgeX", "BTC", "")
	if err != nil {
		t.Fatalf("resolve edgeX/BTC: %v", err)
	}
	if sub.ContractID != "10000001" {
		t.Fatalf("contract_id=%q want 10000001", sub.ContractID)
	}
}

// TestResolveConventionOnlyPlatformsDoNotQueryDB asserts the §C
// short-circuit: binance/okx/bybit/bitget/mexc/bingx synthesize
// their api_symbol from the base alone, so the DB MUST NOT be hit.
func TestResolveConventionOnlyPlatformsDoNotQueryDB(t *testing.T) {
	reader := &fakeSnapshotReader{}
	r := newDBResolver(t, reader, 5*time.Minute, time.Now)
	for _, p := range []string{"binance", "okx", "bybit", "bitget", "mexc", "bingx"} {
		if _, err := r.Resolve(p, "BTC", ""); err != nil {
			t.Fatalf("%s/BTC: %v", p, err)
		}
	}
	if reader.calls.Load() != 0 {
		t.Fatalf("convention-only platforms must NOT query DB; got %d calls", reader.calls.Load())
	}
}

// TestResolveTTLRefreshAfterExpiry verifies the per-platform 5min
// TTL: a second call within the window MUST NOT re-query DB; a
// call after expiry MUST re-query.
func TestResolveTTLRefreshAfterExpiry(t *testing.T) {
	reader := &fakeSnapshotReader{
		rows: map[string][]SnapshotRow{
			"lighter": {{Platform: "lighter", APISymbol: "BTC", BaseAsset: "BTC", APIMarketID: "1", MarketType: "perp", MarketSurface: "perp", InstrumentKind: "canonical"}},
		},
	}
	now := time.Unix(0, 0)
	r := newDBResolver(t, reader, 5*time.Minute, func() time.Time { return now })
	if _, err := r.Resolve("lighter", "BTC", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("lighter", "BTC", ""); err != nil {
		t.Fatal(err)
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("within TTL: expected 1 call, got %d", reader.calls.Load())
	}
	now = now.Add(10 * time.Minute)
	if _, err := r.Resolve("lighter", "BTC", ""); err != nil {
		t.Fatal(err)
	}
	if reader.calls.Load() != 2 {
		t.Fatalf("after TTL: expected 2 calls total, got %d", reader.calls.Load())
	}
}

// TestResolveDBEmptyFallsBackToFile pins the F5 safety net: when
// the DB returns no rows for a DB-first platform, the resolver
// falls back to the file dump rather than failing the lookup.
// Without a file dump in the temp dir the second leg returns
// ErrSymbolUnsupported so we can assert both legs were tried.
func TestResolveDBEmptyFallsBackToFile(t *testing.T) {
	reader := &fakeSnapshotReader{rows: map[string][]SnapshotRow{}}
	r := newDBResolver(t, reader, 5*time.Minute, time.Now)
	if _, err := r.Resolve("lighter", "BTC", ""); !errors.Is(err, ErrSymbolUnsupported) {
		t.Fatalf("empty DB + missing file dump should yield ErrSymbolUnsupported, got %v", err)
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 DB call before file fallback; got %d", reader.calls.Load())
	}
}

// TestResolveDBErrorFallsBackToFile mirrors the DB-empty case but
// for a query error (transient outage / lock wait timeout). The
// resolver MUST proceed to the file fallback rather than propagating
// the DB error — backfill must remain best-effort.
func TestResolveDBErrorFallsBackToFile(t *testing.T) {
	reader := &fakeSnapshotReader{err: errors.New("sql: connection refused")}
	r := newDBResolver(t, reader, 5*time.Minute, time.Now)
	_, err := r.Resolve("lighter", "BTC", "")
	if !errors.Is(err, ErrSymbolUnsupported) {
		t.Fatalf("DB error must fall back to file; got %v", err)
	}
}
