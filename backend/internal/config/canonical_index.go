package config

import "strings"

// CanonicalIndex is the reverse-direction lookup table built from
// symbol_mapping.yaml's `aliases:` map. Given a (platform, raw base
// asset) tuple it returns the V1-canonical key used to unify the
// symbol across the dashboard's Top30 aggregation, the divergence
// "edgeX 未上线" alert cards, and the V1 liquidity monitor page —
// keeping a single source of truth for "PAXG on edgeX == XAUT on
// bitget == XAU on binance == GOLD canonical".
//
// The index does not mutate or own its inputs; callers can rebuild
// it cheaply when the YAML is reloaded.
type CanonicalIndex struct {
	// byPlatform[platformLower][baseUpper] = canonical.
	byPlatform map[string]map[string]string
	// canonicals is the set of declared canonical keys (uppercase).
	canonicals map[string]struct{}
	// crossPlatform[baseUpper] = canonical when exactly ONE
	// canonical claims that alias across all platforms; "" when
	// two or more canonicals collide on the same alias. Lets the
	// resolver fold a missing-from-YAML platform (e.g. bitget's
	// CL-USDT (perp)) onto the right canonical (OIL) when the
	// answer is globally unambiguous.
	crossPlatform map[string]string
	// platformsByCanonical[canonical] = number of distinct platforms
	// that declare at least one alias for this canonical in
	// symbol_mapping.yaml. Used by IsPlatformExclusive to flag
	// "specialty" listings (only on a single venue) so the
	// liquidity-alert path (#10 / #11) can skip them — there is
	// nothing to compare against when only one platform offers the
	// market.
	platformsByCanonical map[string]int
}

// NewCanonicalIndex builds the reverse index from the raw symbol_mapping
// entries. Each (platform, alias) pair in the per-canonical alias map
// becomes an entry. Conflicts (same alias mapped to two canonicals on
// the same platform) keep the first declaration so YAML order is
// authoritative.
func NewCanonicalIndex(symbols []symbolYAML) *CanonicalIndex {
	idx := &CanonicalIndex{
		byPlatform:           map[string]map[string]string{},
		canonicals:           map[string]struct{}{},
		crossPlatform:        map[string]string{},
		platformsByCanonical: map[string]int{},
	}
	// owners[baseUpper] = set of canonicals that claim that alias on
	// any platform. Used post-pass to decide which entries qualify for
	// the unambiguous cross-platform fallback.
	owners := map[string]map[string]struct{}{}
	// platformSets[canonical] = set of platforms that declare any
	// alias for this canonical; converted to a count after the pass.
	platformSets := map[string]map[string]struct{}{}
	for _, s := range symbols {
		canonical := strings.ToUpper(strings.TrimSpace(s.Canonical))
		if canonical == "" {
			continue
		}
		idx.canonicals[canonical] = struct{}{}
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
			declaredAny := false
			for _, alias := range aliases {
				base := strings.ToUpper(strings.TrimSpace(alias))
				if base == "" {
					continue
				}
				declaredAny = true
				if _, exists := perPlatform[base]; !exists {
					perPlatform[base] = canonical
				}
				ownerSet, ok := owners[base]
				if !ok {
					ownerSet = map[string]struct{}{}
					owners[base] = ownerSet
				}
				ownerSet[canonical] = struct{}{}
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
			continue
		}
		for canonical := range ownerSet {
			idx.crossPlatform[base] = canonical
		}
	}
	for canonical, set := range platformSets {
		idx.platformsByCanonical[canonical] = len(set)
	}
	return idx
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
	base = strings.ToUpper(strings.TrimSpace(base))
	if base == "" {
		return ""
	}
	if idx == nil || len(idx.byPlatform) == 0 {
		return base
	}
	plat := strings.ToLower(strings.TrimSpace(platform))
	if perPlatform, ok := idx.byPlatform[plat]; ok {
		if canonical, ok := perPlatform[base]; ok {
			return canonical
		}
	}
	if canonical, ok := idx.crossPlatform[base]; ok {
		return canonical
	}
	return base
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
