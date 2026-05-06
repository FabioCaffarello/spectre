# Observability (v1alpha2)

> **Operational companion** to
> [ADR-0031](../adr/0031-observability-framework.md).
> ADR-0031 commits OpenTelemetry as the umbrella standard
> for metrics, traces, and structured logs; this document
> walks the operational surface for contributors emitting
> telemetry and operators consuming it.

## §1 — The observability stack

```
Per-service emissions                                      Deployment-side
─────────────────────────                                  ──────────────────

         ┌─── OTLP/gRPC ──────────────────────────┐
         │                                        │
       traces ───▶                                ▼
                                          ┌──────────────────┐
       metrics ──▶ (also) Prometheus ────▶│  opentelemetry-  │ ──▶ traces backend (Jaeger / Tempo / etc.)
                   /metrics scrape         │   collector      │ ──▶ metrics backend (Prometheus / Mimir / etc.)
                                          │  (chart subchart)│ ──▶ logs backend (collected from stdout)
       logs ─────▶ stdout JSON ───────────┴──────────────────┘
                   (collected by deployment-side aggregator —
                    Loki / Vector / Fluent Bit / etc.)
```

Three signals, three paths:

| Signal | Per-service emission | Resilience path | Backend |
|---|---|---|---|
| **Traces** | OTLP/gRPC to local collector | (no resilience path; traces have less hard-realtime requirements than metrics) | Jaeger / Tempo / Datadog APM / Honeycomb (deployment-side choice) |
| **Metrics** | OTLP/gRPC to local collector | Prometheus `/metrics` scrape on port 9090 | Prometheus / Mimir / VictoriaMetrics (deployment-side) |
| **Logs** | JSON lines to stdout | (none — stdout is the boundary) | Loki / Vector / Fluent Bit / OpenObserve (deployment-side) |

ADR-0031 §2 commits the choice of OpenTelemetry over
single-purpose alternatives — polyglot maturity across
Rust + Go + Python + TypeScript is the deciding factor; the
local collector is the boundary that decouples per-service
code from vendor backend choice.

## §2 — Per-service expectations

Every service in the catalog exposes the canonical surface
per ADR-0031 §3:

