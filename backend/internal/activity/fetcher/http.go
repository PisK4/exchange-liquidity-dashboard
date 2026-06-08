package fetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
)

type Request struct {
	URL         string
	Platform    string
	SourceGroup string
	FetchMode   string
	Headers     map[string]string
}

type Result struct {
	Platform     string
	SourceGroup  string
	SourceURL    string
	FetchMode    string
	Payload      []byte
	PayloadHash  string
	ContentHash  string
	HTTPStatus   int
	ContentType  string
	FetchedAt    time.Time
	ElapsedMS    int64
	AttemptCount int
	ProxyUsed    bool
}

type HTTPFetcher struct {
	client       *http.Client
	timeout      time.Duration
	now          func() time.Time
	maxAttempts  int
	retryBackoff func(attempt int) time.Duration
	proxyUsed    bool
}

type Option func(*HTTPFetcher)

func WithMaxAttempts(maxAttempts int) Option {
	return func(f *HTTPFetcher) {
		if maxAttempts > 0 {
			f.maxAttempts = maxAttempts
		}
	}
}

func WithRetryBackoff(backoff func(attempt int) time.Duration) Option {
	return func(f *HTTPFetcher) {
		if backoff != nil {
			f.retryBackoff = backoff
		}
	}
}

func WithProxyUsed(proxyUsed bool) Option {
	return func(f *HTTPFetcher) {
		f.proxyUsed = proxyUsed
	}
}

func NewHTTPFetcher(client *http.Client, timeout time.Duration, opts ...Option) *HTTPFetcher {
	if client == nil {
		client = http.DefaultClient
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	f := &HTTPFetcher{
		client:       client,
		timeout:      timeout,
		now:          time.Now,
		maxAttempts:  3,
		retryBackoff: defaultRetryBackoff,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

func (f *HTTPFetcher) Fetch(ctx context.Context, req Request) (Result, error) {
	startedAt := time.Now()
	maxAttempts := f.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		got, err := f.fetchOnce(ctx, req)
		elapsedMS := time.Since(startedAt).Milliseconds()
		if err == nil {
			got.ElapsedMS = elapsedMS
			got.AttemptCount = attempt
			got.ProxyUsed = f.proxyUsed
			if shouldRetryHTTPStatus(got.HTTPStatus) && attempt < maxAttempts {
				lastErr = errors.New(http.StatusText(got.HTTPStatus))
				if waitErr := f.waitBeforeRetry(ctx, attempt); waitErr != nil {
					return Result{}, f.wrapError(req, waitErr, elapsedMS, attempt)
				}
				continue
			}
			return got, nil
		}
		lastErr = err
		if !shouldRetryError(err) || attempt >= maxAttempts {
			return Result{}, f.wrapError(req, err, elapsedMS, attempt)
		}
		if waitErr := f.waitBeforeRetry(ctx, attempt); waitErr != nil {
			return Result{}, f.wrapError(req, waitErr, elapsedMS, attempt)
		}
	}
	return Result{}, f.wrapError(req, lastErr, time.Since(startedAt).Milliseconds(), maxAttempts)
}

func (f *HTTPFetcher) fetchOnce(ctx context.Context, req Request) (Result, error) {
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

func (f *HTTPFetcher) waitBeforeRetry(ctx context.Context, attempt int) error {
	backoff := f.retryBackoff
	if backoff == nil {
		backoff = defaultRetryBackoff
	}
	delay := backoff(attempt)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (f *HTTPFetcher) wrapError(req Request, err error, elapsedMS int64, attemptCount int) error {
	return &activity.FetchError{
		Err: err,
		Metadata: activity.FetchMetadata{
			SourceURL:        req.URL,
			FetchMode:        req.FetchMode,
			ElapsedMS:        elapsedMS,
			AttemptCount:     attemptCount,
			ProxyUsed:        f.proxyUsed,
			LastErrorMessage: errorString(err),
		},
	}
}

func shouldRetryHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func shouldRetryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "temporary") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "tls handshake timeout")
}

func defaultRetryBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	return time.Duration(attempt) * 200 * time.Millisecond
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
