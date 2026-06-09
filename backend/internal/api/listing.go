package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"edgex-ops-intelligence/backend/internal/listing"
)

// ListingReader is the narrow read interface the API uses to surface
// Listing Agent state. *listing.Repository satisfies it; tests pass
// fakes so the API tests stay independent of MySQL.
type ListingReader interface {
	ListCandidates(ctx context.Context, f listing.CandidateFilter) ([]listing.Candidate, error)
	GetCandidate(ctx context.Context, id int64) (listing.Candidate, error)
	ListCandidateSignals(ctx context.Context, candidateID int64, includeRaw bool) ([]listing.SignalObservation, error)
	ListSourceHealth(ctx context.Context) ([]listing.SourceState, error)
	ListDeliveries(ctx context.Context, f listing.DeliveryFilter) ([]listing.DeliveryOutbox, error)
}

// ErrListingDisabled is returned by ListingReader implementations
// when the Listing Agent is configured off; the API maps it to 503.
var ErrListingDisabled = errors.New("listing agent disabled")

// Option configures a Server at construction time. The Listing API
// surface lands behind WithListingReader so legacy callers stay
// source-compatible with the existing NewServer signature.
type Option func(*Server)

// WithListingReader attaches a ListingReader so the /api/listing/*
// routes serve real data. Without this option those routes return
// 503 with a stable ListingUnavailableResponse payload.
func WithListingReader(reader ListingReader) Option {
	return func(s *Server) { s.listing = reader }
}

func (s *Server) registerListingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/listing/candidates", s.listingCandidates)
	mux.HandleFunc("/api/listing/candidates/", s.listingCandidateDetail)
	mux.HandleFunc("/api/listing/source-health", s.listingSourceHealth)
	mux.HandleFunc("/api/listing/deliveries", s.listingDeliveries)
	s.registerListingCallback(mux)
}

func (s *Server) listingCandidates(w http.ResponseWriter, r *http.Request) {
	if s.listing == nil {
		writeListingUnavailable(w, "listing reader not configured")
		return
	}
	q := r.URL.Query()
	filter := listing.CandidateFilter{
		Status:       q.Get("status"),
		EvidenceKind: q.Get("evidence_kind"),
		Platform:     q.Get("platform"),
		Symbol:       q.Get("symbol"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	candidates, err := s.listing.ListCandidates(r.Context(), filter)
	if err != nil {
		writeListingError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"candidates": candidatesToWire(candidates, false),
		"count":      len(candidates),
	})
}

func (s *Server) listingCandidateDetail(w http.ResponseWriter, r *http.Request) {
	if s.listing == nil {
		writeListingUnavailable(w, "listing reader not configured")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/listing/candidates/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "invalid candidate id", http.StatusBadRequest)
		return
	}
	switch {
	case len(parts) == 1:
		candidate, err := s.listing.GetCandidate(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			writeListingError(w, err)
			return
		}
		signals, err := s.listing.ListCandidateSignals(r.Context(), id, true)
		if err != nil {
			writeListingError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"candidate": candidateToWire(candidate, true),
			"signals":   signalsToWire(signals, true),
		})
	case len(parts) == 2 && parts[1] == "signals":
		signals, err := s.listing.ListCandidateSignals(r.Context(), id, true)
		if err != nil {
			writeListingError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"signals": signalsToWire(signals, true),
			"count":   len(signals),
		})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) listingSourceHealth(w http.ResponseWriter, r *http.Request) {
	if s.listing == nil {
		writeListingUnavailable(w, "listing reader not configured")
		return
	}
	rows, err := s.listing.ListSourceHealth(r.Context())
	if err != nil {
		writeListingError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"source_key":              row.SourceKey,
			"source_type":             row.SourceType,
			"platform":                row.Platform,
			"status":                  row.Status,
			"last_success_at":         row.LastSuccessAt,
			"last_error_at":           row.LastErrorAt,
			"consecutive_error_count": row.ConsecutiveErrorCount,
			"schema_drift_count":      row.SchemaDriftCount,
			"disabled_until":          row.DisabledUntil,
			"last_error":              row.LastError,
			"source_context_json":     json.RawMessage(rawJSONOrDefault(row.SourceContextJSON, []byte(`{}`))),
			"updated_at":              row.UpdatedAt,
		})
	}
	writeJSON(w, map[string]any{"sources": out, "count": len(out)})
}

