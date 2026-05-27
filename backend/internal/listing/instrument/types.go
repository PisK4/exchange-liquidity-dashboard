// Package instrument owns the platform-specific normalizers and the
// diff engine for the Listing Agent P1 instrument poller. Each
// platform exports a Normalize* function and a small Source builder
// the engine uses to drive its lifecycle.
package instrument

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// NormalizerVersion is stamped into t_listing_instrument_snapshot rows
// and onto signal payloads so future re-parsing can be selectively
// triggered when a parser ships a fix.
const NormalizerVersion = "v1"

// NormalizedInstrument is the platform-neutral view of a single
// exchange instrument. The raw JSON is preserved verbatim for the
// audit trail; RawJSONHash is the SHA-256 hex digest of that JSON.
type NormalizedInstrument struct {
	Platform             string          `json:"platform"`
	MarketType           string          `json:"market_type"`
	APISymbol            string          `json:"api_symbol"`
	APIMarketID          string          `json:"api_market_id,omitempty"`
	DisplaySymbol        string          `json:"display_symbol,omitempty"`
	CanonicalSymbol      string          `json:"canonical_symbol"`
	BaseAsset            string          `json:"base_asset,omitempty"`
	QuoteAsset           string          `json:"quote_asset,omitempty"`
	SettleAsset          string          `json:"settle_asset,omitempty"`
	MarketSurface        string          `json:"market_surface"`
	InstrumentKind       string          `json:"instrument_kind"`
	ContractType         string          `json:"contract_type,omitempty"`
	StatusRaw            string          `json:"status_raw,omitempty"`
	StatusNormalized     string          `json:"status_normalized"`
	StatusFieldName      string          `json:"status_field_name,omitempty"`
	ListingTimeTS        *time.Time      `json:"listing_time_ts,omitempty"`
	ListingTimeFieldName string          `json:"listing_time_field_name,omitempty"`
	DelistFlag           bool            `json:"delist_flag,omitempty"`
	RawJSON              json.RawMessage `json:"-"`
	RawJSONHash          string          `json:"raw_json_hash"`
}

// computeHash returns the sha256 hex digest used as RawJSONHash.
func computeHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// nowFromUnixMillis returns a *time.Time parsed from a millisecond
// epoch. Returns nil when ms is zero or negative.
func nowFromUnixMillis(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

// nowFromUnixSeconds returns *time.Time from epoch seconds.
func nowFromUnixSeconds(s int64) *time.Time {
	if s <= 0 {
		return nil
	}
	t := time.Unix(s, 0).UTC()
	return &t
}

// SchemaDriftError is returned by normalizers when an upstream payload
// no longer matches the contract used by this parser. The instrument
// poller increments t_listing_source_state.schema_drift_count and
// skips emitting candidates for that source until drift clears.
type SchemaDriftError struct {
	Platform string
	Message  string
}

func (e *SchemaDriftError) Error() string {
	if e == nil {
		return ""
	}
	if e.Platform == "" {
		return e.Message
	}
	return e.Platform + ": " + e.Message
}
