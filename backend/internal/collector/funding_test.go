package collector

import (
	"math"
	"testing"
)

func TestFundingPeriodHours(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		want     int
		wantOK   bool
	}{
		{"binance is 8h", "binance", 8, true},
		{"okx is 8h", "okx", 8, true},
		{"bybit is 8h", "bybit", 8, true},
		{"bitget is 8h V1", "bitget", 8, true},
		{"bingx is 8h", "bingx", 8, true},
		{"mexc is 8h", "mexc", 8, true},
		{"gate is 8h V1", "gate", 8, true},
		{"hyperliquid is 1h", "hyperliquid", 1, true},
		{"lighter is 1h", "lighter", 1, true},
		{"edgeX is 4h", "edgeX", 4, true},

		{"case-insensitive EDGEX resolves", "EDGEX", 4, true},
		{"case-insensitive Binance resolves", "Binance", 8, true},
		{"whitespace gets trimmed", "  bybit  ", 8, true},

		{"unknown platform returns false", "drift_exchange", 0, false},
		{"empty string returns false", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FundingPeriodHours(tc.platform)
			if ok != tc.wantOK {
				t.Fatalf("FundingPeriodHours(%q) ok = %v, want %v", tc.platform, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("FundingPeriodHours(%q) = %d, want %d", tc.platform, got, tc.want)
			}
		})
	}
}

func TestNormalizeTo8h(t *testing.T) {
	cases := []struct {
		name        string
		rate        float64
		periodHours int
		want        float64
	}{
		{"8h identity for binance value", 0.003164, 8, 0.003164},
		{"4h doubles for edgeX value", 0.005, 4, 0.010},
		{"1h ×8 for hyperliquid value", 0.00125, 1, 0.010},
		{"1h ×8 for lighter value", 0.0007, 1, 0.0056},
		{"negative 1h rate scales correctly", -0.000573, 1, -0.004584},
		{"zero stays zero", 0, 8, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeTo8h(tc.rate, tc.periodHours)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("NormalizeTo8h(%v, %d) = %v, want %v", tc.rate, tc.periodHours, got, tc.want)
			}
		})
	}
}

func TestNormalizeTo8hRejectsInvalidPeriod(t *testing.T) {
	cases := []struct {
		name   string
		period int
	}{
		{"zero period", 0},
		{"negative period", -8},
		{"unsupported 2h period", 2},
		{"unsupported 6h period", 6},
		{"unsupported 24h period", 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeTo8h(0.005, tc.period)
			if !math.IsNaN(got) {
				t.Fatalf("NormalizeTo8h(0.005, %d) = %v, want NaN", tc.period, got)
			}
		})
	}
}

func TestSanityCheckRate8h(t *testing.T) {
	cases := []struct {
		name string
		rate float64
		want bool
	}{
		{"typical positive passes", 0.005, true},
		{"typical negative passes", -0.01, true},
		{"zero passes", 0, true},
		{"upper boundary exactly 0.5%", 0.5, true},
		{"lower boundary exactly -0.5%", -0.5, true},
		{"slightly above threshold fails", 0.5001, false},
		{"slightly below threshold fails", -0.5001, false},
		{"absurd value fails", 5.0, false},
		{"NaN fails", math.NaN(), false},
		{"+Inf fails", math.Inf(1), false},
		{"-Inf fails", math.Inf(-1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanityCheckRate8h(tc.rate)
			if got != tc.want {
				t.Fatalf("SanityCheckRate8h(%v) = %v, want %v", tc.rate, got, tc.want)
			}
		})
	}
}

func TestIsKnownPeriod(t *testing.T) {
	if !IsKnownPeriod("edgeX") {
		t.Fatal("IsKnownPeriod(edgeX) should be true")
	}
	if !IsKnownPeriod("HYPERLIQUID") {
		t.Fatal("IsKnownPeriod(HYPERLIQUID) should be true (case-insensitive)")
	}
	if IsKnownPeriod("drift_exchange") {
		t.Fatal("IsKnownPeriod(drift_exchange) should be false")
	}
}
