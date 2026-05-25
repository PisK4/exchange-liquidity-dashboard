'use client';

// Anything dashboard-client / fetcher / watchlist writes to localStorage
// is namespaced under "edgex-dashboard". When the operator lands on this
// error boundary because of a stale watchlist (e.g. ?watchlist= points
// at a delisted canonical) Retry alone loops: the URL + persisted
// watchlist replay the same bad fetch. resetAndGoHome wipes everything
// under the namespace and hard-navigates to "/" so the page reloads
// with a clean state and re-seeds the default canonical.
const STORAGE_PREFIX = 'edgex-dashboard';

function resetAndGoHome() {
  try {
    const keys = Object.keys(window.localStorage);
    for (const k of keys) {
      if (k.startsWith(STORAGE_PREFIX)) {
        window.localStorage.removeItem(k);
      }
    }
  } catch {
    // localStorage may be unavailable (private mode, quota); fall through
    // to the navigation so the user at least leaves the broken URL.
  }
  window.location.href = '/';
}

export default function Error({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <section className="panel span-12" style={{ padding: 16 }}>
      <h2>API error</h2>
      <p className="error">{error.message}</p>
      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button className="control" onClick={reset}>Retry</button>
        <button className="control" onClick={resetAndGoHome}>回到首页 (重置状态)</button>
      </div>
    </section>
  );
}
