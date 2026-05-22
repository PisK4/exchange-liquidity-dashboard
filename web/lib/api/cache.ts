// Bump CACHE_SCHEMA_VERSION whenever an API response shape changes in a way
// that older cached envelopes can no longer be safely rendered (new required
// fields, renamed keys, removed enums, etc.). Bumping invalidates every
// existing localStorage entry under the previous namespace, which is the
// intended fail-safe — better an empty fallback than a crash on stale shape.
export const CACHE_SCHEMA_VERSION = 'v1';
const NAMESPACE = `edgex-dashboard:${CACHE_SCHEMA_VERSION}:`;
const TTL_MS = 24 * 60 * 60 * 1000;

type Envelope<T> = { ts: number; data: T };

export function loadCached<T>(key: string): T | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(NAMESPACE + key);
    if (!raw) return null;
    const env = JSON.parse(raw) as Envelope<T>;
    if (typeof env?.ts !== 'number') return null;
    if (Date.now() - env.ts > TTL_MS) return null;
    return env.data;
  } catch {
    return null;
  }
}

export function saveCached<T>(key: string, data: T): void {
  if (typeof window === 'undefined') return;
  try {
    const payload: Envelope<T> = { ts: Date.now(), data };
    window.localStorage.setItem(NAMESPACE + key, JSON.stringify(payload));
  } catch {
    // quota exceeded, private mode, or non-serializable payload — swallow silently.
  }
}
