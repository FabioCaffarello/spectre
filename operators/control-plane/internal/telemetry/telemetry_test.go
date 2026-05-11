/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/operators/control-plane/api/v1alpha2"
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

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := spectrev1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1.AddToScheme: %v", err)
	}
	return scheme
}

func TestRegister_ExposesEngineDialFailuresCounter(t *testing.T) {
	registry := prometheus.NewRegistry()
	cacheReader := clientfake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	metrics, err := Register(registry, cacheReader)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	metrics.EngineDialFailuresTotal.Inc()
	metrics.EngineDialFailuresTotal.Inc()
	got := testutil.ToFloat64(metrics.EngineDialFailuresTotal)
	if got != 2 {
		t.Errorf("EngineDialFailuresTotal = %v; want 2", got)
	}
}

func TestScrapeJobPhaseCollector_CountsByPhase(t *testing.T) {
	scheme := newTestScheme(t)
	jobs := []client.Object{
		makeScrapeJob("pending-1", spectrev1alpha2.ScrapeJobPhasePending),
		makeScrapeJob("running-1", spectrev1alpha2.ScrapeJobPhaseRunning),
		makeScrapeJob("running-2", spectrev1alpha2.ScrapeJobPhaseRunning),
		makeScrapeJob("completed-1", spectrev1alpha2.ScrapeJobPhaseCompleted),
		// Phase unset → counted as Pending per the collector's
		// pre-Reconcile-stamping contract.
		makeScrapeJob("uninitialised", ""),
	}
	cacheReader := clientfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(jobs...).
		Build()

	registry := prometheus.NewRegistry()
	if _, err := Register(registry, cacheReader); err != nil {
		t.Fatalf("Register: %v", err)
	}

	expected := `
# HELP spectre_operator_scrapejobs_total Total number of ScrapeJob resources by status phase.
# TYPE spectre_operator_scrapejobs_total gauge
spectre_operator_scrapejobs_total{phase="Completed"} 1
spectre_operator_scrapejobs_total{phase="Failed"} 0
spectre_operator_scrapejobs_total{phase="Pending"} 2
spectre_operator_scrapejobs_total{phase="Running"} 2
`
	if err := testutil.GatherAndCompare(
		registry,
		strings.NewReader(expected),
		"spectre_operator_scrapejobs_total",
	); err != nil {
		t.Fatalf("unexpected metric output: %v", err)
	}
}

func makeScrapeJob(name string, phase spectrev1alpha2.ScrapeJobPhase) *spectrev1alpha2.ScrapeJob {
	return &spectrev1alpha2.ScrapeJob{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Status:     spectrev1alpha2.ScrapeJobStatus{Phase: phase},
	}
}
