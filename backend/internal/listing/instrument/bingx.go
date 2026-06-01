package instrument

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// BingX spot endpoint /openApi/spot/v1/common/symbols returns rows like:
//
//	{"symbol":"BTC-USDT","status":1,"baseAsset":"BTC","quoteAsset":"USDT"}
//
// status is documented as integer (1=trading; 0=halted/pre-launch).
type bingxSpotRaw struct {
	Symbol     string `json:"symbol"`
	Status     int    `json:"status"`
	BaseAsset  string `json:"baseAsset"`
	QuoteAsset string `json:"quoteAsset"`
}

// BingX swap endpoint /openApi/swap/v2/quote/contracts returns rows like:
//
//	{"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":...}
//
// "asset" is the base; "quoteAsset" is the settle/quote. Status is also
// integer (1=trading; 5=pre-launch; 25=settling; 0/2/3/4/5=other states).
//
// pricePrecision, quantityPrecision, size and tradeMinQuantity are
// schema-stable spec fields surfaced into StableHashExtras so a
// legitimate contract rotation still flips the hash. feeRate /
// makerFeeRate / takerFeeRate / triggerFeeRate are intentionally
// excluded — fee schedules can be re-quoted by the exchange without
// it being a listing event.
type bingxSwapRaw struct {
	Symbol            string  `json:"symbol"`
	Status            int     `json:"status"`
	Asset             string  `json:"asset"`
	QuoteAsset        string  `json:"quoteAsset"`
	LaunchTime        int64   `json:"launchTime"`
	ContractID        string  `json:"contractId"`
	PricePrecision    int     `json:"pricePrecision"`
	QuantityPrecision int     `json:"quantityPrecision"`
	Size              string  `json:"size"`
	TradeMinQuantity  float64 `json:"tradeMinQuantity"`
}

// NormalizeBingXSpotSymbol turns one /openApi/spot/v1/common/symbols
// row into a NormalizedInstrument. Spot status code semantics:
//
//	1 = trading        → active
//	0 = halted/pending → paused (NOT active; spec F7 guards against
//	                    silent promotion to active)
//
// Any other status is left as "unknown" so an operator visually
// notices schema drift without the row being misclassified.
func NormalizeBingXSpotSymbol(raw json.RawMessage) (NormalizedInstrument, error) {
	var p bingxSpotRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bingx", Message: "decode spot symbol: " + err.Error()}
	}
	if strings.TrimSpace(p.Symbol) == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bingx", Message: "missing symbol"}
	}
	status := mapBingXSpotStatus(p.Status)
	base := strings.ToUpper(strings.TrimSpace(p.BaseAsset))
	quote := strings.ToUpper(strings.TrimSpace(p.QuoteAsset))
	if base == "" || quote == "" {
		if parts := strings.SplitN(p.Symbol, "-", 2); len(parts) == 2 {
			if base == "" {
				base = strings.ToUpper(parts[0])
			}
			if quote == "" {
				quote = strings.ToUpper(parts[1])
			}
		}
	}
	kind := bingxInstrumentKind(base)
	n := NormalizedInstrument{
		Platform:         "bingx",
		MarketType:       "spot",
		APISymbol:        p.Symbol,
		CanonicalSymbol:  canonicalSymbol(base, p.Symbol),
		BaseAsset:        base,
		QuoteAsset:       quote,
		SettleAsset:      quote,
		MarketSurface:    "spot",
		InstrumentKind:   kind,
		StatusRaw:        fmt.Sprintf("%d", p.Status),
		StatusNormalized: status,
		StatusFieldName:  "status",
		DelistFlag:       status == "delisted",
		RawJSON:          append(json.RawMessage(nil), raw...),
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}

// NormalizeBingXSwapSymbol turns one /openApi/swap/v2/quote/contracts
// row into a NormalizedInstrument. The synthetic-instrument prefix
// check (spec F7) lives here so refresh_job's SQL filter only needs
// one canonical column (instrument_kind) regardless of provider.
func NormalizeBingXSwapSymbol(raw json.RawMessage) (NormalizedInstrument, error) {
	var p bingxSwapRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bingx", Message: "decode swap symbol: " + err.Error()}
	}
	if strings.TrimSpace(p.Symbol) == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bingx", Message: "missing symbol"}
	}
	status := mapBingXSwapStatus(p.Status)
	base := strings.ToUpper(strings.TrimSpace(p.Asset))
	quote := strings.ToUpper(strings.TrimSpace(p.QuoteAsset))
	if base == "" || quote == "" {
		if parts := strings.SplitN(p.Symbol, "-", 2); len(parts) == 2 {
			if base == "" {
				base = strings.ToUpper(parts[0])
			}
			if quote == "" {
				quote = strings.ToUpper(parts[1])
			}
		}
	}
	kind := bingxInstrumentKind(base)
	n := NormalizedInstrument{
		Platform:             "bingx",
		MarketType:           "swap",
		APISymbol:            p.Symbol,
		CanonicalSymbol:      canonicalSymbol(base, p.Symbol),
		BaseAsset:            base,
		QuoteAsset:           quote,
		SettleAsset:          quote,
		MarketSurface:        "perp",
		InstrumentKind:       kind,
		StatusRaw:            fmt.Sprintf("%d", p.Status),
		StatusNormalized:     status,
		StatusFieldName:      "status",
		ListingTimeTS:        nowFromUnixMillis(p.LaunchTime),
		ListingTimeFieldName: "launchTime",
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
		StableHashExtras: map[string]string{
			"contractId":        strings.TrimSpace(p.ContractID),
			"pricePrecision":    strconv.Itoa(p.PricePrecision),
			"quantityPrecision": strconv.Itoa(p.QuantityPrecision),
			"size":              strings.TrimSpace(p.Size),
			"tradeMinQuantity":  strconv.FormatFloat(p.TradeMinQuantity, 'f', -1, 64),
		},
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}

// bingxInstrumentKind classifies the base asset. Per spec F7, BingX
// hosts a large set of synthetic tokens whose base names start with
// NCSK (US equity) or NCCO (commodity). These rows must NOT enter the
// listed_universe view because they are not real crypto assets.
func bingxInstrumentKind(baseAsset string) string {
	base := strings.ToUpper(strings.TrimSpace(baseAsset))
	if strings.HasPrefix(base, "NCSK") || strings.HasPrefix(base, "NCCO") {
		return "synthetic"
	}
	return "canonical"
}

func mapBingXSpotStatus(code int) string {
	switch code {
	case 1:
		return "active"
	case 0:
		return "paused"
	case 5:
		return "pre_listing"
	case 25:
		return "delisted"
	default:
		return "unknown"
	}
}

func mapBingXSwapStatus(code int) string {
	switch code {
	case 1:
		return "active"
	case 0:
		return "paused"
	case 2, 3, 4:
		return "paused"
	case 5:
		return "pre_listing"
	case 25:
		return "delisted"
	default:
		return "unknown"
	}
}
