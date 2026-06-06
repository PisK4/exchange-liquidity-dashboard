package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
)

// defaultCatalogResolverTTL is the per-platform refresh window for
// the DB-first cache. 5 minutes aligns with the listing instrument
// poll cadence so a fresh DB row becomes visible to the backfiller
// within one poll boundary without any cross-component invalidation.
const defaultCatalogResolverTTL = 5 * time.Minute

// SnapshotRow is the minimum projection of t_listing_instrument_snapshot
// the catalog resolver needs. Keeping it in this package (rather than
// importing listing.InstrumentSnapshot) avoids a circular dependency:
// the listing package owns the Repository; cmd/ops-intelligence wires an
// adapter that fans out the listing.Repository method into this
// shape.
type SnapshotRow struct {
	Platform       string
	MarketType     string
	APISymbol      string
	APIMarketID    string
	BaseAsset      string
	MarketSurface  string
	InstrumentKind string
	RawJSON        []byte
}

// SnapshotReader is the optional dependency that drives the DB-first
// resolver path for the 4 platforms whose api_symbol cannot be
// synthesized from convention alone (hyperliquid, gate, lighter,
// edgeX). When nil, the resolver behaves exactly like the previous
// file-only implementation.
type SnapshotReader interface {
	ListLatestInstrumentSnapshotsByPlatform(ctx context.Context, platform string) ([]SnapshotRow, error)
}

// ErrSymbolUnsupported is returned when a (platform, baseAsset) pair cannot
// be resolved to a tradable SymbolSub for any of the following reasons:
//
//   - the platform does not list a USDT-quoted linear perp for that base
//     (e.g. lighter / hyperliquid only cover a subset of coins);
//   - the raw-instruments dump is older than the listing event;
//   - a required per-platform field (gate quanto_multiplier, lighter
//     market_id, edgeX contract_id) is absent in the dump.
//
// Callers (Top30Backfiller) must skip the row rather than fabricate values.
var ErrSymbolUnsupported = errors.New("symbol unsupported on platform")

// CatalogResolver answers "given a base asset like BTC and a platform like
// gate, what SymbolSub should I pass into adapter.FetchDailyVolumeHistory?".
//
// The V1 instrument_catalog.yaml only carries entries for the three
// configured display symbols (BTC/ETH/SOL). Top30 needs the same metadata
// for any base asset that lands in a platform's top-N volume ranking, which
// includes 30~70 distinct symbols per platform. Re-querying each exchange's
// instrument endpoint at runtime is both noisy and racy (rate limits,
// transient outages), so the resolver lazy-loads the per-platform JSON
// dumps already persisted under `backend/docs/raw-instruments/` by
// `make catalog`. Those dumps are the same source-of-truth used to build
// the yaml, just before the V1-symbol filter is applied.
//
// Resolution strategy:
//   - For binance / okx / bybit / bitget / bingx / mexc / hyperliquid the
//     api_symbol is a pure convention (e.g. binance "BTCUSDT", okx
//     "BTC-USDT-SWAP"), so a dump load is unnecessary for synthesis. We
//     still load hyperliquid's universe list to fail-fast on bases the
//     platform doesn't actually list.
//   - For gate the quanto_multiplier is mandatory for USD conversion and
//     varies per contract; read from gate-futures-usdt/*.json.
//   - For lighter the market_id is the only way to query candles; read
//     from lighter-perp/*.json.
//   - For edgeX the contract_id is needed to query getKline; read from
//     edgeX-perp-v1/*.json via coinList → baseCoinId → contractList.
//
// Concurrency: resolver is safe to share across goroutines. The dump cache
// is populated once per platform on first access and reused for the
// process lifetime.
type CatalogResolver struct {
	dumpsDir  string
	snapshots SnapshotReader
	ttl       time.Duration
	nowFn     func() time.Time

	mu    sync.Mutex
	cache map[string]platformCacheEntry // platform → entry
}

// platformCacheEntry pins a per-platform lookup with its load time so
// the resolver can decide on each Resolve call whether the cached
// entry is still fresh.
type platformCacheEntry struct {
	lookup   platformLookup
	loadedAt time.Time
}

