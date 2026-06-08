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

const defaultEdgeXPerpV2WSURL = "wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws"

type EdgeXPerpV2BookProvider interface {
	Snapshot(contractID string) ([]domain.Level, []domain.Level, time.Time, error)
	SourceEndpoint() string
}

type EdgeXPerpV2WSProvider struct {
	url        string
	proxy      string
	staleAfter time.Duration

	mu            sync.RWMutex
	books         map[string]*edgeXPerpV2BookState
	lastMessageAt time.Time
}

type edgeXPerpV2BookState struct {
	bids        map[float64]float64
	asks        map[float64]float64
	lastVersion int64
	updatedAt   time.Time
	ready       bool
	lastErr     string
}

type edgeXPerpV2WSMessage struct {
	Type      string          `json:"type"`
	Channel   string          `json:"channel"`
	DataType  string          `json:"dataType"`
	Timestamp int64           `json:"timestamp"`
	TS        int64           `json:"ts"`
	Data      json.RawMessage `json:"data"`
}

type edgeXPerpV2WSDepth struct {
	ContractID   any                    `json:"contractId"`
	ContractName string                 `json:"contractName"`
	StartVersion any                    `json:"startVersion"`
	EndVersion   any                    `json:"endVersion"`
	Version      any                    `json:"version"`
	Bids         []edgeXPerpV2WSLevel   `json:"bids"`
	Asks         []edgeXPerpV2WSLevel   `json:"asks"`
	Extra        map[string]interface{} `json:"-"`
}

type edgeXPerpV2WSLevel struct {
	Price any `json:"price"`
	Size  any `json:"size"`
}

func NewEdgeXPerpV2WSProvider(url string, staleAfter time.Duration) *EdgeXPerpV2WSProvider {
	return NewEdgeXPerpV2WSProviderWithProxy(url, staleAfter, "")
}

func NewEdgeXPerpV2WSProviderWithProxy(url string, staleAfter time.Duration, proxy string) *EdgeXPerpV2WSProvider {
	if url == "" {
		url = defaultEdgeXPerpV2WSURL
	}
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	return &EdgeXPerpV2WSProvider{url: url, proxy: proxy, staleAfter: staleAfter, books: map[string]*edgeXPerpV2BookState{}}
}

func (p *EdgeXPerpV2WSProvider) SourceEndpoint() string {
	if p == nil || p.url == "" {
		return defaultEdgeXPerpV2WSURL
	}
	return p.url
}

func (p *EdgeXPerpV2WSProvider) Run(ctx context.Context, contractIDs []string) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := p.runOnce(ctx, contractIDs); err != nil && ctx.Err() == nil {
			log.Printf("edgeX perp v2 ws disconnected: %v", err)
			p.markAllError(contractIDs, err)
		}
		if err := sleepWithContext(ctx, backoff); err != nil {
			return
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (p *EdgeXPerpV2WSProvider) Snapshot(contractID string) ([]domain.Level, []domain.Level, time.Time, error) {
	contractID = strings.TrimSpace(contractID)
	p.mu.RLock()
	state := p.books[contractID]
	if state == nil {
		p.mu.RUnlock()
		return nil, nil, time.Time{}, fmt.Errorf("edgeX perp v2 contract %s ws book not ready", contractID)
	}
	if state.lastErr != "" {
		err := state.lastErr
		p.mu.RUnlock()
		return nil, nil, state.updatedAt, errors.New(err)
	}
	if !state.ready {
		p.mu.RUnlock()
		return nil, nil, time.Time{}, fmt.Errorf("edgeX perp v2 contract %s ws book not ready", contractID)
	}
	if time.Since(state.updatedAt) > p.staleAfter {
		updatedAt := state.updatedAt
		p.mu.RUnlock()
		return nil, nil, updatedAt, fmt.Errorf("edgeX perp v2 contract %s ws book stale since %s", contractID, updatedAt.Format(time.RFC3339))
	}
	bids := edgeXPerpV2LevelsFromBook(state.bids, true)
	asks := edgeXPerpV2LevelsFromBook(state.asks, false)
	updatedAt := state.updatedAt
	p.mu.RUnlock()
	if len(bids) == 0 || len(asks) == 0 {
		return nil, nil, updatedAt, fmt.Errorf("edgeX perp v2 contract %s ws book empty", contractID)
	}
	return bids, asks, updatedAt, nil
}

func (p *EdgeXPerpV2WSProvider) ReadyCount(contractIDs []string) int {
	count := 0
	for _, id := range contractIDs {
		if _, _, _, err := p.Snapshot(id); err == nil {
			count++
		}
	}
	return count
}

func (p *EdgeXPerpV2WSProvider) runOnce(ctx context.Context, contractIDs []string) error {
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
	p.markMessageReceived(time.Now().UTC())

	for _, id := range contractIDs {
		if err := conn.WriteJSON(map[string]any{"type": "subscribe", "channel": fmt.Sprintf("depth.%s.200", id)}); err != nil {
			return err
		}
	}

	pingTicker := time.NewTicker(60 * time.Second)
	defer pingTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pingTicker.C:
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			}
		}
	}()

	for ctx.Err() == nil {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		p.markMessageReceived(time.Now().UTC())
		if err := p.handleMessage(data); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (p *EdgeXPerpV2WSProvider) handleMessage(data []byte) error {
	var msg edgeXPerpV2WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}
	contractID, ok := edgeXPerpV2ContractIDFromChannel(msg.Channel)
	if !ok {
		return nil
	}
	depths := edgeXPerpV2DepthsFromRaw(msg.Data)
	if len(depths) == 0 {
		return nil
	}
	dataType := strings.ToLower(msg.DataType + " " + msg.Type)
	for _, depth := range depths {
		id := edgeXPerpV2ValueString(depth.ContractID)
		if id == "" {
			id = contractID
		}
		if strings.Contains(dataType, "snapshot") || strings.Contains(dataType, "subscribed") {
			p.applyEdgeXPerpV2Snapshot(id, depth, msg.Timestamp, msg.TS)
			continue
		}
		if err := p.applyEdgeXPerpV2Update(id, depth, msg.Timestamp, msg.TS); err != nil {
			return err
		}
	}
	return nil
}

