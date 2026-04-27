# Control plane

The Spectre control plane is a Kubernetes-native operator that lets
operators submit, observe, and (eventually) schedule extraction jobs
against the engine and its adapters. PR14 opens Phase 3 with the
operator's foundation: a kubebuilder v4 scaffold under
`core/control-plane/`, a `ScrapeJob` Custom Resource Definition, and
a state-machine reconciler whose execution path is a stub. Real job
execution lands in PR15.

## Phase 3 status

| Component                          | Status            | PR    |
|------------------------------------|-------------------|-------|
| kubebuilder scaffold               | shipped           | PR14  |
| `ScrapeJob` CRD (v1alpha1)         | shipped           | PR14  |
| State-machine reconciler           | shipped (stubbed) | PR14  |
| `JobRunner` interface              | shipped           | PR14  |
| `StubRunner`                       | shipped           | PR14  |
| `SubprocessRunner` (engine invoke) | not started       | PR15  |
| `ScrapeFleet` CRD                  | not started       | PR16+ |
| `ScrapeSchedule` CRD               | not started       | PR16+ |
| Helm chart                         | not started       | PR17+ |
| Webhook validators                 | not started       | PR18+ |
| Observability (metrics/traces)     | not started       | PR18+ |

The reconciler in PR14 transitions phases on the right schedule but
does not actually execute extractions. This is intentional —
ADR-0019 §3 records why a sleep-based stub is the right scope for the
kickoff PR, and ADR-0019 §5 records how the `JobRunner` interface
makes PR15's drop-in real.

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

PR14 ships envtest-based unit tests; running the operator against a
real cluster is optional but useful for the kickoff demo.

### Unit tests (no cluster required)

```bash
just op-test
# Equivalent to: cd core/control-plane && make test
```

`make test` downloads apiserver and etcd binaries via setup-envtest,
runs the reconciler suite (5 transition tests + 1 runner test), and
prints coverage. First run takes ~2 minutes (binary downloads);
cached runs complete in under 30 seconds.

### Local cluster (kind / minikube)

```bash
# 1. Bring up a cluster (kind shown; minikube works similarly).
kind create cluster --name spectre-dev

# 2. Install the CRD.
just op-install-crds
# Equivalent to: cd core/control-plane && make install

# 3. Run the operator against the current kubectl context.
just op-run
# Equivalent to: cd core/control-plane && make run
# Leaves the operator in the foreground; Ctrl-C to stop.

# 4. In another terminal, apply a sample.
kubectl apply -f core/control-plane/config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml

# 5. Watch the phase transitions.
kubectl get scrapejob -w
```

Expected output (timing matches the StubRunner's 5-second sleep):

```
NAME               PHASE       ROWS   AGE
hello-hackernews   Pending     0      1s
hello-hackernews   Running     0      2s
hello-hackernews   Completed   0      7s
```

`RowsExtracted: 0` is correct for PR14 — the StubRunner does not
produce output. PR15 wires the real engine invocation and the column
will reflect the actual row count.

### Tearing down

```bash
just op-uninstall-crds          # delete CRD + any in-flight ScrapeJobs
kind delete cluster --name spectre-dev
```

## Architecture decisions

The substantive design choices are recorded in
[ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md).
Six axes:

1. **kubebuilder v4 over operator-sdk or controller-runtime direct.**
   Reference convention; marker-driven generation; envtest integration.
2. **`ScrapeJob` is the only CRD in PR14.** `ScrapeFleet` /
   `ScrapeSchedule` build on top of `ScrapeJob`; the reverse is
   awkward.
3. **Subprocess-in-operator-pod execution.** Extends the project's
   "subprocess + protocol" thesis to a third nesting level
   (operator → engine → adapter, all in one Pod). The textbook
   alternative — Kubernetes `Job` per `ScrapeJob` — is documented
   as the v1alpha2 escape hatch.
4. **Phase as state-machine source of truth.** `Status.Conditions`
   is diagnostic, not authoritative. `kubectl wait` works against
   `.status.phase`.
5. **`JobRunner` interface as the engine seam.** PR14's `StubRunner`
   and PR15's `SubprocessRunner` share one signature. The reconciler
   is unaware of either implementation.
6. **`outputSink` accepts only `"stdout"` in v1alpha1.** The schema
   is forward-compatible with `s3://`, `pvc://`, `webhook://`,
   `kafka://`; the reconciler implements them in v1alpha2+.

## What is not implemented yet

These deferrals are intentional and have PR pointers:

- **Real job execution.** `StubRunner` sleeps and returns `(0, nil)`.
  PR15 ships `SubprocessRunner` that shells out to the spectre engine
  binary, captures JSONL, and reports row counts. Grep
  `core/control-plane/` for `// TODO(PR15)` to find the swap site.
- **Output sinks beyond stdout.** `s3://`, `pvc://`, `webhook://`,
  `kafka://` are valid `OutputSink` strings at the schema level but
  the reconciler rejects them with an explicit error. PR15+ adds
  per-sink support.
- **Per-job resource isolation.** `Spec.Resources` is recorded but
  not enforced; v1alpha1's single-Pod execution model cannot impose
  OS-level limits on individual jobs.
- **Concurrent reconciliation.** PR14 uses controller-runtime's
  default `MaxConcurrentReconciles=1`. Phase 3 follow-ups will tune
  this once real execution lands.
- **Fan-out and scheduling.** `ScrapeFleet` (parallel jobs over a
  parameter list) and `ScrapeSchedule` (cron-like recurrence) are
  PR16+ work. Both build on `ScrapeJob` semantics.
- **Helm chart.** `helm/spectre-control-plane/` is PR17+.
- **Webhook validators.** Beyond `+kubebuilder:validation` markers
  on the CRD, validating/mutating webhooks are PR18+.
- **Observability.** Prometheus metrics, OpenTelemetry traces, and
  structured logs beyond controller-runtime's defaults are
  Phase 3 follow-up.

## Where to start contributing

- **Read [ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
  end-to-end.** Six pages; one decision per axis.
- **Run `just op-test`.** envtest's first run pulls binaries; expect
  ~2 minutes the first time.
- **Read the reconciler at
  [`internal/controller/scrapejob_controller.go`](../../core/control-plane/internal/controller/scrapejob_controller.go).**
  It is a 130-line `switch` over phases; the `// TODO(PR15)` marker
  shows where PR15 lands.
- **For PR15:** the seam is `JobRunner` in
  [`internal/runner/runner.go`](../../core/control-plane/internal/runner/runner.go).
  Drop a `SubprocessRunner` next to `StubRunner`; swap the
  constructor in [`cmd/main.go`](../../core/control-plane/cmd/main.go);
  the reconciler and tests stay frozen.