// platformLookup is either dump-backed (entries enumerated) or convention-
// only (synthesise on demand). Convention-only platforms still cache an
// empty struct so the heavy bootstrap path runs at most once.
type platformLookup struct {
	conventionOnly bool
	entries        map[string]domain.SymbolSub // baseAsset → sub
}

// NewCatalogResolver creates an empty resolver rooted at dumpsDir. The
// directory layout must match what `make catalog` writes, i.e.
// `<dumpsDir>/<platform-surface>/<YYYY-MM-DD>.json`. Lookups for platforms
// whose dump is missing degrade to ErrSymbolUnsupported (unless the
// platform is convention-only, in which case the symbol is synthesised).
func NewCatalogResolver(dumpsDir string) *CatalogResolver {
	return NewCatalogResolverWithDB(dumpsDir, nil, defaultCatalogResolverTTL, nil)
}

// NewCatalogResolverWithDB constructs a resolver wired to both the
// file dumps directory and a SnapshotReader. When snapshots is
// non-nil the resolver tries the DB first for the 4 DB-first
// platforms (hyperliquid / gate / lighter / edgeX) and falls back
// to the file dump on either an error or an empty result, so the
// monthly catalog stays a safety net. TTL bounds how long a cached
// per-platform lookup is reused; pass 0 to default to 5 minutes.
func NewCatalogResolverWithDB(dumpsDir string, snapshots SnapshotReader, ttl time.Duration, now func() time.Time) *CatalogResolver {
	if ttl <= 0 {
		ttl = defaultCatalogResolverTTL
	}
	if now == nil {
		now = time.Now
	}
	return &CatalogResolver{
		dumpsDir:  dumpsDir,
		snapshots: snapshots,
		ttl:       ttl,
		nowFn:     now,
		cache:     map[string]platformCacheEntry{},
	}
}

// Resolve returns a SymbolSub suitable for adapter.FetchDailyVolumeHistory
// given a (platform, baseAsset) pair. `displaySymbol` is the full
// normalised symbol from the Top30 roster (e.g. "BTC-USDT (perp)") and is
// echoed back into the SymbolSub so persisted DailyVolumeAggregate rows
// key consistently with the live CoinGecko writer.
//
// Returns ErrSymbolUnsupported (wrapped with platform + base) when the
// platform cannot serve that base, leaving the caller to skip the row.
func (r *CatalogResolver) Resolve(platform, baseAsset, displaySymbol string) (domain.SymbolSub, error) {
	base := strings.ToUpper(strings.TrimSpace(baseAsset))
	if base == "" {
		return domain.SymbolSub{}, fmt.Errorf("%w: empty base asset", ErrSymbolUnsupported)
	}
	if displaySymbol == "" {
		displaySymbol = base + "-USDT (perp)"
	}

	lookup, err := r.lookupForPlatform(platform)
	if err != nil {
		return domain.SymbolSub{}, err
	}

	if lookup.conventionOnly {
		sub, ok := synthesizeConvention(platform, base)
		if !ok {
			return domain.SymbolSub{}, fmt.Errorf("%w: %s %s", ErrSymbolUnsupported, platform, base)
		}
		sub.DisplaySymbol = displaySymbol
		sub.BaseAsset = base
		return sub, nil
	}

	sub, ok := lookup.entries[base]
	if !ok {
		return domain.SymbolSub{}, fmt.Errorf("%w: %s %s", ErrSymbolUnsupported, platform, base)
	}
	sub.DisplaySymbol = displaySymbol
	sub.BaseAsset = base
	return sub, nil
}

// lookupForPlatform returns the cached entry when it is still within
// the TTL window; otherwise it (re)loads the lookup via the DB-first
// path with file fallback.
func (r *CatalogResolver) lookupForPlatform(platform string) (platformLookup, error) {
	r.mu.Lock()
	entry, ok := r.cache[platform]
	now := r.nowFn()
	if ok && now.Sub(entry.loadedAt) < r.ttl {
		r.mu.Unlock()
		return entry.lookup, nil
	}
	r.mu.Unlock()

	loaded, err := r.loadPlatform(platform)
	if err != nil {
		return platformLookup{}, err
	}
	r.mu.Lock()
	r.cache[platform] = platformCacheEntry{lookup: loaded, loadedAt: now}
	r.mu.Unlock()
	return loaded, nil
}

