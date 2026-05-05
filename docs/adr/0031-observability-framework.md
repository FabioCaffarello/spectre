---
status: accepted
date: 2026-05-05
deciders: [Fabio Caffarello]
---

# Observability framework

## §1 — Context and Problem Statement

The v1alpha1 platform's observability surface is **stdout +
nothing else**. Every service (engine, three adapters,
control-plane operator) emits unstructured or
loosely-structured log lines to stdout; `kubectl logs`,
`docker compose logs`, and `tail -f` are the only debugging
tools. There are **no metrics**, **no traces**, **no
correlation IDs**, **no quality measurements**.

The shape was defensible during the refactor (R1 → R8.1) —
adding observability scaffolding before the architecture
stabilised would have meant rewriting it as the platform
shape evolved. The shape is **not defensible** in v1alpha2:

- **15 catalog services** ([ADR-0036](0036-microservices-catalog-expansion.md))
  multiply the failure surface. Each per-step engine call
  fans out to N services; without trace context propagation,
  no operator can see "this job failed at step 47 in the
  enricher service due to a Mongo timeout downstream of a
  schema-registry call".
- **Latency budgets are real and accepted**
  ([ADR-0037 §4.6](0037-engine-as-orchestrator.md)) at ~5 ms
  per step typical. Without metrics, no operator can verify
  the budget is met or detect regression.
- **User pilot data quality** depends on observability
  (Wave 4 framework v1 §5). The pilot's per-layer
  questionnaire is biased without quality measurements
  (extraction completeness, schema-validation pass rate,
  per-target success ratios).
- **Cross-cutting auditability** is needed for the audit-log
  service (slot 8 per ADR-0036 §3.3); audit emission depends
  on the same correlation-id plumbing trace propagation
  needs.

This ADR commits the v1alpha2 platform to the
**OpenTelemetry framework** as the cross-cutting standard
for metrics, traces, and structured logs. It is one of two
cross-cutting framework ADRs in R9.3 (the other is
[ADR-0032](0032-service-to-service-mtls.md) — service-to-service
mTLS); together they make the canonical service shape from
ADR-0036 §5.4 + §5.5 normative for every Wave 3+ build PR.

### §1.1 — What ADR-0036 §5.4 already commits

ADR-0036 §5.4 lists the canonical observability surface
each service exposes: gRPC reflection (per ADR-0021's
existing pattern); gRPC `Health` service following ADR-0030's
Kubernetes-native `grpc:` probe convention; Prometheus
`/metrics` on a sidecar port; OpenTelemetry trace context
propagation via gRPC metadata; structured logging (JSON to
stdout) with mandated fields (`service`, `level`,
`timestamp`, `request_id`, `job_id`, `tenant_id`,
`latency_ms`, `caller`).

That commitment is structural — it tells a build PR *what
to expose*, not *how to emit*. ADR-0031 fills the *how*: which
OpenTelemetry SDK per language, what semantic conventions to
follow, what metric naming conventions, what trace span
structure, what log fields are required versus
service-specific, and how the per-service emissions compose
into platform-wide observability.

### §1.2 — What ADR-0028 §3.6 already commits

ADR-0028 §3.6 (the original infra-services catalog
observability section) committed each service to: gRPC
reflection, Health probe, structured logging to stdout,
metrics endpoint (Prometheus or OpenTelemetry push — choice
deferred to per-service-build-PR), OpenTelemetry traces
with context propagation. This ADR **resolves the
choice ADR-0028 §3.6 deferred** (Prometheus vs OTel push)
and codifies the per-service-build conventions ADR-0028 §3.6
left as "per build PR".

### §1.3 — What this ADR does not yet land

No service code, no per-service instrumentation, no
collector configuration, no dashboards land in R9.3. This
ADR is contract-only. The first observability PR is **Wave
3** (per [`docs/roadmap.md`](../roadmap.md) §4 in R9.7) —
likely the engine's first OTel SDK integration plus the
operator's first Prometheus `/metrics` endpoint. Subsequent
Wave 5+ build PRs follow the canonical surface ADR-0036 §5.4
mandates and ADR-0031 here makes normative.

