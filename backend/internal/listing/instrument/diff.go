package instrument

import "time"

// DiffEvent is one normalized state-transition emitted by Diff.
// Source-platform identity (platform / api_symbol / canonical_symbol /
// market_surface / instrument_kind) is duplicated here so callers can
// turn the event into a SignalObservation without re-reading the
// snapshot row.
type DiffEvent struct {
	Subtype         string
	Platform        string
	APISymbol       string
	CanonicalSymbol string
	MarketSurface   string
	InstrumentKind  string
	StatusFrom      string
	StatusTo        string
	ListingTimeFrom *time.Time
	ListingTimeTo   *time.Time
	StableHashFrom  string
	StableHashTo    string
}

// Diff compares the previous snapshot (nil for first sighting) with
// the current normalized instrument and returns ordered DiffEvents.
//
// Baseline rule: when prev is nil and baselineReady is false the
// caller has not yet completed the bootstrap pass, so no events are
// emitted. This avoids the cold-start problem where every existing
// instrument would otherwise be misreported as new_symbol.
func Diff(prev *NormalizedInstrument, curr NormalizedInstrument, baselineReady bool) []DiffEvent {
	if prev == nil {
		if !baselineReady {
			return nil
		}
		return []DiffEvent{{
			Subtype:         "new_symbol",
			Platform:        curr.Platform,
			APISymbol:       curr.APISymbol,
			CanonicalSymbol: curr.CanonicalSymbol,
			MarketSurface:   curr.MarketSurface,
			InstrumentKind:  curr.InstrumentKind,
			StatusTo:        curr.StatusNormalized,
			ListingTimeTo:   curr.ListingTimeTS,
			StableHashTo:    curr.StableHash,
		}}
	}
	var events []DiffEvent
	statusChanged := prev.StatusNormalized != curr.StatusNormalized
	switch {
	case statusChanged && prev.StatusNormalized == "delisted" && curr.StatusNormalized == "active":
		events = append(events, baseEvent("relisted", prev, curr))
	case statusChanged && curr.StatusNormalized == "delisted":
		events = append(events, baseEvent("delisted", prev, curr))
	case statusChanged:
		events = append(events, baseEvent("status_changed", prev, curr))
	}
	if !timesEqual(prev.ListingTimeTS, curr.ListingTimeTS) && curr.ListingTimeTS != nil {
		events = append(events, baseEvent("listing_time_changed", prev, curr))
	}
	if len(events) == 0 && prev.StableHash != curr.StableHash {
		events = append(events, baseEvent("metadata_changed", prev, curr))
	}
	return events
}

func baseEvent(subtype string, prev *NormalizedInstrument, curr NormalizedInstrument) DiffEvent {
	return DiffEvent{
		Subtype:         subtype,
		Platform:        curr.Platform,
		APISymbol:       curr.APISymbol,
		CanonicalSymbol: curr.CanonicalSymbol,
		MarketSurface:   curr.MarketSurface,
		InstrumentKind:  curr.InstrumentKind,
		StatusFrom:      prev.StatusNormalized,
		StatusTo:        curr.StatusNormalized,
		ListingTimeFrom: prev.ListingTimeTS,
		ListingTimeTo:   curr.ListingTimeTS,
		StableHashFrom:  prev.StableHash,
		StableHashTo:    curr.StableHash,
	}
}

func timesEqual(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}
