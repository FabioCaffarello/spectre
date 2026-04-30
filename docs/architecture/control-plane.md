# Control plane

The Spectre control plane is a Kubernetes-native operator that lets
operators submit, observe, and (eventually) schedule extraction jobs
against the engine and its adapters. PR14 opened Phase 3 with the
operator's foundation: a kubebuilder v4 scaffold under
`operators/control-plane/`, a `ScrapeJob` Custom Resource Definition, and
a state-machine reconciler. R3.1 wired the operator as a thin gRPC
client of the engine service. R3.2 evolves the CRD to v1alpha2 with
two substantive additions — `EngineRef` (Service or Endpoint) and
`OutputSink` (a discriminated union over Stdout, Kafka, S3, and
Webhook variants, CEL-validated).

## Phase 3 status

| Component                          | Status            | PR / Phase |
|------------------------------------|-------------------|------------|
| kubebuilder scaffold               | shipped           | PR14       |
| State-machine reconciler           | shipped           | PR14       |
| `JobRunner` interface              | shipped           | PR14       |
| `StubRunner`                       | shipped           | PR14       |
| `EngineClientRunner` (gRPC)        | shipped           | R3.1       |
| Operator image (unbundled)         | shipped           | R3.1       |
| `ScrapeJob` CRD v1alpha2           | shipped           | R3.2       |
| `OutputSink.Stdout`                | shipped           | R3.2       |
| `OutputSink.Kafka`                 | shipped           | R4.4       |
| `OutputSink.S3`                    | shipped           | R5.1       |
| `OutputSink.Webhook`               | shipped           | R5.1       |
| PostgreSQL job state               | shipped           | R4.2       |
| Per-service Dockerfiles            | shipped           | R6.1       |
| Compose stack (app services)       | shipped           | R6.2       |
| Operator in Compose + DinD/kind    | shipped           | R6.3       |
| Helm chart                         | not started       | R7.1       |
| `ScrapeFleet` / `ScrapeSchedule`   | not started       | post-Phase3|

PR14 shipped the reconciler with a sleep-based stub. PR15–PR18
delivered a bundled-image execution model where the operator,
the engine binary, and three adapter runtimes ran together as
nested subprocesses inside one Pod. The R1.1–R3.1 refactor
(ADR-0020) replaced that model with service-per-component:

- The engine is a stateless gRPC service (R2.3, ADR-0020 §3) that
  exposes `spectre.engine.v1alpha1.Engine.RunJob` on
  `0.0.0.0:8090` and dials adapters from `AdapterRegistry` via
  env vars (ADR-0021 §5).
- The three reference adapters (Playwright, SeleniumBase,
  curl-impersonate) are long-running gRPC services (R2.2) that
  bind `0.0.0.0:809{1,2,3}` and register `grpc.health.v1.Health`
  (ADR-0021 §6).
- The control plane (R3.1) is a thin gRPC client of the engine
  service. `EngineClientRunner` dials the engine, opens a
  `RunJob` stream, and forwards every `Row.json_line` event into
  the operator's stdout (so `kubectl logs <operator-pod>` keeps
  working per ADR-0019 §6).

R3.2 evolves the CRD to v1alpha2: per-`ScrapeJob` engine
selection via `EngineRef`, structured output-sink configuration
via the `OutputSink` discriminated union, and CEL-validated
schemas for both. v1alpha1 is deleted entirely (master strategy
§3.3 — breaking change without conversion webhook).

ADR-0019 §5's `JobRunner` interface seam held byte-for-byte
through three implementations (`StubRunner`, `SubprocessRunner`
in PR15, `EngineClientRunner` in R3.1). R3.2 keeps the interface
but moves runner construction inside `Reconcile` — each
`ScrapeJob`'s resolved endpoint may differ.

## Operator image

Per R3.1, the operator image is a Go static binary on
`gcr.io/distroless/static:nonroot`:

```
spectre-control-plane:dev (~17 MB on disk)
└── /manager      # kubebuilder manager (Go, CGO_ENABLED=0)
```

`/usr/local/bin/spectre` and `/opt/spectre/adapters/*` are gone —
the engine and adapters run as separate services. Per-service
Dockerfiles for them shipped in R6.1 (orchestrated via
`docker buildx bake`). R6.2 wired the application services
(engine + three adapters) into the Compose stack
(`docker-compose.yml`, ADR-0025). R6.3 brought the operator into
that same Compose stack alongside a local kind cluster running in
the devcontainer's Docker-in-Docker daemon (ADR-0025 §6 R6.3
update + ADR-0018 §3a). Production packaging via Helm chart is
R7.1 (ADR-0026).