## §2 — Decision summary

R9.3 commits the platform to the following observability
posture. Each commitment is **normative** for Wave 3+ build
PRs.

### §2.1 — OpenTelemetry as umbrella standard

OpenTelemetry (OTel) is the cross-cutting framework for
metrics, traces, and structured logs. The choice over
single-purpose alternatives:

- **Prometheus alone** would cover metrics but not traces;
  the per-step service-orchestration fan-out
  (ADR-0037 §3.2) needs distributed traces fundamentally.
- **Jaeger / Zipkin alone** would cover traces but not
  metrics; per-service Prometheus exporters are the
  ecosystem-standard for Kubernetes-native scrape.
- **Custom telemetry** would re-invent what OTel provides
  with worse interoperability across the polyglot stack
  (Rust + Go + Python + TypeScript per ADR-0027).
- **OpenTelemetry** unifies all three signals (metrics,
  traces, logs) under one SDK family with mature
  per-language implementations and broad collector / vendor
  support.

OTel's polyglot maturity is the deciding factor. Each
language ADR-0027 §3.2 commits to has a stable OTel SDK
(`opentelemetry-rust`, `opentelemetry-go`,
`opentelemetry-python`, `@opentelemetry/api` for TypeScript);
each is in production use at scale.

### §2.2 — OTLP as the protocol

**OTLP** (OpenTelemetry Protocol, gRPC + HTTP variants) is
the wire format every service speaks. Specifically:

- **OTLP/gRPC** for emission to a collector (lower
  latency; matches the platform's gRPC posture per
  [ADR-0022](0022-tcp-grpc-transport.md))
- **OTLP/HTTP** as fallback when OTLP/gRPC is not viable
  (TypeScript browser-side consumers, CI test scaffolding)

Services do not emit directly to a vendor backend (Datadog,
New Relic, Honeycomb, Grafana Cloud, ...) — they emit to a
**local collector** (`opentelemetry-collector`), which the
deployment-side configuration routes onward. This shape:

- Decouples per-service code from vendor choice (the
  collector is the boundary).
- Allows aggregation, sampling, filtering, and metric
  derivation at the collector layer without redeploying
  services.
- Matches the cloud-native observability pattern adopted
  across the OTel ecosystem.

The collector is not a service in ADR-0036's catalog — it
is **deployment infrastructure** (per the ADR-0028 §6
preserved rejection of `telemetry-collector` as a service we
build). The Helm chart (per ADR-0030 + ADR-0036 §5.2) ships
with an optional `opentelemetry-collector` subchart
reference; deployments that already run a collector wire
their endpoint.

### §2.3 — Prometheus `/metrics` as scrape complement

Even with OTLP as the primary metric path, every service
**also exposes a Prometheus `/metrics` endpoint**. The
duplication is deliberate:

- **OTLP push** is the metric primary — services emit metric
  events with full OTel semantic conventions; the collector
  applies aggregation and routes onward.
- **Prometheus `/metrics` scrape** is the resilience path
  — when the collector is unavailable, Kubernetes-native
  Prometheus scrape (or the Bitnami `kube-prometheus-stack`
  chart) still surfaces per-service health.

The two paths emit the same metric set; the collector and
the Prometheus scrape both feed a deployment-level
Prometheus instance. Operators choose whichever path suits
their existing observability stack; the service is unaware
of the choice. This is the "two paths to one source of
truth" pattern ADR-0030 §6 already adopted for image
references (chart-level override vs in-image default).

### §2.4 — Structured logs to stdout

Every service emits **JSON-line-delimited logs to stdout**
following the canonical mandated-fields list ADR-0036 §5.4
codifies. Logs are **not** emitted to OTel / OTLP for
v1alpha2:

- **Why not OTel logs**: the OpenTelemetry log SDK is the
  newest of the three signals (metrics + traces + logs);
  per-language maturity varies; Rust support is incomplete
  at v1alpha2 authoring (2026-05). Reassess in v1beta1.
