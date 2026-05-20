const NAMESPACE = 'edgex-dashboard:v1:';
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
