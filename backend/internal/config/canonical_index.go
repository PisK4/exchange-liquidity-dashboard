package config

import "strings"

// CanonicalIndex is the reverse-direction lookup table built from
// symbol_mapping.yaml's `aliases:` map. Given a (platform, raw base
// asset) tuple it returns the V1-canonical key used to unify the
// symbol across Ops Intelligence Top30 aggregation, the divergence
// "edgeX 未上线" alert cards, and the V1 liquidity monitor page —
// keeping a single source of truth for "PAXG on edgeX == XAUT on
// bitget == XAU on binance == GOLD canonical".
//
// The index does not mutate or own its inputs; callers can rebuild
// it cheaply when the YAML is reloaded.
type CanonicalIndex struct {
	// byPlatform[platformLower][baseUpper] = canonical.
	byPlatform map[string]map[string]string
	// byPlatformIdentity[platformLower][baseUpper] = identity metadata
	// from the symbol_mapping.yaml entry that declared the alias.
	byPlatformIdentity map[string]map[string]CanonicalIdentity
	// canonicals is the set of declared canonical keys (uppercase).
	canonicals map[string]struct{}
	// byCanonicalIdentity[canonical] = identity metadata when the
	// canonical is declared by exactly one symbol_mapping entry.
	// Duplicated canonicals (for example BTC perp + edgeX perp_v2)
	// are marked ambiguous so callers do not accidentally inherit the
	// wrong surface/kind metadata.
	byCanonicalIdentity map[string]CanonicalIdentity
	canonicalAmbiguous  map[string]struct{}
	// crossPlatform[baseUpper] = canonical when exactly ONE
	// canonical claims that alias across all platforms; "" when
	// two or more canonicals collide on the same alias. Lets the
	// resolver fold a missing-from-YAML platform (e.g. bitget's
	// CL-USDT (perp)) onto the right canonical (OIL) when the
	// answer is globally unambiguous.
	crossPlatform map[string]string
	// crossPlatformIdentity mirrors crossPlatform with the full symbol
	// metadata. crossPlatformAmbiguous records aliases claimed by more
	// than one canonical so ResolveIdentity can surface the ambiguity
	// without silently choosing an owner.
	crossPlatformIdentity  map[string]CanonicalIdentity
	crossPlatformAmbiguous map[string]struct{}
	// platformsByCanonical[canonical] = number of distinct platforms
	// that declare at least one alias for this canonical in
	// symbol_mapping.yaml. Used by IsPlatformExclusive to flag
	// "specialty" listings (only on a single venue) so the
	// liquidity-alert path (#10 / #11) can skip them — there is
	// nothing to compare against when only one platform offers the
	// market.
	platformsByCanonical map[string]int
}

const (
	CanonicalMatchPlatformAlias = "platform_alias"
	CanonicalMatchCrossPlatform = "cross_platform_alias"
	CanonicalMatchIdentity      = "canonical_identity"
	CanonicalMatchNoMatch       = "no_match"
	CanonicalMatchAmbiguous     = "ambiguous"
)

// CanonicalIdentity is the runtime-visible identity metadata resolved
// from symbol_mapping.yaml. It lets Listing Agent ingestion keep native
// exchange fields (api_symbol/base_asset/raw_json) while replacing only
// business identity fields (canonical/display/surface/kind) before
// snapshot/signal/candidate creation.
type CanonicalIdentity struct {
	Canonical      string
	DisplaySymbol  string
	DisplayName    string
	AssetCategory  string
	MarketSurface  string
	InstrumentKind string
	Matched        bool
	MatchedAlias   string
	MatchKind      string
}