// loadPlatform dispatches to per-platform loaders. For the 4 DB-first
// platforms it consults the SnapshotReader first and falls back to
// the file dump when (a) no reader is wired, (b) the reader returns
// an error, or (c) the reader returns zero rows. Convention-only
// platforms never consult the reader so the DB stays untouched for
// the 6 platforms whose api_symbol is fully synthesizable.
func (r *CatalogResolver) loadPlatform(platform string) (platformLookup, error) {
	switch platform {
	case "binance", "okx", "bybit", "bitget", "bingx", "mexc":
		return platformLookup{conventionOnly: true}, nil
	case "hyperliquid":
		if lookup, ok := r.loadFromSnapshots(platform, decodeHyperliquidSnapshot); ok {
			return lookup, nil
		}
		return r.loadHyperliquid()
	case "gate":
		if lookup, ok := r.loadFromSnapshots(platform, decodeGateSnapshot); ok {
			return lookup, nil
		}
		return r.loadGate()
	case "lighter":
		if lookup, ok := r.loadFromSnapshots(platform, decodeLighterSnapshot); ok {
			return lookup, nil
		}
		return r.loadLighter()
	case "edgeX":
		if lookup, ok := r.loadFromSnapshots(platform, decodeEdgeXSnapshot); ok {
			return lookup, nil
		}
		return r.loadEdgeX()
	default:
		return platformLookup{}, fmt.Errorf("%w: unknown platform %q", ErrSymbolUnsupported, platform)
	}
}

// loadFromSnapshots is the DB-first scaffolding used by all 4
// DB-first platforms. The decoder is responsible for turning a row
// into a SymbolSub; returning ok=false on any single row is fine
// because the caller is the file fallback.
//
// The function returns ok=true ONLY when at least one row was
// successfully decoded so the caller distinguishes "DB up but no
// rows yet for this platform" (fall back to file) from "DB had data
// (use it)".
func (r *CatalogResolver) loadFromSnapshots(platform string, decode func(SnapshotRow) (string, domain.SymbolSub, bool)) (platformLookup, bool) {
	if r.snapshots == nil {
		return platformLookup{}, false
	}
	rows, err := r.snapshots.ListLatestInstrumentSnapshotsByPlatform(context.Background(), platform)
	if err != nil || len(rows) == 0 {
		return platformLookup{}, false
	}
	out := make(map[string]domain.SymbolSub, len(rows))
	for _, row := range rows {
		base, sub, ok := decode(row)
		if !ok {
			continue
		}
		out[base] = sub
	}
	if len(out) == 0 {
		return platformLookup{}, false
	}
	return platformLookup{entries: out}, true
}

// decodeHyperliquidSnapshot turns one hyperliquid snapshot row into a
// SymbolSub. api_symbol is preserved verbatim because the
// candleSnapshot endpoint accepts case-sensitive coin names (kPEPE,
// xyz:CL, ...).
func decodeHyperliquidSnapshot(row SnapshotRow) (string, domain.SymbolSub, bool) {
	base := strings.ToUpper(strings.TrimSpace(row.BaseAsset))
	if base == "" {
		return "", domain.SymbolSub{}, false
	}
	apiSymbol := strings.TrimSpace(row.APISymbol)
	if apiSymbol == "" {
		apiSymbol = row.BaseAsset
	}
	return base, domain.SymbolSub{
		Platform:  "hyperliquid",
		Canonical: base,
		APISymbol: apiSymbol,
	}, true
}