### Deployment shapes

| Shape                         | Operator location  | Engine location              | Kubernetes API     | Status              |
|-------------------------------|--------------------|------------------------------|--------------------|---------------------|
| Pre-R6.2 hybrid               | host               | host (`just engine-run`)     | external kind      | retired in R6.2     |
| R6.2                          | host (`just op-run`)| Compose container            | external kind      | retired in R6.3     |
| **R6.3** (current)            | Compose container  | Compose container            | in-DinD kind       | **shipped**         |
| R7.1+ Kubernetes (Helm)       | Pod                | Pod                          | production cluster | not started         |

## Engine endpoint resolution

R3.2 introduces per-`ScrapeJob` engine selection via
`Spec.EngineRef`. The reconciler resolves the endpoint at the
`Pending → Running` transition with the following precedence:

1. `Spec.EngineRef.Endpoint` — used verbatim. The Endpoint form
   is intended for ad-hoc or out-of-cluster testing.
2. `Spec.EngineRef.Service` — rendered as
   `<name>.<namespace>.svc.cluster.local:<port>`. The Service
   namespace defaults to the ScrapeJob's own namespace; the port
   defaults to 8090 (ADR-0021's canonical engine port,
   ADR-0025 §7). The Service form is the canonical in-cluster
   pattern.
3. `Spec.EngineRef` is `nil` — the operator falls back to its
   startup-time configuration:
   - `--engine-endpoint=<host:port>` flag, or
   - `SPECTRE_ENGINE_ENDPOINT` env var, or
   - the hard-coded default `127.0.0.1:8090` (the Compose host
     port for the engine container — R6.2 / ADR-0025).

The `EngineRef` CR field is CEL-validated by the apiserver:

```
exactly one of service, endpoint must be set
```

`Status.ResolvedEngineEndpoint` records the host:port the
operator actually dialed. Inspecting it via
`kubectl get scrapejob <name> -o jsonpath='{.status.resolvedEngineEndpoint}'`
makes resolution decisions visible — if a spec uses the Service
form but the status shows the default endpoint, resolution fell
back.

The `EngineClientRunner` is constructed per-`Reconcile` from the
resolved endpoint, dials the engine per `RunJob` invocation, and
respects the context deadline the reconciler attaches from
`Spec.TimeoutSeconds`. Plain-text gRPC is acceptable in v1alpha1
because the operator-engine traffic stays on a private network
namespace (Compose / Pod network); TLS / mTLS is deferred to
v1alpha2 of the transport contract per ADR-0022 §6.

## Output sinks

R3.2 introduces `Spec.OutputSink` — a discriminated union over
four variants: `Stdout`, `Kafka`, `S3`, `Webhook`. The apiserver
enforces "exactly one variant set" via CEL:

```
exactly one of stdout, kafka, s3, webhook must be set
```

Every variant is wired post-R5.1. The reconciler accepts any of
the four sinks at admission; engine-side admission gating
surfaces destination-specific failures (e.g.
`KAFKA_UNAVAILABLE`, `S3_UNAVAILABLE`, `WEBHOOK_POST_FAILED`)
when the engine's runtime context does not support the
configured sink.

| Sink     | Runtime status         | Lands in PR | Engine-side behaviour |
|----------|------------------------|-------------|------------------------|
| Stdout   | Implemented (R3.2 / R4.2) | R3.2     | Engine streams Row events to the operator's stdout (`kubectl logs`); audit copy persisted to `job_rows`. |
| Kafka    | Implemented (R4.4)     | R4.4        | Engine publishes one message per row to the configured topic. Admission gate at engine startup; `KAFKA_UNAVAILABLE` if broker absent (ADR-0023 §3 R4.4 addendum). |
| S3       | Implemented (R5.1)     | R5.1        | Engine buffers JSONL output in memory, single `PutObject` at completion. Admission gate at engine startup with INFO-level fallback for BYO-credentials mode; `S3_UNAVAILABLE` if uploader unconfigured (ADR-0024 §3 + §5). |
| Webhook  | Implemented (R5.1)     | R5.1        | Engine POSTs rows to the configured URL — per-row or batched. Per-job admission at the executor; `WEBHOOK_POST_FAILED` after retries exhaust (ADR-0024 §4 + §5). |

