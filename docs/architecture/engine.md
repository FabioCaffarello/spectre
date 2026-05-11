# Engine

The Spectre engine is a stateless gRPC service that compiles a
Spectre DSL document into a sequence of Driver Protocol RPCs and
streams the resulting JSONL rows back to the caller. After R2.3
(ADR-0020 §3) it is no longer a CLI; the binary at
`engines/engine/src/bin/spectre.rs` does one thing — start the gRPC
service.

This document describes the post-R2.3 shape. For the DSL → plan
→ execution pipeline that lives inside the engine, see
[ADR-0012](../adr/0012-engine-dsl-and-execution-pipeline.md);
for the transport contract between engine and adapters, see
[ADR-0022](../adr/0022-tcp-grpc-transport.md); for the adapter
discovery model, see [ADR-0021](../adr/0021-service-discovery-and-environment.md).

## Service contract

```
service Engine {
  rpc RunJob(RunJobRequest) returns (stream RunJobResponse);
}
```

The full schema lives at
[`proto/spectre/engine/v1alpha1/engine.proto`](../../proto/spectre/engine/v1alpha1/engine.proto).
Two notes:

- The protocol is *internal*. Only the engine implements the
  server side; only the control plane (R3.1) and direct clients
  driving jobs end-to-end (`grpcurl` in transitional local-dev
  workflows) consume it. Adapter authors do not implement it —
  they implement `spectre.driver.v1alpha1`, the public driver
  protocol that has been frozen since PR7.
- The protocol is `v1alpha1`. Adding fields and RPCs is
  non-breaking; renaming or removing them is. Resist over-design
  here: extending the contract to cover hypothetical future
  needs (job priority hints, sink configuration, status RPCs)
  pollutes the wire shape that the control plane has to evolve
  against.

`RunJob` returns a server-streaming response of
`RunJobResponse` events. The `oneof event` field carries one of:

- `Row { json_line }` — one extracted row, encoded as the same
  JSONL string the legacy executor wrote to stdout. Preserves
  DSL semantics so consumers can forward the line unchanged.
- `Completed { rows_extracted }` — terminal; the plan ran to
  completion. Always exactly one terminal event closes the
  stream.
- `Failed { error_code, error_message }` — terminal; the plan
  failed mid-execution. `error_code` maps to the engine's
  `EngineError` taxonomy and is stable across patch releases
  (`UNKNOWN_DRIVER`, `TRANSPORT`, `DRIVER`, `CAPABILITY_MISSING`,
  `JOB`, `PLAN`, `OUTPUT`, `IO`, `INTERNAL`).

Cancellation: the client cancels by closing the stream. There
is no explicit `CancelJob` RPC; gRPC's stream cancellation is
the only mechanism v1alpha1 offers. v1alpha2 may add an
explicit RPC if the control plane's reconciler logic ends up
needing one.

## Adapter discovery

At startup the engine builds an `AdapterRegistry` mapping each
DSL driver name to a TCP endpoint:

| Driver name        | Environment variable                 | Default          |
|--------------------|--------------------------------------|------------------|
| `playwright`       | `SPECTRE_PLAYWRIGHT_ENDPOINT`        | `127.0.0.1:8091` |
| `seleniumbase`     | `SPECTRE_SELENIUMBASE_ENDPOINT`      | `127.0.0.1:8092` |
| `curl-impersonate` | `SPECTRE_CURL_IMPERSONATE_ENDPOINT`  | `127.0.0.1:8093` |

Defaults bind to `127.0.0.1` so the engine is reachable when run
as a native binary against a Compose-running adapter set (the
Compose host-port mapping is 1:1). The Compose stack (R6.2,
ADR-0025) and Helm chart (R7.1) override the variables with
service-DNS values (`grpc://playwright-adapter:8091` in Compose,
`<svc>.<ns>.svc.cluster.local:8091` in Kubernetes). Resolution
is lazy: the engine does not pre-dial at startup, so engine and
adapters can come up in any order under Compose / Kubernetes.

## Health check

The binary registers `grpc.health.v1.Health` alongside the
engine service on the same TCP listener. It returns `SERVING`
from process startup for both the empty service name (which
`grpc_health_probe` queries by default) and
`spectre.engine.v1alpha1.Engine` — the same pattern adapters
already use (ADR-0021 §6). Compose and Kubernetes wire the
endpoint into readiness probes.

## Why the CLI was retired

ADR-0013 made the engine binary the user-facing `spectre` CLI:
`spectre run`, `spectre validate`, `spectre version`. ADR-0020
§3 supersedes that decision. The reasoning:

- The refactor introduces two new user-facing entry points: the
  Kubernetes operator (`kubectl apply -f scrapejob.yaml`,
  R3.1+) and the Compose stack (`docker compose up`, R6.2).
  Keeping `spectre run` as a third path means three entry
  points with overlapping responsibilities.
- Master strategy §2.2 forbids legacy paths surviving a
  transport refactor. Preserving the CLI as "dev convenience"
  reintroduces the monolithic mental model the refactor exists
  to retire.
