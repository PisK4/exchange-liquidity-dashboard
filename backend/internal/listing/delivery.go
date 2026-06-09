package listing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DeliveryDeps wires the delivery worker dependencies. WebhookURL is
// resolved at engine start time from cfg.Runtime.ListingAgent.Delivery
// and is intentionally never persisted; the outbox row only stores
// target_channel.
type DeliveryDeps struct {
	WebhookURL    string
	WebhookSecret string
	// ResolveWebhook, when non-nil, is called per outbox row to pick
	// the destination webhook (and its secret) based on the row's
	// event_type. Engine wiring sets this so listing-announcement
	// rows go to cfg.Alert.Webhooks.Listing and liquidity rows go to
	// cfg.Alert.Webhooks.Liquidity. When nil, WebhookURL /
	// WebhookSecret are used for every row (legacy path used by
	// tests and the single-webhook deployment).
	ResolveWebhook func(eventType string) (url, secret string)
	Client         *http.Client
	Now            func() time.Time
	BatchSize      int
	// RetryBackoff returns the wait time for the n-th retry attempt
	// (1-based). When nil, an exponential default is used.
	RetryBackoff func(attempt int) time.Duration
	// DedupeKeyPrefix, when non-empty, restricts the drain to outbox
	// rows whose dedupe_key starts with this exact prefix (`LIKE
	// 'prefix%'`). Production callers leave it empty so the drain
	// covers every due row; smoke / preview scripts set it to a
	// runtime-unique marker so test rows can flow through the real
	// drain path without ever clobbering production traffic.
	DedupeKeyPrefix string
}

// DeliveryResult is the per-run summary of a DrainDueOutbox call.
type DeliveryResult struct {
	Sent     int
	Failed   int
	Retried  int
	Disabled int
}