// decodeGateSnapshot extracts quanto_multiplier from raw_json. A
// snapshot without quanto_multiplier is unusable for USD conversion,
// so we skip it (the file fallback may have it).
func decodeGateSnapshot(row SnapshotRow) (string, domain.SymbolSub, bool) {
	base := strings.ToUpper(strings.TrimSpace(row.BaseAsset))
	if base == "" {
		return "", domain.SymbolSub{}, false
	}
	var raw struct {
		QuantoMultiplier string `json:"quanto_multiplier"`
	}
	if len(row.RawJSON) > 0 {
		_ = json.Unmarshal(row.RawJSON, &raw)
	}
	mult := parseFloatOrZero(raw.QuantoMultiplier)
	if mult <= 0 {
		return "", domain.SymbolSub{}, false
	}
	return base, domain.SymbolSub{
		Platform:         "gate",
		Canonical:        base,
		APISymbol:        strings.ToUpper(strings.TrimSpace(row.APISymbol)),
		QuantoMultiplier: mult,
	}, true
}

// decodeLighterSnapshot uses api_market_id (the normalizer writes the
// market_id integer there as a string).
func decodeLighterSnapshot(row SnapshotRow) (string, domain.SymbolSub, bool) {
	base := strings.ToUpper(strings.TrimSpace(row.BaseAsset))
	if base == "" {
		return "", domain.SymbolSub{}, false
	}
	if strings.TrimSpace(row.APIMarketID) == "" {
		return "", domain.SymbolSub{}, false
	}
	var mid int
	if _, err := fmt.Sscanf(strings.TrimSpace(row.APIMarketID), "%d", &mid); err != nil {
		return "", domain.SymbolSub{}, false
	}
	return base, domain.SymbolSub{
		Platform:  "lighter",
		Canonical: base,
		APISymbol: strings.ToUpper(strings.TrimSpace(row.BaseAsset)),
		MarketID:  &mid,
	}, true
}

// decodeEdgeXSnapshot extracts contractId from raw_json.
func decodeEdgeXSnapshot(row SnapshotRow) (string, domain.SymbolSub, bool) {
	base := strings.ToUpper(strings.TrimSpace(row.BaseAsset))
	if base == "" {
		return "", domain.SymbolSub{}, false
	}
	var raw struct {
		ContractID   string `json:"contractId"`
		ContractName string `json:"contractName"`
	}
	if len(row.RawJSON) > 0 {
		_ = json.Unmarshal(row.RawJSON, &raw)
	}
	if strings.TrimSpace(raw.ContractID) == "" {
		return "", domain.SymbolSub{}, false
	}
	apiSymbol := strings.TrimSpace(raw.ContractName)
	if apiSymbol == "" {
		apiSymbol = strings.TrimSpace(row.APISymbol)
	}
	return base, domain.SymbolSub{
		Platform:   "edgeX",
		Canonical:  base,
		APISymbol:  apiSymbol,
		ContractID: raw.ContractID,
	}, true
}

// synthesizeConvention builds a SymbolSub for convention-only platforms.
// The api_symbol formats match each exchange's USDT-margined linear perp
// naming, verified against the existing adapter.history endpoints and the
// raw-instruments dumps.
func synthesizeConvention(platform, base string) (domain.SymbolSub, bool) {
	switch platform {
	case "binance":
		return domain.SymbolSub{Platform: "binance", Canonical: base, APISymbol: base + "USDT"}, true
	case "okx":
		return domain.SymbolSub{Platform: "okx", Canonical: base, APISymbol: base + "-USDT-SWAP"}, true
	case "bybit":
		return domain.SymbolSub{Platform: "bybit", Canonical: base, APISymbol: base + "USDT"}, true
	case "bitget":
		return domain.SymbolSub{Platform: "bitget", Canonical: base, APISymbol: base + "USDT"}, true
	case "bingx":
		return domain.SymbolSub{Platform: "bingx", Canonical: base, APISymbol: base + "-USDT"}, true
	case "mexc":
		return domain.SymbolSub{Platform: "mexc", Canonical: base, APISymbol: base + "_USDT"}, true
	}
	return domain.SymbolSub{}, false
}

