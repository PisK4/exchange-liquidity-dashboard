package listing

import "sync"

// MetricRecorder is the narrow sink the listing agent uses to emit
// counter increments. The production wiring is intentionally a
// no-op (the dashboard does not yet ship a metrics scraper); tests
// supply InMemoryMetrics so the assertions can verify a counter
// fired without standing up prometheus. New counters MUST go through
// this interface so swapping in a real prometheus registry later is
// a one-file change rather than a code-wide refactor.
type MetricRecorder interface {
	Inc(name string, labels ...string)
	Add(name string, delta float64, labels ...string)
}

// NopMetrics is the default sink — every dependency that takes a
// MetricRecorder accepts it so production wiring can pass NopMetrics{}
// without affecting behaviour.
type NopMetrics struct{}

func (NopMetrics) Inc(string, ...string)            {}
func (NopMetrics) Add(string, float64, ...string)   {}

// InMemoryMetrics is the test sink. It records every Inc/Add call so
// assertions like "listed_universe_shrink_fallback_total fired once
// with platform=edgeX" stay readable.
type InMemoryMetrics struct {
	mu       sync.Mutex
	Counters map[string]float64
	Labels   map[string][][]string
}

// NewInMemoryMetrics returns a freshly initialised sink.
func NewInMemoryMetrics() *InMemoryMetrics {
	return &InMemoryMetrics{
		Counters: map[string]float64{},
		Labels:   map[string][][]string{},
	}
}

// Inc records a unit increment with the supplied label values.
func (m *InMemoryMetrics) Inc(name string, labels ...string) {
	m.Add(name, 1, labels...)
}

// Add records a delta with the supplied label values.
func (m *InMemoryMetrics) Add(name string, delta float64, labels ...string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := metricKey(name, labels)
	m.Counters[key] += delta
	m.Labels[name] = append(m.Labels[name], append([]string(nil), labels...))
}

// Value returns the cumulative counter value for name plus the
// supplied label values. Returns 0 when the counter never fired.
func (m *InMemoryMetrics) Value(name string, labels ...string) float64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Counters[metricKey(name, labels)]
}

func metricKey(name string, labels []string) string {
	key := name
	for _, l := range labels {
		key += "|" + l
	}
	return key
}