`docs/architecture/output-sinks.md` is the per-sink reference;
ADR-0023 §3 R4.4 addendum and ADR-0024 are the load-bearing
architectural records.

## CEL validation

Both `EngineRef` and `OutputSink` use CEL `XValidation` rules
(stable in Kubernetes 1.25+) instead of custom admission
webhooks. The discriminated-union pattern — Go struct with
optional pointer fields, exactly one set — is the modern
Kubernetes idiom (Knative, Tekton, Argo CD all use it) and
sidesteps webhook TLS / certificate / failure-policy operational
overhead.

Spectre's target environments (kind / Compose for local testing,
modern Kubernetes 1.31+ for production) all support CEL
validation. envtest binaries that match production (1.31.x)
exercise the rules in the reconciler suite.

The CEL rules surface in the generated CRD YAML under
`x-kubernetes-validations`:

```yaml
spec:
  versions:
  - name: v1alpha2
    schema:
      openAPIV3Schema:
        properties:
          spec:
            properties:
              engineRef:
                x-kubernetes-validations:
                - rule: '(has(self.service) ? 1 : 0) + (has(self.endpoint) ? 1 : 0) == 1'
                  message: exactly one of service, endpoint must be set
              outputSink:
                x-kubernetes-validations:
                - rule: '(has(self.stdout) ? 1 : 0) + (has(self.kafka) ? 1 : 0) + (has(self.s3) ? 1 : 0) + (has(self.webhook) ? 1 : 0) == 1'
                  message: exactly one of stdout, kafka, s3, webhook must be set
```

Manual verification:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: spectre.io/v1alpha2
kind: ScrapeJob
metadata:
  name: bad-engine-ref
spec:
  jobDSL: "spectre: v1alpha1\n"
  engineRef:
    service:
      name: spectre-engine
    endpoint: 1.2.3.4:8090   # both set — should reject
  outputSink:
    stdout: {}
EOF
# Error from server (Invalid):
#   ScrapeJob.spectre.io "bad-engine-ref" is invalid:
#   spec.engineRef: Invalid value: "object":
#   exactly one of service, endpoint must be set
```

## The ScrapeJob CRD

`ScrapeJob` represents a single execution of a DSL job. The full
schema lives at
[`api/v1alpha2/scrapejob_types.go`](../../operators/control-plane/api/v1alpha2/scrapejob_types.go);
the canonical YAML is
[`config/crd/bases/spectre.io_scrapejobs.yaml`](../../operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml).

### Spec

| Field            | Type                          | Required | v1alpha2 behaviour                                                       |
|------------------|-------------------------------|----------|--------------------------------------------------------------------------|
| `jobDSL`         | string                        | yes      | Inline YAML body of the DSL job (`MinLength=1`).                         |
| `engineRef`      | `EngineRef`                   | no       | Service-or-Endpoint, CEL-validated. `nil` falls back to the operator's default endpoint. |
| `outputSink`     | `OutputSink`                  | yes      | Discriminated union (Stdout / Kafka / S3 / Webhook), CEL-validated. v1alpha2 implements only Stdout. |
| `timeoutSeconds` | int32                         | no       | Defaults to 600 (10 minutes); propagated as a context deadline.          |
| `resources`      | `corev1.ResourceRequirements` | no       | Hint forwarded to the engine; not enforced at the operator level (the engine runs in its own Pod). |

### Status

| Field                    | Type                  | Set by                                                       |
|--------------------------|-----------------------|--------------------------------------------------------------|
| `phase`                  | `ScrapeJobPhase` enum | Reconciler. State machine: Pending → Running → Completed \| Failed. |
| `startedAt`              | timestamp             | Reconciler at Pending → Running.                             |
| `completedAt`            | timestamp             | Reconciler at any → terminal phase.                          |
| `rowsExtracted`          | int64                 | Reconciler from `JobRunner.Run` return value.                |
| `error`                  | string                | Reconciler on validation or runner failure.                  |
| `resolvedEngineEndpoint` | string                | Reconciler at Pending → Running. Records the host:port actually dialed (debug aid). |
| `conditions`             | `[]metav1.Condition`  | Diagnostic; not the source of truth (see ADR-0019 §4).       |

### Phase semantics

```
                  ┌───────────┐     valid spec     ┌───────────┐     run ok     ┌─────────────┐
   create ──────► │  Pending  │ ─────────────────► │  Running  │ ─────────────► │  Completed  │
                  └───────────┘                    └───────────┘                └─────────────┘
                       │                                 │
                       │ unsupported sink                │ run error
                       ▼                                 ▼
                  ┌───────────┐                    ┌───────────┐
                  │  Failed   │                    │  Failed   │
                  └───────────┘                    └───────────┘
