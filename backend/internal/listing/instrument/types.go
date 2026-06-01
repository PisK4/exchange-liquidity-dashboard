// Package instrument owns the platform-specific normalizers and the
// diff engine for the Listing Agent P1 instrument poller. Each
// platform exports a Normalize* function and a small Source builder
// the engine uses to drive its lifecycle.
package instrument

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NormalizerVersion is stamped into t_listing_instrument_snapshot rows
// and onto signal payloads so future re-parsing can be selectively
// triggered when a parser ships a fix.
//
// v1 → v2 (2026-06-02): hash recipe changed from sha256(RawJSON) to a
// projection-based StableHash (see ComputeStableHash) so that
// time-varying market data fields inside raw rows (mark_price,
// funding_rate, daily_*, last_trade_price, open_interest, fee schedule
// re-quotes) no longer flip the hash on every 5-min poll. The
// instrument poll driver guards against the rollover so the cutover
// does not produce a one-shot metadata_changed firehose.
const NormalizerVersion = "v2"

// NormalizedInstrument is the platform-neutral view of a single
// exchange instrument. The raw JSON is preserved verbatim for audit
// (t_listing_signal_observation.raw_payload_hash on each diff signal
// continues to be sha256(RawJSON) and is unaffected by this rename).
//
// StableHash replaces the previous RawJSONHash semantics: it is the
// sha256 hex of a deterministic projection of schema-stable fields
// plus per-platform StableHashExtras. The DB column name remains
// `raw_json_hash` for schema compatibility.
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
	// StableHash is sha256(projection); StableHashExtras lets each
	// normalizer surface a handful of schema-stable platform-specific
	// fields (Gate quanto_multiplier, EdgeX tickSize/stepSize/contractId,
	// Hyperliquid maxLeverage, ...) so genuine contract-spec rotations
	// still flip the hash while market jitter inside RawJSON does not.
	StableHashExtras map[string]string `json:"-"`
	StableHash       string            `json:"stable_hash"`
}

// ComputeStableHash returns the deterministic sha256 hex projection
// used for instrument_diff comparisons. The projection deliberately
// excludes RawJSON so volatile market fields embedded in upstream
// rows (last_price, mark_price, funding_rate, daily_*, open_interest,
// fee schedule re-quotes) do not produce false metadata_changed
// signals.
//
// Callers must populate StableHashExtras before invoking this method.
// Each normalizer owns the white-list of schema-stable platform
// fields that go into Extras (see per-platform comments).
func (n NormalizedInstrument) ComputeStableHash() string {
	var listingMs int64
	if n.ListingTimeTS != nil {
		listingMs = n.ListingTimeTS.UnixMilli()
	}
	extraKeys := make([]string, 0, len(n.StableHashExtras))
	for k := range n.StableHashExtras {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	parts := []string{
		n.Platform,
		n.MarketType,
		n.APISymbol,
		n.APIMarketID,
		n.CanonicalSymbol,
		n.BaseAsset,
		n.QuoteAsset,
		n.SettleAsset,
		n.MarketSurface,
		n.InstrumentKind,
		n.ContractType,
		n.StatusRaw,
		n.StatusNormalized,
		n.StatusFieldName,
		strconv.FormatBool(n.DelistFlag),
		strconv.FormatInt(listingMs, 10),
		n.ListingTimeFieldName,
	}
	for _, k := range extraKeys {
		parts = append(parts, k+"="+n.StableHashExtras[k])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// computeRawHash returns sha256(raw). Retained for any caller that
// still needs the raw-payload digest (e.g. signal_observation audit
// columns). NOT used for snapshot hash comparison anymore.
func computeRawHash(raw []byte) string {
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
