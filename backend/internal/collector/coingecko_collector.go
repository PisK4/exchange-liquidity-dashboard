package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/marketdata/coingecko"
)

// CoinGeckoCollector polls CoinGecko's /derivatives endpoint on a fixed
// cadence and writes platform-level 24h volumes, daily UPSERT rollups, and
// per-platform Top30 rankings into the Store.
//
// Lifecycle:
//   - One CoinGeckoCollector is constructed per process from the runtime
//     CoinGeckoConfig.
//   - Collector.Run starts the loop; it blocks until ctx is cancelled.
//   - CollectOnce can also be invoked synchronously (e.g. by tests or a
//     manual smoke target).
//
// Concurrency: the collector itself runs single-threaded; the Store APIs it
// calls are responsible for their own locking.
type CoinGeckoCollector struct {
	cfg              config.CoinGeckoConfig
	client           *coingecko.Client
	mapping          *coingecko.Mapping
	cache            *coingecko.TickerCache
	store            *Store
	universe         *config.ListedUniverse
	top30BackfillJob Top30BackfillScheduler
}

type Top30BackfillScheduler interface {
	EnqueueTop30Backfill(context.Context, map[string][]RosterEntry)
}

// NewCoinGeckoCollector constructs a collector. client may be nil if the
// caller wants to swap in a test double via SetClient.
func NewCoinGeckoCollector(cfg config.CoinGeckoConfig, store *Store, client *coingecko.Client, mapping *coingecko.Mapping) *CoinGeckoCollector {
	return &CoinGeckoCollector{
		cfg:     cfg,
		client:  client,
		mapping: mapping,
		cache:   coingecko.NewTickerCache(cfg.CacheTTL),
		store:   store,
	}
}

// configuredDisplaySymbols returns the set of V1 display symbols configured
// in symbol_mapping.yaml so the CoinGecko collector can write per-symbol
// daily aggregates only for symbols the operator cares about. The map value
// is unused; emptiness implies "no V1 symbols configured, skip per-symbol
// rows". The store keeps its symbol slice immutable post-construction so
// this snapshot is safe to read without locks.
func (c *CoinGeckoCollector) configuredDisplaySymbols() map[string]struct{} {
	out := map[string]struct{}{}
	if c.store == nil {
		return out
	}
	for _, sub := range c.store.cfg.Symbols {
		if sub.DisplaySymbol == "" {
			continue
		}
		out[sub.DisplaySymbol] = struct{}{}
	}
	return out
}

// SetListedUniverse attaches the per-platform base-asset universe used to
// resolve the "edgeX 已上线?" column on the Top30 tab. Passing a nil or
// unloaded universe leaves the existing rows untouched (legacy "否" UI).
// Safe to call before CollectOnce; subsequent calls overwrite.
func (c *CoinGeckoCollector) SetListedUniverse(u *config.ListedUniverse) {
	c.universe = u
}

func (c *CoinGeckoCollector) SetTop30BackfillScheduler(scheduler Top30BackfillScheduler) {
	c.top30BackfillJob = scheduler
}

// Run starts the periodic /derivatives pull loop until ctx is cancelled.
// First poll executes immediately so the dashboard surfaces data on boot
// rather than after one full PullInterval.
func (c *CoinGeckoCollector) Run(ctx context.Context) {
	interval := c.cfg.PullInterval
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	if err := c.CollectOnce(ctx); err != nil {
		log.Printf("coingecko: initial collection failed: %v", err)
	}
	go c.runDailyBackfill(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.CollectOnce(ctx); err != nil {
				log.Printf("coingecko: periodic collection failed: %v", err)
			}
		}
	}
}

