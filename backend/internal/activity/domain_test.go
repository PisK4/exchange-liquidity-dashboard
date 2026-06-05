package activity

import (
	"strings"
	"testing"
)

func TestReviewStatusForDecisionMapsFiveOpsActions(t *testing.T) {
	cases := []struct {
		action string
		want   string
	}{
		{DecisionFollowNow, ReviewApproved},
		{DecisionBenchmarkWatch, ReviewApproved},
		{DecisionDifferentiate, ReviewApproved},
		{DecisionNoFollow, ReviewApproved},
		{DecisionIgnoreDuplicate, ReviewRejected},
	}
	for _, c := range cases {
		got, ok := ReviewStatusForDecision(c.action)
		if !ok {
			t.Fatalf("ReviewStatusForDecision(%q) ok=false", c.action)
		}
		if got != c.want {
			t.Fatalf("ReviewStatusForDecision(%q)=%q want %q", c.action, got, c.want)
		}
	}
	if _, ok := ReviewStatusForDecision("open_source_url"); ok {
		t.Fatalf("unexpectedly accepted non-decision action")
	}
}

func TestBuildEventDedupeKeyPrefersExternalIDThenCanonicalURL(t *testing.T) {
	got := BuildEventDedupeKey("Binance", "cms_article_detail", "  abc-123  ", "https://example.test/a")
	if got != "binance|cms_article_detail|abc-123" {
		t.Fatalf("dedupe with external id = %q", got)
	}
	got = BuildEventDedupeKey("Gate", "launchpool_project_list", "", "https://www.gate.com/launchpool?id=42&utm_source=x")
	if got != "gate|launchpool_project_list|https://www.gate.com/launchpool?id=42" {
		t.Fatalf("dedupe with canonical url = %q", got)
	}
}

func TestPrepareRawEvidencePayloadTruncatesOverLimit(t *testing.T) {
	payload := strings.Repeat("a", MaxRawPayloadBytes+10)
	got := PrepareRawEvidencePayload(payload, MaxRawPayloadBytes)
	if !got.Truncated {
		t.Fatalf("expected truncated payload")
	}
	if got.SizeBytes != int64(len(payload)) {
		t.Fatalf("size=%d want %d", got.SizeBytes, len(payload))
	}
	if len(got.PayloadText) != 0 {
		t.Fatalf("truncated payload_text should be empty, got %d bytes", len(got.PayloadText))
	}
	if len(got.Preview) != MaxRawPayloadBytes {
		t.Fatalf("preview len=%d want %d", len(got.Preview), MaxRawPayloadBytes)
	}
	if got.Hash == "" || len(got.Hash) != 64 {
		t.Fatalf("hash=%q", got.Hash)
	}
}
