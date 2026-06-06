package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"edgex-ops-intelligence/backend/internal/domain"
)

const defaultLighterWSURL = "wss://mainnet.zklighter.elliot.ai/stream?readonly=true"

type LighterBookProvider interface {
	Snapshot(marketID int) ([]domain.Level, []domain.Level, time.Time, error)
}

type LighterWSProvider struct {
	url        string
	proxy      string
	staleAfter time.Duration

	mu    sync.RWMutex
	books map[int]*lighterBookState
}

type lighterBookState struct {
	bids      map[float64]float64
	asks      map[float64]float64
	lastNonce int64
	updatedAt time.Time
	ready     bool
	lastErr   string
}

type lighterWSMessage struct {
	Type          string             `json:"type"`
	Channel       string             `json:"channel"`
	Timestamp     int64              `json:"timestamp"`
	LastUpdatedAt int64              `json:"last_updated_at"`
	OrderBook     lighterWSOrderBook `json:"order_book"`
}

type lighterWSOrderBook struct {
	Code          int              `json:"code"`
	Asks          []lighterWSLevel `json:"asks"`
	Bids          []lighterWSLevel `json:"bids"`
	Nonce         int64            `json:"nonce"`
	BeginNonce    int64            `json:"begin_nonce"`
	LastUpdatedAt int64            `json:"last_updated_at"`
}

type lighterWSLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

func NewLighterWSProvider(url string, staleAfter time.Duration) *LighterWSProvider {
	return NewLighterWSProviderWithProxy(url, staleAfter, "")
}

// NewLighterWSProviderWithProxy is the proxy-aware constructor. proxy may
// be empty (direct connection) or an http(s)/socks5 URL pointing at a
// forward proxy that can reach the Lighter WS endpoint. Same rationale as
// adapter.NewWithLighterAndProxy: required when the container runtime
// itself cannot dial Lighter directly.
func NewLighterWSProviderWithProxy(url string, staleAfter time.Duration, proxy string) *LighterWSProvider {
	if url == "" {
		url = defaultLighterWSURL
	}
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	return &LighterWSProvider{url: url, proxy: proxy, staleAfter: staleAfter, books: map[int]*lighterBookState{}}
}

