package indicators

import (
	"math"
	"testing"

	"edgex-ops-intelligence/backend/internal/domain"
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
		Bids: []domain.Level{{Price: 99, Size: 1}, {Price: 98, Size: 2}},
		Asks: []domain.Level{{Price: 101, Size: 1}, {Price: 102, Size: 2}},
	}
	if got := BuySlippageBP(book, 101); math.Abs(got-100) > 0.0001 {
		t.Fatalf("expected buy slippage relative to mid, got %f", got)
	}
	if got := SellSlippageBP(book, 99); math.Abs(got-100) > 0.0001 {
		t.Fatalf("expected sell slippage relative to mid, got %f", got)
	}
}

func TestValidSpreadRejectsCrossedBook(t *testing.T) {
	if _, ok := ValidSpreadBP(101, 99); ok {
		t.Fatalf("crossed book must not be represented as a valid zero-spread book")
	}
	if got, ok := ValidSpreadBP(99, 101); !ok || math.Abs(got-200) > 0.0001 {
		t.Fatalf("valid spread = %f ok=%v", got, ok)
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