// CollectOnce performs a single end-to-end pipeline step: fetch tickers,
// derive platform-level + Top30 + daily rollups, and persist them.
func (c *CoinGeckoCollector) CollectOnce(ctx context.Context) error {
	if c.client == nil {
		return errors.New("coingecko: client not initialised")
	}
	if c.mapping == nil {
		return errors.New("coingecko: mapping not initialised")
	}

	now := time.Now().UTC()
	tickers, endpoint, cached, err := c.fetchTickers(ctx, now)
	if err != nil {
		c.store.RecordCoinGeckoPullFailure(now, err)
		return err
	}
	if cached {
		log.Printf("coingecko: serving cached /derivatives snapshot (%d tickers)", len(tickers))
	}

	// 1) Bucket every relevant ticker per (platform, displaySymbol). For
	//    each platform we keep the highest 24h volume ticker per display
	//    symbol — CoinGecko sometimes lists the same pair multiple times
	//    (e.g. BTCUSDT and BTC-USDT-PERP) so picking the deepest one
	//    matches the operator's mental model of "the canonical perp".
	byPlatformSymbol := map[string]map[string]coingecko.Ticker{}
	unknownMarkets := map[string]int{}
	for _, t := range tickers {
		platform, ok := c.mapping.PlatformByMarketName(t.Market)
		if !ok {
			unknownMarkets[strings.TrimSpace(t.Market)]++
			continue
		}
		display := coingecko.NormaliseSymbol(t.Symbol)
		if display == "" {
			continue
		}
		bucket, ok := byPlatformSymbol[platform]
		if !ok {
			bucket = map[string]coingecko.Ticker{}
			byPlatformSymbol[platform] = bucket
		}
		current, exists := bucket[display]
		if !exists || t.Volume24HUSD() > current.Volume24HUSD() {
			bucket[display] = t
		}
	}
	if len(unknownMarkets) > 0 {
		c.logUnknownMarketsOnce(unknownMarkets)
	}

	// 2) Platform-level 24h aggregates: sum of best-per-symbol volumes,
	//    rolled into one row per platform. We also stash today's daily
	//    UPSERT (one platform-level row keyed by UTC day).
	platforms := sortedKeys(byPlatformSymbol)
	platformAggs := make([]domain.PlatformVolumeAggregate, 0, len(platforms))
	dailyRows := make([]domain.DailyVolumeAggregate, 0, len(platforms))
	day := startOfUTCDay(now)
	for _, platform := range platforms {
		symbols := byPlatformSymbol[platform]
		var (
			totalVol float64
			totalOI  float64
		)
		for _, t := range symbols {
			totalVol += t.Volume24HUSD()
			totalOI += t.OpenInterestUSD()
		}
		platformAggs = append(platformAggs, domain.PlatformVolumeAggregate{
			Platform:        platform,
			SnapshotTS:      now,
			Volume24HUSD:    totalVol,
			OpenInterestUSD: totalOI,
			DataSource:      domain.DataSourceCoinGecko,
			SourceEndpoint:  endpoint,
			Status:          domain.StatusComplete,
		})
		dailyRows = append(dailyRows, domain.DailyVolumeAggregate{
			Platform:       platform,
			Day:            day,
			Volume24HUSD:   totalVol,
			Status:         domain.StatusComplete,
			DataSource:     domain.DataSourceCoinGecko,
			SourceEndpoint: endpoint,
			SnapshotTS:     now,
		})
	}
	c.store.SaveCoinGeckoPlatformVolumes(platformAggs)
	c.store.SaveDailyVolumeAggregates(dailyRows)

	// 2b) Per-symbol daily aggregates for the V1 configured display
	//     symbols (BTC/ETH/SOL perp). Each (platform, display_symbol) is
	//     UPSERTed once per UTC day; the in-memory mergeDailyAggregate
	//     dedup keeps the latest-observed value for the day, which lets
	//     symbolShare7dLocked compute 单币种 7d 市占率 once we have at
	//     least one day per platform. Only configured symbols are written
	//     so /derivatives chatter cannot pollute the per-symbol map.
	// The configured V1 symbols (BTC/ETH/SOL) MUST always be written so
	// symbolShare7dLocked has guaranteed coverage even when the rolling
	// Top30 doesn't list them on a particular platform. The per-platform
	// Top60 set (by 24h volume) is then unioned in to feed Top30Backfiller
	// gap-detection at steady state — without it a brand-new top mover
	// would have to wait for the next daily backfill round to acquire its
	// first per-symbol daily row. SaveDailyVolumeAggregates dedups by
	// (platform, day, display_symbol) so duplicates between the two sets
	// collapse to a single store entry per (platform, symbol) per day.
	symbolUniverse := map[string]map[string]struct{}{}
	for _, platform := range platforms {
		symbolUniverse[platform] = map[string]struct{}{}
		for display := range c.configuredDisplaySymbols() {
			symbolUniverse[platform][display] = struct{}{}
		}
		ranked := flattenByVolume(byPlatformSymbol[platform])
		limit := len(ranked)
		if limit > 60 {
			limit = 60
		}
		for i := 0; i < limit; i++ {
			symbolUniverse[platform][coingecko.NormaliseSymbol(ranked[i].Symbol)] = struct{}{}
		}
	}
	symbolRows := make([]domain.DailyVolumeAggregate, 0, 60*len(platforms))
	for _, platform := range platforms {
		symbols := byPlatformSymbol[platform]
		for display := range symbolUniverse[platform] {
			t, ok := symbols[display]
			if !ok {
				continue
			}
			vol := t.Volume24HUSD()
			if vol <= 0 {
				continue
			}
			symbolRows = append(symbolRows, domain.DailyVolumeAggregate{
				Platform:       platform,
				DisplaySymbol:  display,
				Day:            day,
				Volume24HUSD:   vol,
				Status:         domain.StatusComplete,
				DataSource:     domain.DataSourceCoinGecko,
				SourceEndpoint: endpoint,
				SnapshotTS:     now,
			})
		}
	}
	if len(symbolRows) > 0 {
		c.store.SaveDailyVolumeAggregates(symbolRows)
	}

	// 3) Top30 per platform: sort the best-per-symbol bucket by 24h volume
	//    descending, take the top 30 rows. Volume7D / Delta7D start as
	//    insufficient_history; future pipeline stages can populate them by
	//    walking dailySymbolVolumes once 7d of history accumulates.
	top30ByPlatform := make(map[string][]domain.Top30Row, len(platforms))
	for _, platform := range platforms {
		rows := make([]domain.Top30Row, 0, 30)
		ranked := flattenByVolume(byPlatformSymbol[platform])
		limit := len(ranked)
		if limit > 30 {
			limit = 30
		}
		for i := 0; i < limit; i++ {
			t := ranked[i]
			rows = append(rows, domain.Top30Row{
				Rank:           i + 1,
				Platform:       platform,
				Symbol:         coingecko.NormaliseSymbol(t.Symbol),
				Volume24HUSD:   t.Volume24HUSD(),
				Volume7DStatus: domain.StatusInsufficientHistory,
				Delta7DStatus:  domain.StatusInsufficientHistory,
				DataSource:     domain.DataSourceCoinGecko,
				SourceEndpoint: endpoint,
				Status:         domain.StatusComplete,
				SnapshotTS:     now,
			})
		}
		if len(rows) > 0 {
			top30ByPlatform[platform] = rows
		}
	}

	// 4) Cross-platform enrichment: derive `edgex_listed`, `competitor_top30
	//    _coverage` and `suggested_action` for every row. Coverage is the
	//    count of competitor (non-edgeX) platforms whose Top30 contains the
	//    exact normalised symbol; the demo UI fixes the denominator at 9 so
	//    we leave the divisor to the front-end. EdgeX listing is base-asset
	//    only (BTC vs BTC-USD/USDT/USDC etc all collapse to the same base).
	coverage := buildCompetitorCoverage(top30ByPlatform)
	enrichTop30Rows(top30ByPlatform, coverage, c.universe)
	var previousRoster map[string][]RosterEntry
	if c.top30BackfillJob != nil {
		previousRoster = c.store.Top30RosterUnion()
	}
	for platform, rows := range top30ByPlatform {
		c.store.SaveTop30(platform, rows)
	}
	if c.top30BackfillJob != nil {
		if entries := newTop30RosterEntries(previousRoster, top30ByPlatform); len(entries) > 0 {
			c.top30BackfillJob.EnqueueTop30Backfill(ctx, entries)
		}
	}

	c.store.RecordCoinGeckoPullSuccess(now)
	return nil
}

