package listing

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildDepthFetcherNilFetcherErrors(t *testing.T) {
	d := BuildDepthFetcher(nil, 0, 0)
	_, _, err := d(context.Background(), "BTC", []string{"binance"})
	if err == nil {
		t.Errorf("err = nil, want non-nil for nil fetcher")
	}
}

func TestBuildDepthFetcherNoSourcesReturnsNothing(t *testing.T) {
	d := BuildDepthFetcher(func(context.Context, string, string, DepthMarketKind) (float64, string, error) {
		return 0, "", ErrDepthUnavailable
	}, 0, 0)
	spot, perp, err := d(context.Background(), "BTC", nil)
	if err != nil || spot != nil || perp != nil {
		t.Errorf("expected (nil,nil,nil), got (%v,%v,%v)", spot, perp, err)
	}
}

func TestBuildDepthFetcherPicksLargestSpotAndPerp(t *testing.T) {
	stub := func(ctx context.Context, platform, canonical string, kind DepthMarketKind) (float64, string, error) {
		switch {
		case platform == "binance" && kind == DepthKindSpot:
			return 100_000, "2pct", nil
		case platform == "binance" && kind == DepthKindPerp:
			return 1_200_000, "2pct", nil
		case platform == "bybit" && kind == DepthKindSpot:
			return 200_000, "2pct", nil
		case platform == "bybit" && kind == DepthKindPerp:
			return 800_000, "2pct", nil
		}
		return 0, "", ErrDepthUnavailable
	}
	d := BuildDepthFetcher(stub, 0, 0)
	spot, perp, err := d(context.Background(), "ABC", []string{"binance", "bybit"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spot == nil || spot.Platform != "bybit" || spot.USDValue != 200_000 {
		t.Errorf("spot = %+v, want bybit/200k", spot)
	}
	if perp == nil || perp.Platform != "binance" || perp.USDValue != 1_200_000 {
		t.Errorf("perp = %+v, want binance/1.2M", perp)
	}
}

func TestBuildDepthFetcherSkipsUnavailableSilently(t *testing.T) {
	stub := func(ctx context.Context, platform, canonical string, kind DepthMarketKind) (float64, string, error) {
		if platform == "binance" && kind == DepthKindPerp {
			return 500_000, "2pct", nil
		}
		return 0, "", ErrDepthUnavailable
	}
	d := BuildDepthFetcher(stub, 0, 0)
	spot, perp, err := d(context.Background(), "X", []string{"binance", "bitget"})
	if err != nil {
		t.Errorf("err = %v, want nil (ErrDepthUnavailable should be silent)", err)
	}
	if spot != nil {
		t.Errorf("spot = %+v, want nil", spot)
	}
	if perp == nil || perp.USDValue != 500_000 {
		t.Errorf("perp = %+v", perp)
	}
}

func TestBuildDepthFetcherRecordsNonSentinelErrors(t *testing.T) {
	stub := func(ctx context.Context, platform, canonical string, kind DepthMarketKind) (float64, string, error) {
		if platform == "binance" && kind == DepthKindPerp {
			return 500_000, "2pct", nil
		}
		return 0, "", errors.New("upstream 500")
	}
	d := BuildDepthFetcher(stub, 0, 0)
	_, perp, err := d(context.Background(), "X", []string{"binance"})
	if perp == nil || perp.USDValue != 500_000 {
		t.Errorf("perp = %+v", perp)
	}
	if err == nil || !strings.Contains(err.Error(), "upstream 500") {
		t.Errorf("err = %v, want upstream 500 mention", err)
	}
}

func TestBuildDepthFetcherZeroValueIgnored(t *testing.T) {
	stub := func(ctx context.Context, platform, canonical string, kind DepthMarketKind) (float64, string, error) {
		return 0, "", nil
	}
	d := BuildDepthFetcher(stub, 0, 0)
	spot, perp, err := d(context.Background(), "X", []string{"binance"})
	if spot != nil || perp != nil {
		t.Errorf("zero-value returns must be ignored")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestBuildDepthFetcherPerCallTimeout(t *testing.T) {
	var callsAborted int32
	stub := func(ctx context.Context, platform, canonical string, kind DepthMarketKind) (float64, string, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return 100, "2pct", nil
		case <-ctx.Done():
			atomic.AddInt32(&callsAborted, 1)
			return 0, "", ctx.Err()
		}
	}
	d := BuildDepthFetcher(stub, 0, 30*time.Millisecond)
	_, _, err := d(context.Background(), "X", []string{"binance"})
	if err == nil {
		t.Errorf("expected timeout error")
	}
	if atomic.LoadInt32(&callsAborted) < 1 {
		t.Errorf("expected at least 1 aborted call, got %d", callsAborted)
	}
}
