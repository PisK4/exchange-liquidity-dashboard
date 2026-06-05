package parser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"edgex-dashboard/backend/internal/activity"
)

type RawDocument struct {
	Platform      string
	SourceGroup   string
	SourceURL     string
	FetchMode     string
	Payload       []byte
	RequiresLogin bool
	Personalized  bool
}

type rawActivity struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Summary      string            `json:"summary"`
	URL          string            `json:"url"`
	ActivityType string            `json:"activity_type"`
	Symbols      []string          `json:"symbols"`
	RewardPools  []json.RawMessage `json:"reward_pools"`
}

func ParseBinance(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseOKX(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseBingX(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseGate(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseMEXC(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseBybit(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseBitget(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseHyperliquid(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseLighter(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func parseGeneric(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	var raw rawActivity
	if err := json.Unmarshal(doc.Payload, &raw); err != nil {
		return nil, err
	}
	sourceURL := strings.TrimSpace(raw.URL)
	if sourceURL == "" {
		sourceURL = doc.SourceURL
	}
	contentHash := sha256Hex(doc.Payload)
	warnings := []string{}
	needsReview := doc.RequiresLogin || doc.Personalized
	if needsReview {
		warnings = append(warnings, "requires_login_or_personalized")
	}
	symbols := make([]activity.ActivityEventSymbol, 0, len(raw.Symbols))
	for i, sym := range raw.Symbols {
		sym = strings.ToUpper(strings.TrimSpace(sym))
		if sym == "" {
			continue
		}
		symbols = append(symbols, activity.ActivityEventSymbol{
			CanonicalSymbol: sym,
			DisplaySymbol:   sym + "-USDT",
			MarketSurface:   "perp",
			Role:            "target",
			SortOrder:       i,
		})
	}
	parserWarnings, _ := json.Marshal(warnings)
	sourceCtx, _ := json.Marshal(map[string]any{
		"source_group": doc.SourceGroup,
		"source_url":   doc.SourceURL,
		"fetch_mode":   doc.FetchMode,
	})
	rewardPools := json.RawMessage(`[]`)
	hasRewardPool := false
	if len(raw.RewardPools) > 0 {
		b, _ := json.Marshal(raw.RewardPools)
		rewardPools = b
		hasRewardPool = true
	}
	ev := activity.ActivityEvent{
		Platform:              strings.ToLower(strings.TrimSpace(doc.Platform)),
		SourceGroup:           doc.SourceGroup,
		SourceExternalID:      strings.TrimSpace(raw.ID),
		SourceURL:             sourceURL,
		Title:                 strings.TrimSpace(raw.Title),
		ActivityType:          strings.TrimSpace(raw.ActivityType),
		TargetSymbols:         symbols,
		ContentText:           strings.TrimSpace(raw.Summary),
		ContentHash:           contentHash,
		DedupeKey:             activity.BuildEventDedupeKey(doc.Platform, doc.SourceGroup, raw.ID, sourceURL),
		ConfidenceScore:       0.9,
		NeedsHumanReview:      needsReview,
		AutoPushAllowed:       !needsReview,
		ReviewStatus:          activity.ReviewPending,
		EventVersion:          1,
		ParserVersion:         "activity-parser-v1",
		SourceContextJSON:     sourceCtx,
		ParserWarningsJSON:    parserWarnings,
		RewardPoolsJSON:       rewardPools,
		TaskConditionsJSON:    json.RawMessage(`[]`),
		EligibilityRulesJSON:  json.RawMessage(`[]`),
		RichFieldsSummaryJSON: json.RawMessage(`{}`),
		HasRewardPool:         hasRewardPool,
	}
	return []activity.ActivityEvent{ev}, nil
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
