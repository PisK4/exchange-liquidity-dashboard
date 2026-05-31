// Package fetcher owns the HTTP layer that bridges the per-platform
// instrument normalizers (internal/listing/instrument) and the
// announcement parsers (internal/listing/announcement) to the actual
// CEX REST + CMS endpoints. Each platform gets one small fetcher
// closure that drives `RunInstrumentPoll` / `RunAnnouncementPoll`
// via the listing engine's Source slices.
//
// Design contract:
//
//   - Fetchers MUST be pure (no DB, no shared state). They take an
//     http.Client and produce either []NormalizedInstrument (for
//     instrument sources) or []json.RawMessage (for announcement
//     sources). Persistence is the driver's responsibility.
//
//   - Fetchers MUST be context-aware: every outbound request is
//     constructed with ctx so a slow upstream can be cancelled by
//     the source-health wrapper without leaking goroutines.
//
//   - Fetchers MUST surface upstream schema changes via the
//     listing/instrument.SchemaDriftError or
//     listing/announcement.SchemaDriftError types so the poller can
//     bump t_listing_source_state.schema_drift_count and trip
//     disabled_until once the drift threshold is reached.
//
//   - Fetchers MUST NOT swallow non-2xx responses — they map to an
//     ordinary error so PollWithSourceHealth can classify them as
//     transient and retry on the next tick.
package fetcher

import (
	"net/http"
	"strings"
	"time"
)

// DefaultUserAgent identifies traffic from this binary in upstream
// access logs and helps operators triage incidents. CEX CMS endpoints
// (Binance, Bybit, Bitget) often reject requests with the default Go
// `Go-http-client/2.0` UA, so every fetcher must use one of these
// helpers (or set its own) when constructing requests.
const DefaultUserAgent = "edgex-dashboard/listing-fetcher (+https://edgex.exchange)"

// DefaultRequestTimeout bounds the wait for a single fetch round
// trip. Cold-start ticks call into multiple sources serially; a
// runaway upstream on one of them MUST NOT stall the whole engine
// tick for more than this duration.
const DefaultRequestTimeout = 20 * time.Second

// HTTPDeps is the shared transport surface every fetcher closure
// closes over. Production main.go builds one HTTPDeps (with the
// exchange proxy applied) and passes it to BuildListingSources;
// tests inject a per-test client pointed at an httptest server.
type HTTPDeps struct {
	// Client is the *http.Client used for all upstream calls. It
	// MUST have a non-zero Timeout (NewHTTPClient sets one) so a
	// hung CEX cannot starve the listing engine tick.
	Client *http.Client
	// UserAgent overrides the User-Agent header on every outbound
	// request. Leave blank to inherit DefaultUserAgent.
	UserAgent string
}

// effectiveUserAgent returns the UA the fetcher should send. A
// caller-supplied value wins; otherwise the package default applies.
func (d HTTPDeps) effectiveUserAgent() string {
	if ua := strings.TrimSpace(d.UserAgent); ua != "" {
		return ua
	}
	return DefaultUserAgent
}
