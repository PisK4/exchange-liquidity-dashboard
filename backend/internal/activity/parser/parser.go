package parser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"edgex-ops-intelligence/backend/internal/activity"
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
	Name         string            `json:"name"`
	Summary      string            `json:"summary"`
	Content      string            `json:"content"`
	URL          string            `json:"url"`
	Link         string            `json:"link"`
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
	raws, err := parseRawActivities(doc)
	if err != nil {
		return nil, err
	}
	events := make([]activity.ActivityEvent, 0, len(raws))
	for _, raw := range raws {
		ev := buildEvent(doc, raw)
		if ev.Title == "" {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

func parseRawActivities(doc RawDocument) ([]rawActivity, error) {
	var root any
	if err := json.Unmarshal(doc.Payload, &root); err == nil {
		out := []rawActivity{}
		collectJSONActivities(root, &out)
		if len(out) > 0 {
			return out, nil
		}
	}
	return parseDocumentActivity(doc), nil
}

func collectJSONActivities(v any, out *[]rawActivity) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			collectJSONActivities(item, out)
		}
	case map[string]any:
		raw := rawActivity{
			ID:           firstString(x, "id", "articleCode", "code", "uuid", "slug", "project_id", "pid"),
			Title:        firstString(x, "title", "name", "articleTitle", "activityName", "project_name", "subject"),
			Summary:      firstString(x, "summary", "description", "content", "body", "brief", "desc"),
			URL:          firstString(x, "url", "link", "sourceUrl", "source_url", "articleUrl", "redirectUrl", "href"),
			ActivityType: firstString(x, "activity_type", "activityType", "type", "category", "annType"),
			Symbols:      stringList(x, "symbols", "target_symbols", "tokens", "coins"),
		}
		if raw.Title != "" {
			*out = append(*out, raw)
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "content" || k == "body" || k == "description" {
				continue
			}
			collectJSONActivities(x[k], out)
		}
	}
}

func parseDocumentActivity(doc RawDocument) []rawActivity {
	text := string(doc.Payload)
	links := extractHTMLLinks(text, doc.SourceURL)
	if len(links) > 0 {
		return links
	}
	title := firstDocumentTitle(text)
	if title == "" {
		title = doc.Platform + " " + doc.SourceGroup
	}
	return []rawActivity{{
		ID:      sha256Hex([]byte(doc.Platform + doc.SourceGroup + doc.SourceURL)),
		Title:   title,
		Summary: cleanText(text),
		URL:     doc.SourceURL,
	}}
}

func buildEvent(doc RawDocument, raw rawActivity) activity.ActivityEvent {
	if raw.Title == "" {
		raw.Title = raw.Name
	}
	if raw.Summary == "" {
		raw.Summary = raw.Content
	}
	sourceURL := strings.TrimSpace(raw.URL)
	if sourceURL == "" {
		sourceURL = raw.Link
	}
	if sourceURL == "" {
		sourceURL = doc.SourceURL
	}
	identity := strings.TrimSpace(raw.ID)
	if identity == "" {
		identity = sha256Hex([]byte(raw.Title + "|" + sourceURL))[:16]
	}
	contentHash := sha256Hex([]byte(raw.Title + "|" + raw.Summary + "|" + sourceURL))
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
		SourceExternalID:      identity,
		SourceURL:             sourceURL,
		Title:                 strings.TrimSpace(raw.Title),
		ActivityType:          inferActivityType(doc, raw),
		TargetSymbols:         symbols,
		ContentText:           strings.TrimSpace(raw.Summary),
		ContentHash:           contentHash,
		DedupeKey:             activity.BuildEventDedupeKey(doc.Platform, doc.SourceGroup, identity, sourceURL),
		ConfidenceScore:       0.9,
		NeedsHumanReview:      needsReview,
		AutoPushAllowed:       !needsReview,
		EventStatus:           activity.EventStatusActive,
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
	return ev
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch vv := v.(type) {
			case string:
				if strings.TrimSpace(vv) != "" {
					return strings.TrimSpace(vv)
				}
			case float64:
				return strconv.FormatFloat(vv, 'f', -1, 64)
			}
		}
	}
	return ""
}

func stringList(m map[string]any, keys ...string) []string {
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case []any:
			out := make([]string, 0, len(vv))
			for _, item := range vv {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		case []string:
			return vv
		case string:
			return []string{vv}
		}
	}
	return nil
}

func inferActivityType(doc RawDocument, raw rawActivity) string {
	if strings.TrimSpace(raw.ActivityType) != "" {
		return strings.TrimSpace(raw.ActivityType)
	}
	needle := strings.ToLower(doc.SourceGroup + " " + raw.Title + " " + raw.Summary)
	switch {
	case strings.Contains(needle, "launchpool") || strings.Contains(needle, "poolx") || strings.Contains(needle, "hodler"):
		return "launchpool"
	case strings.Contains(needle, "airdrop") || strings.Contains(needle, "giveaway") || strings.Contains(needle, "candydrop"):
		return "airdrop_campaign"
	case strings.Contains(needle, "competition") || strings.Contains(needle, "m-day") || strings.Contains(needle, "trade to"):
		return "futures_trading_competition"
	case strings.Contains(needle, "delist"):
		return "delisting_signal"
	case doc.Platform == "hyperliquid":
		return "non_cex_update_event"
	case doc.Platform == "lighter" || strings.Contains(needle, "points") || strings.Contains(needle, "incentive"):
		return "incentive_rule_snapshot"
	case strings.Contains(needle, "listing"):
		return "listing_trading_campaign"
	default:
		return "activity_notice"
	}
}

func firstDocumentTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`),
		regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`),
	} {
		if match := re.FindStringSubmatch(text); len(match) > 1 {
			return cleanText(match[1])
		}
	}
	return ""
}

func extractHTMLLinks(text, base string) []rawActivity {
	re := regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(text, 50)
	out := []rawActivity{}
	for _, match := range matches {
		title := cleanText(match[2])
		if len(title) < 8 || !looksActivityTitle(title) {
			continue
		}
		out = append(out, rawActivity{
			ID:      sha256Hex([]byte(match[1] + "|" + title))[:16],
			Title:   title,
			Summary: title,
			URL:     absolutizeURL(base, match[1]),
		})
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func looksActivityTitle(title string) bool {
	needle := strings.ToLower(title)
	keywords := []string{"launchpool", "airdrop", "campaign", "competition", "giveaway", "reward", "poolx", "m-day", "listing", "delist", "points", "incentive", "earn"}
	for _, keyword := range keywords {
		if strings.Contains(needle, keyword) {
			return true
		}
	}
	return false
}

func absolutizeURL(base, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return u.ResolveReference(ref).String()
}

func cleanText(text string) string {
	text = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 2000 {
		return text[:2000]
	}
	return text
}
