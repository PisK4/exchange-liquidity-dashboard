package activity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidDecisionToken  = errors.New("invalid decision token")
	ErrDecisionTokenExpired  = errors.New("decision token expired")
	ErrInvalidDecisionAction = errors.New("invalid decision action")
)

type DecisionTokenClaims struct {
	EventID      int64     `json:"event_id"`
	EventVersion int       `json:"event_version"`
	ContentHash  string    `json:"content_hash"`
	Action       string    `json:"action"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func GenerateDecisionToken(claims DecisionTokenClaims, secret string) (string, error) {
	if _, ok := ReviewStatusForDecision(claims.Action); !ok {
		return "", ErrInvalidDecisionAction
	}
	if strings.TrimSpace(secret) == "" || claims.EventID <= 0 || claims.EventVersion <= 0 || claims.ContentHash == "" || claims.ExpiresAt.IsZero() {
		return "", ErrInvalidDecisionToken
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(payload)
	sigPart := signTokenPayload(payloadPart, secret)
	return payloadPart + "." + sigPart, nil
}

func VerifyDecisionToken(token, secret string, now time.Time) (DecisionTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || strings.TrimSpace(secret) == "" {
		return DecisionTokenClaims{}, ErrInvalidDecisionToken
	}
	want := signTokenPayload(parts[0], secret)
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return DecisionTokenClaims{}, ErrInvalidDecisionToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return DecisionTokenClaims{}, fmt.Errorf("%w: %v", ErrInvalidDecisionToken, err)
	}
	var claims DecisionTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return DecisionTokenClaims{}, fmt.Errorf("%w: %v", ErrInvalidDecisionToken, err)
	}
	if _, ok := ReviewStatusForDecision(claims.Action); !ok {
		return DecisionTokenClaims{}, ErrInvalidDecisionAction
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !claims.ExpiresAt.After(now) {
		return DecisionTokenClaims{}, ErrDecisionTokenExpired
	}
	return claims, nil
}

func signTokenPayload(payloadPart, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadPart))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