func newTop30RosterEntries(previous map[string][]RosterEntry, current map[string][]domain.Top30Row) map[string][]RosterEntry {
	seen := map[string]map[string]struct{}{}
	for platform, entries := range previous {
		if seen[platform] == nil {
			seen[platform] = map[string]struct{}{}
		}
		for _, entry := range entries {
			base := strings.ToUpper(strings.TrimSpace(entry.BaseAsset))
			if base != "" {
				seen[platform][base] = struct{}{}
			}
		}
	}
	out := map[string][]RosterEntry{}
	for platform, rows := range current {
		added := map[string]struct{}{}
		for _, row := range rows {
			base := baseAssetFromSymbol(row.Symbol)
			if base == "" {
				continue
			}
			if _, ok := seen[platform][base]; ok {
				continue
			}
			if _, ok := added[base]; ok {
				continue
			}
			added[base] = struct{}{}
			out[platform] = append(out[platform], RosterEntry{BaseAsset: base, DisplaySymbol: row.Symbol})
		}
		if len(out[platform]) == 0 {
			delete(out, platform)
			continue
		}
		sort.Slice(out[platform], func(i, j int) bool { return out[platform][i].BaseAsset < out[platform][j].BaseAsset })
	}
	return out
}

// buildCompetitorCoverage counts, for every full normalised symbol observed
// in any non-edgeX platform's Top30 ranking, how many such platforms had it.
// edgeX is excluded so the count matches the demo's "9 家竞品 Top30 覆盖"
// semantics regardless of whether edgeX Top30 is populated this round.
func buildCompetitorCoverage(top30ByPlatform map[string][]domain.Top30Row) map[string]int {
	out := map[string]int{}
	for platform, rows := range top30ByPlatform {
		if platform == "edgeX" {
			continue
		}
		for _, r := range rows {
			out[r.Symbol]++
		}
	}
	return out
}