// NewCanonicalIndex builds the reverse index from the raw symbol_mapping
// entries. Each (platform, alias) pair in the per-canonical alias map
// becomes an entry. Conflicts (same alias mapped to two canonicals on
// the same platform) keep the first declaration so YAML order is
// authoritative.
func NewCanonicalIndex(symbols []symbolYAML) *CanonicalIndex {
	idx := &CanonicalIndex{
		byPlatform:             map[string]map[string]string{},
		byPlatformIdentity:     map[string]map[string]CanonicalIdentity{},
		canonicals:             map[string]struct{}{},
		byCanonicalIdentity:    map[string]CanonicalIdentity{},
		canonicalAmbiguous:     map[string]struct{}{},
		crossPlatform:          map[string]string{},
		crossPlatformIdentity:  map[string]CanonicalIdentity{},
		crossPlatformAmbiguous: map[string]struct{}{},
		platformsByCanonical:   map[string]int{},
	}
	// owners[baseUpper] = set of canonicals that claim that alias on
	// any platform. Used post-pass to decide which entries qualify for
	// the unambiguous cross-platform fallback.
	owners := map[string]map[string]CanonicalIdentity{}
	// platformSets[canonical] = set of platforms that declare any
	// alias for this canonical; converted to a count after the pass.
	platformSets := map[string]map[string]struct{}{}
	for _, s := range symbols {
		canonical := strings.ToUpper(strings.TrimSpace(s.Canonical))
		if canonical == "" {
			continue
		}
		identity := canonicalIdentityFromYAML(s, canonical)
		idx.canonicals[canonical] = struct{}{}
		if prev, ok := idx.byCanonicalIdentity[canonical]; ok {
			if !canonicalIdentityMetadataEqual(prev, identity) {
				idx.canonicalAmbiguous[canonical] = struct{}{}
			}
		} else {
			idx.byCanonicalIdentity[canonical] = identity
		}
		for platform, aliases := range s.Aliases {
			plat := strings.ToLower(strings.TrimSpace(platform))
			if plat == "" {
				continue
			}
			perPlatform, ok := idx.byPlatform[plat]
			if !ok {
				perPlatform = map[string]string{}
				idx.byPlatform[plat] = perPlatform
			}
			perPlatformIdentity, ok := idx.byPlatformIdentity[plat]
			if !ok {
				perPlatformIdentity = map[string]CanonicalIdentity{}
				idx.byPlatformIdentity[plat] = perPlatformIdentity
			}
			declaredAny := false
			for _, alias := range aliases {
				base := strings.ToUpper(strings.TrimSpace(alias))
				if base == "" {
					continue
				}
				declaredAny = true
				if _, exists := perPlatform[base]; !exists {
					perPlatform[base] = canonical
					perPlatformIdentity[base] = identity
				}
				ownerSet, ok := owners[base]
				if !ok {
					ownerSet = map[string]CanonicalIdentity{}
					owners[base] = ownerSet
				}
				ownerSet[canonical] = identity
			}
			if declaredAny {
				set, ok := platformSets[canonical]
				if !ok {
					set = map[string]struct{}{}
					platformSets[canonical] = set
				}
				set[plat] = struct{}{}
			}
		}
	}
	for base, ownerSet := range owners {
		if len(ownerSet) != 1 {
			idx.crossPlatformAmbiguous[base] = struct{}{}
			continue
		}
		for canonical, identity := range ownerSet {
			idx.crossPlatform[base] = canonical
			idx.crossPlatformIdentity[base] = identity
		}
	}
	for canonical, set := range platformSets {
		idx.platformsByCanonical[canonical] = len(set)
	}
	return idx
}

func canonicalIdentityFromYAML(s symbolYAML, canonical string) CanonicalIdentity {
	return CanonicalIdentity{
		Canonical:      canonical,
		DisplaySymbol:  strings.TrimSpace(s.DisplaySymbol),
		DisplayName:    strings.TrimSpace(s.DisplayName),
		AssetCategory:  strings.TrimSpace(s.AssetCategory),
		MarketSurface:  strings.TrimSpace(s.MarketSurface),
		InstrumentKind: strings.TrimSpace(s.InstrumentKind),
	}
}

func canonicalIdentityMetadataEqual(a, b CanonicalIdentity) bool {
	return a.DisplaySymbol == b.DisplaySymbol &&
		a.DisplayName == b.DisplayName &&
		a.AssetCategory == b.AssetCategory &&
		a.MarketSurface == b.MarketSurface &&
		a.InstrumentKind == b.InstrumentKind
}