```

Terminal phases (`Completed`, `Failed`) are sticky: the reconciler
returns without requeue once one is reached. This makes
`kubectl wait --for=jsonpath='{.status.phase}'=Completed` work as
expected.

## Upgrading from v1alpha1

R3.2 deletes v1alpha1 entirely. Per master strategy §3.3, no
conversion webhook is implemented — there are no production
users to migrate. v1alpha1 ScrapeJob CRs still in a cluster on
upgrade are orphaned: their backing CRD version is gone, so
`kubectl` operations on them fail until they're deleted.

The dev/staging upgrade procedure:

```bash
# 1. Drain v1alpha1 ScrapeJobs (any phase — terminal CRs are
#    purely informational).
kubectl delete scrapejob --all --all-namespaces

# 2. Install the v1alpha2 CRD (Helm or raw kustomize).
helm upgrade spectre charts/spectre        # R7.1 path
# or
kubectl apply -k operators/control-plane/config/crd/

# 3. Apply v1alpha2 CRs.
kubectl apply -f operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_hello-hackernews.yaml
```

`Status.ResolvedEngineEndpoint` lets you confirm `EngineRef`
resolution post-upgrade:

```bash
kubectl get scrapejob hello-hackernews \
  -o jsonpath='{.status.resolvedEngineEndpoint}'
```

## Running locally

### Unit tests (no cluster required)

```bash
just op-test
# Equivalent to: cd operators/control-plane && make test
```

`make test` downloads apiserver and etcd binaries via setup-envtest,
runs the reconciler suite (state-machine transitions, EngineRef
resolution, OutputSink enforcement, ResolvedEngineEndpoint
status) and the runner suite (`StubRunner`, `EngineClientRunner`
over an in-process bufconn gRPC server), and prints coverage.
First run takes ~2 minutes (binary downloads); cached runs
complete in under 30 seconds.

### Operator-as-Compose-service against a kind API server

R6.3 collapses the R6.2 host/Compose split into a single Compose
stack with the operator as the eleventh service (ADR-0025 §6
R6.3 update). The kind cluster lives in the devcontainer's
Docker-in-Docker daemon; `just kind-up` is the lifecycle entry
point. The standard end-to-end flow from inside the devcontainer:

```bash
# First-time setup is automatic via .devcontainer/post-create.sh
# (kind-up + crds-install). For an explicit walkthrough:
just images                # build the five service images via bake
just kind-up               # idempotent — creates 'spectre-dev' cluster
just crds-install          # apply the v1alpha2 ScrapeJob CRD
just compose-up            # eleven services, including operator

# Apply a sample ScrapeJob.
kubectl apply -f operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_endpoint.yaml
kubectl get scrapejob -w   # Pending → Running → Completed

# Inspect operator activity.
docker compose logs -f control-plane | grep -E 'reconciling|completed|failed'

# Confirm Postgres has the row.
docker exec spectre-postgres psql -U spectre -d spectre \
  -c 'SELECT id, status, output_sink_kind, rows_extracted FROM jobs ORDER BY created_at DESC LIMIT 1'
