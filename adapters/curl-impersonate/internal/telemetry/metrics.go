// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the adapter-side `spectre_adapter_*` counters /
// gauges / histograms ADR-0031 §5.3 mandates. The `kind` label is
// applied once at registration via `ConstLabels` so every
// observation carries it without per-call construction noise.
type Metrics struct {
	// SessionsActive — `spectre_adapter_sessions_active{kind}`.
	// Incremented on Initialize success, decremented on Close
	// success (and on per-session orphan sweeps in the
	// sessions.Manager when a stale Redis entry is reaped).
	SessionsActive prometheus.Gauge
	// InitializeDuration — `spectre_adapter_initialize_duration_seconds{kind}`.
	InitializeDuration prometheus.Histogram
	// NavigateDuration — `spectre_adapter_navigate_duration_seconds{kind,result}`.
	// `result` ∈ `{success, failure}` — `timeout` is reserved for
	// the Wave 5 circuit-breaker landing.
	NavigateDuration *prometheus.HistogramVec
	// ExtractDuration — `spectre_adapter_extract_duration_seconds{kind,result}`.
	ExtractDuration *prometheus.HistogramVec
	// CapabilityViolationsTotal —
	// `spectre_adapter_capability_violations_total{kind,capability}`.
	// Incremented when a requested capability is not in the
	// adapter's manifest.
	CapabilityViolationsTotal *prometheus.CounterVec
}

// Register constructs the §5.3 instruments with `kind=Kind` as a
// constant label and registers them on `registry`. The default
// histogram buckets match OTel's recommended seconds-scale
// bucketing for adapter-side operations (sub-millisecond → tens
// of seconds covers the Driver Protocol envelope).
func Register(registry prometheus.Registerer) (*Metrics, error) {
	constLabels := prometheus.Labels{"kind": Kind}

	m := &Metrics{
		SessionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "spectre_adapter_sessions_active",
			Help:        "Active driver sessions held by the adapter.",
			ConstLabels: constLabels,
		}),
		InitializeDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "spectre_adapter_initialize_duration_seconds",
			Help:        "Driver.Initialize RPC duration in seconds.",
			ConstLabels: constLabels,
			Buckets:     defaultDurationBuckets,
		}),
		NavigateDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "spectre_adapter_navigate_duration_seconds",
			Help:        "Driver.Navigate RPC duration in seconds.",
			ConstLabels: constLabels,
			Buckets:     defaultDurationBuckets,
		}, []string{"result"}),
		ExtractDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "spectre_adapter_extract_duration_seconds",
			Help:        "Driver.Extract RPC duration in seconds.",
			ConstLabels: constLabels,
			Buckets:     defaultDurationBuckets,
		}, []string{"result"}),
		CapabilityViolationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "spectre_adapter_capability_violations_total",
			Help:        "Initialize requests for capabilities not in the adapter manifest.",
			ConstLabels: constLabels,
		}, []string{"capability"}),
	}

	for _, c := range []prometheus.Collector{
		m.SessionsActive,
		m.InitializeDuration,
		m.NavigateDuration,
		m.ExtractDuration,
		m.CapabilityViolationsTotal,
	} {
		if err := registry.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// defaultDurationBuckets matches OTel's recommended-for-RPC
// histogram buckets (rough span: 5ms → 10s). Wider than the
// Prometheus client_golang default (5ms → 10s vs 5ms → 10s
// nominal); refined further only when real-deployment data
// demands.
var defaultDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}