// Resolve returns the canonical key for (platform, base) using the
// alias map. When no alias matches the function falls back to the
// uppercased base, so callers can use the return value as an
// aggregation key without any extra null-handling.
//
// The receiver is nil-safe: a nil *CanonicalIndex behaves like an
// empty index (always returns identity), so the helper composes with
// the existing CanonicaliseSymbol pipeline even before the resolver
// has been wired in.
func (idx *CanonicalIndex) Resolve(platform, base string) string {
	return idx.ResolveIdentity(platform, base).Canonical
}

// ResolveIdentity returns the full canonical identity for (platform, base).
// The lookup order is:
//   - exact platform alias (authoritative, returns that symbol entry metadata)
//   - unambiguous cross-platform alias
//   - canonical identity fallback when the canonical has a single metadata row
//   - uppercase identity fallback
//
// Ambiguous aliases/canonicals return the uppercased base with MatchKind
// "ambiguous" and Matched=false so callers can fail closed or preserve the
// caller-provided surface/kind instead of inheriting arbitrary metadata.
func (idx *CanonicalIndex) ResolveIdentity(platform, base string) CanonicalIdentity {
	base = strings.ToUpper(strings.TrimSpace(base))
	if base == "" {
		return CanonicalIdentity{MatchKind: CanonicalMatchNoMatch}
	}
	if idx == nil {
		return CanonicalIdentity{Canonical: base, MatchKind: CanonicalMatchNoMatch}
	}
	plat := strings.ToLower(strings.TrimSpace(platform))
	if perPlatform, ok := idx.byPlatformIdentity[plat]; ok {
		if identity, ok := perPlatform[base]; ok {
			identity.Matched = true
			identity.MatchedAlias = base
			identity.MatchKind = CanonicalMatchPlatformAlias
			return identity
		}
	}
	if _, ambiguous := idx.crossPlatformAmbiguous[base]; ambiguous {
		return CanonicalIdentity{Canonical: base, MatchKind: CanonicalMatchAmbiguous}
	}
	if identity, ok := idx.crossPlatformIdentity[base]; ok {
		identity.Matched = true
		identity.MatchedAlias = base
		identity.MatchKind = CanonicalMatchCrossPlatform
		return identity
	}
	if _, ambiguous := idx.canonicalAmbiguous[base]; ambiguous {
		return CanonicalIdentity{Canonical: base, MatchKind: CanonicalMatchAmbiguous}
	}
	if identity, ok := idx.byCanonicalIdentity[base]; ok {
		identity.Matched = true
		identity.MatchedAlias = base
		identity.MatchKind = CanonicalMatchIdentity
		return identity
	}
	return CanonicalIdentity{Canonical: base, MatchKind: CanonicalMatchNoMatch}
}

// IsCanonical reports whether s (case-insensitive) is a canonical
// key declared in symbol_mapping.yaml. Used by the divergence
// resolver to short-circuit when the input base already matches a
// canonical name (e.g. "GOLD" itself on a platform that does not
// publish an alias for it).
func (idx *CanonicalIndex) IsCanonical(s string) bool {
	if idx == nil {
		return false
	}
	_, ok := idx.canonicals[strings.ToUpper(strings.TrimSpace(s))]
	return ok
}

// IsPlatformExclusive reports whether the canonical is only declared
// on a single platform in symbol_mapping.yaml — i.e. there is no
// peer to compare its depth or volume against. The liquidity-alert
// pipeline (#10 / #11) uses this to skip "specialty" listings such
// as Hyperliquid HIP-2 index perps where ranking against zero
// comparators is meaningless.
//
// Unknown canonicals return false (conservative — we'd rather see
// the alert and notice the missing alias than silently swallow it).
func (idx *CanonicalIndex) IsPlatformExclusive(canonical string) bool {
	if idx == nil {
		return false
	}
	key := strings.ToUpper(strings.TrimSpace(canonical))
	if key == "" {
		return false
	}
	count, ok := idx.platformsByCanonical[key]
	if !ok {
		return false
	}
	return count <= 1
}

// ResolveCanonical is the divergence.CanonicalResolver adaptor — it
// simply delegates to Resolve. Defining it as a separate method
// (rather than renaming Resolve) keeps Resolve's existing call sites
// untouched while letting *CanonicalIndex satisfy the divergence
// interface directly.
func (idx *CanonicalIndex) ResolveCanonical(platform, base string) string {
	return idx.Resolve(platform, base)
}