- The conformance suite plus `grpcurl` cover developer needs
  in the R2.3 → R6.2 window. R6.2 lands a `just example-<name>`
  recipe per sample so the manual `grpcurl` flow is short-lived.

## Statelessness in v1alpha1

The engine holds no per-job state beyond the duration of a
single `RunJob` call. There is no in-memory cache of compiled
plans, no pending-jobs queue, no session affinity to a specific
adapter pod. R4.2 introduces PostgreSQL-backed job persistence;
the engine writes job rows to Postgres at that point. Until
then, a restarted engine pod loses no work — there is no work
in flight that survives the call.

The implication for deployment: engine pods are
horizontally-scalable by replica count. Any engine pod can
serve any `RunJob`; the registry is the same in every pod.

## See also

- [ADR-0020](../adr/0020-microservices-architecture-supersession.md)
  — refactor architectural commitment; CLI retirement.
- [ADR-0021](../adr/0021-service-discovery-and-environment.md)
  — adapter discovery via environment variables.
- [ADR-0022](../adr/0022-tcp-grpc-transport.md) — TCP gRPC
  transport contract.
- [ADR-0012](../adr/0012-engine-dsl-and-execution-pipeline.md)
  — DSL parser, planner, and executor pipeline.
- [`proto/spectre/engine/v1alpha1/engine.proto`](../../proto/spectre/engine/v1alpha1/engine.proto)
  — the wire contract.

## v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe the
> v1alpha1 engine — a thick monolithic orchestrator that
> handles DSL parsing, plan generation, driver gRPC calls,
> per-job state, output sink dispatch, and JSONL row
> emission in-process. Phase R9 commits the engine's
> v1alpha2 evolution; this subsection forwards readers to
> the new shape.*

The v1alpha2 engine becomes a **conductor of platform
services** rather than a god object. DSL parsing, plan
generation, per-job state, sink dispatch, and the
unchanged Driver Protocol gRPC client stay in the engine;
13 v1alpha1 responsibilities (proxy acquisition, CAPTCHA
solving, fingerprint rotation, rate limiting, session
persistence, schema validation, post-extraction
enrichment, dedup, cost emission, audit, credential
acquisition, URL queue, driver routing) move to catalog
services per [ADR-0036](../adr/0036-microservices-catalog-expansion.md)
across Waves 5 – 10.

The engine evolution + per-step orchestration sequence +
five latency mitigation strategies + per-service
degradation modes are codified in
[ADR-0037](../adr/0037-engine-as-orchestrator.md). The
operational walkthrough lives at
[`engine-orchestrator.md`](engine-orchestrator.md).

The Driver Protocol stays **frozen** per
[ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md);
v1alpha2 DSL primitives (pagination, conditional,
multi-step navigation, schema declaration, transforms) are
**engine-internal** — the parser expands them into
v1alpha1-shaped Driver Protocol calls. See
[`dsl-evolution.md`](dsl-evolution.md) +
[ADR-0035](../adr/0035-dsl-evolution-driver-abstraction.md).

**W3.1 (2026-05-11) observability landing** — the engine
binary gains an OpenTelemetry SDK foundation per
[ADR-0031](../adr/0031-observability-framework.md). Concrete
changes:

- `src/telemetry/` module hosts `Telemetry::init` (sets the
  global W3C `TraceContextPropagator`, registers an
  `SdkTracerProvider` always — OTLP exporter attaches only
  when `OTEL_EXPORTER_OTLP_ENDPOINT` is set —, and registers
  the §5.1 metric handles).
- An axum `:9090/metrics` sidecar serves OpenMetrics text.
  Port read from `SPECTRE_METRICS_PORT` (default `9090`).
- `engine.run_job` server-kind span opens at `RunJob` entry,
  extracting any parent context from the gRPC metadata.
  Child spans `engine.parse_dsl` / `engine.generate_plan` /
  `engine.execute_plan` / `engine.assemble_row` plus the
  five `spectre.driver.v1alpha1.Driver/<Rpc>` client spans
  inherit it.
- `tracing_subscriber` writes one JSON line per event to
  stdout with the eleven mandatory fields from ADR-0031 §3.4
  (`trace_id` / `span_id` read from the active OTel context).

The five metric instruments §5.1 lands record at:

| Metric | Recording site |
|---|---|
| `spectre_engine_jobs_active` | `server.rs::run_job_inner` accept / `stream_run_job` exit |
| `spectre_engine_jobs_completed_total{result}` | terminal-event dispatch |
| `spectre_engine_step_duration_seconds` | per `PlanStep` iteration in `executor.rs::run_inner` |
| `spectre_engine_step_service_call_duration_seconds{service}` | per `client.rs` RPC method |
| `spectre_engine_rows_emitted_total{sink}` | per row in `stream_run_job` drain loop |

`spectre_engine_circuit_breaker_state` (the sixth §5.1
metric) is reserved for Wave 5 when the circuit breaker
materialises per ADR-0037 §5.3.
