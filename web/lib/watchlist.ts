export const WATCHLIST_STORAGE_KEY = 'edgex-ops-intelligence:watchlist:v1';

// V1 caps the list at 10 entries because the toolbar chips render
// horizontally and the underlying /api/snapshot/liquidity endpoint has no
// batch variant — fan-out beyond ~10 concurrent requests starts to
// noticeably degrade first paint on the operator's machine and would
// push CoinGecko's Demo-tier budget toward saturation on every poll
// cycle. Raising this needs a batch endpoint and a corresponding bump
// to the upstream rate limits.
export const MAX_WATCHLIST = 10;

// When the user empties their watchlist we want a non-broken default
// rather than a blank panel. BTC is the most universally tracked symbol
// across all 10 venues we cover, so it is the safest fallback and
// matches the spec's decision E (empty watchlist → fallback default
// symbol). The fallback name MUST be a canonical short ticker that the
// backend's Store.ResolveSymbol can map to a display_symbol.
export const WATCHLIST_DEFAULT_FALLBACK = 'BTC';

const URL_WATCHLIST_PARAM = 'watchlist';
const URL_SYMBOL_PARAM = 'symbol';

// normalizeSymbol uppercases and trims. The frontend stores canonical
// tickers (BTC / ETH / SOL), not the long-form 'BTC-USDT (perp)', because
// the URL query param has to round-trip through human-shared links
// without URL-encoding becoming a UX hazard, and the backend resolver
// happily accepts both forms.
export function normalizeSymbol(symbol: string): string {
  return symbol.trim().toUpperCase();
}

function dedupeAndCap(items: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of items) {
    const sym = normalizeSymbol(raw);
    if (!sym) continue;
    if (seen.has(sym)) continue;
    seen.add(sym);
    out.push(sym);
    if (out.length >= MAX_WATCHLIST) break;
  }
  return out;
}

// parseURLWatchlist extracts the watchlist from a URL query string in a
// way that is safe to call in SSR (no window access). Splits on ',' and
// runs the result through dedupeAndCap so the SSR render matches the
// CSR rules exactly. Empty / undefined input → empty array; callers
// decide the fallback policy.
export function parseURLWatchlist(raw: string | undefined | null): string[] {
  if (!raw) return [];
  return dedupeAndCap(raw.split(','));
}

// formatWatchlistParam turns a watchlist back into the canonical URL
// param value. Returns null when the list is empty so callers can use it
// to drive replaceState's "remove this param" branch.
export function formatWatchlistParam(items: string[]): string | null {
  const normalized = dedupeAndCap(items);
  if (normalized.length === 0) return null;
  return normalized.join(',');
}

// loadFromLocalStorage reads and parses the persisted watchlist. Safely
// handles SSR (returns null), missing key (returns null), and corrupted
// JSON (returns null and quietly removes the bad value so the next
// session starts clean). The caller distinguishes null (we don't know
// what the user picked) from [] (the user explicitly emptied it).
export function loadFromLocalStorage(): string[] | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(WATCHLIST_STORAGE_KEY);
    if (raw === null) return null;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      window.localStorage.removeItem(WATCHLIST_STORAGE_KEY);
      return null;
    }
    return dedupeAndCap(parsed.filter((x): x is string => typeof x === 'string'));
  } catch {
    try {
      window.localStorage.removeItem(WATCHLIST_STORAGE_KEY);
    } catch {
      // localStorage may be disabled in private mode; nothing to do.
    }
    return null;
  }
}

// saveToLocalStorage writes the canonical watchlist. Empty input clears
// the key entirely so loadFromLocalStorage returns null next time and
// the resolver falls back through to its default. Wrapped in try/catch
// because Safari private mode and storage quota exhaustion both throw
// from setItem.
export function saveToLocalStorage(items: string[]): void {
  if (typeof window === 'undefined') return;
  try {
    if (items.length === 0) {
      window.localStorage.removeItem(WATCHLIST_STORAGE_KEY);
      return;
    }
    window.localStorage.setItem(WATCHLIST_STORAGE_KEY, JSON.stringify(dedupeAndCap(items)));
  } catch {
    // Storage write failure is non-fatal for the dashboard; the URL is
    // the canonical source of truth for the current session.
  }
}

