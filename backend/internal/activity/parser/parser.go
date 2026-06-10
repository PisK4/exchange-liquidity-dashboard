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
	"time"

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
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Name           string            `json:"name"`
	Summary        string            `json:"summary"`
	Content        string            `json:"content"`
	URL            string            `json:"url"`
	Link           string            `json:"link"`
	ActivityType   string            `json:"activity_type"`
	Symbols        []string          `json:"symbols"`
	RewardPools    []json.RawMessage `json:"reward_pools"`
	PublishTime    *time.Time
	RawTimeText    string
	TimeConfidence string
	SourceContext  map[string]any
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
	return parseBybitAnnouncements(ctx, doc)
}

func ParseBitget(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseGeneric(ctx, doc)
}

func ParseHyperliquid(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	return parseHyperliquidEntries(ctx, doc)
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
			URL:          firstString(x, "articleUrl", "article_url", "href", "redirectUrl", "redirect_url", "url", "link", "sourceUrl", "source_url"),
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

var bybitAnnouncementSlugRE = regexp.MustCompile(`blt[0-9a-f]{16}`)

func parseBybitAnnouncements(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	var envelope struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Title         string      `json:"title"`
				Description   string      `json:"description"`
				URL           string      `json:"url"`
				DateTimestamp json.Number `json:"dateTimestamp"`
				PublishTime   json.Number `json:"publishTime"`
				Type          struct {
					Title string `json:"title"`
					Key   string `json:"key"`
				} `json:"type"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(doc.Payload, &envelope); err != nil {
		return parseGeneric(ctx, doc)
	}
	if envelope.RetCode != 0 {
		return nil, nil
	}
	if len(envelope.Result.List) == 0 {
		return parseGeneric(ctx, doc)
	}
	raws := make([]rawActivity, 0, len(envelope.Result.List))
	for _, item := range envelope.Result.List {
		publish := parseEpochMillis(firstJSONNumber(item.PublishTime, item.DateTimestamp))
		rawTime := firstNonEmptyString(item.PublishTime.String(), item.DateTimestamp.String())
		raws = append(raws, rawActivity{
			ID:             deriveBybitAnnouncementID(item.URL),
			Title:          item.Title,
			Summary:        item.Description,
			URL:            item.URL,
			ActivityType:   item.Type.Title,
			Symbols:        extractUpperSymbols(item.Title),
			PublishTime:    publish,
			RawTimeText:    rawTime,
			TimeConfidence: timestampConfidence(publish),
			SourceContext: map[string]any{
				"bybit_type_key":   item.Type.Key,
				"bybit_type_title": item.Type.Title,
			},
		})
	}
	return buildEventsFromRaw(ctx, doc, raws)
}

func deriveBybitAnnouncementID(rawURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return ""
	}
	if match := bybitAnnouncementSlugRE.FindString(trimmed); match != "" {
		return match
	}
	return sha256Hex([]byte(trimmed))[:16]
}

func parseHyperliquidEntries(ctx context.Context, doc RawDocument) ([]activity.ActivityEvent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	var envelope struct {
		Entries []struct {
			Title     string `json:"title"`
			CreatedAt string `json:"createdAt"`
			Preview   string `json:"preview"`
			UUID      string `json:"uuid"`
			Hash      string `json:"hash"`
			Category  string `json:"category"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(doc.Payload, &envelope); err != nil || len(envelope.Entries) == 0 {
		return parseGeneric(ctx, doc)
	}
	raws := make([]rawActivity, 0, len(envelope.Entries))
	for _, item := range envelope.Entries {
		publish := parseRFC3339Time(item.CreatedAt)
		sourceURL := doc.SourceURL
		if strings.TrimSpace(item.UUID) != "" {
			sourceURL = hyperliquidEntryURL(doc.SourceURL, item.UUID)
		}
		id := firstNonEmptyString(item.UUID, item.Hash)
		raws = append(raws, rawActivity{
			ID:             id,
			Title:          item.Title,
			Summary:        item.Preview,
			URL:            sourceURL,
			ActivityType:   hyperliquidActivityType(item.Category, item.Title),
			Symbols:        extractHyperliquidSymbols(item.Title),
			PublishTime:    publish,
			RawTimeText:    item.CreatedAt,
			TimeConfidence: timestampConfidence(publish),
			SourceContext: map[string]any{
				"source_hash": item.Hash,
				"uuid":        item.UUID,
				"category":    item.Category,
			},
		})
	}
	return buildEventsFromRaw(ctx, doc, raws)
}

func buildEventsFromRaw(ctx context.Context, doc RawDocument, raws []rawActivity) ([]activity.ActivityEvent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
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
	if len(raw.SourceContext) > 0 {
		ctx := map[string]any{
			"source_group": doc.SourceGroup,
			"source_url":   doc.SourceURL,
			"fetch_mode":   doc.FetchMode,
		}
		for k, v := range raw.SourceContext {
			ctx[k] = v
		}
		sourceCtx, _ = json.Marshal(ctx)
	}
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
	if raw.PublishTime != nil {
		t := raw.PublishTime.UTC()
		ev.PublishTime = &t
	}
	ev.RawTimeText = strings.TrimSpace(raw.RawTimeText)
	ev.TimeParseConfidence = strings.TrimSpace(raw.TimeConfidence)
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

func firstJSONNumber(candidates ...json.Number) json.Number {
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func parseEpochMillis(n json.Number) *time.Time {
	if n == "" {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v <= 0 {
		return nil
	}
	t := time.UnixMilli(v).UTC()
	return &t
}

func parseRFC3339Time(raw string) *time.Time {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

func timestampConfidence(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "unknown"
	}
	return "source_published_at"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

var upperSymbolRE = regexp.MustCompile(`\b[A-Z0-9]{2,20}\b`)
var quotedSymbolRE = regexp.MustCompile(`\b([0-9A-Z]{2,20})(USDT|USDC|USD)\b`)

func extractUpperSymbols(text string) []string {
	upper := strings.ToUpper(text)
	out := []string{}
	seen := map[string]struct{}{}
	for _, match := range quotedSymbolRE.FindAllStringSubmatch(upper, -1) {
		if len(match) < 2 {
			continue
		}
		appendSymbol(&out, seen, match[1])
	}
	if len(out) > 0 {
		return out
	}
	matches := upperSymbolRE.FindAllString(upper, -1)
	for _, match := range matches {
		appendSymbol(&out, seen, match)
	}
	return out
}

var symbolStopwords = map[string]struct{}{
	"AND": {}, "ANNOUNCEMENT": {}, "API": {}, "BYBIT": {}, "CAMPAIGN": {}, "CONTRACT": {},
	"CRYPTO": {}, "FEE": {}, "FOR": {}, "FUTURES": {}, "HTTP": {}, "LAUNCH": {},
	"LIST": {}, "LISTING": {}, "MARKET": {}, "NEW": {}, "ON": {}, "PERP": {},
	"PERPETUAL": {}, "REWARD": {}, "REWARDS": {}, "SPOT": {}, "THE": {}, "TO": {},
	"TRADE": {}, "TRADING": {}, "UPDATE": {}, "USD": {}, "USDC": {}, "USDT": {},
	"WILL": {}, "WITH": {},
}

func isSymbolStopword(token string) bool {
	_, ok := symbolStopwords[token]
	return ok
}

func appendSymbol(out *[]string, seen map[string]struct{}, token string) {
	token = strings.TrimSpace(strings.ToUpper(token))
	if token == "" || isSymbolStopword(token) {
		return
	}
	if _, ok := seen[token]; ok {
		return
	}
	seen[token] = struct{}{}
	*out = append(*out, token)
}

func hyperliquidEntryURL(entriesURL, uuid string) string {
	u, err := url.Parse(entriesURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "https://dzjnlsk4rxci0.cloudfront.net/mainnet/entry-" + uuid + ".json"
	}
	path := u.Path
	idx := strings.LastIndex(path, "/")
	if idx >= 0 {
		path = path[:idx+1]
	} else {
		path = "/"
	}
	u.Path = path + "entry-" + strings.TrimSpace(uuid) + ".json"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func hyperliquidActivityType(category, title string) string {
	needle := strings.ToLower(category + " " + title)
	switch {
	case strings.Contains(needle, "delist"):
		return "delisting_signal"
	case strings.Contains(needle, "listing") || strings.Contains(needle, "enabled spot"):
		return "listing_trading_campaign"
	default:
		return "non_cex_update_event"
	}
}

var hyperliquidQuotedSymbolRE = regexp.MustCompile(`\b([0-9A-Z]{2,20})[\s\-/]*(USDC|USD)\b`)

func extractHyperliquidSymbols(title string) []string {
	upper := strings.ToUpper(title)
	out := []string{}
	seen := map[string]struct{}{}
	for _, match := range hyperliquidQuotedSymbolRE.FindAllStringSubmatch(upper, -1) {
		if len(match) < 2 {
			continue
		}
		sym := strings.TrimSpace(match[1])
		if sym == "" || isSymbolStopword(sym) {
			continue
		}
		if _, ok := seen[sym]; ok {
			continue
		}
		seen[sym] = struct{}{}
		out = append(out, sym)
	}
	if len(out) > 0 {
		return out
	}
	if strings.Contains(strings.ToLower(title), "enabled spot") {
		return extractUpperSymbols(title)
	}
	return nil
}