// enrichTop30Rows mutates the passed Top30 rows in place: it stamps each row
// with edgeX listing status (base-asset match against the universe),
// competitor Top30 coverage count, and a suggested operator action derived
// from those two signals. When the universe is unloaded the listing columns
// stay on their zero values so the UI keeps showing legacy "否".
func enrichTop30Rows(top30ByPlatform map[string][]domain.Top30Row, coverage map[string]int, universe *config.ListedUniverse) {
	listingLoaded := universe.Loaded()
	for platform, rows := range top30ByPlatform {
		for i := range rows {
			cov := coverage[rows[i].Symbol]
			rows[i].CoverageCount = cov
			rows[i].CoverageStatus = domain.StatusComplete

			listed := false
			if listingLoaded {
				listed = universe.IsListed("edgeX", baseAssetFromSymbol(rows[i].Symbol))
				rows[i].EdgexListed = listed
				rows[i].ListedStatus = domain.StatusComplete
			}

			action, actionStatus := deriveSuggestedAction(platform, listed, cov, listingLoaded)
			rows[i].Action = action
			rows[i].ActionStatus = actionStatus
		}
		top30ByPlatform[platform] = rows
	}
}

// baseAssetFromSymbol extracts the canonical base ticker from a CoinGecko
// normalised symbol such as "BTC-USDT (perp)" → "BTC". Symbols without a "-"
// or a trailing " (...)" suffix are returned uppercased unchanged so unusual
// shapes (e.g. "1000PEPE") still round-trip cleanly into the universe lookup.
func baseAssetFromSymbol(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, " "); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "-"); idx >= 0 {
		s = s[:idx]
	}
	return strings.ToUpper(s)
}