// DrainDueOutbox iterates over due pending/retry outbox rows, posts
// the JSON payload to the Lark webhook, updates the row status, and
// records a t_listing_delivery_attempt audit row.
//
// Status transitions:
//   - WebhookURL empty               => disabled
//   - 2xx response                   => sent
//   - non-2xx + attempts < max       => retry (next_attempt_at scheduled)
//   - non-2xx + attempts >= max      => failed
//   - network error                  => same retry/failed split as non-2xx
func DrainDueOutbox(ctx context.Context, repo *Repository, deps DeliveryDeps) (DeliveryResult, error) {
	if repo == nil {
		return DeliveryResult{}, errors.New("listing delivery: repo is nil")
	}
	if deps.Client == nil {
		deps.Client = http.DefaultClient
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.BatchSize <= 0 {
		deps.BatchSize = 50
	}
	if deps.RetryBackoff == nil {
		deps.RetryBackoff = defaultRetryBackoff
	}
	now := deps.Now()
	rows, err := repo.loadDueOutbox(ctx, now, deps.BatchSize, deps.DedupeKeyPrefix)
	if err != nil {
		return DeliveryResult{}, fmt.Errorf("load due outbox: %w", err)
	}
	out := DeliveryResult{}
	for _, row := range rows {
		suppressed, err := suppressStaleDecisionOutbox(ctx, repo, row, now)
		if err != nil {
			return out, err
		}
		if suppressed {
			out.Disabled++
			continue
		}
		webhookURL, webhookSecret := resolveRowWebhook(deps, row.EventType)
		// Treat empty webhook URL as disabled regardless of stored status.
		if strings.TrimSpace(webhookURL) == "" {
			if err := repo.markOutboxDisabled(ctx, row.ID, now); err != nil {
				return out, fmt.Errorf("mark disabled %d: %w", row.ID, err)
			}
			out.Disabled++
			continue
		}
		attempt := row.AttemptCount + 1
		body, sendErr := postLarkWebhook(ctx, deps.Client, webhookURL, webhookSecret, row.PayloadJSON, now)
		status := OutboxStatusSent
		var httpStatus *int
		var errMsg string
		if sendErr != nil {
			status = OutboxStatusRetry
			errMsg = sendErr.Error()
			if hb, ok := sendErr.(*httpStatusError); ok {
				v := hb.code
				httpStatus = &v
			}
		} else {
			code := http.StatusOK
			httpStatus = &code
		}
		if status == OutboxStatusRetry && attempt >= row.MaxAttempts {
			status = OutboxStatusFailed
		}
		nextAttempt := now.Add(deps.RetryBackoff(attempt))
		if err := repo.updateOutboxAfterSend(ctx, row.ID, status, attempt, nextAttempt, errMsg, now, sendErr == nil); err != nil {
			return out, fmt.Errorf("update outbox %d: %w", row.ID, err)
		}
		if err := repo.recordAttempt(ctx, row.ID, attempt, status, httpStatus, errMsg, string(body), now); err != nil {
			return out, fmt.Errorf("record attempt %d: %w", row.ID, err)
		}
		switch status {
		case OutboxStatusSent:
			out.Sent++
		case OutboxStatusRetry:
			out.Retried++
		case OutboxStatusFailed:
			out.Failed++
		}
	}
	return out, nil
}

func suppressStaleDecisionOutbox(ctx context.Context, repo *Repository, row DeliveryOutbox, now time.Time) (bool, error) {
	if row.EventType != DeliveryEventListingDecisionCandidate {
		return false, nil
	}
	candidateID, ok := decisionCandidateIDFromOutbox(row)
	if !ok {
		return false, nil
	}
	candidate, err := repo.GetCandidate(ctx, candidateID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("load decision candidate %d for outbox %d: %w", candidateID, row.ID, err)
	}
	if candidate.LifecycleStatus != LifecycleAlreadyListed {
		return false, nil
	}
	if err := repo.markOutboxSuppressed(ctx, row.ID, "suppressed: candidate already listed on edgeX", now); err != nil {
		return false, fmt.Errorf("suppress stale decision outbox %d: %w", row.ID, err)
	}
	return true, nil
}

func decisionCandidateIDFromOutbox(row DeliveryOutbox) (int64, bool) {
	parts := strings.Split(row.DedupeKey, "|")
	if len(parts) >= 3 && parts[0] == "listing_decision" {
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil && id > 0 {
			return id, true
		}
	}
	var envelope struct {
		Card struct {
			Elements []struct {
				Tag     string `json:"tag"`
				Actions []struct {
					Value struct {
						CandidateID int64 `json:"candidate_id"`
					} `json:"value"`
				} `json:"actions"`
			} `json:"elements"`
		} `json:"card"`
	}
	if err := json.Unmarshal(row.PayloadJSON, &envelope); err != nil {
		return 0, false
	}
	for _, el := range envelope.Card.Elements {
		if el.Tag != "action" {
			continue
		}
		for _, action := range el.Actions {
			if action.Value.CandidateID > 0 {
				return action.Value.CandidateID, true
			}
		}
	}
	return 0, false
}

// resolveRowWebhook returns the webhook URL + secret to use for the
// given outbox row's event_type. When deps.ResolveWebhook is wired
// it owns the routing; otherwise the legacy single-webhook fields
// (WebhookURL / WebhookSecret) are returned so existing call sites
// (tests, smoke scripts) keep working unchanged.
func resolveRowWebhook(deps DeliveryDeps, eventType string) (string, string) {
	if deps.ResolveWebhook != nil {
		if url, secret := deps.ResolveWebhook(eventType); strings.TrimSpace(url) != "" {
			return url, secret
		}
		return "", ""
	}
	return deps.WebhookURL, deps.WebhookSecret
}

// LarkSign returns the base64-encoded HMAC-SHA256 signature Lark /
// Feishu expects when a webhook secret is configured. The signed
// payload is `{timestamp}\n{secret}` per Lark's documentation.
func LarkSign(secret string, ts time.Time) string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", ts.Unix(), secret)))
	mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func defaultRetryBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 30 * time.Second
	case attempt == 2:
		return 2 * time.Minute
	case attempt == 3:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}

type httpStatusError struct {
	code int
	body string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("webhook non-2xx: %d %s", e.code, e.body)
}

func postLarkWebhook(ctx context.Context, client *http.Client, url, secret string, payload []byte, now time.Time) ([]byte, error) {
	// Merge sign + timestamp + content into a single object so Lark
	// can verify the signature.
	envelope := map[string]any{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &envelope)
	}
	if secret != "" {
		envelope["timestamp"] = now.Unix()
		envelope["sign"] = LarkSign(secret, now)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal lark body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, &httpStatusError{code: resp.StatusCode, body: string(respBody)}
	}
	return respBody, nil
}

