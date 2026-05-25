import { loadCached, saveCached } from './cache';

const SERVER_API_BASE = process.env.SERVER_API_BASE ?? process.env.NEXT_PUBLIC_API_BASE ?? 'http://127.0.0.1:8080';
const BROWSER_API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? '';

function apiBase(): string {
  return typeof window === 'undefined' ? SERVER_API_BASE : BROWSER_API_BASE;
}

export type GetJSONOptions = {
  signal?: AbortSignal;
};

export async function getJSON<T>(path: string, options: GetJSONOptions = {}): Promise<T> {
  const res = await fetch(`${apiBase()}${path}`, { cache: 'no-store', signal: options.signal });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export async function getJSONWithFallback<T>(
  path: string,
  keyOrOptions: string | GetJSONOptions = path,
  optionsArg: GetJSONOptions = {},
): Promise<T> {
  // Support the legacy 2-arg signature getJSONWithFallback(path, cacheKey)
  // and the new shape getJSONWithFallback(path, { signal }) so existing
  // callers compile without touching every call site at once. When the
  // second argument is an object we treat it as options and use the path
  // itself as the cache key.
  const key = typeof keyOrOptions === 'string' ? keyOrOptions : path;
  const options = typeof keyOrOptions === 'string' ? optionsArg : keyOrOptions;
  try {
    const data = await getJSON<T>(path, options);
    saveCached(key, data);
    return data;
  } catch (err) {
    // AbortError must propagate untouched — the cached value would be
    // stale relative to the new (still-running) request and falling
    // through to it would resurrect the cancelled state.
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw err;
    }
    const cached = loadCached<T>(key);
    if (cached !== null) return cached;
    throw err;
  }
}
