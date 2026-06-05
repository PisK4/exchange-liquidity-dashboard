package activity

import (
	"errors"
	"testing"
	"time"
)

func TestDecisionTokenBindsEventVersionHashActionAndTTL(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	claims := DecisionTokenClaims{
		EventID:      42,
		EventVersion: 3,
		ContentHash:  "abc123",
		Action:       DecisionBenchmarkWatch,
		ExpiresAt:    now.Add(30 * 24 * time.Hour),
	}
	token, err := GenerateDecisionToken(claims, "secret")
	if err != nil {
		t.Fatalf("GenerateDecisionToken err=%v", err)
	}
	got, err := VerifyDecisionToken(token, "secret", now)
	if err != nil {
		t.Fatalf("VerifyDecisionToken err=%v", err)
	}
	if got.EventID != claims.EventID || got.EventVersion != claims.EventVersion || got.ContentHash != claims.ContentHash || got.Action != claims.Action {
		t.Fatalf("claims mismatch: got=%+v want=%+v", got, claims)
	}

	tampered := token[:len(token)-1] + "x"
	if _, err := VerifyDecisionToken(tampered, "secret", now); !errors.Is(err, ErrInvalidDecisionToken) {
		t.Fatalf("tampered err=%v want ErrInvalidDecisionToken", err)
	}
	if _, err := VerifyDecisionToken(token, "other-secret", now); !errors.Is(err, ErrInvalidDecisionToken) {
		t.Fatalf("wrong secret err=%v want ErrInvalidDecisionToken", err)
	}
	if _, err := VerifyDecisionToken(token, "secret", claims.ExpiresAt.Add(time.Second)); !errors.Is(err, ErrDecisionTokenExpired) {
		t.Fatalf("expired err=%v want ErrDecisionTokenExpired", err)
	}
}

func TestDecisionTokenRejectsInvalidAction(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	_, err := GenerateDecisionToken(DecisionTokenClaims{
		EventID:      42,
		EventVersion: 1,
		ContentHash:  "hash",
		Action:       "open_original",
		ExpiresAt:    now.Add(time.Hour),
	}, "secret")
	if !errors.Is(err, ErrInvalidDecisionAction) {
		t.Fatalf("GenerateDecisionToken err=%v want ErrInvalidDecisionAction", err)
	}
}