// loadDueOutbox returns up to `limit` outbox rows whose status is
// pending or retry and whose next_attempt_at is due. Disabled rows
// are intentionally returned too so a webhook URL re-introduction
// (operator flips the flag back on) can re-evaluate them.
//
// When dedupeKeyPrefix is non-empty the query is further restricted
// to dedupe_key LIKE 'prefix%'. Production callers pass "" (no extra
// filter) and behaviour is identical to before; smoke / preview
// scripts pass a runtime-unique marker so test rows flow through the
// real drain path in isolation from live traffic.
func (r *Repository) loadDueOutbox(ctx context.Context, now time.Time, limit int, dedupeKeyPrefix string) ([]DeliveryOutbox, error) {
	if r.db == nil {
		return nil, errors.New("listing delivery: no db attached")
	}
	args := []any{OutboxStatusPending, OutboxStatusRetry, now}
	query := `SELECT id, event_type, dedupe_key, target_channel, status, attempt_count, max_attempts,
		         next_attempt_at, payload_json, last_error, sent_at, created_at, updated_at
		    FROM t_listing_delivery_outbox
		   WHERE status IN (?, ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`
	if strings.TrimSpace(dedupeKeyPrefix) != "" {
		query += ` AND dedupe_key LIKE ?`
		args = append(args, dedupeKeyPrefix+"%")
	}
	query += ` ORDER BY next_attempt_at ASC
		   LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryOutbox
	for rows.Next() {
		o, err := scanOutboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Repository) markOutboxDisabled(ctx context.Context, id int64, now time.Time) error {
	return r.markOutboxSuppressed(ctx, id, "webhook url not configured", now)
}

func (r *Repository) markOutboxSuppressed(ctx context.Context, id int64, reason string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_listing_delivery_outbox SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		OutboxStatusDisabled, reason, now, id,
	)
	return err
}

func (r *Repository) updateOutboxAfterSend(ctx context.Context, id int64, status string, attempt int, nextAttempt time.Time, lastErr string, now time.Time, sent bool) error {
	var sentAt sql.NullTime
	if sent {
		sentAt = sql.NullTime{Time: now, Valid: true}
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_listing_delivery_outbox
		    SET status = ?, attempt_count = ?, next_attempt_at = ?, last_error = ?,
		        sent_at = ?, updated_at = ?
		  WHERE id = ?`,
		status, attempt, nextAttempt, nullString(lastErr), sentAt, now, id,
	)
	return err
}

// recordAttempt writes one audit row per (outbox_id, attempt_no).
// The ON DUPLICATE KEY UPDATE clause keeps the worker idempotent
// across operator interventions: when an outbox row's attempt_count
// is manually reset (e.g. to re-drive a stuck row through DrainDueOutbox)
// the next delivery attempt may reuse an attempt_no that already
// exists in t_listing_delivery_attempt. Without the upsert, the
// duplicate-key error bubbles up from DrainDueOutbox before
// out.Sent/Failed/Retried is incremented, so the operator sees a
// misleading delivery summary even though the outbox row itself was
// updated correctly. Upserting the latest status / http_status /
// error / response_body / attempted_at onto the existing audit row
// matches operator intent ("this is the latest attempt") and keeps
// the unique key intact for production traffic where attempt_no is
// monotonic and the conflict path is never taken.
func (r *Repository) recordAttempt(ctx context.Context, outboxID int64, attempt int, status string, httpStatus *int, errMsg, responseBody string, attemptedAt time.Time) error {
	var http any
	if httpStatus != nil {
		http = *httpStatus
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_listing_delivery_attempt
		   (outbox_id, attempt_no, status, http_status, error_message, attempted_at, response_body, latency_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   status = VALUES(status),
		   http_status = VALUES(http_status),
		   error_message = VALUES(error_message),
		   attempted_at = VALUES(attempted_at),
		   response_body = VALUES(response_body),
		   latency_ms = VALUES(latency_ms)`,
		outboxID, attempt, status, http, nullString(errMsg), attemptedAt, nullString(responseBody), 0,
	)
	return err
}
