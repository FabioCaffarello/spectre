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

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/operators/control-plane/api/v1alpha2"
)

// Metrics holds the operator-side counters / gauges ADR-0031 §5.2
// mandates. controller-runtime's default `controller_runtime_*`
// metrics remain untouched — these three add the
// `spectre_operator_*` surface on top.
type Metrics struct {
	// EngineDialFailuresTotal counts EngineClientRunner dial errors.
	// Incremented from the reconciler when a `runner.Run` returns a
	// dial-level failure (network unreachable, deadline exceeded,
	// engine pod not yet serving). Per-Reconcile failures of other
	// kinds (Postgres, stream-level) do NOT increment this counter
	// — the label is specifically "could not establish the gRPC
	// channel".
	EngineDialFailuresTotal prometheus.Counter
}

// Register registers the three §5.2 metric series with `registry`.
// `cacheReader` provides the source for the `spectre_operator_
// scrapejobs_total{phase}` gauge collector: on every Prometheus
// scrape the collector lists ScrapeJobs through the cache and
// re-counts by `Status.Phase`, so the gauge always reflects the
// real cluster state rather than tracking phase transitions in
// memory (which would lose accuracy across operator restarts).
//
// `spectre_operator_scrapebatches_total{phase}` (ADR-0031 §5.2's
// third Wave-6+ deferral) is intentionally NOT registered here —
// emitting a permanently-empty gauge clutters scrape output. It
// will register alongside the ScrapeBatch CRD landing in Wave 6
// per ADR-0033.
func Register(registry prometheus.Registerer, cacheReader client.Reader) (*Metrics, error) {
	m := &Metrics{
		EngineDialFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "spectre_operator_engine_dial_failures_total",
			Help: "Total number of EngineClientRunner dial failures during reconciliation.",
		}),
	}
	if err := registry.Register(m.EngineDialFailuresTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(newScrapeJobPhaseCollector(cacheReader)); err != nil {
		return nil, err
	}
	return m, nil
}

// scrapeJobPhaseCollector is a prometheus.Collector that lists
// ScrapeJobs from the controller cache on every scrape and emits
// one gauge value per known phase. Listing through the cache is
// cheap (in-memory informer cache; no apiserver round-trip).
type scrapeJobPhaseCollector struct {
	cacheReader client.Reader
	desc        *prometheus.Desc
}

func newScrapeJobPhaseCollector(cacheReader client.Reader) *scrapeJobPhaseCollector {
	return &scrapeJobPhaseCollector{
		cacheReader: cacheReader,
		desc: prometheus.NewDesc(
			"spectre_operator_scrapejobs_total",
			"Total number of ScrapeJob resources by status phase.",
			[]string{"phase"},
			nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *scrapeJobPhaseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

// Collect implements prometheus.Collector. The phase enumeration is
// fixed at the ADR-0019 §4 state machine — emitting zero for unseen
// phases preserves the label cardinality stability scrape consumers
// (Prometheus / Grafana) expect.
func (c *scrapeJobPhaseCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCollectTimeout)
	defer cancel()
	var list spectrev1alpha2.ScrapeJobList
	if err := c.cacheReader.List(ctx, &list); err != nil {
		// Cache list failures are rare (informer is in-memory); when
		// they happen we emit zeros so the scrape doesn't fail
		// outright. Prometheus surfaces the gap via the absence of
		// the metric on the next successful scrape.
		return
	}
	counts := map[string]float64{}
	for i := range list.Items {
		phase := string(list.Items[i].Status.Phase)
		if phase == "" {
			// An uninitialised phase counts under `Pending` per
			// ADR-0019 §4 (the reconciler stamps `Pending` on first
			// observation; the brief window before that is
			// effectively pending).
			phase = string(spectrev1alpha2.ScrapeJobPhasePending)
		}
		counts[phase]++
	}
	for _, phase := range knownPhases {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, counts[phase], phase)
	}
}

var knownPhases = []string{
	string(spectrev1alpha2.ScrapeJobPhasePending),
	string(spectrev1alpha2.ScrapeJobPhaseRunning),
	string(spectrev1alpha2.ScrapeJobPhaseCompleted),
	string(spectrev1alpha2.ScrapeJobPhaseFailed),
}

// defaultCollectTimeout bounds the cache List call so a degenerate
// cache cannot stall the Prometheus scrape window.
const defaultCollectTimeout = 2 * 1000 * 1000 * 1000 // 2 seconds in nanoseconds
