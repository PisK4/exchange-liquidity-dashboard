package listing

import "strings"

var nonListingTargetAssets = map[string]struct{}{
	"AUSD":   {},
	"BUSD":   {},
	"CRVUSD": {},
	"DAI":    {},
	"EURC":   {},
	"FDUSD":  {},
	"FRAX":   {},
	"GUSD":   {},
	"LUSD":   {},
	"PYUSD":  {},
	"RLUSD":  {},
	"SUSD":   {},
	"TUSD":   {},
	"USD":    {},
	"USD1":   {},
	"USDC":   {},
	"USDD":   {},
	"USDE":   {},
	"USDP":   {},
	"USDS":   {},
	"USDT":   {},
}

func isNonListingTargetAsset(symbol string) bool {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "" {
		return false
	}
	_, ok := nonListingTargetAssets[s]
	return ok
}

func isNonListingTargetSignal(s SignalObservation) bool {
	return isNonListingTargetAsset(s.CanonicalSymbol) || isNonListingTargetAsset(s.BaseAsset)
}
