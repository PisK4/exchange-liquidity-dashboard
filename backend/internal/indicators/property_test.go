package indicators

import (
	"math"
	"testing"
	"testing/quick"

	"edgex-ops-intelligence/backend/internal/domain"
)

func TestIndicatorProperties(t *testing.T) {
	t.Run("mid price positive when best bid ask are positive", func(t *testing.T) {
		prop := func(bidRaw, spreadRaw uint16) bool {
			bid := 1 + float64(bidRaw%10_000)
			ask := bid + 0.01 + float64(spreadRaw%1_000)/100
			mid := MidPrice(domain.OrderBookSnapshot{
				Bids: []domain.Level{{Price: bid, Size: 1}},
				Asks: []domain.Level{{Price: ask, Size: 1}},
			})
			return mid > 0
		}
		if err := quick.Check(prop, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("depth totals are non negative", func(t *testing.T) {
		prop := func(sizeRaw, tierRaw uint16) bool {
			size := float64(sizeRaw % 1_000)
			tier := 0.0001 + float64(tierRaw%200)/10_000
			depth := DepthAtTier(domain.OrderBookSnapshot{
				Bids: []domain.Level{{Price: 99, Size: size}},
				Asks: []domain.Level{{Price: 101, Size: size}},
			}, tier)
			return depth.BidUSD >= 0 && depth.AskUSD >= 0 && depth.TotalUSD >= 0
		}
		if err := quick.Check(prop, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("imbalance stays bounded", func(t *testing.T) {
		prop := func(bidRaw, askRaw uint32) bool {
			bid := float64(bidRaw % 1_000_000)
			ask := float64(askRaw % 1_000_000)
			v := Imbalance(bid, ask)
			return v >= -100 && v <= 100
		}
		if err := quick.Check(prop, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("slippage is non negative for monotonic books", func(t *testing.T) {
		prop := func(amountRaw uint16) bool {
			amount := 1 + float64(amountRaw%10_000)
			book := domain.OrderBookSnapshot{
				Bids: []domain.Level{{Price: 99.5, Size: 100}, {Price: 99, Size: 100}},
				Asks: []domain.Level{{Price: 100.5, Size: 100}, {Price: 101, Size: 100}},
			}
			buy := BuySlippageBP(book, amount)
			sell := SellSlippageBP(book, amount)
			return buy >= 0 && sell >= 0 && !math.IsNaN(buy) && !math.IsNaN(sell)
		}
		if err := quick.Check(prop, nil); err != nil {
			t.Fatal(err)
		}
	})
}
