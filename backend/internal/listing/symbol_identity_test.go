package listing

import (
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing/announcement"
	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

type fakeIdentityResolver map[string]config.CanonicalIdentity

func (r fakeIdentityResolver) ResolveIdentity(platform, base string) config.CanonicalIdentity {
	if identity, ok := r[platform+"|"+base]; ok {
		return identity
	}
	return config.CanonicalIdentity{Canonical: base, MatchKind: config.CanonicalMatchNoMatch}
}

func TestApplyInstrumentSymbolIdentityPreservesNativeFields(t *testing.T) {
	resolver := fakeIdentityResolver{
		"mexc|EBAYSTOCK": {
			Canonical:      "EBAY",
			DisplaySymbol:  "EBAY-USDT (perp)",
			MarketSurface:  "synthetic_futures",
			InstrumentKind: "synthetic",
			Matched:        true,
		},
	}
	n := instrument.NormalizedInstrument{
		Platform:        "mexc",
		MarketType:      "contract",
		APISymbol:       "EBAYSTOCK_USDT",
		CanonicalSymbol: "EBAYSTOCK",
		BaseAsset:       "EBAYSTOCK",
		QuoteAsset:      "USDT",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
	}
	n.StableHash = n.ComputeStableHash()

	got := ApplyInstrumentSymbolIdentity(n, resolver)

	if got.CanonicalSymbol != "EBAY" || got.DisplaySymbol != "EBAY-USDT (perp)" {
		t.Fatalf("business identity = %s/%s", got.CanonicalSymbol, got.DisplaySymbol)
	}
	if got.MarketSurface != "synthetic_futures" || got.InstrumentKind != "synthetic" {
		t.Fatalf("identity kind = %s/%s", got.MarketSurface, got.InstrumentKind)
	}
	if got.APISymbol != "EBAYSTOCK_USDT" || got.BaseAsset != "EBAYSTOCK" || got.QuoteAsset != "USDT" {
		t.Fatalf("native fields changed: %+v", got)
	}
	if got.StableHash == n.StableHash {
		t.Fatal("stable hash should be recomputed after identity changes")
	}
}

func TestApplyAnnouncementSymbolIdentityUsesRawBaseFallback(t *testing.T) {
	resolver := fakeIdentityResolver{
		"hyperliquid|XYZ:EBAY": {
			Canonical:      "EBAY",
			DisplaySymbol:  "EBAY-USD (perp)",
			MarketSurface:  "synthetic_futures",
			InstrumentKind: "synthetic",
			Matched:        true,
		},
	}
	sym := announcement.ParsedAnnouncementSymbol{
		RawSymbol:       "XYZ:EBAY",
		CanonicalSymbol: "XYZ:EBAY",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
	}

	got := ApplyAnnouncementSymbolIdentity("hyperliquid", sym, resolver)

	if got.CanonicalSymbol != "EBAY" || got.DisplaySymbol != "EBAY-USD (perp)" {
		t.Fatalf("announcement identity = %+v", got)
	}
	if got.RawSymbol != "XYZ:EBAY" {
		t.Fatalf("raw symbol changed: %+v", got)
	}
}

func TestApplySignalSymbolIdentityNormalizesLegacyUnfusedSignal(t *testing.T) {
	resolver := fakeIdentityResolver{
		"gate|TSLAX": {
			Canonical:      "TSLA",
			MarketSurface:  "synthetic_futures",
			InstrumentKind: "synthetic",
			Matched:        true,
		},
	}
	s := SignalObservation{
		SourcePlatform:  "gate",
		CanonicalSymbol: "TSLAX",
		BaseAsset:       "TSLAX",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
	}

	got := ApplySignalSymbolIdentity(s, resolver)

	if got.CanonicalSymbol != "TSLA" || got.MarketSurface != "synthetic_futures" || got.InstrumentKind != "synthetic" {
		t.Fatalf("signal identity = %+v", got)
	}
}

func TestApplyInstrumentSymbolIdentityNoMatchKeepsInput(t *testing.T) {
	n := instrument.NormalizedInstrument{
		Platform:        "binance",
		APISymbol:       "1000PEPEUSDT",
		CanonicalSymbol: "1000PEPE",
		BaseAsset:       "1000PEPE",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
	}
	got := ApplyInstrumentSymbolIdentity(n, fakeIdentityResolver{})
	if got.CanonicalSymbol != n.CanonicalSymbol || got.BaseAsset != n.BaseAsset || got.MarketSurface != n.MarketSurface {
		t.Fatalf("no-match changed instrument: %+v", got)
	}
}
