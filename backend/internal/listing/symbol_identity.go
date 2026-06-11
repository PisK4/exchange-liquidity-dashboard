package listing

import (
	"strings"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing/announcement"
	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// SymbolIdentityResolver is the small slice of config.CanonicalIndex used by
// Listing Agent runtime ingestion. It keeps the listing package decoupled from
// config internals while allowing tests to inject focused fake resolvers.
type SymbolIdentityResolver interface {
	ResolveIdentity(platform, base string) config.CanonicalIdentity
}

// ApplyInstrumentSymbolIdentity replaces only business identity fields on a
// normalized instrument. Native exchange fields (APISymbol, BaseAsset, RawJSON)
// are deliberately preserved for auditability and future exchange API calls.
func ApplyInstrumentSymbolIdentity(n instrument.NormalizedInstrument, resolver SymbolIdentityResolver) instrument.NormalizedInstrument {
	if resolver == nil {
		return n
	}
	base := strings.ToUpper(strings.TrimSpace(n.BaseAsset))
	if base == "" {
		base = strings.ToUpper(strings.TrimSpace(n.CanonicalSymbol))
	}
	n = applyIdentityToInstrument(n, resolver.ResolveIdentity(n.Platform, base))
	n.StableHash = n.ComputeStableHash()
	return n
}

func applyIdentityToInstrument(n instrument.NormalizedInstrument, identity config.CanonicalIdentity) instrument.NormalizedInstrument {
	if !identity.Matched || strings.TrimSpace(identity.Canonical) == "" {
		return n
	}
	n.CanonicalSymbol = strings.ToUpper(strings.TrimSpace(identity.Canonical))
	if v := strings.TrimSpace(identity.DisplaySymbol); v != "" {
		n.DisplaySymbol = v
	}
	if v := strings.TrimSpace(identity.MarketSurface); v != "" {
		n.MarketSurface = v
	}
	if v := strings.TrimSpace(identity.InstrumentKind); v != "" {
		n.InstrumentKind = v
	}
	return n
}

// ApplyAnnouncementSymbolIdentity mirrors ApplyInstrumentSymbolIdentity for
// parsed announcement children. RawSymbol/BaseAsset are preferred lookup keys
// when parsers provide them; CanonicalSymbol remains the fallback for legacy
// parser outputs.
func ApplyAnnouncementSymbolIdentity(platform string, sym announcement.ParsedAnnouncementSymbol, resolver SymbolIdentityResolver) announcement.ParsedAnnouncementSymbol {
	if resolver == nil {
		return sym
	}
	base := firstNonEmptyUpper(sym.BaseAsset, sym.RawSymbol, sym.CanonicalSymbol)
	identity := resolver.ResolveIdentity(platform, base)
	if !identity.Matched || strings.TrimSpace(identity.Canonical) == "" {
		return sym
	}
	sym.CanonicalSymbol = strings.ToUpper(strings.TrimSpace(identity.Canonical))
	if v := strings.TrimSpace(identity.DisplaySymbol); v != "" {
		sym.DisplaySymbol = v
	}
	if v := strings.TrimSpace(identity.MarketSurface); v != "" {
		sym.MarketSurface = v
	}
	if v := strings.TrimSpace(identity.InstrumentKind); v != "" {
		sym.InstrumentKind = v
	}
	return sym
}

// ApplySignalSymbolIdentity is a narrow legacy safety net for already-written
// unfused signals. Backfill remains the primary historical repair path; this
// helper prevents a missed old signal from creating a split candidate.
func ApplySignalSymbolIdentity(s SignalObservation, resolver SymbolIdentityResolver) SignalObservation {
	if resolver == nil {
		return s
	}
	base := firstNonEmptyUpper(s.BaseAsset, s.CanonicalSymbol)
	identity := resolver.ResolveIdentity(s.SourcePlatform, base)
	if !identity.Matched || strings.TrimSpace(identity.Canonical) == "" {
		return s
	}
	s.CanonicalSymbol = strings.ToUpper(strings.TrimSpace(identity.Canonical))
	if v := strings.TrimSpace(identity.DisplaySymbol); v != "" {
		s.DisplaySymbol = v
	}
	if v := strings.TrimSpace(identity.MarketSurface); v != "" {
		s.MarketSurface = v
	}
	if v := strings.TrimSpace(identity.InstrumentKind); v != "" {
		s.InstrumentKind = v
	}
	return s
}

func firstNonEmptyUpper(values ...string) string {
	for _, value := range values {
		if v := strings.ToUpper(strings.TrimSpace(value)); v != "" {
			return v
		}
	}
	return ""
}
