package main

import (
	"fmt"
	"strings"
)

// frontendURL returns the canonical front-end trade page URL for a given
// (platform, marketType, baseAsset, quoteAsset, apiSymbol). Returned URL is
// what a human clicks to verify "this trading pair really exists on this
// exchange". Per platform decision tree intentionally explicit; if a market
// type is unsupported, return ("", false).
func frontendURL(platform, marketType, base, quote, apiSymbol string) (string, bool) {
	switch platform {
	case "binance":
		return binanceURL(marketType, base, quote, apiSymbol)
	case "okx":
		return okxURL(marketType, base, quote)
	case "bybit":
		return bybitURL(marketType, base, quote, apiSymbol)
	case "bitget":
		return bitgetURL(marketType, apiSymbol)
	case "bingx":
		return bingxURL(marketType, base, quote, apiSymbol)
	case "mexc":
		return mexcURL(marketType, apiSymbol)
	case "gate":
		return gateURL(marketType, apiSymbol)
	case "hyperliquid":
		return "https://app.hyperliquid.xyz/trade/" + base, true
	case "lighter":
		return "https://app.lighter.xyz/trade/" + base, true
	case "edgeX":
		return edgexURL(marketType, base, quote)
	}
	return "", false
}

func binanceURL(marketType, base, quote, apiSymbol string) (string, bool) {
	switch marketType {
	case "spot":
		return fmt.Sprintf("https://www.binance.com/zh-CN/trade/%s_%s?type=spot", base, quote), true
	case "usd-m":
		return "https://www.binance.com/zh-CN/futures/" + apiSymbol, true
	case "coin-m":
		return "https://www.binance.com/zh-CN/delivery/" + apiSymbol, true
	}
	return "", false
}

func okxURL(marketType, base, quote string) (string, bool) {
	pair := strings.ToLower(base) + "-" + strings.ToLower(quote)
	switch marketType {
	case "spot":
		return "https://www.okx.com/trade-spot/" + pair, true
	case "swap":
		return "https://www.okx.com/trade-swap/" + pair + "-swap", true
	case "futures":
		return "https://www.okx.com/trade-futures/" + pair, true
	}
	return "", false
}

func bybitURL(marketType, base, quote, apiSymbol string) (string, bool) {
	switch marketType {
	case "spot":
		return fmt.Sprintf("https://www.bybit.com/zh-MY/trade/spot/%s/%s", base, quote), true
	case "linear":
		return "https://www.bybit.com/trade/usdt/" + apiSymbol, true
	case "inverse":
		return "https://www.bybit.com/trade/inverse/" + apiSymbol, true
	}
	return "", false
}

func bitgetURL(marketType, apiSymbol string) (string, bool) {
	switch marketType {
	case "spot":
		return "https://www.bitget.com/spot/" + apiSymbol, true
	case "usdt-futures":
		return "https://www.bitget.com/futures/usdt/" + apiSymbol, true
	case "coin-futures":
		return "https://www.bitget.com/futures/coin/" + apiSymbol, true
	case "usdc-futures":
		return "https://www.bitget.com/futures/usdc/" + apiSymbol, true
	}
	return "", false
}

func bingxURL(marketType, base, quote, apiSymbol string) (string, bool) {
	switch marketType {
	case "spot":
		return "https://bingx.com/zh-cn/spot/" + apiSymbol + "/", true
	case "swap":
		return fmt.Sprintf("https://bingx.com/zh-cn/perpetual/%s-%s/", base, quote), true
	}
	return "", false
}

func mexcURL(marketType, apiSymbol string) (string, bool) {
	switch marketType {
	case "spot":
		return "https://www.mexc.com/exchange/" + apiSymbol, true
	case "contract":
		return "https://www.mexc.com/futures/" + apiSymbol, true
	}
	return "", false
}

func gateURL(marketType, apiSymbol string) (string, bool) {
	switch marketType {
	case "spot":
		return "https://www.gate.io/trade/" + apiSymbol, true
	case "futures-usdt":
		return "https://www.gate.io/futures/USDT/" + apiSymbol, true
	}
	return "", false
}

func edgexURL(marketType, base, quote string) (string, bool) {
	pair := base + "-" + quote
	// Pro front-end uses a slugged display symbol (e.g. BTCUSD) under /en-US/trade.
	proSymbol := base + "USD"
	switch marketType {
	case "perp-v1":
		return "https://pro.edgex.exchange/en-US/trade/" + proSymbol, true
	case "perp-v2":
		return "https://edgex-prod-v2.edgex.exchange/trade/" + pair, true
	case "spot":
		return "https://spot.edgex.exchange/trade/" + pair, true
	}
	return "", false
}

// perpMarketTypeFor returns the canonical perp market_type slug for a
// platform, i.e. which MarketDump key is the source-of-truth for "the perp"
// on that platform.
func perpMarketTypeFor(platform string) string {
	switch platform {
	case "binance":
		return "usd-m"
	case "okx":
		return "swap"
	case "bybit":
		return "linear"
	case "bitget":
		return "usdt-futures"
	case "bingx":
		return "swap"
	case "mexc":
		return "contract"
	case "gate":
		return "futures-usdt"
	case "hyperliquid":
		return "perp"
	case "lighter":
		return "perp"
	case "edgeX":
		return "perp-v1"
	}
	return ""
}

// apiLevelCapDefault mirrors adapter.apiLevelCap. Kept locally so the crawler
// can populate catalog yaml without a circular dependency.
func apiLevelCapDefault(platform string) int {
	switch platform {
	case "binance":
		return 2000
	case "okx":
		return 10000
	case "bybit":
		return 2000
	case "bitget":
		return 200
	case "bingx":
		return 2000
	case "mexc":
		return 2000
	case "gate":
		return 400
	case "hyperliquid":
		return 40
	case "edgeX":
		return 400
	case "lighter":
		return 0
	}
	return 0
}

// expectedQuoteFor returns the quote asset we expect the canonical perp to
// settle in. Hyperliquid / Lighter quote in USDC; everyone else USDT.
func expectedQuoteFor(platform string) string {
	switch platform {
	case "hyperliquid", "lighter":
		return "USDC"
	}
	return "USDT"
}
