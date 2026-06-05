package fetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"time"
)

type Request struct {
	URL         string
	Platform    string
	SourceGroup string
	FetchMode   string
	Headers     map[string]string
}

type Result struct {
	Platform    string
	SourceGroup string
	SourceURL   string
	FetchMode   string
	Payload     []byte
	PayloadHash string
	ContentHash string
	HTTPStatus  int
	ContentType string
	FetchedAt   time.Time
}

type HTTPFetcher struct {
	client  *http.Client
	timeout time.Duration
	now     func() time.Time
}

func NewHTTPFetcher(client *http.Client, timeout time.Duration) *HTTPFetcher {
	if client == nil {
		client = http.DefaultClient
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPFetcher{client: client, timeout: timeout, now: time.Now}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, req Request) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 ActivityRadar/1.0")
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, err
	}
	hash := sha256Hex(body)
	return Result{
		Platform:    req.Platform,
		SourceGroup: req.SourceGroup,
		SourceURL:   req.URL,
		FetchMode:   req.FetchMode,
		Payload:     body,
		PayloadHash: hash,
		ContentHash: hash,
		HTTPStatus:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		FetchedAt:   f.now().UTC(),
	}, nil
}

type Profile struct {
	Name        string
	ClientHint  string
	Description string
}

func ProfileByName(name string) (Profile, bool) {
	switch name {
	case "chrome120":
		return Profile{Name: "chrome120", ClientHint: "chrome", Description: "Chrome 120-compatible TLS/client hint profile"}, true
	case "safari17_0":
		return Profile{Name: "safari17_0", ClientHint: "safari", Description: "Safari 17.0-compatible TLS/client hint profile"}, true
	default:
		return Profile{}, false
	}
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