// resolveWatchlist applies the spec's tri-source precedence:
//
//   URL  >  localStorage  >  [defaultSymbol]
//
// SSR callers MUST pass localStorage = null (we cannot read it server-
// side without a hydration mismatch). On CSR the dashboard reads
// localStorage in a useEffect and re-resolves once the value is known;
// resolveURLWasExplicit gives the consumer the signal needed to decide
// whether the URL should be rewritten via replaceState.
export function resolveWatchlist(args: {
  urlParam: string | undefined | null;
  localStorage: string[] | null;
  defaultSymbol: string;
}): { items: string[]; source: 'url' | 'storage' | 'default' } {
  const fromURL = parseURLWatchlist(args.urlParam);
  if (fromURL.length > 0) {
    return { items: fromURL, source: 'url' };
  }
  if (args.localStorage && args.localStorage.length > 0) {
    return { items: dedupeAndCap(args.localStorage), source: 'storage' };
  }
  return { items: [normalizeSymbol(args.defaultSymbol)], source: 'default' };
}

// addSymbol returns a new array with `symbol` appended (deduped, capped).
// The existing order is preserved so the operator's mental model of
// "newest chip appears on the right" is honoured.
export function addSymbol(items: string[], symbol: string): string[] {
  return dedupeAndCap([...items, symbol]);
}

// removeSymbol returns a new array with `symbol` filtered out. Comparison
// is case-insensitive so the toolbar can pass either the displayed
// uppercase chip text or the raw user-typed value.
export function removeSymbol(items: string[], symbol: string): string[] {
  const target = normalizeSymbol(symbol);
  return items.filter((s) => normalizeSymbol(s) !== target);
}

// applyURLState performs the replaceState side-effect with the canonical
// URL shape. Pulled out so unit tests can assert the param mutation
// without involving the actual browser History API.
//
// When `opts.syncSymbol` is true AND items collapses to a single chip,
// this also rewrites ?symbol= so the headline fetch in DashboardClient
// (which is keyed on query.symbol) lines up with the lone chip. Without
// this coupling the V1 detail panels would render data for whichever
// symbol was last in ?symbol= — e.g. the default 'BTC' — even though
// the toolbar chip shows the user-picked symbol.
//
// `syncSymbol` defaults to false because the post-mount hydration call
// (which restores a localStorage watchlist on an otherwise bare URL)
// must NOT overwrite a legitimate deep-link ?symbol= value: doing so
// would break ?symbol=BTC-USDT (perp) style legacy bookmarks (case
// re-normalization), and would let stale localStorage leak across test
// runs that deep-link to a different headline symbol. The side-effect
// bus inside DashboardClient passes syncSymbol=true because that path
// is reached only when the watchlist mutates from a user action (chip
// add/remove, WatchlistCard expand), which IS the right moment to
// align ?symbol= with the lone chip.
//
// Multi-chip mode always leaves ?symbol= untouched because there is no
// "headline symbol" in that mode and preserving the prior value keeps
// category-pill / dropdown deep-links roundtripping correctly.
export function applyURLState(items: string[], opts: { syncSymbol?: boolean } = {}): void {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  const value = formatWatchlistParam(items);
  if (value === null) {
    url.searchParams.delete(URL_WATCHLIST_PARAM);
  } else {
    url.searchParams.set(URL_WATCHLIST_PARAM, value);
  }
  if (opts.syncSymbol && items.length === 1) {
    url.searchParams.set(URL_SYMBOL_PARAM, normalizeSymbol(items[0]));
  }
  // Strip a trailing '?' so empty-query URLs render cleanly.
  const search = url.searchParams.toString();
  const next = url.pathname + (search ? `?${search}` : '') + url.hash;
  window.history.replaceState(window.history.state, '', next);
}