// loadGate reads gate-futures-usdt/<latest>.json and extracts the
// quanto_multiplier for every USDT-margined contract. The list is the raw
// /api/v4/futures/usdt/contracts response (a JSON array at top level).
func (r *CatalogResolver) loadGate() (platformLookup, error) {
	path, err := latestDump(r.dumpsDir, "gate-futures-usdt")
	if err != nil {
		return platformLookup{}, err
	}
	var rows []struct {
		Name             string `json:"name"`
		QuantoMultiplier string `json:"quanto_multiplier"`
		InDelisting      bool   `json:"in_delisting"`
	}
	if err := readJSON(path, &rows); err != nil {
		return platformLookup{}, err
	}
	out := make(map[string]domain.SymbolSub, len(rows))
	for _, row := range rows {
		if row.InDelisting {
			continue
		}
		name := strings.ToUpper(row.Name)
		if !strings.HasSuffix(name, "_USDT") {
			continue
		}
		base := strings.TrimSuffix(name, "_USDT")
		mult := parseFloatOrZero(row.QuantoMultiplier)
		if mult <= 0 {
			continue
		}
		out[base] = domain.SymbolSub{
			Platform:         "gate",
			Canonical:        base,
			APISymbol:        name,
			QuantoMultiplier: mult,
		}
	}
	return platformLookup{entries: out}, nil
}

// loadLighter reads lighter-perp/<latest>.json and extracts market_id from
// the order_book_details array.
func (r *CatalogResolver) loadLighter() (platformLookup, error) {
	path, err := latestDump(r.dumpsDir, "lighter-perp")
	if err != nil {
		return platformLookup{}, err
	}
	var payload struct {
		OrderBookDetails []struct {
			Symbol   string `json:"symbol"`
			MarketID int    `json:"market_id"`
		} `json:"order_book_details"`
	}
	if err := readJSON(path, &payload); err != nil {
		return platformLookup{}, err
	}
	out := make(map[string]domain.SymbolSub, len(payload.OrderBookDetails))
	for _, row := range payload.OrderBookDetails {
		base := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if base == "" {
			continue
		}
		mid := row.MarketID
		out[base] = domain.SymbolSub{
			Platform:  "lighter",
			Canonical: base,
			APISymbol: base,
			MarketID:  &mid,
		}
	}
	return platformLookup{entries: out}, nil
}

// loadHyperliquid reads hyperliquid-perp/<latest>.json universe list. The
// dump is a 2-element JSON array; index 0 carries the metadata block we
// need (`universe`), index 1 is the live mids snapshot we ignore.
//
// In addition to the main perp universe, this loader also scans for any
// `hyperliquid-perpdex-<name>` dump directories produced by
// build-catalog and merges their HIP-3 builder-deployed dex universes
// (e.g. `xyz:CL`, `xyz:SP500`) into the same lookup map. The dex-prefixed
// coin symbols are stored as-is (lowercase prefix) in api_symbol because
// Hyperliquid's `candleSnapshot` endpoint accepts the prefixed coin
// directly. The map key is uppercase to match baseAssetFromSymbol's
// output for Top30 rows like "XYZ:CL-USD (perp)".
func (r *CatalogResolver) loadHyperliquid() (platformLookup, error) {
	path, err := latestDump(r.dumpsDir, "hyperliquid-perp")
	if err != nil {
		return platformLookup{}, err
	}
	var dump []json.RawMessage
	if err := readJSON(path, &dump); err != nil {
		return platformLookup{}, err
	}
	if len(dump) == 0 {
		return platformLookup{}, fmt.Errorf("%w: hyperliquid dump empty", ErrSymbolUnsupported)
	}
	var meta struct {
		Universe []struct {
			Name string `json:"name"`
		} `json:"universe"`
	}
	if err := json.Unmarshal(dump[0], &meta); err != nil {
		return platformLookup{}, fmt.Errorf("decode hyperliquid universe: %w", err)
	}
	out := make(map[string]domain.SymbolSub, len(meta.Universe))
	for _, row := range meta.Universe {
		base := strings.ToUpper(strings.TrimSpace(row.Name))
		if base == "" {
			continue
		}
		out[base] = domain.SymbolSub{
			Platform:  "hyperliquid",
			Canonical: base,
			APISymbol: row.Name,
		}
	}
	r.mergeHyperliquidPerpDexes(out)
	return platformLookup{entries: out}, nil
}

