package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/listing"
)

type fakeListingReader struct {
	candidates    []listing.Candidate
	signals       []listing.SignalObservation
	source        []listing.SourceState
	deliveries    []listing.DeliveryOutbox
	candidate     listing.Candidate
	candidateErr  error
	candidatesErr error
}

func (f fakeListingReader) ListCandidates(ctx context.Context, filter listing.CandidateFilter) ([]listing.Candidate, error) {
	return f.candidates, f.candidatesErr
}
func (f fakeListingReader) GetCandidate(ctx context.Context, id int64) (listing.Candidate, error) {
	if f.candidateErr != nil {
		return listing.Candidate{}, f.candidateErr
	}
	return f.candidate, nil
}
func (f fakeListingReader) ListCandidateSignals(ctx context.Context, candidateID int64, includeRaw bool) ([]listing.SignalObservation, error) {
	return f.signals, nil
}
func (f fakeListingReader) ListSourceHealth(ctx context.Context) ([]listing.SourceState, error) {
	return f.source, nil
}
func (f fakeListingReader) ListDeliveries(ctx context.Context, filter listing.DeliveryFilter) ([]listing.DeliveryOutbox, error) {
	return f.deliveries, nil
}

func TestListingCandidatesReturns503WhenReaderMissing(t *testing.T) {
	server := NewServer(config.Config{}, fakeStoreReader{})
	req := httptest.NewRequest(http.MethodGet, "/api/listing/candidates", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "unavailable" || body["feature"] != "listing_agent" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListingCandidatesReturnsList(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	reader := fakeListingReader{candidates: []listing.Candidate{{
		ID: 1, CanonicalSymbol: "ABC", DisplaySymbol: "ABC-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical",
		LifecycleStatus: listing.LifecycleConfirmedListingCandidate,
		EvidenceKind:    listing.EvidenceAnnouncementAndAPI,
		ConfidenceLevel: listing.ConfidenceHigh,
		FirstObservedAt: now, LastObservedAt: now,
	}}}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithListingReader(reader))
	req := httptest.NewRequest(http.MethodGet, "/api/listing/candidates?limit=5&platform=binance", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["count"].(float64) != 1 {
		t.Fatalf("count = %v, want 1", body["count"])
	}
	candidates := body["candidates"].([]any)
	first := candidates[0].(map[string]any)
	if first["canonical_symbol"] != "ABC" {
		t.Fatalf("candidate[0] = %+v", first)
	}
	if _, has := first["top30_enrichment"]; has {
		t.Fatalf("list endpoint must not surface top30_enrichment")
	}
}

func TestListingCandidateDetailIncludesRawPayload(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	reader := fakeListingReader{
		candidate: listing.Candidate{
			ID: 7, CanonicalSymbol: "ABC", DisplaySymbol: "ABC-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical",
			LifecycleStatus: listing.LifecycleConfirmedListingCandidate, FirstObservedAt: now, LastObservedAt: now,
		},
		signals: []listing.SignalObservation{{
			ID: 99, SignalType: listing.SignalAnnouncementListing, CanonicalSymbol: "ABC", MarketSurface: "perp", InstrumentKind: "canonical",
			Fingerprint: "fp", PayloadJSON: []byte(`{"title":"ABC"}`), RawPayloadJSON: []byte(`{"id":"a1"}`), ObservedAt: now,
		}},
	}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithListingReader(reader))
	req := httptest.NewRequest(http.MethodGet, "/api/listing/candidates/7", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	signals := body["signals"].([]any)
	first := signals[0].(map[string]any)
	if _, has := first["raw_payload"]; !has {
		t.Fatalf("detail endpoint must include raw_payload, got %+v", first)
	}
}

func TestListingDeliveriesReturnsRows(t *testing.T) {
	reader := fakeListingReader{deliveries: []listing.DeliveryOutbox{{
		ID: 1, EventType: listing.DeliveryEventTop30HotGap, DedupeKey: "k", TargetChannel: listing.DeliveryChannelLarkTop30,
		Status: listing.OutboxStatusSent, PayloadJSON: []byte(`{}`),
	}}}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithListingReader(reader))
	req := httptest.NewRequest(http.MethodGet, "/api/listing/deliveries?event_type=top30_hot_gap", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}
