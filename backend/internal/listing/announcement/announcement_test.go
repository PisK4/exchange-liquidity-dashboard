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
		if s.ListingTimeTS != nil {
			t.Fatalf("announcement publishTime must not be copied to listing time: %+v", s.ListingTimeTS)
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
	if got.Symbols[0].ListingTimeTS != nil {
		t.Fatalf("announcement publishTime must not be copied to listing time: %+v", got.Symbols[0].ListingTimeTS)
	}
}

func TestParseHyperliquidAnnouncementPerpSingleSymbol(t *testing.T) {
	raw := []byte(`{"hash":"h1","title":"New listing: NIL-USD perps","createdAt":"2026-05-30T10:00:00Z","sourceUrl":"https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/info-endpoint"}`)
	got, err := ParseHyperliquidAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.Platform != "hyperliquid" || got.AnnouncementID != "h1" {
		t.Fatalf("unexpected announcement = %+v", got)
	}
	if got.ParseConfidence != ConfidenceHigh || got.SignalSubtype != SubtypePerpListing {
		t.Fatalf("unexpected parse classification: confidence=%q subtype=%q", got.ParseConfidence, got.SignalSubtype)
	}
	if len(got.Symbols) != 1 {
		t.Fatalf("symbols len = %d, want 1 (%+v)", len(got.Symbols), got.Symbols)
	}
	s := got.Symbols[0]
	if s.CanonicalSymbol != "NIL" || s.DisplaySymbol != "NIL-USD (perp)" || s.MarketSurface != "perp" {
		t.Fatalf("unexpected symbol = %+v", s)
	}
	if s.SignalSubtype != SubtypePerpListing || s.SourceModule != "hyperliquid_entries" {
		t.Fatalf("unexpected symbol metadata = %+v", s)
	}
	if got.PublishedAt == nil {
		t.Fatalf("published_at should be parsed from createdAt")
	}
	if s.ListingTimeTS != nil {
		t.Fatalf("announcement createdAt must not be copied to listing time: %+v", s.ListingTimeTS)
	}
}

func TestParseHyperliquidAnnouncementPerpMultiSymbol(t *testing.T) {
	raw := []byte(`{"id":"hl-multi","title":"New listing: HYPER-USD, ZORA-USD, and INIT-USDC perps","createdAt":"2026-05-30T10:00:00Z"}`)
	got, err := ParseHyperliquidAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if len(got.Symbols) != 3 {
		t.Fatalf("symbols len = %d, want 3 (%+v)", len(got.Symbols), got.Symbols)
	}
	want := []string{"HYPER-USD (perp)", "ZORA-USD (perp)", "INIT-USDC (perp)"}
	for i, sym := range got.Symbols {
		if sym.DisplaySymbol != want[i] {
			t.Fatalf("symbol[%d] = %+v, want display %q", i, sym, want[i])
		}
	}
}

func TestParseHyperliquidAnnouncementSpotAddedAndEnabled(t *testing.T) {
	added := []byte(`{"id":"spot-added","title":"Added spot PUMP","createdAt":"2026-05-30T10:00:00Z"}`)
	got, err := ParseHyperliquidAnnouncement(added)
	if err != nil {
		t.Fatalf("Parse added spot err = %v", err)
	}
	if got.ParseConfidence != ConfidenceMedium || got.SignalSubtype != SubtypeSpotListing {
		t.Fatalf("unexpected added spot classification: %+v", got)
	}
	if len(got.Symbols) != 1 || got.Symbols[0].CanonicalSymbol != "PUMP" || got.Symbols[0].MarketSurface != "spot" {
		t.Fatalf("unexpected added spot symbols = %+v", got.Symbols)
	}

	enabled := []byte(`{"id":"spot-enabled","title":"Enabled spot BTC","createdAt":"2026-05-30T10:00:00Z","sourceModule":"activity_agent"}`)
	got, err = ParseHyperliquidAnnouncement(enabled)
	if err != nil {
		t.Fatalf("Parse enabled spot err = %v", err)
	}
	if len(got.Symbols) != 1 || got.Symbols[0].CanonicalSymbol != "BTC" || got.Symbols[0].SourceModule != "activity_agent" {
		t.Fatalf("unexpected enabled spot symbols = %+v", got.Symbols)
	}
}

func TestParseHyperliquidAnnouncementDelistingAuditOnly(t *testing.T) {
	raw := []byte(`{"id":"delist-1","title":"Validator vote to delist MYRO perps","category":"delisting","createdAt":"2026-05-30T10:00:00Z"}`)
	got, err := ParseHyperliquidAnnouncement(raw)
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if got.ParseConfidence != ConfidenceAuditOnly || got.SignalSubtype != SubtypeIrrelevant {
		t.Fatalf("delisting must be audit_only/irrelevant, got confidence=%q subtype=%q", got.ParseConfidence, got.SignalSubtype)
	}
	if len(got.Symbols) != 0 {
		t.Fatalf("delisting must not emit listing symbols, got %+v", got.Symbols)
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
