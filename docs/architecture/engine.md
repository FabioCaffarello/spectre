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
