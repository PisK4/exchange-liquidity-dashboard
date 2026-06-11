package config

import (
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ListedUniverse is the per-platform union of base assets currently listed
// across all of that platform's catalog markets (perp + spot). It is
// regenerated alongside instrument_catalog.yaml by build-catalog, and is the
// authoritative source for the "edgeX 已上线?" column on the Top30 tab and any
// future "listed?" lookup that does not need quote-asset resolution.
//
// Match semantics are base-asset, case-insensitive. Quote (USD vs USDT vs
// USDC) is intentionally ignored at this layer.
type ListedUniverse struct {
	SchemaVersion int                       `yaml:"schema_version" json:"schema_version"`
	GeneratedAt   string                    `yaml:"generated_at" json:"generated_at"`
	GeneratedBy   string                    `yaml:"generated_by" json:"generated_by"`
	Platforms     map[string]ListedPlatform `yaml:"platforms" json:"platforms"`
	loaded        bool
}

// ListedPlatform holds the sorted base-asset universe for a single platform.
// BaseAssets is the human-readable, deterministic list shipped on disk; the
// in-memory set is built lazily for O(1) IsListed lookup.
type ListedPlatform struct {
	BaseAssets []string              `yaml:"base_assets" json:"base_assets"`
	Entries    []ListedIdentityEntry `yaml:"entries,omitempty" json:"entries,omitempty"`

	set      map[string]struct{}
	entrySet map[string]struct{}
}

// ListedIdentityEntry is the identity-aware form of a listed market. It keeps
// legacy base_assets intact while letting Listing Agent distinguish e.g. a
// canonical TSLA perp from a synthetic stock future that happens to share the
// same canonical ticker.
type ListedIdentityEntry struct {
	CanonicalSymbol string `yaml:"canonical_symbol" json:"canonical_symbol"`
	DisplaySymbol   string `yaml:"display_symbol,omitempty" json:"display_symbol,omitempty"`
	BaseAsset       string `yaml:"base_asset,omitempty" json:"base_asset,omitempty"`
	APISymbol       string `yaml:"api_symbol,omitempty" json:"api_symbol,omitempty"`
	MarketSurface   string `yaml:"market_surface" json:"market_surface"`
	InstrumentKind  string `yaml:"instrument_kind" json:"instrument_kind"`
}

// LoadListedUniverse parses listed_universe.yaml. A missing file is not an
// error: the returned universe will simply have Loaded()=false, which the
// collector interprets as "feature disabled, skip enrichment". Parse errors
// are surfaced so a malformed file fails fast at boot.
func LoadListedUniverse(path string) (*ListedUniverse, error) {
	u := &ListedUniverse{Platforms: map[string]ListedPlatform{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, u); err != nil {
		return nil, err
	}
	u.normalise()
	u.loaded = true
	return u, nil
}

// NewListedUniverseFromMap is a convenience for tests: build a populated
// universe directly from a {platform: [base, ...]} map.
func NewListedUniverseFromMap(platforms map[string][]string) *ListedUniverse {
	u := &ListedUniverse{Platforms: map[string]ListedPlatform{}}
	for p, bases := range platforms {
		u.Platforms[p] = ListedPlatform{BaseAssets: append([]string(nil), bases...)}
	}
	u.normalise()
	u.loaded = true
	return u
}

// Loaded reports whether the universe was successfully populated from a file
// (or a test constructor). When false, every IsListed call returns false and
// the collector should leave EdgexListed/ListedStatus on their zero values so
// the UI keeps showing legacy "否" rather than a misleading badge.
func (u *ListedUniverse) Loaded() bool {
	if u == nil {
		return false
	}
	return u.loaded
}

// IsListed reports whether `base` (case-insensitive) is in the platform's
// universe. Unknown platforms return false. A nil receiver returns false so
// callers can blindly pass an unloaded universe.
func (u *ListedUniverse) IsListed(platform, base string) bool {
	if u == nil {
		return false
	}
	platformEntry, ok := u.Platforms[platform]
	if !ok {
		return false
	}
	if platformEntry.set == nil {
		return false
	}
	_, ok = platformEntry.set[strings.ToUpper(strings.TrimSpace(base))]
	return ok
}

// IsListedIdentity reports whether a platform lists the exact semantic market
// identity. When a platform has identity entries, exact
// canonical+surface+kind matching is authoritative. Legacy base_assets remain
// as a fallback for old seed files that have not yet been refreshed.
func (u *ListedUniverse) IsListedIdentity(platform, canonical, marketSurface, instrumentKind string) bool {
	if u == nil {
		return false
	}
	platformEntry, ok := u.Platforms[platform]
	if !ok {
		return false
	}
	if len(platformEntry.entrySet) > 0 {
		_, ok := platformEntry.entrySet[listedIdentityKey(canonical, marketSurface, instrumentKind)]
		return ok
	}
	return u.IsListed(platform, canonical)
}

// BaseAssets returns a sorted copy of the platform's base-asset list. Empty
// slice when the platform is missing or the universe was never loaded.
func (u *ListedUniverse) BaseAssets(platform string) []string {
	if u == nil {
		return nil
	}
	p, ok := u.Platforms[platform]
	if !ok {
		return nil
	}
	out := make([]string, len(p.BaseAssets))
	copy(out, p.BaseAssets)
	return out
}

// normalise upper-cases, dedups, and sorts every platform's BaseAssets and
// rebuilds the lookup set. Safe to call multiple times.
func (u *ListedUniverse) normalise() {
	for name, p := range u.Platforms {
		seen := make(map[string]struct{}, len(p.BaseAssets))
		clean := make([]string, 0, len(p.BaseAssets))
		for _, b := range p.BaseAssets {
			b = strings.ToUpper(strings.TrimSpace(b))
			if b == "" {
				continue
			}
			if _, ok := seen[b]; ok {
				continue
			}
			seen[b] = struct{}{}
			clean = append(clean, b)
		}
		sort.Strings(clean)

		entrySeen := make(map[string]struct{}, len(p.Entries))
		entries := make([]ListedIdentityEntry, 0, len(p.Entries))
		for _, entry := range p.Entries {
			entry.CanonicalSymbol = strings.ToUpper(strings.TrimSpace(entry.CanonicalSymbol))
			entry.BaseAsset = strings.ToUpper(strings.TrimSpace(entry.BaseAsset))
			entry.APISymbol = strings.ToUpper(strings.TrimSpace(entry.APISymbol))
			entry.DisplaySymbol = strings.TrimSpace(entry.DisplaySymbol)
			entry.MarketSurface = strings.ToLower(strings.TrimSpace(entry.MarketSurface))
			entry.InstrumentKind = strings.ToLower(strings.TrimSpace(entry.InstrumentKind))
			if entry.CanonicalSymbol == "" {
				entry.CanonicalSymbol = entry.BaseAsset
			}
			if entry.CanonicalSymbol == "" {
				continue
			}
			key := listedIdentityKey(entry.CanonicalSymbol, entry.MarketSurface, entry.InstrumentKind)
			if _, ok := entrySeen[key]; ok {
				continue
			}
			entrySeen[key] = struct{}{}
			entries = append(entries, entry)
		}
		sort.Slice(entries, func(i, j int) bool {
			ki := listedIdentityKey(entries[i].CanonicalSymbol, entries[i].MarketSurface, entries[i].InstrumentKind)
			kj := listedIdentityKey(entries[j].CanonicalSymbol, entries[j].MarketSurface, entries[j].InstrumentKind)
			return ki < kj
		})
		u.Platforms[name] = ListedPlatform{
			BaseAssets: clean,
			Entries:    entries,
			set:        seen,
			entrySet:   entrySeen,
		}
	}
}

func listedIdentityKey(canonical, marketSurface, instrumentKind string) string {
	return strings.ToUpper(strings.TrimSpace(canonical)) + "|" +
		strings.ToLower(strings.TrimSpace(marketSurface)) + "|" +
		strings.ToLower(strings.TrimSpace(instrumentKind))
}
