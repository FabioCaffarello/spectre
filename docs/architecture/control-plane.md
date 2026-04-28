# Control plane

The Spectre control plane is a Kubernetes-native operator that lets
operators submit, observe, and (eventually) schedule extraction jobs
against the engine and its adapters. PR14 opens Phase 3 with the
operator's foundation: a kubebuilder v4 scaffold under
`core/control-plane/`, a `ScrapeJob` Custom Resource Definition, and
a state-machine reconciler whose execution path is a stub. Real job
execution lands in PR15.

## Phase 3 status

| Component                          | Status            | PR / Phase |
|------------------------------------|-------------------|------------|
| kubebuilder scaffold               | shipped           | PR14       |
| `ScrapeJob` CRD (v1alpha1)         | shipped           | PR14       |
| State-machine reconciler           | shipped           | PR14       |
| `JobRunner` interface              | shipped           | PR14       |
| `StubRunner`                       | shipped           | PR14       |
| `EngineClientRunner` (gRPC)        | shipped           | R3.1       |
| Operator image (unbundled)         | shipped           | R3.1       |
| `ScrapeJob` CRD v1alpha2           | not started       | R3.2       |
| PostgreSQL job state               | not started       | R4.2       |
| Output sinks (S3 / webhook / Kafka)| not started       | R5         |
| Per-service Dockerfiles            | not started       | R6.1       |
| Compose stack                      | not started       | R6.2       |
| Helm chart                         | not started       | R7.1       |
| `ScrapeFleet` / `ScrapeSchedule`   | not started       | post-Phase3|

PR14 shipped the reconciler with a sleep-based stub. PR15–PR18
delivered a bundled-image execution model where the operator,
the engine binary, and three adapter runtimes ran together as
nested subprocesses inside one Pod. The R1.1–R3.1 refactor
(ADR-0020) replaced that model with service-per-component:

- The engine is a stateless gRPC service (R2.3, ADR-0020 §3) that
  exposes `spectre.engine.v1alpha1.Engine.RunJob` on
  `0.0.0.0:9090` and dials adapters from `AdapterRegistry` via
  env vars (ADR-0021 §5).
- The three reference adapters (Playwright, SeleniumBase,
  curl-impersonate) are long-running gRPC services (R2.2) that
  bind `0.0.0.0:909{1,2,3}` and register `grpc.health.v1.Health`
  (ADR-0021 §6).
- The control plane (R3.1) is a thin gRPC client of the engine
  service. `EngineClientRunner` dials the engine, opens a
  `RunJob` stream, and forwards every `Row.json_line` event into
  the operator's stdout (so `kubectl logs <operator-pod>` keeps
  working per ADR-0019 §6).

ADR-0019 §5's `JobRunner` interface seam held byte-for-byte
through three implementations (`StubRunner`, `SubprocessRunner`
in PR15, `EngineClientRunner` in R3.1); the reconciler control
flow and the envtest suite are unchanged from PR14.

## Operator image

Per R3.1, the operator image is a Go static binary on
`gcr.io/distroless/static:nonroot`:

```
spectre-control-plane:dev (~50 MB on disk)
└── /manager      # kubebuilder manager (Go, CGO_ENABLED=0)
```

`/usr/local/bin/spectre` and `/opt/spectre/adapters/*` are gone —
the engine and adapters run as separate services. Per-service
Dockerfiles for them are R6.1 work; the multi-service local-dev
flow (`docker compose up`) is R6.2 (ADR-0025); production
packaging via Helm chart is R7.1 (ADR-0026).

## Engine endpoint

The manager dials the engine's gRPC service. The endpoint is
configured at startup via, in order of precedence:

1. `--engine-endpoint=<host:port>` flag.
2. `SPECTRE_ENGINE_ENDPOINT` environment variable.
3. Hard-coded default `127.0.0.1:9090` (matches `just engine-run`'s
   listener).

In the Compose stack (R6.2) the env var is set to `engine:9090`.
In the Helm chart (R7.1) it is rendered from values onto the
operator Deployment. Plain-text gRPC is acceptable in v1alpha1
because the operator-engine traffic stays on a private network
namespace (Compose / Pod network). TLS / mTLS is deferred to
v1alpha2 per ADR-0022 §6.

The `EngineClientRunner` dials the endpoint per `RunJob`
invocation (no connection pooling), respects the context deadline
the reconciler attaches from `Spec.TimeoutSeconds`, and surfaces
gRPC stream cancellation as `ctx.Err()` to the reconciler.