| Surface | Where | What it does |
|---|---|---|
| gRPC reflection | service port | `grpc.reflection.v1alpha.ServerReflection` (per ADR-0021's pattern) |
| gRPC `Health` | service port | `grpc.health.v1.Health`; Kubernetes-native `grpc:` probes dial it |
| Prometheus `/metrics` | port `9090` (uniform) | OpenMetrics format; metrics + runtime + process |
| OTLP traces | (push) | Trace context via gRPC metadata; W3C Trace Context |
| Structured logs | stdout | JSON lines with 9 mandatory fields |

The mandatory log fields per ADR-0031 §3.4:

```json
{
  "timestamp":       "2026-05-06T03:14:15.123Z",
  "level":           "INFO",
  "service":         "proxy-broker",
  "service_version": "0.5.0-alpha.0",
  "caller":          "internal/server/acquire.go:42",
  "message":         "proxy lease acquired",
  "trace_id":        "1234abcd...",
  "span_id":         "5678efgh",
  "request_id":      "req-9876",
  "job_id":          "scrape-products-2026-05-06-001",
  "tenant_id":       "tenant-a",
  "latency_ms":      42.7
}
```

Service-specific fields are additive (e.g.,
`proxy_id` from proxy-broker; `schema_ref` from
schema-registry). `latency_ms` is conditionally mandatory —
emitted on events completing a measurable operation; absent
otherwise.

Per-language OTel SDK choices per ADR-0031 §3.5 — the
canonical libraries for each ADR-0027 §3.2 language.

## §3 — Trace propagation flow

A job execution emits a **single trace** spanning every
service involved. Trace context propagates via W3C Trace
Context (`traceparent` + `tracestate` headers in gRPC
metadata).

```
trace: <trace_id>
└── span: operator.reconcile_scrapejob (root)
    └── span: engine.run_job
        ├── span: input_broker.next_url
        ├── span: proxy_broker.acquire
        ├── span: fingerprint_broker.select
        ├── span: rate_limit_broker.reserve
        ├── span: session_store.load
        ├── span: secret_broker.fetch
        ├── span: schema_registry.get
        ├── span: driver_router.pick
        ├── span: driver.navigate_query_extract
        │   └── (driver-side spans per adapter)
        ├── span: schema_registry.validate (per row)
        ├── span: enricher.enrich (per row)
        ├── span: dedup_service.is_new (per row)
        ├── span: sink.emit (per row, per sink)
        └── span: cost_tracker.emit (async; same trace)
            span: audit_log.emit (async; same trace)
```

The async emissions (cost-tracker, audit-log) propagate the
trace context but emit fire-and-forget per
[ADR-0037 §4.3](../adr/0037-engine-as-orchestrator.md);
spans complete asynchronously at the collector boundary.

Span naming follows the OTel RPC semantic convention:
`<package>.<service>/<rpc>` for protocol calls
(`spectre.proxy.v1alpha1.Proxy/Acquire`); `<service>.<op>`
for non-RPC operations (`engine.parse_dsl`).

## §4 — Metrics taxonomy

Each service emits a mandatory set per ADR-0031 §5.

### §4.1 — Engine metrics

```
spectre_engine_jobs_active                                 (gauge)
spectre_engine_jobs_completed_total{result}                (counter)
spectre_engine_step_duration_seconds                       (histogram)
spectre_engine_step_service_call_duration_seconds{service} (histogram)
spectre_engine_rows_emitted_total{sink}                    (counter)
spectre_engine_circuit_breaker_state{service,state}        (gauge)
```

### §4.2 — Operator metrics

Inherits `controller-runtime` defaults
(`controller_runtime_reconcile_total`,
`controller_runtime_reconcile_time_seconds`) plus:

```
spectre_operator_scrapejobs_total{phase}                   (gauge)
spectre_operator_scrapebatches_total{phase}                (gauge)  # Wave 6+
spectre_operator_engine_dial_failures_total                (counter)
```

### §4.3 — Adapter metrics

```
spectre_adapter_sessions_active{kind}                      (gauge)
spectre_adapter_initialize_duration_seconds{kind}          (histogram)
spectre_adapter_navigate_duration_seconds{kind,result}     (histogram)
spectre_adapter_extract_duration_seconds{kind,result}      (histogram)
spectre_adapter_capability_violations_total{kind,capability} (counter)
```

### §4.4 — Infra-service metrics (canonical pattern)

Every infra-service exposes:

```
spectre_<slot>_requests_total{rpc,result}                  (counter)
spectre_<slot>_request_duration_seconds{rpc}               (histogram)
spectre_<slot>_provider_health{provider,status}            (gauge)  # gate-A services
```

Plus service-specific metrics — e.g.:

```
spectre_proxy_broker_pool_size{provider,region}            (gauge)
spectre_schema_registry_schemas_total                      (counter)
spectre_input_broker_urls_in_state{state}                  (gauge)
spectre_cost_tracker_cost_units{provider,unit}             (counter)
```

### §4.5 — Cross-cutting metrics

Every service emits:

- **OTel-standard `process_*`** (CPU, memory, file
  descriptors)
- **Runtime metrics** — Go GC, Rust allocator, Python GIL,
  Node event loop per language-specific OTel semantic
  conventions

These are SDK-default — emitted without per-service code;
ADR-0031 §3.5 mandates they are not disabled.

## §5 — Failure categorisation workflow

Every error emits with a structured `DriverError.Code` per
[ADR-0009](../adr/0009-navigate-and-session-lifecycle.md) +
ADR-0031 §6:

| Surface | Field |
|---|---|
| Metric label | `code` (e.g., `code="PROXY_BROKER_UNAVAILABLE"`) |
| Log field | `error_code` |
| Trace span attribute | `error.code` (OTel semantic convention) |
| Operator status | `status.failureCode` (per ADR-0019's CRD shape) |

Per-service unavailability codes that ADR-0031 §6.2
enumerates extend ADR-0009's enum at the same Wave as the
service materialises:

```
PROXY_BROKER_UNAVAILABLE          (Wave 5)
CAPTCHA_UNREACHABLE               (Wave 5)
RATE_LIMIT_UNAVAILABLE            (Wave 7)
SCHEMA_REGISTRY_UNAVAILABLE       (Wave 6)
INPUT_BROKER_UNAVAILABLE          (Wave 6)
ENRICHER_UNAVAILABLE              (Wave 10)
DEDUP_UNAVAILABLE                 (Wave 10)
AUDIT_LOG_UNAVAILABLE             (Wave 9)
COST_TRACKER_UNAVAILABLE          (Wave 9)
SESSION_STORE_UNAVAILABLE         (Wave 8)
FINGERPRINT_BROKER_UNAVAILABLE    (Wave 7)
SECRET_BROKER_UNAVAILABLE         (Wave 8)
SCHEMA_VALIDATION_FAILED          (Wave 6)
SCHEMA_NOT_FOUND                  (Wave 6)
FAILURE_AFTER_FALLBACKS           (Wave 10)
WEBHOOK_DELIVERY_FAILED           (Wave 9)
```

The taxonomy is **append-only** — new codes are added per
ADR-0009 §5 evolution rules; existing codes do not change
semantics.

## §6 — Quality metrics

User pilot data quality (Wave 4) depends on extraction
**quality metrics** distinct from operational metrics. Per
ADR-0031 §8:

```
spectre_extraction_completeness_ratio{schema_ref,target}   (gauge)
spectre_schema_validation_pass_ratio{schema_ref}           (gauge)
spectre_extraction_success_ratio{target,driver}            (gauge)
spectre_dedup_collision_ratio{schema_ref}                  (gauge)
```

Quality metrics are **emitted by the engine** at the
per-row-emission boundary; aggregation lives at the
collector / Prometheus layer. The metrics surface in the
Wave 4 pilot questionnaire as quantitative companions to
the qualitative per-layer feedback.

Implementation lands at **Wave 9** alongside cost-tracker
+ audit-log. R9.3's ADR-0031 records the metric shape; Wave
9 lands the engine-side emission paths.

## §7 — Debugging workflow examples

### §7.1 — A job failed; find the cause

```
1. Get the job's trace ID:
   kubectl get scrapejob <name> -o jsonpath='{.status.traceId}'

2. Query the trace backend (Jaeger / Tempo) for trace_id:
   → reveals which service span errored

3. Query the metric for the error code:
   sum(rate(spectre_<slot>_requests_total{result!="OK",code="<CODE>"}[5m]))
   → reveals if this is a one-off or pattern

4. Query the structured logs for trace_id:
   → reveals per-service context (provider chosen, retry attempts, etc.)
```

### §7.2 — A tenant is exceeding cost budget

```
1. Query the cost-tracker rollup:
   spectre cost-tracker GetTenantRollup tenant-a
   → totals per emitter / per provider / per target

2. Drill into per-job:
   ListTenantRollups tenant-a --since 24h
   → top jobs by cost

3. Per-job ledger:
   GetJobLedger <job_id>
   → per-emission events
```

### §7.3 — Latency regression in production

```
1. Compare per-step duration across versions:
   histogram_quantile(0.95, rate(spectre_engine_step_duration_seconds_bucket[5m]))

2. Identify the slow service:
   histogram_quantile(0.95,
     rate(spectre_engine_step_service_call_duration_seconds_bucket[5m])) by (service)

3. Drill into traces:
   Filter spans where the slow service appears; inspect attributes
```

## §8 — Reference materials

### ADRs

- [ADR-0009](../adr/0009-navigate-and-session-lifecycle.md)
  — `DriverError.Code` taxonomy
- [ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
  — operator's `status.failureCode` surface
- [ADR-0021](../adr/0021-service-discovery.md) — gRPC
  reflection
- [ADR-0027](../adr/0027-sdk-strategy.md) — per-language SDK
  + OTel adoption
- [ADR-0030 §3](../adr/0030-helm-chart-structure.md) —
  Kubernetes-native `grpc:` probes
- [ADR-0031](../adr/0031-observability-framework.md) — the
  framework (source)
- [ADR-0036 §5.4](../adr/0036-microservices-catalog-expansion.md)
  — canonical observability surface this doc operationalises
- [ADR-0037 §3.2](../adr/0037-engine-as-orchestrator.md) —
  per-step orchestration emitting traces
- [ADR-0038](../adr/0038-cost-tracking-attribution.md) — cost
  emission shape consuming this framework

### Companion docs

- [`platform-architecture.md`](platform-architecture.md) §4
  — execution flow visualisation
- [`engine-orchestrator.md`](engine-orchestrator.md) §3-§4 —
  latency / degradation operational detail

### External

- OpenTelemetry specification: <https://opentelemetry.io/docs/specs/otel/>
- OpenTelemetry semantic conventions: <https://opentelemetry.io/docs/specs/semconv/>
- W3C Trace Context: <https://www.w3.org/TR/trace-context/>
- OpenMetrics: <https://openmetrics.io/>
