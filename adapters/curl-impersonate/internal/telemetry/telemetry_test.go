// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInit_SucceedsWithoutOtlpEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := Init(context.Background(), "test-version")
	if err != nil {
		t.Fatalf("Init: unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init: shutdown must be non-nil")
	}
	if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
		t.Fatalf("shutdown: unexpected error: %v", shutdownErr)
	}
}

func TestStripScheme_RemovesKnownPrefixes(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"http://collector:4317", "collector:4317"},
		{"https://collector:4317", "collector:4317"},
		{"collector:4317", "collector:4317"},
		{"", ""},
	} {
		if got := stripScheme(c.in); got != c.want {
			t.Errorf("stripScheme(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestRegister_StampsKindLabelAndCounters(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := Register(registry)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Bump every instrument once so it surfaces in the scrape
	// output. Prometheus omits zero-observation histograms /
	// counters by default; the gauge is exposed unconditionally.
	metrics.SessionsActive.Inc()
	metrics.InitializeDuration.Observe(0.01)
	metrics.NavigateDuration.WithLabelValues("success").Observe(0.02)
	metrics.ExtractDuration.WithLabelValues("failure").Observe(0.03)
	metrics.CapabilityViolationsTotal.WithLabelValues("screenshot").Inc()

	// Assert the canonical `kind="curl_impersonate"` const label
	// + `result=…` partition both surface end-to-end. Histogram
	// bucket lines are omitted from the expected output for
	// brevity; the `_count` lines below cover the labelled
	// observation path.
	expected := `
# HELP spectre_adapter_capability_violations_total Initialize requests for capabilities not in the adapter manifest.
# TYPE spectre_adapter_capability_violations_total counter
spectre_adapter_capability_violations_total{capability="screenshot",kind="curl_impersonate"} 1
# HELP spectre_adapter_sessions_active Active driver sessions held by the adapter.
# TYPE spectre_adapter_sessions_active gauge
spectre_adapter_sessions_active{kind="curl_impersonate"} 1
`
	if err := testutil.GatherAndCompare(
		registry,
		strings.NewReader(expected),
		"spectre_adapter_capability_violations_total",
		"spectre_adapter_sessions_active",
	); err != nil {
		t.Fatalf("metric output mismatch: %v", err)
	}
}