func (p *LighterWSProvider) Run(ctx context.Context, marketIDs []int) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := p.runOnce(ctx, marketIDs); err != nil && ctx.Err() == nil {
			log.Printf("lighter ws disconnected: %v", err)
			p.markAllError(marketIDs, err)
		}
		if err := sleepWithContext(ctx, backoff); err != nil {
			return
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (p *LighterWSProvider) Snapshot(marketID int) ([]domain.Level, []domain.Level, time.Time, error) {
	p.mu.RLock()
	state := p.books[marketID]
	if state == nil || !state.ready {
		p.mu.RUnlock()
		return nil, nil, time.Time{}, fmt.Errorf("lighter market %d ws book not ready", marketID)
	}
	if state.lastErr != "" {
		err := state.lastErr
		p.mu.RUnlock()
		return nil, nil, state.updatedAt, errors.New(err)
	}
	if time.Since(state.updatedAt) > p.staleAfter {
		updatedAt := state.updatedAt
		p.mu.RUnlock()
		return nil, nil, updatedAt, fmt.Errorf("lighter market %d ws book stale since %s", marketID, updatedAt.Format(time.RFC3339))
	}
	bids := levelsFromBook(state.bids, true)
	asks := levelsFromBook(state.asks, false)
	updatedAt := state.updatedAt
	p.mu.RUnlock()
	if len(bids) == 0 || len(asks) == 0 {
		return nil, nil, updatedAt, fmt.Errorf("lighter market %d ws book empty", marketID)
	}
	return bids, asks, updatedAt, nil
}

func (p *LighterWSProvider) ReadyCount(marketIDs []int) int {
	count := 0
	for _, id := range marketIDs {
		if _, _, _, err := p.Snapshot(id); err == nil {
			count++
		}
	}
	return count
}

func (p *LighterWSProvider) runOnce(ctx context.Context, marketIDs []int) error {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	if p.proxy != "" {
		if parsed, err := url.Parse(p.proxy); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			dialer.Proxy = http.ProxyURL(parsed)
		}
	}
	conn, _, err := dialer.DialContext(ctx, p.url, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	for _, id := range marketIDs {
		if err := conn.WriteJSON(map[string]any{"type": "subscribe", "channel": fmt.Sprintf("order_book/%d", id)}); err != nil {
			return err
		}
	}

	pongTicker := time.NewTicker(60 * time.Second)
	defer pongTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pongTicker.C:
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			}
		}
	}()

	for ctx.Err() == nil {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg lighterWSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		marketID, ok := lighterMarketIDFromChannel(msg.Channel)
		if !ok {
			continue
		}
		switch msg.Type {
		case "subscribed/order_book":
			p.applyLighterSnapshot(marketID, msg.OrderBook, msg.Timestamp, msg.LastUpdatedAt)
		case "update/order_book":
			if err := p.applyLighterUpdate(marketID, msg.OrderBook, msg.Timestamp); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (p *LighterWSProvider) applyLighterSnapshot(marketID int, ob lighterWSOrderBook, ts int64, outerUpdatedAt int64) {
	state := &lighterBookState{bids: map[float64]float64{}, asks: map[float64]float64{}, lastNonce: ob.Nonce, updatedAt: lighterTimestamp(ts, ob.LastUpdatedAt, outerUpdatedAt), ready: true}
	applyLighterLevels(state.asks, ob.Asks)
	applyLighterLevels(state.bids, ob.Bids)
	p.mu.Lock()
	p.books[marketID] = state
	p.mu.Unlock()
}

func (p *LighterWSProvider) applyLighterUpdate(marketID int, ob lighterWSOrderBook, ts int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.books[marketID]
	if state == nil || !state.ready {
		return nil
	}
	if state.lastNonce > 0 && ob.BeginNonce != state.lastNonce {
		state.ready = false
		state.lastErr = fmt.Sprintf("lighter market %d nonce gap: begin=%d previous=%d", marketID, ob.BeginNonce, state.lastNonce)
		return errors.New(state.lastErr)
	}
	applyLighterLevels(state.asks, ob.Asks)
	applyLighterLevels(state.bids, ob.Bids)
	state.lastNonce = ob.Nonce
	state.updatedAt = lighterTimestamp(ts, ob.LastUpdatedAt, 0)
	state.lastErr = ""
	return nil
}

func (p *LighterWSProvider) markAllError(marketIDs []int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range marketIDs {
		state := p.books[id]
		if state == nil {
			state = &lighterBookState{bids: map[float64]float64{}, asks: map[float64]float64{}}
			p.books[id] = state
		}
		state.ready = false
		state.lastErr = err.Error()
		state.updatedAt = time.Now().UTC()
	}
}

func applyLighterLevels(book map[float64]float64, rows []lighterWSLevel) {
	for _, row := range rows {
		price, _ := strconv.ParseFloat(row.Price, 64)
		size, _ := strconv.ParseFloat(row.Size, 64)
		if price <= 0 {
			continue
		}
		if size <= 0 {
			delete(book, price)
			continue
		}
		book[price] = size
	}
}

func levelsFromBook(book map[float64]float64, descending bool) []domain.Level {
	levels := make([]domain.Level, 0, len(book))
	for price, size := range book {
		if price > 0 && size > 0 {
			levels = append(levels, domain.Level{Price: price, Size: size})
		}
	}
	sort.Slice(levels, func(i, j int) bool {
		if descending {
			return levels[i].Price > levels[j].Price
		}
		return levels[i].Price < levels[j].Price
	})
	return levels
}

func lighterTimestamp(values ...int64) time.Time {
	for _, value := range values {
		if value <= 0 {
			continue
		}
		switch {
		case value > 1e15:
			return time.UnixMicro(value).UTC()
		case value > 1e12:
			return time.UnixMilli(value).UTC()
		default:
			return time.Unix(value, 0).UTC()
		}
	}
	return time.Now().UTC()
}

func lighterMarketIDFromChannel(channel string) (int, bool) {
	_, suffix, ok := strings.Cut(channel, ":")
	if !ok {
		return 0, false
	}
	id, err := strconv.Atoi(suffix)
	return id, err == nil
}

func lighterMarketID(sub domain.SymbolSub) (int, error) {
	if sub.MarketID != nil {
		return *sub.MarketID, nil
	}
	return 0, fmt.Errorf("lighter %s: market_id missing from catalog (run `make catalog`)", sub.Canonical)
}

func LighterMarketIDs() []int {
	return []int{1, 0, 2}
}
