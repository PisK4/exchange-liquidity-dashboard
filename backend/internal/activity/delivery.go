package activity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type DeliveryStore interface {
	LoadDueActivityOutbox(ctx context.Context, now time.Time, limit int) ([]DeliveryOutbox, error)
	MarkActivityOutboxDisabledNoWebhook(ctx context.Context, id int64, now time.Time) error
	UpdateActivityOutboxAfterSend(ctx context.Context, id int64, status string, attempt int, nextAttempt time.Time, lastErr string, now time.Time, sent bool) error
	RecordActivityDeliveryAttempt(ctx context.Context, outboxID int64, attempt int, status string, httpStatus *int, errMsg, responseBody string, attemptedAt time.Time) error
}

type DeliveryDeps struct {
	WebhookURL          string
	WebhookURLByChannel map[string]string
	Client              *http.Client
	Now                 func() time.Time
	BatchSize           int
	RetryBackoff        func(attempt int) time.Duration
	SendSpacing         time.Duration
}

type DeliveryResult struct {
	Sent     int
	Failed   int
	Retried  int
	Disabled int
}

func DrainDueOutbox(ctx context.Context, store DeliveryStore, deps DeliveryDeps) (DeliveryResult, error) {
	if store == nil {
		return DeliveryResult{}, errors.New("activity delivery: store is nil")
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
	now := deps.Now().UTC()
	rows, err := store.LoadDueActivityOutbox(ctx, now, deps.BatchSize)
	if err != nil {
		return DeliveryResult{}, err
	}
	res := DeliveryResult{}
	for _, row := range rows {
		webhookURL := webhookURLForOutboxRow(deps, row)
		if strings.TrimSpace(webhookURL) == "" {
			if err := store.MarkActivityOutboxDisabledNoWebhook(ctx, row.ID, now); err != nil {
				return res, err
			}
			res.Disabled++
			continue
		}
		attempt := row.AttemptCount + 1
		body, sendErr := postActivityLarkWebhook(ctx, deps.Client, webhookURL, row.PayloadJSON)
		status := DeliveryStatusSent
		var httpStatus *int
		errMsg := ""
		if sendErr != nil {
			status = DeliveryStatusRetry
			errMsg = sendErr.Error()
			if hb, ok := sendErr.(*activityHTTPStatusError); ok {
				v := hb.code
				httpStatus = &v
			}
		} else {
			v := http.StatusOK
			httpStatus = &v
		}
		maxAttempts := row.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}
		if status == DeliveryStatusRetry && attempt >= maxAttempts {
			status = DeliveryStatusFailed
		}
		nextAttempt := now.Add(deps.RetryBackoff(attempt))
		if err := store.UpdateActivityOutboxAfterSend(ctx, row.ID, status, attempt, nextAttempt, errMsg, now, sendErr == nil); err != nil {
			return res, err
		}
		if err := store.RecordActivityDeliveryAttempt(ctx, row.ID, attempt, status, httpStatus, errMsg, string(body), now); err != nil {
			return res, err
		}
		switch status {
		case DeliveryStatusSent:
			res.Sent++
		case DeliveryStatusRetry:
			res.Retried++
		case DeliveryStatusFailed:
			res.Failed++
		}
		if deps.SendSpacing > 0 {
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(deps.SendSpacing):
			}
		}
	}
	return res, nil
}

func webhookURLForOutboxRow(deps DeliveryDeps, row DeliveryOutbox) string {
	channel := strings.TrimSpace(row.TargetChannel)
	if channel != "" && deps.WebhookURLByChannel != nil {
		if got := strings.TrimSpace(deps.WebhookURLByChannel[channel]); got != "" {
			return got
		}
	}
	if channel != "" && channel != DeliveryChannelLarkActivity {
		return ""
	}
	return deps.WebhookURL
}

func defaultRetryBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 30 * time.Second
	case attempt == 2:
		return 2 * time.Minute
	default:
		return 10 * time.Minute
	}
}

type activityHTTPStatusError struct {
	code int
	body string
}

func (e *activityHTTPStatusError) Error() string {
	return fmt.Sprintf("activity webhook non-2xx: %d %s", e.code, e.body)
}

func postActivityLarkWebhook(ctx context.Context, client *http.Client, webhookURL string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, &activityHTTPStatusError{code: resp.StatusCode, body: string(body)}
	}
	return body, nil
}