// canonicalDailyKey collapses every quote-currency variant of a perp
// display symbol onto the single 'BASE-USDT (perp)' form used as the
// canonical key for per-symbol daily aggregates and KPI computations.
// edgeX reports BTC-USD (perp), some bingx markets list BTC-USDC (perp);
// V1 KPIs intentionally treat all of these as the same logical 'BTC perp'
// so the share-of-volume math sums them. The TOP30 tab still shows the
// platform-native symbol — only the daily-aggregate write path and the
// 7d window enrichment lookup go through this normalisation.
//
// Idempotent: any input that already ends in '-USDT (perp)' is returned
// unchanged.
func canonicalDailyKey(displaySymbol string) string {
	s := strings.TrimSpace(displaySymbol)
	if !strings.HasSuffix(s, " (perp)") {
		return displaySymbol
	}
	head := strings.TrimSuffix(s, " (perp)")
	idx := strings.LastIndex(head, "-")
	if idx <= 0 {
		return displaySymbol
	}
	base := head[:idx]
	quote := strings.ToUpper(head[idx+1:])
	switch quote {
	case "USD", "USDC", "USDT":
		return base + "-USDT (perp)"
	default:
		return displaySymbol
	}
}

// deriveSuggestedAction mirrors the badge ladder in the source demo HTML
// (architecture/方案设计/EdgeX运营/原需求/edgeX · 流动性 & 深度监控面板 (Demo)(3).html
// renderTop30). The edgeX self-tab is excluded because "建议动作" against
// edgeX's own Top30 is operationally meaningless. The action is left blank
// (with status=insufficient_history) when the listing universe is missing
// because "保持/上架" decisions hinge on knowing whether edgeX lists the
// symbol.
func deriveSuggestedAction(platform string, listed bool, coverage int, listingLoaded bool) (string, string) {
	if platform == "edgeX" {
		return "", ""
	}
	if !listingLoaded {
		return "", domain.StatusInsufficientHistory
	}
	switch {
	case listed && coverage >= 6:
		return "考虑拉新活动", domain.StatusComplete
	case listed:
		return "保持", domain.StatusComplete
	case coverage >= 7:
		return "优先上架", domain.StatusComplete
	case coverage >= 5:
		return "评估上架", domain.StatusComplete
	default:
		return "观望", domain.StatusComplete
	}
}

// fetchTickers returns either a fresh /derivatives response or the cached
// one if a previous call landed within TTL. cached==true is informational
// and only used for log output.
func (c *CoinGeckoCollector) fetchTickers(ctx context.Context, now time.Time) ([]coingecko.Ticker, string, bool, error) {
	if cached, endpoint, ok := c.cache.Get(now); ok {
		return cached, endpoint, true, nil
	}
	tickers, endpoint, err := c.client.FetchDerivatives(ctx)
	if err != nil {
		return nil, endpoint, false, err
	}
	c.cache.Put(now, tickers, endpoint)
	return tickers, endpoint, false, nil
}

// logUnknownMarketsOnce logs CoinGecko market_name values we couldn't map to
// any internal platform; capped to the 5 most-seen entries so a noisy
// upstream doesn't drown out other log output.
func (c *CoinGeckoCollector) logUnknownMarketsOnce(counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	type entry struct {
		name  string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for name, count := range counts {
		entries = append(entries, entry{name: name, count: count})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })
	top := entries
	if len(top) > 5 {
		top = top[:5]
	}
	parts := make([]string, 0, len(top))
	for _, e := range top {
		parts = append(parts, fmt.Sprintf("%q×%d", e.name, e.count))
	}
	log.Printf("coingecko: %d unmapped markets ignored (sample: %s)", len(counts), strings.Join(parts, ", "))
}