## The ScrapeJob CRD

`ScrapeJob` represents a single execution of a DSL job. The full
schema lives at
[`api/v1alpha1/scrapejob_types.go`](../../core/control-plane/api/v1alpha1/scrapejob_types.go);
the canonical YAML is
[`config/crd/bases/spectre.io_scrapejobs.yaml`](../../core/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml).

### Spec

| Field            | Type                          | Required | v1alpha1 behavior                                                        |
|------------------|-------------------------------|----------|--------------------------------------------------------------------------|
| `jobDSL`         | string                        | yes      | Inline YAML body of the DSL job (`MinLength=1`).                         |
| `outputSink`     | string                        | no       | Only `""` and `"stdout"` are accepted; other syntaxes return Failed.     |
| `timeoutSeconds` | int32                         | no       | Defaults to 600 (10 minutes); propagated as a context deadline.          |
| `adapterImage`   | string                        | no       | Reserved for v1alpha2 per-Pod-per-job execution; v1alpha1 ignores.       |
| `resources`      | `corev1.ResourceRequirements` | no       | Recorded for v1alpha2; v1alpha1 cannot enforce per-job limits.           |

### Status

| Field            | Type                  | Set by                                                       |
|------------------|-----------------------|--------------------------------------------------------------|
| `phase`          | `ScrapeJobPhase` enum | Reconciler. State machine: Pending → Running → Completed \| Failed. |
| `startedAt`      | timestamp             | Reconciler at Pending → Running.                             |
| `completedAt`    | timestamp             | Reconciler at any → terminal phase.                          |
| `rowsExtracted`  | int64                 | Reconciler from `JobRunner.Run` return value.                |
| `error`          | string                | Reconciler on validation or runner failure.                  |
| `conditions`     | `[]metav1.Condition`  | Diagnostic; not the source of truth (see ADR-0019 §4).       |

### Phase semantics

```
                  ┌───────────┐     valid spec     ┌───────────┐     run ok     ┌─────────────┐
   create ──────► │  Pending  │ ─────────────────► │  Running  │ ─────────────► │  Completed  │
                  └───────────┘                    └───────────┘                └─────────────┘
                       │                                 │
                       │ invalid spec                    │ run error
                       ▼                                 ▼
                  ┌───────────┐                    ┌───────────┐
                  │  Failed   │                    │  Failed   │
                  └───────────┘                    └───────────┘
```

Terminal phases (`Completed`, `Failed`) are sticky: the reconciler
returns without requeue once one is reached. This makes `kubectl wait
--for=jsonpath='{.status.phase}'=Completed` work as expected.

## Running locally

### Unit tests (no cluster required)

```bash
just op-test
# Equivalent to: cd core/control-plane && make test
```

`make test` downloads apiserver and etcd binaries via setup-envtest,
runs the reconciler suite (state-machine transitions) and the
runner suite (`StubRunner`, `EngineClientRunner` over an
in-process bufconn gRPC server), and prints coverage. First run
takes ~2 minutes (binary downloads); cached runs complete in under
30 seconds.

### Host operator against a local engine + adapter

R3.1 splits execution across services. To exercise the operator
end-to-end against a `kubectl apply`-driven `ScrapeJob`, run the
engine, an adapter, and the operator in three terminals:

```bash
# Terminal 1 — engine (gRPC service on 127.0.0.1:9090)
just engine-run

# Terminal 2 — Playwright adapter (gRPC service on 127.0.0.1:9091)
just pw-run 9091

# Terminal 3 — operator against the current kubectl context.
# SPECTRE_ENGINE_ENDPOINT defaults to 127.0.0.1:9090; override to
# point at a remote engine if needed.
just op-install-crds
just op-run

# Terminal 4 — apply a sample and watch.
kubectl apply -f core/control-plane/config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml
kubectl get scrapejob -w
```

Phase transitions: `Pending → Running → Completed`. JSONL rows
stream from the engine to the operator's stdout (the foreground
terminal running `just op-run`) per ADR-0019 §6.

The Compose stack (R6.2) replaces this multi-terminal flow with a
single `docker compose up`; that becomes the canonical local-dev
loop once R6.2 lands.

### Tearing down

```bash
just op-uninstall-crds          # delete CRD + any in-flight ScrapeJobs
# Stop the engine and adapter terminals with Ctrl-C.
```

## Architecture decisions

