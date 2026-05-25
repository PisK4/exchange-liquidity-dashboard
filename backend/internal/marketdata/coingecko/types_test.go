package coingecko

import (
	"encoding/json"
	"math"
	"testing"
)

func TestOptionalFlexibleNumberUnmarshal(t *testing.T) {
	type wrapper struct {
		FundingRate OptionalFlexibleNumber `json:"funding_rate"`
	}
	cases := []struct {
		name      string
		payload   string
		wantValid bool
		wantValue float64
	}{
		{"explicit null reads as unset", `{"funding_rate": null}`, false, 0},
		{"missing key reads as unset", `{}`, false, 0},
		{"empty string reads as unset", `{"funding_rate": ""}`, false, 0},
		{"numeric zero reads as set zero", `{"funding_rate": 0}`, true, 0},
		{"negative literal reads as set negative", `{"funding_rate": -0.00123}`, true, -0.00123},
		{"stringified positive reads as set positive", `{"funding_rate": "0.003164"}`, true, 0.003164},
		{"stringified negative reads as set negative", `{"funding_rate": "-0.001000"}`, true, -0.001000},
		{"stringified zero reads as set zero", `{"funding_rate": "0"}`, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w wrapper
			if err := json.Unmarshal([]byte(tc.payload), &w); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if w.FundingRate.Valid != tc.wantValid {
				t.Fatalf("Valid = %v, want %v", w.FundingRate.Valid, tc.wantValid)
			}
			if w.FundingRate.Valid && math.Abs(w.FundingRate.Value-tc.wantValue) > 1e-12 {
				t.Fatalf("Value = %v, want %v", w.FundingRate.Value, tc.wantValue)
			}
		})
	}
}

func TestOptionalFlexibleNumberPtr(t *testing.T) {
	t.Run("unset returns nil", func(t *testing.T) {
		f := OptionalFlexibleNumber{}
		if got := f.Ptr(); got != nil {
			t.Fatalf("Ptr() = %v, want nil", got)
		}
	})
	t.Run("set zero returns ptr to zero", func(t *testing.T) {
		f := OptionalFlexibleNumber{Valid: true, Value: 0}
		got := f.Ptr()
		if got == nil {
			t.Fatal("Ptr() = nil, want non-nil")
		}
		if *got != 0 {
			t.Fatalf("*Ptr() = %v, want 0", *got)
		}
	})
	t.Run("set positive returns ptr to copy", func(t *testing.T) {
		f := OptionalFlexibleNumber{Valid: true, Value: 0.003164}
		got := f.Ptr()
		if got == nil {
			t.Fatal("Ptr() = nil, want non-nil")
		}
		if *got != 0.003164 {
			t.Fatalf("*Ptr() = %v, want 0.003164", *got)
		}
		*got = 99
		if f.Value != 0.003164 {
			t.Fatalf("mutating Ptr leaked back into struct: %v", f.Value)
		}
	})
}

func TestOptionalFlexibleNumberRejectsBadInput(t *testing.T) {
	var f OptionalFlexibleNumber
	if err := f.UnmarshalJSON([]byte(`"not-a-number"`)); err == nil {
		t.Fatal("expected parse error for non-numeric string, got nil")
	}
	if err := f.UnmarshalJSON([]byte(`{"object": true}`)); err == nil {
		t.Fatal("expected parse error for object literal, got nil")
	}
}

func TestTickerFundingRateRoundTrips(t *testing.T) {
	payload := `[
		{"market": "Binance", "symbol": "BTCUSDT", "price": "100", "volume_24h": "1", "funding_rate": "0.003164"},
		{"market": "Bitget", "symbol": "BTCUSDT", "price": "100", "volume_24h": "1", "funding_rate": null},
		{"market": "Lighter", "symbol": "BTCUSDT", "price": "100", "volume_24h": "1"}
	]`
	var tickers []Ticker
	if err := json.Unmarshal([]byte(payload), &tickers); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(tickers) != 3 {
		t.Fatalf("len(tickers) = %d, want 3", len(tickers))
	}
	if !tickers[0].FundingRate.Valid || tickers[0].FundingRate.Value != 0.003164 {
		t.Fatalf("binance funding_rate not parsed: %+v", tickers[0].FundingRate)
	}
	if tickers[1].FundingRate.Valid {
		t.Fatalf("explicit null should be unset, got %+v", tickers[1].FundingRate)
	}
	if tickers[2].FundingRate.Valid {
		t.Fatalf("missing field should be unset, got %+v", tickers[2].FundingRate)
	}
}