// backfillRequestPaceMin is the floor delay between successive backfill
// requests so a single Run does not exceed CoinGecko's Demo-tier rate
// budget (empirically ~5-10 req/min before bursts trip a 429 even when the
// per-minute average is below the documented cap). Exposed as vars so
// tests can crush them to zero.
var (
	backfillRequestPaceMin = 6500 * time.Millisecond
	backfillRetryBackoffs  = []time.Duration{30 * time.Second, 60 * time.Second}
)

// BackfillVolumeHistory pulls the last `days` of per-platform 24h volume
// from CoinGecko's /exchanges/{id}/volume_chart (Demo-tier endpoint;
// the /range variant requires Pro), converts each daily BTC sample to USD
// via /coins/bitcoin/market_chart/range, and UPSERTs the result into
// t_daily_volume_aggregate as data_source="coingecko_backfill".
//
// Idempotent and side-effect light: the mysql_store UPSERT rule keeps any
// existing "coingecko" or "native" row over a backfill row, so this can be
// re-run on every boot or every day without overwriting fresh data.
//
// Rate-limit awareness: we pace requests at backfillRequestPaceMin and
// transparently retry a single 429 per platform with a back-off proportional
// to retry-after-style guidance from the upstream client.
func (c *CoinGeckoCollector) BackfillVolumeHistory(ctx context.Context, days int) error {
	if c.client == nil {
		return errors.New("coingecko: client not initialised")
	}
	if c.mapping == nil {
		return errors.New("coingecko: mapping not initialised")
	}
	if days < 1 {
		days = 1
	}
	if days > 30 {
		days = 30
	}
	now := time.Now().UTC()
	from := startOfUTCDay(now).AddDate(0, 0, -days)
	to := now

	pricePts, btcEndpoint, err := c.client.FetchBitcoinPriceChartRange(ctx, from, to)
	if err != nil {
		return fmt.Errorf("fetch BTC price chart: %w", err)
	}
	btcByDay := bucketLatestPriceByDay(pricePts)
	if len(btcByDay) == 0 {
		return errors.New("coingecko: BTC price chart returned no usable samples")
	}
	log.Printf("coingecko backfill: fetched %d BTC price samples covering %d UTC days (endpoint=%s)", len(pricePts), len(btcByDay), btcEndpoint)

	platforms := sortedKeys(c.cfg.ExchangeID)
	totalRows := 0
	for _, platform := range platforms {
		exchangeID, ok := c.mapping.ExchangeIDFor(platform)
		if !ok || exchangeID == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backfillRequestPaceMin):
		}
		points, endpoint, err := c.fetchVolumeChartWithRetry(ctx, exchangeID, days)
		if err != nil {
			log.Printf("coingecko backfill: %s (%s) failed: %v", platform, exchangeID, err)
			continue
		}
		volByDay := bucketLatestVolumeByDay(points)
		rows := make([]domain.DailyVolumeAggregate, 0, len(volByDay))
		for day, btcVol := range volByDay {
			btcUSD, ok := btcByDay[day]
			if !ok || btcUSD <= 0 {
				continue
			}
			rows = append(rows, domain.DailyVolumeAggregate{
				Day:            day,
				Platform:       platform,
				Volume24HUSD:   btcVol * btcUSD,
				DataSource:     domain.DataSourceCoinGeckoBackfill,
				SourceEndpoint: endpoint,
				Status:         domain.StatusComplete,
				SnapshotTS:     now,
			})
		}
		if len(rows) == 0 {
			log.Printf("coingecko backfill: %s (%s) returned 0 usable rows", platform, exchangeID)
			continue
		}
		c.store.SaveDailyVolumeAggregates(rows)
		totalRows += len(rows)
		log.Printf("coingecko backfill: %s persisted %d daily rows", platform, len(rows))
	}
	log.Printf("coingecko backfill: complete, %d platforms processed, %d rows persisted", len(platforms), totalRows)
	return nil
}

