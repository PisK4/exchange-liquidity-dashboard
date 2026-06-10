package instrument

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EdgeX getMetaData responses share a common shape across the three
// public surfaces (perp_v1 / perp_v2 / spot): a `data.coinList` array
// of base-coin metadata and a `data.contractList` array of tradable
// contracts that reference baseCoinId. The normalizer joins both so
// the snapshot exposes the canonical base asset alongside the contract
// row.
type edgeXContractRaw struct {
	ContractID    string `json:"contractId"`
	BaseCoinID    string `json:"baseCoinId"`
	ContractName  string `json:"contractName"`
	EnableTrade   *bool  `json:"enableTrade"`
	EnableDisplay *bool  `json:"enableDisplay"`
	TickSize      string `json:"tickSize"`
	StepSize      string `json:"stepSize"`
	QuoteCoinID   string `json:"quoteCoinId"`
	SettleCoinID  string `json:"settleCoinId"`
}

type edgeXCoinRaw struct {
	CoinID   string `json:"coinId"`
	CoinName string `json:"coinName"`
}

type edgeXMetaEnvelope struct {
	Data struct {
		CoinList     []edgeXCoinRaw    `json:"coinList"`
		ContractList []json.RawMessage `json:"contractList"`
	} `json:"data"`
}

// edgeXMarketSurfaceFor maps the configured market_type to the
// t_listing_instrument_snapshot.market_surface column. perp_v1 and
// perp_v2 are surfaced as "perp"; "spot" stays "spot". Unknown
// values are rejected so the poll fails loud at wiring time.
func edgeXMarketSurfaceFor(marketType string) (string, error) {
	switch marketType {
	case "perp_v1", "perp_v2":
		return "perp", nil
	case "spot":
		return "spot", nil
	default:
		return "", fmt.Errorf("edgeX: unknown market_type %q", marketType)
	}
}

// NormalizeEdgeXContract turns one contractList entry into a
// NormalizedInstrument. The caller supplies the resolved base asset
// (looked up via baseCoinId → coinName) because the contract row
// itself only carries the coin id.
//
// Spec F1: contractId / tickSize / stepSize must be preserved in
// raw_json so the DB-first CatalogResolver can read platform-specific
// fields without hitting raw-instruments JSON dumps.
//
// Status semantics: enableTrade=false means an existing EdgeX market
// cannot trade and is treated as delisted. enableDisplay=false only
// means the contract is not visible on the product surface; it may be
// an internal/preconfigured market that has never launched, so we keep
// it as unknown instead of implying a historical delisting.
func NormalizeEdgeXContract(raw json.RawMessage, marketType, baseAsset string) (NormalizedInstrument, error) {
	surface, err := edgeXMarketSurfaceFor(marketType)
	if err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "edgeX", Message: err.Error()}
	}

	var p edgeXContractRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "edgeX", Message: "decode contract: " + err.Error()}
	}
	if strings.TrimSpace(p.ContractName) == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "edgeX", Message: "missing contractName"}
	}

	enableTrade := p.EnableTrade == nil || *p.EnableTrade
	enableDisplay := p.EnableDisplay == nil || *p.EnableDisplay
	status := "active"
	delist := false
	if !enableTrade {
		status = "delisted"
		delist = true
	} else if !enableDisplay {
		status = "unknown"
	}

	base := strings.ToUpper(strings.TrimSpace(baseAsset))
	canonical := base
	if canonical == "" {
		canonical = strings.ToUpper(p.ContractName)
	}

	n := NormalizedInstrument{
		Platform:         "edgeX",
		MarketType:       marketType,
		APISymbol:        p.ContractName,
		CanonicalSymbol:  canonical,
		BaseAsset:        base,
		MarketSurface:    surface,
		InstrumentKind:   "canonical",
		StatusRaw:        edgeXStatusRaw(enableTrade, enableDisplay),
		StatusNormalized: status,
		StatusFieldName:  "enableTrade",
		DelistFlag:       delist,
		RawJSON:          append(json.RawMessage(nil), raw...),
		// EdgeX CatalogResolver DB-first reads contractId / tickSize /
		// stepSize from raw_json (see decodeEdgeXSnapshot). Surfacing
		// the same six spec fields into StableHashExtras lets us flip
		// the hash on legitimate contractId rotations while ignoring
		// any other transient field upstream might add later.
		StableHashExtras: map[string]string{
			"contractId":   strings.TrimSpace(p.ContractID),
			"baseCoinId":   strings.TrimSpace(p.BaseCoinID),
			"quoteCoinId":  strings.TrimSpace(p.QuoteCoinID),
			"settleCoinId": strings.TrimSpace(p.SettleCoinID),
			"tickSize":     strings.TrimSpace(p.TickSize),
			"stepSize":     strings.TrimSpace(p.StepSize),
		},
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}

func edgeXStatusRaw(enableTrade, enableDisplay bool) string {
	switch {
	case !enableTrade:
		return "enable_trade_false"
	case !enableDisplay:
		return "enable_display_false"
	default:
		return "active"
	}
}

// ParseEdgeXMetaPayload decodes a full /getMetaData response and
// returns one NormalizedInstrument per contract. Contracts whose
// baseCoinId is missing from coinList are skipped silently — those
// rows would otherwise create snapshot rows with an empty base
// asset and pollute listed_universe.
func ParseEdgeXMetaPayload(payload []byte, marketType string) ([]NormalizedInstrument, error) {
	if _, err := edgeXMarketSurfaceFor(marketType); err != nil {
		return nil, &SchemaDriftError{Platform: "edgeX", Message: err.Error()}
	}
	var env edgeXMetaEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, &SchemaDriftError{Platform: "edgeX", Message: "decode envelope: " + err.Error()}
	}
	if len(env.Data.ContractList) == 0 {
		return nil, nil
	}
	baseByCoinID := make(map[string]string, len(env.Data.CoinList))
	for _, c := range env.Data.CoinList {
		baseByCoinID[c.CoinID] = strings.ToUpper(strings.TrimSpace(c.CoinName))
	}
	out := make([]NormalizedInstrument, 0, len(env.Data.ContractList))
	for _, row := range env.Data.ContractList {
		var hint edgeXContractRaw
		if err := json.Unmarshal(row, &hint); err != nil {
			continue
		}
		base, ok := baseByCoinID[hint.BaseCoinID]
		if !ok || base == "" {
			continue
		}
		n, err := NormalizeEdgeXContract(row, marketType, base)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}
