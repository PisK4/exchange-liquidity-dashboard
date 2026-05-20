package coingecko

import (
	"encoding/json"
	"strconv"
	"time"
)

// flexibleFloat extracts a float64 from a JSON value that may have been
// decoded with or without json.Decoder.UseNumber(). Returns (0, false) when
// the value is missing or non-numeric.
func flexibleFloat(v any) (float64, bool) {
	switch num := v.(type) {
	case float64:
		return num, true
	case json.Number:
		f, err := num.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		if num == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// Ticker mirrors one row of the array returned by
// GET /api/v3/derivatives?include_tickers=unexpired.
//
// CoinGecko's response uses display names (e.g. "Binance (Futures)") in the
// market field, while the per-exchange detail endpoint
// /derivatives/exchanges/{id} uses the lowercased exchange id
// (e.g. "binance_futures"). Our internal mapping config carries both forms
// (CoinGeckoConfig.MarketName / CoinGeckoConfig.ExchangeID) so we can move
// between them without guessing.
//
// LastTradedAt is sent as a unix-epoch integer in the JSON payload. We use
// FlexibleTime to absorb either int seconds or RFC3339 strings, because
// CoinGecko has flipped the format in the past for some markets.
type Ticker struct {
	Market         string         `json:"market"`
	Symbol         string         `json:"symbol"`
	IndexID        string         `json:"index_id,omitempty"`
	Index          float64        `json:"index,omitempty"`
	BasisRaw       FlexibleNumber `json:"basis,omitempty"`
	Price          FlexibleNumber `json:"price"`
	PriceUSD       FlexibleNumber `json:"price_usd,omitempty"`
	Spread         FlexibleNumber `json:"spread,omitempty"`
	BidAskSpread   FlexibleNumber `json:"bid_ask_spread,omitempty"`
	FundingRate    FlexibleNumber `json:"funding_rate,omitempty"`
	OpenInterest   FlexibleNumber `json:"open_interest,omitempty"`
	Volume24H      FlexibleNumber `json:"volume_24h"`
	ContractType   string         `json:"contract_type,omitempty"`
	ExpiredAtRaw   FlexibleTime   `json:"expired_at,omitempty"`
	LastTradedAt   FlexibleTime   `json:"last_traded_at,omitempty"`
	ConvertedVolMS map[string]any `json:"converted_volume,omitempty"`
}

// Volume24HUSD returns the raw 24h notional as float64. CoinGecko's
// /derivatives endpoint expresses volume_24h in USD by default for
// derivatives tickers, so callers can treat the value as USD-denominated.
func (t Ticker) Volume24HUSD() float64 {
	if t.ConvertedVolMS != nil {
		if v, ok := t.ConvertedVolMS["usd"]; ok {
			if f, ok := flexibleFloat(v); ok && f > 0 {
				return f
			}
		}
	}
	return float64(t.Volume24H)
}

// OpenInterestUSD returns open_interest as float64. CoinGecko emits this in
// USD for derivatives tickers.
func (t Ticker) OpenInterestUSD() float64 { return float64(t.OpenInterest) }

// FlexibleNumber accepts both numeric JSON literals and stringified numbers,
// because CoinGecko occasionally emits the latter for very large derivatives
// volumes. The zero value reads as 0 even from null / missing keys.
type FlexibleNumber float64

func (f *FlexibleNumber) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = FlexibleNumber(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*f = FlexibleNumber(v)
	return nil
}

// FlexibleTime accepts either unix-epoch integers (seconds) or RFC3339
// strings. A null / missing / zero value reads as time.Time{}.
type FlexibleTime time.Time

func (t *FlexibleTime) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*t = FlexibleTime(time.Time{})
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			*t = FlexibleTime(time.Time{})
			return nil
		}
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
		*t = FlexibleTime(parsed.UTC())
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	if n <= 0 {
		*t = FlexibleTime(time.Time{})
		return nil
	}
	*t = FlexibleTime(time.Unix(n, 0).UTC())
	return nil
}

// Time returns the underlying time.Time so callers can use the standard API.
func (t FlexibleTime) Time() time.Time { return time.Time(t) }
