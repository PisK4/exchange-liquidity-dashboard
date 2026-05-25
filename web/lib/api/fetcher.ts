import { loadCached, saveCached } from './cache';

const SERVER_API_BASE = process.env.SERVER_API_BASE ?? process.env.NEXT_PUBLIC_API_BASE ?? 'http://127.0.0.1:8080';
const BROWSER_API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? '';

function apiBase(): string {
  return typeof window === 'undefined' ? SERVER_API_BASE : BROWSER_API_BASE;
}

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${apiBase()}${path}`, { cache: 'no-store' });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export async function getJSONWithFallback<T>(path: string, key: string = path): Promise<T> {
  try {
    const data = await getJSON<T>(path);
    saveCached(key, data);
    return data;
  } catch (err) {
    const cached = loadCached<T>(key);
    if (cached !== null) return cached;
    throw err;
  }
}