- **Why stdout**: every Kubernetes-native log aggregator
  (Loki, Vector, Fluent Bit, OpenObserve, Datadog Agent)
  collects from stdout natively; the
  deployment-infrastructure boundary stays clean.
- **What's not in scope**: log aggregation
  configuration. Per ADR-0028 §6's preserved rejection of
  `log-aggregator` as a service we build, the platform
  emits to stdout; the cluster operator chooses the
  aggregation path.

The mandated-fields list is **service-agnostic**; every
service emits the same nine mandatory fields (§3.4 below).
Service-specific fields are additive.

### §2.5 — Correlation IDs are first-class

Every job execution and every per-step service call carries
a **correlation ID set** in OTel trace context propagated
via gRPC metadata. The mandatory IDs:

- **`trace_id`** — OTel-standard 128-bit trace identifier
- **`span_id`** — OTel-standard 64-bit span identifier
- **`request_id`** — service-level request identifier
  (idempotent retries share `request_id`; OTel `span_id`
  changes per attempt)
- **`job_id`** — Spectre-level job identifier (the ScrapeJob
  CR's name namespaced by tenant)
- **`tenant_id`** — tenant identifier (multi-tenant
  deployments)

The IDs propagate **end-to-end** — submitted at job
creation by the operator; threaded through every per-step
service call by the engine; surfaced in driver-side logs by
each adapter; emitted to sinks (audit, cost, output) with
full context. Without correlation, the per-step
service-orchestration fan-out is invisible at the
platform level; with it, debugging a failed job's trail
through 9+ services is single-query at the collector layer.

## §3 — Per-service canonical surface

The canonical observability surface ADR-0036 §5.4 lists at
high level. This section expands each item into the
normative contract Wave 3+ build PRs follow.

### §3.1 — gRPC reflection

Every service exposes
`grpc.reflection.v1alpha.ServerReflection` per ADR-0021's
existing pattern. No change from v1alpha1. Used by `grpcurl`
and IDE tooling for development; not used in production
hot paths.

### §3.2 — gRPC Health service

Every service exposes `grpc.health.v1.Health` per ADR-0030's
Kubernetes-native `grpc:` probe convention. The chart's
readiness and liveness probes dial the gRPC `Health` service
directly (no `grpc_health_probe` binary in the image; the
Kubernetes 1.27+ `grpc:` probe handles the protocol).

Service implementations of `Health.Check` should be
**lightweight** — return `SERVING` when the service has
started and basic dependencies are reachable; return
`NOT_SERVING` otherwise. Health checks **must not** trigger
heavy work (no database queries; no external API calls);
they execute every few seconds across every replica, and
heavy implementations cause cascading load.

### §3.3 — Prometheus `/metrics` on sidecar port

Every service exposes Prometheus `/metrics` on a **sidecar
port** distinct from the gRPC service port:

- **gRPC service port** — service-specific (engine: 8090;
  adapters: 8091 / 8092 / 8093; control-plane HTTP: 8090;
  infra-services: 8094+ per the §6.1 port-allocation
  pattern from ADR-0036)
- **Metrics sidecar port** — `9090` for every service. The
  uniform port across services simplifies chart values and
  scrape configuration.

The metrics endpoint serves the OpenMetrics text format
(Prometheus's wire format). Metrics use the OTel semantic
conventions where defined; service-specific metrics follow
the OTel naming pattern (`<package>.<service>.<unit>`)
when no semantic convention applies.

### §3.4 — Structured logging fields (mandatory)

Every service emits stdout logs as **JSON lines**, one
event per line, with the following mandatory fields:

| Field | Type | Description |
|---|---|---|
| `timestamp` | RFC 3339 string with sub-millisecond precision | Event time at emission |
| `level` | enum `{TRACE, DEBUG, INFO, WARN, ERROR, FATAL}` | Severity per OTel logs convention |
| `service` | string | Service slot (engine, control-plane, proxy-broker, ...) |
| `service_version` | string | Service binary version (matches the chart `appVersion` for first-party services) |
| `caller` | string | `<file>:<line>` of the emission site |
| `message` | string | Human-readable message; structured fields below carry data |
| `trace_id` | hex string (32 chars) or null | OTel trace ID when in a trace context |
| `span_id` | hex string (16 chars) or null | OTel span ID when in a trace context |
| `request_id` | string or null | Service-level request identifier |
| `job_id` | string or null | Spectre job identifier when in job-scoped work |
| `tenant_id` | string or null | Tenant identifier in multi-tenant deployments |

Service-specific fields are additive (e.g., `proxy_id` from
proxy-broker; `schema_ref` from schema-registry; `target_url`
from the engine). Field naming follows `lower_snake_case`
across the polyglot stack.

The `latency_ms` field ADR-0036 §5.4 lists is **conditionally
mandatory** — emitted on every event that completes a
measurable operation (RPC handler exit, downstream call
completion, per-step iteration end), absent on events that
don't measure latency (startup logs, configuration logs).

### §3.5 — OpenTelemetry SDK per language

Each language ADR-0027 §3.2 commits to uses its OTel SDK:

| Language | Logs SDK | Metrics SDK | Traces SDK |
|---|---|---|---|
| Rust | `tracing` + `tracing-opentelemetry` (logs are stdout JSON; traces emit via `opentelemetry-otlp`) | `opentelemetry-rust` + `opentelemetry-prometheus` | `opentelemetry-rust` + `opentelemetry-otlp` |
| Go | `slog` (stdout JSON) | `go.opentelemetry.io/otel/metric` + `prometheus` | `go.opentelemetry.io/otel/trace` + `otlptracegrpc` |
| Python | `structlog` or `python-json-logger` | `opentelemetry-sdk` + `opentelemetry-exporter-prometheus` | `opentelemetry-sdk` + `opentelemetry-exporter-otlp` |
| TypeScript | Pino with JSON formatter | `@opentelemetry/sdk-metrics` + `@opentelemetry/exporter-prometheus` | `@opentelemetry/sdk-trace-node` + `@opentelemetry/exporter-trace-otlp-grpc` |

The library matrix follows the same pinning policy as
ADR-0023 §8 (libraries committed; versions bumped per
normal-course maintenance; replacements require an ADR
amendment). When a fifth language joins the SDK matrix
(per ADR-0027's admission gate), its OTel SDK choice is
recorded in the per-SDK build PR.

## §4 — Trace propagation rules

The per-step service-orchestration fan-out
(ADR-0037 §3.2's pseudocode) crosses 9+ service boundaries
per job step. Trace propagation makes the fan-out
**queryable** — a single trace ID retrieves the full causal
chain.

### §4.1 — Trace context format

Trace context propagates via the **W3C Trace Context**
specification (`traceparent` + `tracestate` headers per
the W3C standard, OTel-compatible). gRPC metadata carries
the headers in the standard `traceparent` and `tracestate`
metadata keys. HTTP requests (engine → webhook sinks per
ADR-0024 §4) propagate the same headers via standard HTTP
header semantics.

### §4.2 — Trace topology per job

A job execution emits a **single trace** spanning every
service involved. The span topology:

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

The async emissions (cost-tracker, audit-log per
ADR-0037 §4.3) propagate the trace context but emit **fire-
and-forget** — the engine does not block on their
acknowledgement; the spans complete asynchronously at the
collector boundary.

### §4.3 — Span naming conventions

Span names follow the OTel semantic convention for RPC:
`<package>.<service>/<rpc>`. For Spectre services,
`<package>` is the proto package middle segment
(`spectre.proxy.v1alpha1.Proxy/Acquire`,
`spectre.captcha.v1alpha1.Captcha/Solve`, etc.). For
non-RPC operations (DSL parsing, plan generation,
per-row processing), the convention is
`<service>.<operation>` (`engine.parse_dsl`,
`engine.generate_plan`, `engine.assemble_row`).

### §4.4 — Sampling

Sampling is **deployment-side configuration**. Services emit
all spans; the OTel SDK respects the deployment's sampling
rate via OTel context. Default sampling rate at the
collector: `parentbased_traceidratio` at 0.1 (10%); per-job
sampling preserves causal chains (a sampled root span
samples all child spans).

Light deployments may set sampling to 1.0 (every span);
high-volume deployments may set to 0.01 (1%) or use
tail-based sampling at the collector. The choice is
deployment-side; ADR-0031 commits the per-service emission
contract, not the deployment's sampling shape.

## §5 — Metrics taxonomy

Each service emits a mandatory set of metrics following
OTel semantic conventions where applicable. The taxonomy
groups by service category.

### §5.1 — Engine metrics (slot: `engines/engine/`)

Required:

- **`spectre_engine_jobs_active`** (gauge) — currently
  executing jobs
- **`spectre_engine_jobs_completed_total{result}`**
  (counter) — completed jobs with `result` ∈ `{success,
  failure, timeout, cancelled}`
- **`spectre_engine_step_duration_seconds`** (histogram)
  — per-step duration; the histogram buckets follow OTel's
  recommended bucket set
- **`spectre_engine_step_service_call_duration_seconds{service}`**
  (histogram) — per-step service-call duration; `service`
  label identifies the called service
- **`spectre_engine_rows_emitted_total{sink}`** (counter)
  — rows emitted per sink type
- **`spectre_engine_circuit_breaker_state{service,state}`**
  (gauge) — circuit-breaker state per service per
  ADR-0037 §5.3 (`state` ∈ `{closed, half_open, open}`)

### §5.2 — Operator metrics (slot: `operators/control-plane/`)

The operator already exposes
`controller-runtime`-default metrics
(`controller_runtime_reconcile_total`,
`controller_runtime_reconcile_time_seconds`); ADR-0031
preserves these and adds:

- **`spectre_operator_scrapejobs_total{phase}`** (gauge)
  — ScrapeJobs by phase (`pending`, `running`, `completed`,
  `failed`)
- **`spectre_operator_scrapebatches_total{phase}`** (gauge)
  — ScrapeBatches by phase (Wave 6+ per ADR-0033)
- **`spectre_operator_engine_dial_failures_total`**
  (counter) — engine-dial failures during reconciliation

### §5.3 — Adapter metrics (slot: `adapters/<adapter>/`)

Each adapter (Playwright / SeleniumBase / curl-impersonate)
exposes:

- **`spectre_adapter_sessions_active{kind}`** (gauge) —
  active sessions per adapter `kind`
- **`spectre_adapter_initialize_duration_seconds{kind}`**
  (histogram) — Initialize RPC duration
- **`spectre_adapter_navigate_duration_seconds{kind,result}`**
  (histogram) — Navigate RPC duration
- **`spectre_adapter_extract_duration_seconds{kind,result}`**
  (histogram) — Extract RPC duration
- **`spectre_adapter_capability_violations_total{kind,capability}`**
  (counter) — calls to unsupported capabilities (the
  byte-for-byte capability divergence per ADR-0017 §1
  surfaces here)

### §5.4 — Infra-service metrics (slot: `infra-services/<slot>/`)

Each infra-service exposes its own service-specific metric
set, following the OTel semantic conventions for the
operation. The canonical pattern:

- **`spectre_<slot>_requests_total{rpc,result}`** (counter)
  — RPC request count
- **`spectre_<slot>_request_duration_seconds{rpc}`**
  (histogram) — RPC duration per RPC method
- **`spectre_<slot>_provider_health{provider,status}`**
  (gauge) — for gate-A services, per-provider health

Service-specific metrics follow the per-service-build PR's
contract (e.g., `spectre_proxy_broker_pool_size{provider,
region}` for proxy-broker; `spectre_schema_registry_schemas_total`
for schema-registry).

### §5.5 — Cross-cutting metrics

Every service emits:

- **OTel-standard `process_*` metrics** (CPU, memory, file
  descriptors) per the OTel `process` semantic convention
- **`runtime_*`-prefixed runtime metrics** (Go GC, Rust
  allocator, Python GIL, Node event loop) per
  language-specific OTel semantic conventions

These are SDK-default — the language OTel SDK emits them
without per-service code; ADR-0031 mandates they are not
disabled.

## §6 — Failure categorisation

The per-service degradation modes from
[ADR-0037 §5](0037-engine-as-orchestrator.md) require
a **structured failure taxonomy** so dashboards, alerting,
and audit trails can categorise errors consistently.

### §6.1 — DriverError.Code as the taxonomy primitive

[ADR-0009 §5](0009-navigate-and-session-lifecycle.md) defines
`DriverError.Code` — the Spectre-wide error-code enum
adapters and the engine emit. ADR-0031 commits this
taxonomy as the **observability primitive**:

- **Metric labels**: every error-counting metric
  (`*_errors_total`, `*_failures_total`) includes a
  `code` label whose value is the `DriverError.Code` name
  (`PROXY_BROKER_UNAVAILABLE`, `SCHEMA_VALIDATION_FAILED`,
  `RATE_LIMIT_EXCEEDED`, etc.).
- **Structured log field**: error-level events include
  `error_code` field with the same value.
- **Operator status**: the ScrapeJob's `status.failureCode`
  field surfaces the code per ADR-0019's CRD shape.
- **Trace span attributes**: error spans set
  `error.code = <DriverError.Code>` per OTel semantic
  convention.

The taxonomy is **append-only** — new codes are added per
the ADR-0009 §5 evolution rules; existing codes do not
change semantics.

### §6.2 — Engine extensions to ADR-0009

The engine's degradation modes (ADR-0037 §5) introduce
**per-service unavailability codes** that extend ADR-0009's
taxonomy:

- `PROXY_BROKER_UNAVAILABLE` (required-service failure)
- `CAPTCHA_UNREACHABLE` (required-service failure)
- `RATE_LIMIT_UNAVAILABLE` (required-service failure)
- `SCHEMA_REGISTRY_UNAVAILABLE` (required-service failure)
- `INPUT_BROKER_UNAVAILABLE` (required-service failure)
- `ENRICHER_UNAVAILABLE` (graceful degradation; logged
  once per job)
- `DEDUP_UNAVAILABLE` (graceful degradation)
- `AUDIT_LOG_UNAVAILABLE` (graceful degradation)
- `COST_TRACKER_UNAVAILABLE` (graceful degradation)
- `SESSION_STORE_UNAVAILABLE` (graceful degradation)
- `FINGERPRINT_BROKER_UNAVAILABLE` (graceful degradation)
- `SECRET_BROKER_UNAVAILABLE` (degradation; falls back to
  env vars / files)

The codes are added to ADR-0009's enum **at the same Wave**
as their service materialises; no Wave 3 per-service
unavailability codes are emitted before the corresponding
service exists.

### §6.3 — Failure dashboards

Each service ships with a **default dashboard** in the build
PR (Grafana JSON or compatible format under
`infra-services/<slot>/dashboards/<slot>.json`). The
dashboard surfaces:

- Error rate by `code` label
- p50 / p95 / p99 latency per RPC
- Provider health per gate-A service
- Per-tenant breakdowns (multi-tenant deployments)

The dashboards are **starting points**, not exhaustive.
Operators customise per their alerting needs; the per-service
build PR ships the baseline.

## §7 — Cost tracking integration (forward reference)

The `cost-tracker` service (ADR-0036 slot 7; ADR-0038 in
R9.4) consumes cost emissions from per-step service calls.
ADR-0031's metric taxonomy provides the **emission shape**
that cost-tracker ingests:

- **`spectre_<slot>_cost_units{provider,unit}`** (counter)
  — emitted by gate-A services that incur per-call cost
  (proxy-broker emits per-proxy-acquire cost;
  captcha-solver emits per-solve cost)
- **`spectre_engine_compute_seconds{tenant}`** (counter) —
  emitted by the engine for compute-time attribution

cost-tracker subscribes to these metrics via the OTel
collector pipeline and aggregates per-job per-tenant
rollups (per ADR-0038's contract). The cost emission is
**asynchronous** per ADR-0037 §4.3 — the engine does not
block on cost-tracker acknowledgement.

ADR-0031 + ADR-0038 share the contract: ADR-0031 commits
the metric shape; ADR-0038 commits the consumer service.

## §8 — Quality metrics

User pilot data quality (Wave 4) depends on extraction
**quality metrics** distinct from operational metrics. The
canonical quality metrics:

- **`spectre_extraction_completeness_ratio{schema_ref,target}`**
  (gauge) — fraction of expected fields successfully
  extracted per `(schema, target)` pair
- **`spectre_schema_validation_pass_ratio{schema_ref}`**
  (gauge) — fraction of rows passing schema validation per
  schema
- **`spectre_extraction_success_ratio{target,driver}`**
  (gauge) — fraction of jobs succeeding per `(target,
  driver)` pair (catches per-target driver-success
  divergence ADR-0017's strict-subset chain implies)
- **`spectre_dedup_collision_ratio{schema_ref}`** (gauge)
  — duplicate detection rate per schema (informs dedup
  configuration tuning)

Quality metrics are **emitted by the engine** at the
per-row-emission boundary; aggregation lives at the
collector / Prometheus layer. The metrics surface in the
Wave 4 pilot questionnaire as quantitative companions to
the qualitative per-layer feedback (framework v1 §5).

Implementation deferred to **Wave 9** (per
ADR-0036's Wave assignment for cost-tracker + audit-log + the
quality metrics implementation), at which point ADR-0038
materialises and the engine's emission paths land. R9.3's
ADR-0031 records the metric shape; Wave 9 lands the
implementation.

## §9 — Migration sequence

R9.3's ADR-0031 + ADR-0032 are documentation-only; no
service code lands. Per-service instrumentation lands
incrementally across Waves 3 onwards:

| Wave | Observability scope |
|---|---|
| Wave 3 (first observability PR) | Engine first OTel SDK integration: trace context propagation through DSL → driver call; Prometheus `/metrics` on engine + operator (the two services already deployed in v1alpha1). The chart's `opentelemetry-collector` subchart reference lands. |
| Wave 5 (proxy-broker + captcha-solver) | First infra-service instrumentation. Per-service trace spans propagate from engine. Per-service `/metrics` endpoints scrape via the chart's PodMonitor / ServiceMonitor templates. |
| Wave 6+ (per service) | Each new infra-service ships with the canonical observability surface (§3.x) wired and a default dashboard (§6.3) under `infra-services/<slot>/dashboards/`. |
| Wave 9 | Cost-tracker + audit-log services consume the metric / log streams. Quality metrics (§8) emission lands. |
| Wave 10 | Driver-router (if persisted per ADR-0035) consumes per-target success metrics for routing decisions. |

The Wave 3 first-observability PR is **transformational
scope** under the v1alpha2 process rigor matrix
([CONTRIBUTING.md](../../CONTRIBUTING.md), R9.0) — it
introduces the OTel scaffolding the Wave 5+ services
extend. Subsequent per-service observability additions are
**single architectural decision** scope (one ADR if
warranted, one focused PR).

## §10 — Confirmation (acceptance criteria)

The framework is working when the following hold **by the
close of Wave 9**:

- **Every service emits the canonical observability
  surface** (§3) — gRPC reflection + Health + Prometheus
  `/metrics` + OTLP traces + JSON stdout logs with the
  mandatory fields. No service ships without §3.
- **Trace propagation end-to-end** — a single `trace_id`
  retrieves a full job execution's causal chain across
  operator + engine + N services + driver + sinks +
  cost-tracker / audit-log. The §4.2 topology is
  reproducible in the production-smoke gate (R7.2).
- **`DriverError.Code` is the universal failure label** —
  no metric, log, or trace-span attribute uses a
  service-local error string when a `DriverError.Code` value
  exists. PR review rejects ad-hoc error labels.
- **Per-service default dashboards** exist under
  `infra-services/<slot>/dashboards/<slot>.json` for every
  Wave 5+ service.
- **Quality metrics emit at the engine's row boundary** —
  Wave 9 lands the four §8 metrics; the production-smoke
  gate asserts each metric is non-empty for the smoke job.

A signal that the framework needs revision: more than one
Wave build PR encounters a real metric / trace / log
requirement that doesn't fit §3 – §8. That's evidence the
canonical surface is incomplete; the response is an ADR
amendment that extends the surface, not a per-service
deviation.

## §11 — What's deferred / out of scope

R9.3 declines these deliberately. Each is a real concern;
each belongs to a later phase or to deployment-side
configuration.

- **Log aggregation tooling** (Loki, Vector, Fluent Bit,
  OpenObserve, Datadog Agent, ...). Per ADR-0028 §6's
  preserved rejection of `log-aggregator` as a service we
  build, log aggregation is **deployment-side
  configuration** — the platform emits to stdout; the
  cluster operator chooses the aggregator. The Helm chart
  (per ADR-0030 + ADR-0036 §5.2) does not ship a
  log-aggregator subchart.
- **OpenTelemetry log SDK adoption.** v1alpha2 emits logs
  to stdout; OTel logs SDK adoption defers to v1beta1 when
  per-language maturity (especially Rust) catches up.
- **SLO definitions** (per-RPC SLOs, per-job SLOs,
  platform-level SLO). SLOs require pilot evidence (Wave
  4); committing them in R9.3 is premature. Wave 9+ revisits
  with quality-metric data.
- **Distributed tracing storage.** Whether the deployment
  routes OTLP traces to Jaeger / Tempo / Datadog APM /
  Honeycomb is **deployment-side configuration**; the
  collector is the boundary.
- **Per-tenant observability isolation.** Multi-tenant
  deployments may need per-tenant metric / trace / log
  isolation (RBAC at the collector layer, per-tenant
  Prometheus instances, per-tenant trace storage). v1beta1
  scope.
- **Continuous profiling** (CPU / memory profiles via
  pprof, Pyroscope, Parca). Useful but outside the
  three-signal scope of this ADR.
- **eBPF-based observability** (Cilium Hubble, Pixie). Out
  of scope; deployment-side concern.
- **Semantic-convention version pinning.** OTel semantic
  conventions evolve; v1alpha2 pins to whichever versions
  the per-language SDKs ship at Wave 3 authoring; bumps
  follow per-SDK release cadence.
- **Custom per-service exemplars / span events beyond the
  taxonomy.** Per-service build PRs may extend §3.x's
  surface with service-specific events; this ADR commits
  the floor, not the ceiling.

## §12 — Reference materials

- [ADR-0009](0009-navigate-and-session-lifecycle.md) —
  driver error mapping; `DriverError.Code` is the §6 failure
  taxonomy primitive.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane; the operator's failure-code surfacing
  path (§6.1).
- [ADR-0021](0021-service-discovery.md) — service discovery;
  gRPC reflection (§3.1) carries forward unchanged.
- [ADR-0022](0022-tcp-grpc-transport.md) — gRPC transport;
  OTLP/gRPC (§2.2) layers on the same transport.
- [ADR-0024](0024-output-sinks.md) — output sinks; the
  webhook sink propagates trace context per §4.1.
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy;
  per-language OTel SDKs (§3.5) follow the same admission
  gate.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) —
  infra-services catalog; §3.6 of that ADR resolved here.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart;
  the chart's PodMonitor / ServiceMonitor / collector
  references land per §6.2 / §6.3.
- [ADR-0036](0036-microservices-catalog-expansion.md) —
  the 15-service catalog; §5.4 canonical surface that this
  ADR makes normative.
- [ADR-0037](0037-engine-as-orchestrator.md) — engine as
  orchestrator; §4.2 trace topology mirrors §3.2's
  pseudocode flow.
- ADR-0032 (this PR's Cluster B) — service-to-service mTLS;
  certificates carry the trace context propagation
  unchanged.
- ADR-0038 (R9.4, forthcoming) — cost tracking; §7 commits
  the emission shape it consumes.
- OpenTelemetry specification:
  <https://opentelemetry.io/docs/specs/otel/>
- OpenTelemetry semantic conventions:
  <https://opentelemetry.io/docs/specs/semconv/>
- W3C Trace Context: <https://www.w3.org/TR/trace-context/>
- OpenMetrics specification: <https://openmetrics.io/>
