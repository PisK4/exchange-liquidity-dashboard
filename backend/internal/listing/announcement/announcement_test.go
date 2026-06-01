package announcement

import (
	"testing"
)

func TestParseBybitAnnouncementSplitsSymbols(t *testing.T) {
	// Real Bybit "list multiple perps in one announcement" titles
	// pair the base with USDT directly (concatenated form). The
	// parser must extract both base symbols and strip the USDT
	// suffix.
	raw := []byte(`{"id":"a1","title":"ABCUSDT and 1000PEPEUSDT Perpetual Contracts Will Be Listed","publishTime":1893456000000}`)
	got, err := ParseBybitAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.Platform != "bybit" {
		t.Fatalf("platform = %q", got.Platform)
	}
	if got.AnnouncementID != "a1" {
		t.Fatalf("announcement_id = %q", got.AnnouncementID)
	}
	if len(got.Symbols) != 2 {
		t.Fatalf("symbols len = %d, want 2 (%+v)", len(got.Symbols), got.Symbols)
	}
	if got.Symbols[0].CanonicalSymbol != "ABC" || got.Symbols[1].CanonicalSymbol != "1000PEPE" {
		t.Fatalf("unexpected symbols: %+v", got.Symbols)
	}
	for _, s := range got.Symbols {
		if s.MarketSurface != "perp" || s.InstrumentKind != "canonical" {
			t.Fatalf("symbol marketing = %+v", s)
		}
		if s.SignalSubtype != "perp_listing_announcement" {
			t.Fatalf("signal_subtype = %q", s.SignalSubtype)
		}
	}
	if got.PublishedAt == nil {
		t.Fatalf("published_at should be parsed from publishTime")
	}
}

// TestParseBybitAnnouncementRejectsLeadingNoiseWords pins the SLXUSDT
// regression: the title starts with "New Listing:" which previously
// caused the parser to extract "NEW" as a canonical symbol because
// the broad regex matched it and the stopword list did not include
// "NEW". The new parser anchors on the USDT/USDC/USD suffix so noise
// words at the start of the title are silently ignored.
func TestParseBybitAnnouncementRejectsLeadingNoiseWords(t *testing.T) {
	raw := []byte(`{"id":"a2","title":"New Listing: SLXUSDT Perpetual Contract with up to 20x Leverage","publishTime":1893456000000}`)
	got, err := ParseBybitAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if len(got.Symbols) != 1 {
		t.Fatalf("symbols len = %d, want 1 (%+v)", len(got.Symbols), got.Symbols)
	}
	if got.Symbols[0].CanonicalSymbol != "SLX" {
		t.Fatalf("canonical = %q, want SLX (the base, with USDT suffix stripped)", got.Symbols[0].CanonicalSymbol)
	}
}

func TestParseBitgetAnnouncementSingleSymbol(t *testing.T) {
	raw := []byte(`{"announceId":"42","announceTitle":"XYZ USDT-M Perpetual Contract Launch Notice","publishTime":"1893456000000"}`)
	got, err := ParseBitgetAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.Platform != "bitget" || got.AnnouncementID != "42" {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(got.Symbols) != 1 || got.Symbols[0].CanonicalSymbol != "XYZ" {
		t.Fatalf("symbols = %+v", got.Symbols)
	}
}

func TestParseBinanceCMSSchemaDrift(t *testing.T) {
	raw := []byte(`{"unexpected":true}`)
	_, err := ParseBinanceCMSAnnouncement(raw)
	if err == nil {
		t.Fatalf("expected schema drift error")
	}
	if _, ok := err.(*SchemaDriftError); !ok {
		t.Fatalf("expected SchemaDriftError, got %T", err)
	}
}

func TestParseBinanceCMSAcceptsNumericID(t *testing.T) {
	// Binance bapi sometimes returns id as a JSON number rather
	// than a string. The parser must accept both forms to survive
	// schema drift across catalogs / dashboard generations.
	raw := []byte(`{"id":167890,"title":"ABCDEF USDT Perpetual","releaseDate":1893456000000}`)
	got, err := ParseBinanceCMSAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.AnnouncementID != "167890" {
		t.Fatalf("announcement_id = %q, want 167890", got.AnnouncementID)
	}
}

func TestParseBinanceCMSAcceptsStringID(t *testing.T) {
	raw := []byte(`{"id":"abc-123","title":"ABCDEF USDT Perpetual","releaseDate":1893456000000}`)
	got, err := ParseBinanceCMSAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.AnnouncementID != "abc-123" {
		t.Fatalf("announcement_id = %q, want abc-123", got.AnnouncementID)
	}
}

func TestParseSpotAnnouncementIsRejected(t *testing.T) {
	raw := []byte(`{"id":"s1","title":"ABC Will Be Listed on Spot Trading"}`)
	got, err := ParseBybitAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.ParseConfidence != "audit_only" {
		t.Fatalf("spot listing should be audit_only, got %q", got.ParseConfidence)
	}
	if len(got.Symbols) != 0 {
		t.Fatalf("spot announcement must not emit symbols, got %+v", got.Symbols)
	}
}
