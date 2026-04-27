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
| State-machine reconciler           | shipped           | PR14  |
| `JobRunner` interface              | shipped           | PR14  |
| `StubRunner`                       | shipped           | PR14  |
| `SubprocessRunner` (engine invoke) | shipped           | PR15  |
| Operator image bundles engine      | shipped           | PR15  |
| Operator image bundles Playwright  | shipped           | PR16  |
| Operator image bundles SeleniumBase | not started       | PR17  |
| Operator image bundles curl-impersonate | not started   | PR18  |
| `ScrapeFleet` CRD                  | not started       | PR19+ |
| `ScrapeSchedule` CRD               | not started       | PR19+ |
| Helm chart                         | not started       | PR19+ |
| Webhook validators                 | not started       | PR18+ |
| Observability (metrics/traces)     | not started       | PR18+ |

PR14 shipped the reconciler with a sleep-based stub; PR15 wired
`SubprocessRunner`, which shells out to the spectre engine binary
the operator image bundles, captures JSONL on stdout, and reports
`RowsExtracted` to the reconciler. The §5 invariant from ADR-0019
held: the `JobRunner` signature, the reconciler control flow, and
the envtest suite are byte-for-byte unchanged from PR14. The only
in-tree edit to `internal/controller/` was a one-line writer swap
(`io.Discard` → `os.Stdout`) so JSONL rows reach
`kubectl logs <operator-pod>` per ADR-0019 §6.

PR16 lands the Playwright adapter in the operator image. The
runtime base is now the official Microsoft Playwright image
(`mcr.microsoft.com/playwright:v1.59.1-noble`, pinned by digest in
[`adapters/playwright/.playwright-base-image`](../../adapters/playwright/.playwright-base-image)),
which ships Ubuntu 24.04, Node 24, Chromium pinned to the matching
patch release, and every system dependency Chromium needs. The
adapter sources are compiled in a `playwright-builder` stage
(repo-root build context, so the stage can regenerate the
TypeScript protocol bindings before `pnpm build`) and the resulting
artefacts land at `/opt/spectre/adapters/playwright/`:

```
/opt/spectre/adapters/
└── playwright/
    ├── dist/             # compiled JS entry point at dist/index.js
    ├── node_modules/     # production deps (playwright, @bufbuild/protobuf, …)
    ├── driver.yaml       # transports[0].command = ["node", "dist/index.js"]
    └── package.json
```

The on-disk image weighs ~1.0 GB (pull is ~600 MB once the
Microsoft base layer is cached). The manager's `--adapters-path`
flag defaults to `/opt/spectre/adapters`; local development with
`just op-run` overrides this to the workspace `adapters/` directory.
SeleniumBase (PR17) and curl-impersonate (PR18) replicate this
builder-stage pattern in their own stages.

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

### In-cluster smoke (PR16: bundled image, kind)

```bash
# 1. Build the engine and operator images. just op-build-image
#    chains engine-image first.
just op-build-image

# 2. Bring up a kind cluster, load both images, install the CRD,
#    deploy the operator, apply hello-hackernews, poll for
#    Completed, and assert rowsExtracted >= 1. The script exits
#    non-zero on any failure with diagnostic context.
just op-smoke-kind
# Equivalent to: bash core/control-plane/hack/smoke-kind.sh
```

Expected output (against the bundled Playwright adapter):

```
NAME               PHASE       ROWS   AGE
hello-hackernews   Pending     0      1s
hello-hackernews   Running     0      2s
hello-hackernews   Completed   30     12s
```

`RowsExtracted` reflects the JSONL row count the engine emitted on
stdout (one row per Hacker News story; the front page is ~30 at
any given time). The rows themselves stream from the engine
subprocess into the operator container's stdout and surface via
`kubectl logs <operator-pod>` per ADR-0019 §6. The kind smoke is
linux/amd64 only — Apple Silicon hosts cannot run kubeadm reliably
under Rosetta amd64 emulation, so local kind smoke requires either
a Linux host or a remote Linux runner; CI's `operator-smoke-kind`
job exercises the same flow on every PR that touches the operator.

### Host operator against a local cluster (PR15-style)

The PR15 host-operator workflow still works for iterating on the
manager binary itself without rebuilding the image:

```bash
# 1. Bring up a cluster.
kind create cluster --name spectre-dev

# 2. Install the CRD.
just op-install-crds

# 3. Run the operator against the current kubectl context. The
#    --adapters-path override points at the workspace adapters/
#    directory (the bundled-image default of /opt/spectre/adapters
#    does not exist on the developer host).
just op-run

# 4. In another terminal, apply a sample.
kubectl apply -f core/control-plane/config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml

# 5. Watch the phase transitions.
kubectl get scrapejob -w
```

`op-run` runs the operator on the developer host, so "operator
logs" here means the foreground terminal where the recipe is
running.

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

- **SeleniumBase and curl-impersonate bundling.** PR16 shipped the
  Playwright adapter in the operator image; SeleniumBase needs
  Google Chrome (not Chromium) and a uv-managed Python venv (PR17),
  and curl-impersonate needs the native libcurl-impersonate library
  (PR18). Each replicates the PR16 builder-stage pattern in its own
  Dockerfile stage.
- **Output sinks beyond stdout.** `s3://`, `pvc://`, `webhook://`,
  `kafka://` are valid `OutputSink` strings at the schema level but
  the reconciler rejects them with an explicit error. PR16+ adds
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
  It is a ~130-line `switch` over phases; the `runner.JobRunner`
  call site at the Running case is what PR15 wired to a real engine.
- **For PR17 (SeleniumBase bundling):** read PR16's
  [`Dockerfile`](../../core/control-plane/Dockerfile) — the
  `playwright-builder` stage is the pattern PR17's
  `seleniumbase-builder` stage replicates with `uv`. SeleniumBase
  drives Google Chrome (not Chromium), so the runtime base needs
  Chrome installed; either install it on top of the Microsoft
  Playwright base or pick a different base. Resources and volumes
  in `config/manager/manager.yaml` carry over.
- **For PR18 (curl-impersonate bundling):** the Go adapter compiles
  to a single binary (no browser), but it links the native
  libcurl-impersonate. The runtime needs the impersonate variant
  (`curl_chrome116`) on PATH — either via the published release
  tarball (the CI `python` job already documents the pinning) or
  via apt where available.
