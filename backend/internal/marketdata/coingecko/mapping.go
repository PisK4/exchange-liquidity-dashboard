package coingecko

import (
	"strings"
)

// Mapping resolves CoinGecko's two-tier platform identifiers (display
// market_name as emitted by /derivatives, and lowercase exchange_id as used
// by /derivatives/exchanges/{id}) into our internal platform names
// (binance / okx / bybit / ...).
//
// Both maps are required: ExchangeID is the source of truth when calling the
// per-exchange detail endpoint, while MarketName is what we filter on after a
// global /derivatives pull.
//
// Mapping is intentionally cheap and read-only: it is built once at boot from
// CoinGeckoConfig.ExchangeID / MarketName and shared with every collector run.
type Mapping struct {
	platformByMarket   map[string]string
	platformByExchange map[string]string
	exchangeByPlatform map[string]string
	marketByPlatform   map[string]string
}

// NewMapping constructs a Mapping from the runtime config. Empty values are
// silently dropped so a partially-populated yaml block still produces a
// usable mapping for the platforms it does cover.
func NewMapping(exchangeID, marketName map[string]string) *Mapping {
	m := &Mapping{
		platformByMarket:   map[string]string{},
		platformByExchange: map[string]string{},
		exchangeByPlatform: map[string]string{},
		marketByPlatform:   map[string]string{},
	}
	for platform, id := range exchangeID {
		platform = strings.TrimSpace(platform)
		id = strings.TrimSpace(id)
		if platform == "" || id == "" {
			continue
		}
		m.exchangeByPlatform[platform] = id
		m.platformByExchange[normaliseExchangeID(id)] = platform
	}
	for platform, name := range marketName {
		platform = strings.TrimSpace(platform)
		name = strings.TrimSpace(name)
		if platform == "" || name == "" {
			continue
		}
		m.marketByPlatform[platform] = name
		m.platformByMarket[normaliseMarketName(name)] = platform
	}
	return m
}

// PlatformByMarketName resolves a CoinGecko market display name (as found in
// /derivatives tickers[].market) to our internal platform name. Returns
// ("", false) for unknown markets so callers can log and skip them.
func (m *Mapping) PlatformByMarketName(market string) (string, bool) {
	if m == nil {
		return "", false
	}
	p, ok := m.platformByMarket[normaliseMarketName(market)]
	return p, ok
}

// PlatformByExchangeID resolves a CoinGecko lowercase exchange_id (as used by
// /derivatives/exchanges/{id}) to our internal platform name.
func (m *Mapping) PlatformByExchangeID(id string) (string, bool) {
	if m == nil {
		return "", false
	}
	p, ok := m.platformByExchange[normaliseExchangeID(id)]
	return p, ok
}

// ExchangeIDFor returns the CoinGecko exchange_id for our internal platform.
func (m *Mapping) ExchangeIDFor(platform string) (string, bool) {
	if m == nil {
		return "", false
	}
	id, ok := m.exchangeByPlatform[platform]
	return id, ok
}

// MarketNameFor returns the CoinGecko market display name for our internal platform.
func (m *Mapping) MarketNameFor(platform string) (string, bool) {
	if m == nil {
		return "", false
	}
	name, ok := m.marketByPlatform[platform]
	return name, ok
}

// Platforms returns the internal platform identifiers covered by both maps
// (i.e. platforms that have both an exchange_id and a market_name). Order is
// not stable; callers that need a stable order should sort the result.
func (m *Mapping) Platforms() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.exchangeByPlatform))
	for p := range m.exchangeByPlatform {
		if _, ok := m.marketByPlatform[p]; !ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ExchangeIDs returns a slice of all configured CoinGecko exchange_ids. Order
// is not stable.
func (m *Mapping) ExchangeIDs() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.exchangeByPlatform))
	for _, id := range m.exchangeByPlatform {
		out = append(out, id)
	}
	return out
}

// MarketNames returns a slice of all configured CoinGecko market display
// names. Order is not stable.
func (m *Mapping) MarketNames() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.marketByPlatform))
	for _, name := range m.marketByPlatform {
		out = append(out, name)
	}
	return out
}

// NormaliseSymbol converts a CoinGecko ticker symbol (e.g. "BTCUSDT" or
// "BTC-USDT") to the canonical display symbol used by edgex-dashboard
// ("BTC-USDT (perp)"). Symbols not recognised as a perp pair are returned
// unchanged so callers can still log them for triage.
func NormaliseSymbol(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	switch {
	case strings.HasSuffix(s, "USDT"):
		base := strings.TrimSuffix(s, "USDT")
		return strings.TrimSuffix(base, "-") + "-USDT (perp)"
	case strings.HasSuffix(s, "USDC"):
		base := strings.TrimSuffix(s, "USDC")
		return strings.TrimSuffix(base, "-") + "-USDC (perp)"
	case strings.HasSuffix(s, "USD"):
		base := strings.TrimSuffix(s, "USD")
		return strings.TrimSuffix(base, "-") + "-USD (perp)"
	}
	return s
}

func normaliseMarketName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normaliseExchangeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