func (p *EdgeXPerpV2WSProvider) applyEdgeXPerpV2Snapshot(contractID string, depth edgeXPerpV2WSDepth, ts int64, fallbackTS int64) {
	state := &edgeXPerpV2BookState{bids: map[float64]float64{}, asks: map[float64]float64{}, lastVersion: edgeXPerpV2DepthEndVersion(depth), updatedAt: edgeXPerpV2Timestamp(ts, fallbackTS), ready: true}
	applyEdgeXPerpV2Levels(state.bids, depth.Bids)
	applyEdgeXPerpV2Levels(state.asks, depth.Asks)
	p.mu.Lock()
	p.books[strings.TrimSpace(contractID)] = state
	p.mu.Unlock()
}

func (p *EdgeXPerpV2WSProvider) applyEdgeXPerpV2Update(contractID string, depth edgeXPerpV2WSDepth, ts int64, fallbackTS int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	contractID = strings.TrimSpace(contractID)
	state := p.books[contractID]
	if state == nil || !state.ready {
		return nil
	}
	startVersion := edgeXPerpV2DepthStartVersion(depth)
	endVersion := edgeXPerpV2DepthEndVersion(depth)
	if endVersion > 0 && state.lastVersion > 0 && endVersion <= state.lastVersion {
		return nil
	}
	if startVersion > 0 && state.lastVersion > 0 && startVersion > state.lastVersion+1 {
		state.ready = false
		state.lastErr = fmt.Sprintf("edgeX perp v2 contract %s version gap: start=%d previous=%d", contractID, startVersion, state.lastVersion)
		state.updatedAt = time.Now().UTC()
		return errors.New(state.lastErr)
	}
	applyEdgeXPerpV2Levels(state.bids, depth.Bids)
	applyEdgeXPerpV2Levels(state.asks, depth.Asks)
	if endVersion > 0 {
		state.lastVersion = endVersion
	}
	state.updatedAt = edgeXPerpV2Timestamp(ts, fallbackTS)
	state.lastErr = ""
	return nil
}

func (p *EdgeXPerpV2WSProvider) markAllError(contractIDs []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range contractIDs {
		state := p.books[id]
		if state == nil {
			state = &edgeXPerpV2BookState{bids: map[float64]float64{}, asks: map[float64]float64{}}
			p.books[id] = state
		}
		state.ready = false
		state.lastErr = err.Error()
		state.updatedAt = time.Now().UTC()
	}
}

func (p *EdgeXPerpV2WSProvider) markMessageReceived(at time.Time) {
	p.mu.Lock()
	p.lastMessageAt = at
	p.mu.Unlock()
}

func edgeXPerpV2ContractIDFromChannel(channel string) (string, bool) {
	parts := strings.Split(channel, ".")
	if len(parts) < 3 || parts[0] != "depth" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func edgeXPerpV2DepthsFromRaw(raw json.RawMessage) []edgeXPerpV2WSDepth {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var many []edgeXPerpV2WSDepth
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	var one edgeXPerpV2WSDepth
	if err := json.Unmarshal(raw, &one); err == nil {
		return []edgeXPerpV2WSDepth{one}
	}
	return nil
}

func applyEdgeXPerpV2Levels(book map[float64]float64, levels []edgeXPerpV2WSLevel) {
	for _, level := range levels {
		price, okPrice := edgeXPerpV2ValueFloat(level.Price)
		size, okSize := edgeXPerpV2ValueFloat(level.Size)
		if !okPrice || !okSize || price <= 0 {
			continue
		}
		if size <= 0 {
			delete(book, price)
			continue
		}
		book[price] = size
	}
}

func edgeXPerpV2LevelsFromBook(book map[float64]float64, desc bool) []domain.Level {
	prices := make([]float64, 0, len(book))
	for price, size := range book {
		if price > 0 && size > 0 {
			prices = append(prices, price)
		}
	}
	sort.Float64s(prices)
	if desc {
		for i, j := 0, len(prices)-1; i < j; i, j = i+1, j-1 {
			prices[i], prices[j] = prices[j], prices[i]
		}
	}
	out := make([]domain.Level, 0, len(prices))
	for _, price := range prices {
		out = append(out, domain.Level{Price: price, Size: book[price]})
	}
	return out
}

func edgeXPerpV2DepthStartVersion(depth edgeXPerpV2WSDepth) int64 {
	return edgeXPerpV2ValueInt64(depth.StartVersion)
}

func edgeXPerpV2DepthEndVersion(depth edgeXPerpV2WSDepth) int64 {
	if v := edgeXPerpV2ValueInt64(depth.EndVersion); v > 0 {
		return v
	}
	return edgeXPerpV2ValueInt64(depth.Version)
}

func edgeXPerpV2Timestamp(values ...int64) time.Time {
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if value > 1_000_000_000_000 {
			return time.UnixMilli(value).UTC()
		}
		return time.Unix(value, 0).UTC()
	}
	return time.Now().UTC()
}

func edgeXPerpV2ValueString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func edgeXPerpV2ValueInt64(value any) int64 {
	s := edgeXPerpV2ValueString(value)
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func edgeXPerpV2ValueFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	case float64:
		return v, true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