// fetchVolumeChartWithRetry calls /exchanges/{id}/volume_chart and retries
// each backoff in backfillRetryBackoffs after a 429. Any other error is
// returned immediately; the outer loop just logs and skips the platform.
func (c *CoinGeckoCollector) fetchVolumeChartWithRetry(ctx context.Context, exchangeID string, days int) ([]coingecko.VolumeChartPoint, string, error) {
	pts, endpoint, err := c.client.FetchExchangeVolumeChart(ctx, exchangeID, days)
	if err == nil {
		return pts, endpoint, nil
	}
	if !coingecko.IsRateLimited(err) {
		return nil, endpoint, err
	}
	for _, backoff := range backfillRetryBackoffs {
		log.Printf("coingecko backfill: %s 429 received, retrying after %s", exchangeID, backoff)
		select {
		case <-ctx.Done():
			return nil, endpoint, ctx.Err()
		case <-time.After(backoff):
		}
		pts, endpoint, err = c.client.FetchExchangeVolumeChart(ctx, exchangeID, days)
		if err == nil {
			return pts, endpoint, nil
		}
		if !coingecko.IsRateLimited(err) {
			return nil, endpoint, err
		}
	}
	return nil, endpoint, err
}

// runDailyBackfill schedules a daily call to BackfillVolumeHistory(7) at
// roughly UTC 01:00 so any service downtime overnight gets back-filled the
// next morning. The first run also fires shortly after Run() starts to cover
// the case where the collector boots after a long outage. The boot run
// requests 30 days so a fresh deployment immediately hydrates the Share 30d
// window; subsequent daily runs only request 7 days because the rolling
// catch-up window never needs to look further back than that.
func (c *CoinGeckoCollector) runDailyBackfill(ctx context.Context) {
	t := time.NewTimer(90 * time.Second)
	defer t.Stop()
	days := 30
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.BackfillVolumeHistory(ctx, days); err != nil {
				log.Printf("coingecko daily backfill failed: %v", err)
			}
			days = 7
			t.Reset(nextDailyBackfillDelay(time.Now().UTC()))
		}
	}
}

// nextDailyBackfillDelay returns the duration from `now` until the next UTC
// 01:00 instant. Exposed for testing.
func nextDailyBackfillDelay(now time.Time) time.Duration {
	utc := now.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day(), 1, 0, 0, 0, time.UTC)
	if !next.After(utc) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(utc)
}

func bucketLatestPriceByDay(pts []coingecko.PricePoint) map[time.Time]float64 {
	out := map[time.Time]float64{}
	latest := map[time.Time]int64{}
	for _, p := range pts {
		day := startOfUTCDay(time.UnixMilli(p.TimestampMS).UTC())
		if existing, ok := latest[day]; !ok || p.TimestampMS > existing {
			out[day] = p.PriceUSD
			latest[day] = p.TimestampMS
		}
	}
	return out
}

func bucketLatestVolumeByDay(pts []coingecko.VolumeChartPoint) map[time.Time]float64 {
	out := map[time.Time]float64{}
	latest := map[time.Time]int64{}
	for _, p := range pts {
		day := startOfUTCDay(time.UnixMilli(p.TimestampMS).UTC())
		if existing, ok := latest[day]; !ok || p.TimestampMS > existing {
			out[day] = p.VolumeBTC
			latest[day] = p.TimestampMS
		}
	}
	return out
}

func flattenByVolume(bucket map[string]coingecko.Ticker) []coingecko.Ticker {
	out := make([]coingecko.Ticker, 0, len(bucket))
	for _, t := range bucket {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Volume24HUSD() > out[j].Volume24HUSD() })
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
