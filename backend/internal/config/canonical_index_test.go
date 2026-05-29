package config

import "testing"

func TestCanonicalIndex_ResolveByPlatformAlias(t *testing.T) {
	idx := NewCanonicalIndex([]symbolYAML{
		{
			Canonical: "GOLD",
			Aliases: map[string][]string{
				"binance":     {"XAU", "PAXG", "XAUT"},
				"edgeX":       {"PAXG", "XAUT"},
				"hyperliquid": {"XAU", "PAXG"},
			},
		},
		{
			Canonical: "OIL",
			Aliases: map[string][]string{
				"binance":     {"CL"},
				"edgeX":       {"CL"},
				"hyperliquid": {"BRENTOIL"},
			},
		},
		{
			Canonical: "BTC",
		},
	})

	cases := []struct {
		platform string
		base     string
		want     string
	}{
		{"binance", "XAU", "GOLD"},
		{"edgeX", "PAXG", "GOLD"},
		{"edgeX", "XAUT", "GOLD"},
		{"hyperliquid", "BRENTOIL", "OIL"},
		{"edgeX", "CL", "OIL"},

		{"binance", "GOLD", "GOLD"},
		{"binance", "OIL", "OIL"},

		{"edgeX", "PEPE", "PEPE"},
		{"binance", "BTC", "BTC"},

		{"BINANCE", "xau", "GOLD"},
		{"EdgeX", "paxg", "GOLD"},
	}

	for _, tc := range cases {
		got := idx.Resolve(tc.platform, tc.base)
		if got != tc.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", tc.platform, tc.base, got, tc.want)
		}
	}
}

func TestCanonicalIndex_ResolveEmptyInputs(t *testing.T) {
	idx := NewCanonicalIndex(nil)
	if got := idx.Resolve("", ""); got != "" {
		t.Errorf("Resolve(\"\", \"\") = %q, want empty", got)
	}
	if got := idx.Resolve("binance", ""); got != "" {
		t.Errorf("Resolve with empty base should return empty, got %q", got)
	}
	if got := idx.Resolve("", "BTC"); got != "BTC" {
		t.Errorf("Resolve with empty platform should still uppercase identity, got %q", got)
	}
}

func TestCanonicalIndex_NilSafe(t *testing.T) {
	var idx *CanonicalIndex
	if got := idx.Resolve("binance", "xau"); got != "XAU" {
		t.Errorf("nil index should fall back to identity uppercase, got %q", got)
	}
}

func TestCanonicalIndex_CrossPlatformFallback(t *testing.T) {
	// symbol_mapping.yaml may be incomplete — e.g. OIL canonical only
	// lists binance/edgeX/hyperliquid in its alias map, but bitget,
	// bybit, gate, okx also publish `CL-USDT (perp)` rows. As long
	// as the base name `CL` unambiguously resolves to a single
	// canonical across the entire YAML, the index falls back to
	// that canonical for the missing platforms so cross-platform
	// aggregation still merges into one bucket.
	idx := NewCanonicalIndex([]symbolYAML{
		{
			Canonical: "OIL",
			Aliases: map[string][]string{
				"binance":     {"CL"},
				"edgeX":       {"CL"},
				"hyperliquid": {"BRENTOIL"},
			},
		},
		{
			Canonical: "GOLD",
			Aliases: map[string][]string{
				"binance": {"XAU", "PAXG"},
				"edgeX":   {"PAXG"},
			},
		},
	})

	if got := idx.Resolve("bitget", "CL"); got != "OIL" {
		t.Errorf("bitget|CL cross-platform fallback = %q, want OIL", got)
	}
	if got := idx.Resolve("okx", "BRENTOIL"); got != "OIL" {
		t.Errorf("okx|BRENTOIL cross-platform fallback = %q, want OIL", got)
	}
	if got := idx.Resolve("lighter", "PAXG"); got != "GOLD" {
		t.Errorf("lighter|PAXG cross-platform fallback = %q, want GOLD", got)
	}
}

func TestCanonicalIndex_CrossPlatformFallbackSkippedOnConflict(t *testing.T) {
	// Two canonicals claim the same alias on different platforms.
	// The fallback must NOT pick one arbitrarily — it must return
	// identity so the operator notices the conflict via the
	// divergence cards still showing two buckets.
	idx := NewCanonicalIndex([]symbolYAML{
		{
			Canonical: "AAA",
			Aliases:   map[string][]string{"binance": {"COIN"}},
		},
		{
			Canonical: "BBB",
			Aliases:   map[string][]string{"okx": {"COIN"}},
		},
	})
	if got := idx.Resolve("bitget", "COIN"); got != "COIN" {
		t.Errorf("ambiguous alias must not auto-resolve; got %q", got)
	}
}

func TestCanonicalIndex_IsPlatformExclusive(t *testing.T) {
	idx := NewCanonicalIndex([]symbolYAML{
		{
			Canonical: "GOLD",
			Aliases: map[string][]string{
				"binance":     {"XAU"},
				"edgeX":       {"PAXG"},
				"hyperliquid": {"XAU"},
			},
		},
		{
			Canonical: "HYPE_INDEX",
			Aliases: map[string][]string{
				"hyperliquid": {"XYZ:CL"},
			},
		},
		{
			Canonical: "LIGHTER_ONLY",
			Aliases: map[string][]string{
				"lighter": {"WEIRD"},
			},
		},
		{
			Canonical: "PROMISED_NOTHING",
			Aliases:   map[string][]string{"binance": {}}, // empty alias list — not declared
		},
	})

	cases := []struct {
		canonical string
		want      bool
	}{
		{"GOLD", false},             // 3 platforms → not exclusive
		{"HYPE_INDEX", true},        // hyperliquid only
		{"LIGHTER_ONLY", true},      // lighter only
		{"PROMISED_NOTHING", false}, // declared but no alias → no platform counted; falls through to false
		{"BTC", false},              // unknown canonical → false (conservative)
		{"gold", false},             // case-insensitive
		{"  GOLD  ", false},         // trimmed
	}
	for _, tc := range cases {
		if got := idx.IsPlatformExclusive(tc.canonical); got != tc.want {
			t.Errorf("IsPlatformExclusive(%q) = %v, want %v", tc.canonical, got, tc.want)
		}
	}

	var nilIdx *CanonicalIndex
	if nilIdx.IsPlatformExclusive("GOLD") {
		t.Error("nil receiver should return false")
	}
}
