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
	BaseAssets []string `yaml:"base_assets" json:"base_assets"`

	set map[string]struct{}
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
		u.Platforms[name] = ListedPlatform{
			BaseAssets: clean,
			set:        seen,
		}
	}
}