The substantive design choices are recorded in
[ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
and updated by
[ADR-0020](../adr/0020-microservices-architecture-supersession.md):

1. **kubebuilder v4 over operator-sdk or controller-runtime direct.**
   Reference convention; marker-driven generation; envtest
   integration. Preserved.
2. **`ScrapeJob` is the only v1alpha1 CRD.** `ScrapeFleet` /
   `ScrapeSchedule` build on top of `ScrapeJob`. Preserved; the CRD
   evolves to v1alpha2 in R3.2 with breaking-change semantics.
3. **~~Subprocess-in-operator-pod execution.~~** Superseded by
   ADR-0020 §3. The single-Pod, three-nested-process model
   (operator → engine → adapter) is replaced by service-per-component:
   the engine is a gRPC service the operator dials, adapters are
   gRPC services the engine dials.
4. **Phase as state-machine source of truth.** `Status.Conditions`
   is diagnostic, not authoritative. `kubectl wait` works against
   `.status.phase`. Preserved.
5. **`JobRunner` interface as the engine seam.** Preserved and
   vindicated through three implementations: PR14's `StubRunner`,
   PR15's `SubprocessRunner` (retired R3.1), and R3.1's
   `EngineClientRunner`. The reconciler is unaware of which
   implementation is wired.
6. **`outputSink` accepts only `"stdout"` in v1alpha1.** Preserved
   as a CRD-schema commitment. The runtime grows S3, webhook, and
   Kafka sinks under ADR-0024 in R5.

## What is not implemented yet

These deferrals are intentional and have phase pointers:

- **Output sinks beyond stdout.** `s3://`, `webhook://`,
  `kafka://` are valid `OutputSink` strings at the schema level but
  the reconciler rejects them with an explicit error. R5 adds
  per-sink support under ADR-0024.
- **Per-job resource isolation.** `Spec.Resources` is recorded but
  not enforced; the operator does not project the field onto a
  per-job Pod. v1alpha2 may add an opt-in `Mode: Pod` per ADR-0019
  §3's escape hatch.
- **Concurrent reconciliation.** Controller-runtime's default
  `MaxConcurrentReconciles=1` carries forward. Phase 4 may tune
  this once Postgres-backed job state (R4.2) is in.
- **CRD evolution.** R3.2 lands `ScrapeJob` v1alpha2 with breaking
  changes (no conversion webhook — there are no production users
  to migrate per ADR-0020 §3 / ADR-0023).
- **Fan-out and scheduling.** `ScrapeFleet` (parallel jobs over a
  parameter list) and `ScrapeSchedule` (cron-like recurrence) are
  post-Phase-3 work. Both build on `ScrapeJob` semantics.
- **Helm chart.** `helm/spectre-control-plane/` is R7.1 (ADR-0026).
- **Webhook validators.** Beyond `+kubebuilder:validation` markers
  on the CRD, validating/mutating webhooks are post-Phase-3.
- **Observability.** Prometheus metrics, OpenTelemetry traces, and
  structured logs beyond controller-runtime's defaults are
  post-Phase-3 follow-up.

## Where to start contributing

- **Read [ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
  and [ADR-0020](../adr/0020-microservices-architecture-supersession.md)
  end-to-end.** ADR-0019 records the original Phase 3 decisions;
  ADR-0020 supersedes the execution model.
- **Run `just op-test`.** envtest's first run pulls binaries; expect
  ~2 minutes the first time.
- **Read the reconciler at
  [`internal/controller/scrapejob_controller.go`](../../core/control-plane/internal/controller/scrapejob_controller.go).**
  It is a ~130-line `switch` over phases; the `runner.JobRunner`
  call site at the Running case is wired to `EngineClientRunner`
  in production (R3.1) and `StubRunner` in envtest. The reconciler
  itself does not change between the two.
- **Read `EngineClientRunner` at
  [`internal/runner/engine_client.go`](../../core/control-plane/internal/runner/engine_client.go).**
  ~140 lines of gRPC stream consumption; the bufconn-based
  test suite next to it covers the success / failure /
  cancellation / dial-error / writer-error branches.
- **For follow-up phases:** R3.2 evolves the CRD to v1alpha2; R4
  adds Postgres / Redis / Kafka per ADR-0023; R5 adds output
  sinks per ADR-0024; R6.2 brings up the Compose stack as the
  canonical local-dev loop; R7.1 packages everything as a Helm
  chart per ADR-0026.
