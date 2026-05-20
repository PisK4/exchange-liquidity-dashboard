package indicators

import (
	"math"
	"testing"

	"edgex-dashboard/backend/internal/domain"
)

func TestDepthAtTierAndSpread(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Bids: []domain.Level{{Price: 99, Size: 10}, {Price: 98, Size: 20}},
		Asks: []domain.Level{{Price: 101, Size: 10}, {Price: 102, Size: 20}},
	}
	depth := DepthAtTier(book, 0.02)
	if depth.BidUSD != 2950 || depth.AskUSD != 3050 || depth.TotalUSD != 6000 {
		t.Fatalf("unexpected depth: %+v", depth)
	}
	if got := SpreadBP(99, 101); math.Abs(got-200) > 0.0001 {
		t.Fatalf("spread = %f", got)
	}
}

func TestSlippage(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Bids: []domain.Level{{Price: 100, Size: 1}, {Price: 99, Size: 2}},
		Asks: []domain.Level{{Price: 101, Size: 1}, {Price: 102, Size: 2}},
	}
	if got := BuySlippageBP(book, 202); got <= 0 {
		t.Fatalf("expected positive buy slippage, got %f", got)
	}
	if got := SellSlippageBP(book, 200); got <= 0 {
		t.Fatalf("expected positive sell slippage, got %f", got)
	}
}

func TestAdjustedVolumeDiscountsOnlyVolume(t *testing.T) {
	if got := AdjustedVolume("mexc", 100); got != 40 {
		t.Fatalf("mexc adjusted volume = %f", got)
	}
	if got := AdjustedVolume("gate", 100); got != 50 {
		t.Fatalf("gate adjusted volume = %f", got)
	}
	if got := AdjustedVolume("binance", 100); got != 100 {
		t.Fatalf("binance adjusted volume = %f", got)
	}
}
