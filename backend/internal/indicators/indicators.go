package indicators

import (
	"math"

	"edgex-dashboard/backend/internal/domain"
)

func MidPrice(book domain.OrderBookSnapshot) float64 {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return 0
	}
	return (book.Bids[0].Price + book.Asks[0].Price) / 2
}

func SpreadBP(bestBid, bestAsk float64) float64 {
	spread, ok := ValidSpreadBP(bestBid, bestAsk)
	if !ok {
		return 0
	}
	return spread
}

func ValidSpreadBP(bestBid, bestAsk float64) (float64, bool) {
	if bestBid <= 0 || bestAsk <= 0 || bestAsk <= bestBid {
		return 0, false
	}
	return (bestAsk - bestBid) / ((bestAsk + bestBid) / 2) * 10_000, true
}

func DepthAtTier(book domain.OrderBookSnapshot, tier float64) domain.DepthMetrics {
	mid := MidPrice(book)
	if mid <= 0 {
		return domain.DepthMetrics{}
	}
	bidFloor := mid * (1 - tier)
	askCeil := mid * (1 + tier)
	var bid, ask float64
	for _, l := range book.Bids {
		if l.Price >= bidFloor {
			bid += l.Price * l.Size
		}
	}
	for _, l := range book.Asks {
		if l.Price <= askCeil {
			ask += l.Price * l.Size
		}
	}
	return domain.DepthMetrics{BidUSD: bid, AskUSD: ask, TotalUSD: bid + ask}
}

func Imbalance(bidUSD, askUSD float64) float64 {
	denom := bidUSD + askUSD
	if denom <= 0 {
		return 0
	}
	return (bidUSD - askUSD) / denom * 100
}

func BuySlippageBP(book domain.OrderBookSnapshot, amountUSD float64) float64 {
	return slippageBP(book.Asks, MidPrice(book), amountUSD, true)
}

func SellSlippageBP(book domain.OrderBookSnapshot, amountUSD float64) float64 {
	return slippageBP(book.Bids, MidPrice(book), amountUSD, false)
}

func AdjustedVolume(platform string, rawVolume float64) float64 {
	switch platform {
	case "mexc":
		return rawVolume * 0.4
	case "gate":
		return rawVolume * 0.5
	default:
		return rawVolume
	}
}

func slippageBP(levels []domain.Level, mid, amountUSD float64, buy bool) float64 {
	if len(levels) == 0 || mid <= 0 || amountUSD <= 0 {
		return 0
	}
	remaining := amountUSD
	var qty, cost float64
	for _, l := range levels {
		levelUSD := l.Price * l.Size
		takeUSD := math.Min(remaining, levelUSD)
		if l.Price <= 0 {
			continue
		}
		qty += takeUSD / l.Price
		cost += takeUSD
		remaining -= takeUSD
		if remaining <= 0 {
			break
		}
	}
	if qty <= 0 {
		return 0
	}
	avg := cost / qty
	if buy {
		return (avg - mid) / mid * 10_000
	}
	return (mid - avg) / mid * 10_000
}