```

Phase transitions: `Pending → Running → Completed`. The operator
container's stdout (visible via `docker compose logs
control-plane`) replaces the R6.2 foreground terminal. JSONL rows
stream from the engine to the operator's stdout per
ADR-0019 §6.

The `_endpoint` sample dials the Compose-internal `engine:8090`
hostname directly. The same address matches the operator's
`SPECTRE_ENGINE_ENDPOINT` fallback, so a sample without an
`engineRef` field would resolve identically — the explicit
endpoint makes the demo's intent visible.
`spectre_v1alpha2_scrapejob_hello-hackernews.yaml` (Service form)
targets R7.1's in-cluster Helm shape and does not work in the
post-R6.3 dev flow.

### Tearing down

```bash
just compose-down               # stop services, preserve volumes
just kind-down                  # delete kind cluster + kubeconfig
just crds-uninstall             # delete CRD before kind-down (optional)
```

`compose-down` does NOT touch the kind cluster — the lifecycles
are independent (ADR-0025 §6 R6.3 update / phase prompt §4.5).

## Architecture decisions

The substantive design choices are recorded in
[ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
and updated by
[ADR-0020](../adr/0020-microservices-architecture-supersession.md):

1. **kubebuilder v4 over operator-sdk or controller-runtime direct.**
   Reference convention; marker-driven generation; envtest
   integration. Preserved.
2. **`ScrapeJob` is the only CRD.** `ScrapeFleet` /
   `ScrapeSchedule` build on top of `ScrapeJob`. Preserved
   through v1alpha2; the CRD evolves with breaking-change
   semantics in R3.2 (per master strategy §3.3, no conversion
   webhook).
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
   `EngineClientRunner`. R3.2 keeps the interface byte-identical
   but moves construction inside `Reconcile` so each ScrapeJob's
   resolved endpoint can differ.
6. **`outputSink` accepts only `"stdout"`.** Honoured at the
   runtime level: v1alpha2's `OutputSink` is a discriminated
   union and the reconciler implements only the `Stdout` variant.
   `Kafka`, `S3`, and `Webhook` exist as schema fields and
   reject at admission until R4.4 / R5.1 wire them.

## What is not implemented yet

These deferrals are intentional and have phase pointers:

- **Output sinks beyond stdout.** `OutputSink.Kafka`,
  `OutputSink.S3`, and `OutputSink.Webhook` are schema-valid in
  v1alpha2 but the reconciler rejects them with explicit "not
  yet implemented" errors. R4.4 wires Kafka; R5.1 wires S3 and
  Webhook (under ADR-0024).
- **Per-job resource isolation.** `Spec.Resources` is a hint
  forwarded to the engine; the operator does not project it onto
  a per-job Pod. The engine runs in its own Pod with its own
  resources in v1alpha2.
- **Concurrent reconciliation.** Controller-runtime's default
  `MaxConcurrentReconciles=1` carries forward. Phase 4 may tune
  this once Postgres-backed job state (R4.2) is in.
- **CRD evolution.** v1alpha3 is unscheduled. Schema-stable
  additions (Kafka / S3 / Webhook implementations) land within
  v1alpha2; type-changing fields would force a v1alpha3.
- **Conversion webhook.** Master strategy §3.3 forbids; v1alpha1
  was deleted entirely in R3.2.
- **Custom admission webhooks.** CEL covers v1alpha2's validation
  needs (discriminated unions, basic shape rules). Webhook
  validators are post-Phase-3 if a use case emerges.
- **Fan-out and scheduling.** `ScrapeFleet` (parallel jobs over a
  parameter list) and `ScrapeSchedule` (cron-like recurrence) are
  post-Phase-3 work. Both build on `ScrapeJob` semantics.
- **Helm chart.** `helm/spectre-control-plane/` is R7.1 (ADR-0026).
- **Observability.** Prometheus metrics, OpenTelemetry traces, and
  structured logs beyond controller-runtime's defaults are
  post-Phase-3 follow-up.

## Where to start contributing

- **Read [ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
  and [ADR-0020](../adr/0020-microservices-architecture-supersession.md)
  end-to-end.** ADR-0019 records the original Phase 3 decisions
  with R3.2's addendum recording v1alpha2 as the only registered
  version; ADR-0020 supersedes the execution model.
- **Run `just op-test`.** envtest's first run pulls binaries; expect
  ~2 minutes the first time.
- **Read the reconciler at
  [`internal/controller/scrapejob_controller.go`](../../operators/control-plane/internal/controller/scrapejob_controller.go).**
  It is a `switch` over phases with two new helpers
  (`resolveEngineEndpoint`, `validateOutputSink`); the
  `runner.JobRunner` call site at the Running case is wired to
  `EngineClientRunner` in production (R3.1) and `StubRunner` in
  envtest. The `RunnerFactory` field is the per-Reconcile
  construction seam (R3.2).
- **Read `EngineClientRunner` at
  [`internal/runner/engine_client.go`](../../operators/control-plane/internal/runner/engine_client.go).**
  ~140 lines of gRPC stream consumption; the bufconn-based
  test suite next to it covers the success / failure /
  cancellation / dial-error / writer-error branches.
- **For follow-up phases:** R4 adds Postgres / Redis / Kafka per
  ADR-0023; R5 wires S3 / Webhook output sinks per ADR-0024;
  R6.2 brings up the Compose stack as the canonical local-dev
  loop; R7.1 packages everything as a Helm chart per ADR-0026.
