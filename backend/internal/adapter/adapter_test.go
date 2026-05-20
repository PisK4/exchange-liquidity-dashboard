package adapter

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/domain"
)

func TestClassifyDepthMarksSparseBookPartialWhenTwoPercentNotCovered(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Bids:        make([]domain.Level, 20),
		Asks:        make([]domain.Level, 20),
		APILevelCap: 80,
	}
	for i := 0; i < 20; i++ {
		book.Bids[i] = domain.Level{Price: 100 - float64(i)*0.01, Size: 1}
		book.Asks[i] = domain.Level{Price: 100.1 + float64(i)*0.01, Size: 1}
	}
	book.LevelsReturned = len(book.Bids) + len(book.Asks)

	status, reason := classifyDepth(book)
	if status != domain.StatusPartial || reason != domain.ReasonSparseBook {
		t.Fatalf("expected partial sparse_book for shallow book, got status=%s reason=%s farthest=%f", status, reason, farthestDistancePct(book))
	}
}

func TestClassifyDepthMarksCompleteWhenTwoPercentIsCovered(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Bids:        []domain.Level{{Price: 99.9, Size: 1}, {Price: 97.9, Size: 1}},
		Asks:        []domain.Level{{Price: 100.1, Size: 1}, {Price: 102.1, Size: 1}},
		APILevelCap: 4,
	}
	book.LevelsReturned = len(book.Bids) + len(book.Asks)

	status, reason := classifyDepth(book)
	if status != domain.StatusComplete || reason != "" {
		t.Fatalf("expected complete when farthest level covers 2%%, got status=%s reason=%s", status, reason)
	}
}

func TestEdgeXContractIDReturnsCatalogValue(t *testing.T) {
	sub := domain.SymbolSub{Canonical: "BTC", ContractID: "10000001"}
	got, err := edgeXContractID(sub)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "10000001" {
		t.Fatalf("got %q, want 10000001", got)
	}
	if _, err := edgeXContractID(domain.SymbolSub{Canonical: "DOGE"}); err == nil {
		t.Fatal("expected error when ContractID empty")
	}
}

func TestParseEdgeXLevels(t *testing.T) {
	raw := []struct {
		Price string `json:"price"`
		Size  string `json:"size"`
	}{
		{Price: "100.1", Size: "2.5"},
		{Price: "0", Size: "1"},
		{Price: "101.2", Size: ""},
	}
	levels := parseEdgeXLevels(raw)
	if len(levels) != 1 || levels[0].Price != 100.1 || levels[0].Size != 2.5 {
		t.Fatalf("unexpected levels: %+v", levels)
	}
}

func TestShouldRetryTransientFailures(t *testing.T) {
	if !shouldRetry(0, errors.New("timeout")) {
		t.Fatal("expected transport errors to be retried")
	}
	if !shouldRetry(http.StatusTooManyRequests, nil) {
		t.Fatal("expected rate-limit responses to be retried")
	}
	if !shouldRetry(http.StatusBadGateway, nil) {
		t.Fatal("expected 5xx responses to be retried")
	}
	if shouldRetry(http.StatusForbidden, nil) {
		t.Fatal("expected 403 responses not to be retried")
	}
}

func TestLighterMarketIDReturnsCatalogValue(t *testing.T) {
	zero := 0
	one := 1
	if got, _ := lighterMarketID(domain.SymbolSub{MarketID: &zero}); got != 0 {
		t.Fatalf("expected market_id=0, got %d", got)
	}
	if got, _ := lighterMarketID(domain.SymbolSub{MarketID: &one}); got != 1 {
		t.Fatalf("expected market_id=1, got %d", got)
	}
	if _, err := lighterMarketID(domain.SymbolSub{Canonical: "DOGE"}); err == nil {
		t.Fatal("expected error when MarketID nil")
	}
}

func TestLighterSnapshotUpdateAndDelete(t *testing.T) {
	provider := NewLighterWSProvider("", time.Minute)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().UnixMilli(), 0)
	if err := provider.applyLighterUpdate(1, lighterWSOrderBook{
		Asks:       []lighterWSLevel{{Price: "101", Size: "0"}, {Price: "102", Size: "4"}},
		Bids:       []lighterWSLevel{{Price: "98", Size: "5"}},
		BeginNonce: 10,
		Nonce:      11,
	}, time.Now().UnixMilli()); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	bids, asks, _, err := provider.Snapshot(1)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(asks) != 1 || asks[0].Price != 102 || asks[0].Size != 4 {
		t.Fatalf("unexpected asks: %+v", asks)
	}
	if len(bids) != 2 || bids[0].Price != 99 || bids[1].Price != 98 {
		t.Fatalf("unexpected bids: %+v", bids)
	}
}

func TestLighterNonceGapMarksBookUnavailable(t *testing.T) {
	provider := NewLighterWSProvider("", time.Minute)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().UnixMilli(), 0)
	if err := provider.applyLighterUpdate(1, lighterWSOrderBook{BeginNonce: 9, Nonce: 11}, time.Now().UnixMilli()); err == nil {
		t.Fatal("expected nonce gap error")
	}
	if _, _, _, err := provider.Snapshot(1); err == nil {
		t.Fatal("expected snapshot to fail after nonce gap")
	}
}