// mergeHyperliquidPerpDexes scans dumpsDir for any directory matching
// `hyperliquid-perpdex-<name>` and folds each dex's universe into out.
// Missing or malformed dumps are tolerated silently — the main perp
// universe is the only required source; dex coverage is best-effort.
func (r *CatalogResolver) mergeHyperliquidPerpDexes(out map[string]domain.SymbolSub) {
	entries, err := os.ReadDir(r.dumpsDir)
	if err != nil {
		return
	}
	const prefix = "hyperliquid-perpdex-"
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		path, err := latestDump(r.dumpsDir, e.Name())
		if err != nil {
			continue
		}
		var dexMeta struct {
			Universe []struct {
				Name string `json:"name"`
			} `json:"universe"`
		}
		if err := readJSON(path, &dexMeta); err != nil {
			continue
		}
		for _, row := range dexMeta.Universe {
			apiSymbol := strings.TrimSpace(row.Name)
			if apiSymbol == "" {
				continue
			}
			base := strings.ToUpper(apiSymbol)
			out[base] = domain.SymbolSub{
				Platform:  "hyperliquid",
				Canonical: base,
				APISymbol: apiSymbol,
			}
		}
	}
}

// loadEdgeX reads edgeX-perp-v1/<latest>.json and joins coinList →
// baseCoinId → contractList to expose `contract_id` per base. Contracts
// are skipped when enableTrade=false so we never try to back-fill a
// delisted product.
func (r *CatalogResolver) loadEdgeX() (platformLookup, error) {
	path, err := latestDump(r.dumpsDir, "edgeX-perp-v1")
	if err != nil {
		return platformLookup{}, err
	}
	var payload struct {
		Data struct {
			CoinList []struct {
				CoinID   string `json:"coinId"`
				CoinName string `json:"coinName"`
			} `json:"coinList"`
			ContractList []struct {
				BaseCoinID    string `json:"baseCoinId"`
				ContractID    string `json:"contractId"`
				ContractName  string `json:"contractName"`
				EnableTrade   bool   `json:"enableTrade"`
				EnableDisplay bool   `json:"enableDisplay"`
			} `json:"contractList"`
		} `json:"data"`
	}
	if err := readJSON(path, &payload); err != nil {
		return platformLookup{}, err
	}
	baseByCoinID := make(map[string]string, len(payload.Data.CoinList))
	for _, c := range payload.Data.CoinList {
		baseByCoinID[c.CoinID] = strings.ToUpper(c.CoinName)
	}
	out := make(map[string]domain.SymbolSub, len(payload.Data.ContractList))
	for _, c := range payload.Data.ContractList {
		if !c.EnableTrade {
			continue
		}
		base, ok := baseByCoinID[c.BaseCoinID]
		if !ok || base == "" {
			continue
		}
		out[base] = domain.SymbolSub{
			Platform:   "edgeX",
			Canonical:  base,
			APISymbol:  c.ContractName,
			ContractID: c.ContractID,
		}
	}
	return platformLookup{entries: out}, nil
}

// latestDump returns the most recent <date>.json under
// <dumpsDir>/<surface>/. The lexical max works because filenames are
// YYYY-MM-DD.json and `make catalog` writes one per regeneration.
func latestDump(dumpsDir, surface string) (string, error) {
	dir := filepath.Join(dumpsDir, surface)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%w: read %s: %v", ErrSymbolUnsupported, dir, err)
	}
	var latest string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if name > latest {
			latest = name
		}
	}
	if latest == "" {
		return "", fmt.Errorf("%w: no dump under %s", ErrSymbolUnsupported, dir)
	}
	return filepath.Join(dir, latest), nil
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return json.Unmarshal(data, dst)
}

func parseFloatOrZero(s string) float64 {
	if s == "" {
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &f); err != nil {
		return 0
	}
	return f
}