func (s *Server) listingDeliveries(w http.ResponseWriter, r *http.Request) {
	if s.listing == nil {
		writeListingUnavailable(w, "listing reader not configured")
		return
	}
	q := r.URL.Query()
	filter := listing.DeliveryFilter{
		EventType: q.Get("event_type"),
		Status:    q.Get("status"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	rows, err := s.listing.ListDeliveries(r.Context(), filter)
	if err != nil {
		writeListingError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":              row.ID,
			"event_type":      row.EventType,
			"dedupe_key":      row.DedupeKey,
			"target_channel":  row.TargetChannel,
			"status":          row.Status,
			"attempt_count":   row.AttemptCount,
			"max_attempts":    row.MaxAttempts,
			"next_attempt_at": row.NextAttemptAt,
			"payload":         json.RawMessage(row.PayloadJSON),
			"last_error":      row.LastError,
			"sent_at":         row.SentAt,
			"created_at":      row.CreatedAt,
			"updated_at":      row.UpdatedAt,
		})
	}
	writeJSON(w, map[string]any{"deliveries": out, "count": len(out)})
}

func candidatesToWire(in []listing.Candidate, includeRaw bool) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, c := range in {
		out = append(out, candidateToWire(c, includeRaw))
	}
	return out
}

func candidateToWire(c listing.Candidate, includeRaw bool) map[string]any {
	row := map[string]any{
		"id":                     c.ID,
		"canonical_symbol":       c.CanonicalSymbol,
		"display_symbol":         c.DisplaySymbol,
		"market_surface":         c.MarketSurface,
		"instrument_kind":        c.InstrumentKind,
		"lifecycle_status":       c.LifecycleStatus,
		"lifecycle_status_label": c.LifecycleStatusLabel,
		"evidence_kind":          c.EvidenceKind,
		"confidence_level":       c.ConfidenceLevel,
		"business_score":         c.BusinessScore,
		"business_score_version": c.BusinessScoreVersion,
		"recommendation":         c.Recommendation,
		"recommendation_label":   c.RecommendationLabel,
		"source_platforms":       c.SourcePlatforms,
		"first_observed_at":      c.FirstObservedAt,
		"last_observed_at":       c.LastObservedAt,
	}
	if includeRaw && len(c.Top30Enrichment) > 0 {
		row["top30_enrichment"] = json.RawMessage(c.Top30Enrichment)
	}
	return row
}

func signalsToWire(in []listing.SignalObservation, includeRaw bool) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, s := range in {
		row := map[string]any{
			"id":                s.ID,
			"signal_type":       s.SignalType,
			"signal_subtype":    s.SignalSubtype,
			"source_platform":   s.SourcePlatform,
			"market_type":       s.MarketType,
			"api_symbol":        s.APISymbol,
			"canonical_symbol":  s.CanonicalSymbol,
			"display_symbol":    s.DisplaySymbol,
			"market_surface":    s.MarketSurface,
			"instrument_kind":   s.InstrumentKind,
			"status_normalized": s.StatusNormalized,
			"confidence":        s.Confidence,
			"observed_at":       s.ObservedAt,
			"published_at":      s.PublishedAt,
			"listing_time_ts":   s.ListingTimeTS,
			"fingerprint":       s.Fingerprint,
		}
		if len(s.PayloadJSON) > 0 {
			row["payload"] = json.RawMessage(s.PayloadJSON)
		}
		if includeRaw && len(s.RawPayloadJSON) > 0 {
			row["raw_payload"] = json.RawMessage(s.RawPayloadJSON)
		}
		out = append(out, row)
	}
	return out
}

func writeListingUnavailable(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "unavailable",
		"reason":  reason,
		"feature": "listing_agent",
	})
}

func writeListingError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrListingDisabled) {
		writeListingUnavailable(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": err.Error()})
}
