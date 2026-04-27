---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# Control plane architecture and ScrapeJob CRD (Phase 3 kickoff)

## Context and Problem Statement

Phase 2 closed with three reference adapters spanning three runtimes
(Playwright/TypeScript, SeleniumBase/Python, curl-impersonate/Go) all
implementing the v1alpha1 unary surface and passing the conformance
suite (PR12). PR13 opened Phase 2.5 with a Devcontainer and a
distroless engine image. The placeholder `core/control-plane/`
directory has held identity-stub Go code since PR2's code-generation
pipeline; substantive control-plane work was deferred to Phase 3 and
listed in the roadmap as such.

Phase 3 is the project's last unbuilt component and the most ambitious
one remaining: a Kubernetes-native control plane that lets operators
submit, schedule, and observe extraction jobs against the engine and
its adapters. PR14 is to Phase 3 what PR1 was to the project as a
whole — a foundation, not a finished system. This ADR records the
architectural decisions that commit Phase 3 to a particular
trajectory: scaffolding tool, the first CRD, the adapter execution
model, the status semantics, the engine integration boundary, and the
shape of output-sink configuration.

The decisions below settle the design space PR15+ will execute
against. ADR-0001 (the driver protocol as architectural primitive),
ADR-0002 (polyglot language selection — control plane in Go), and
ADR-0007 (protocol code generation) are the load-bearing upstream
context. Of the six decisions, §3 (the adapter execution model) is
the most architecturally consequential — it commits the Phase 3
trajectory to the same "subprocess + protocol" pattern that already
shapes the engine and adapters.

## Decision Drivers

- **Convention recognition.** A senior Kubernetes engineer opening
  the operator should recognise it within a minute. Idiomatic
  scaffolding, idiomatic CRDs, idiomatic reconciler patterns.
- **Minimum viable scope.** PR14 ships scaffolding plus one CRD plus
  a state-machine reconciler. Real execution, fan-out CRDs, Helm
  charts, observability — all deferred. The kickoff PR cements the
  architecture; a follow-up PR proves it works end-to-end.
- **Architectural symmetry with the rest of the project.** Spectre's
  thesis is "subprocess + protocol." The engine launches adapters
  as subprocesses (PR7). The control plane should extend the pattern,
  not introduce a different one.
- **The PR15 seam.** The reconciler's "execution" path is the most
  important interface to get right in PR14. PR15 must drop in a real
  engine-invoking implementation without refactoring the reconciler
  or the test suite.
- **Reviewability.** Each decision should be defensible in isolation
  so that a future revisit (likely v1alpha2) can replace one axis at
  a time without unwinding the others.

## Decisions

### 1. kubebuilder over operator-sdk or controller-runtime direct

Chosen: **kubebuilder v4** for project scaffolding, CRD generation,
RBAC marker processing, and controller wiring.

Reasons in order of weight:

- **Reference convention in the Kubernetes ecosystem.** kubebuilder
  is SIG-API-Machinery's canonical tooling for building operators.
  Reviewers and Kubernetes engineers recognise the layout, the
  marker syntax, and the Makefile conventions immediately.
- **Single source of truth.** CRDs, RBAC, and webhook manifests
  generate from `+kubebuilder:` markers in Go source. The Go types
  are the schema; the YAML is committed generated content that
  stays in sync via `make manifests`.
- **`envtest` integration.** The controller-runtime `envtest`
  package spins up a real apiserver and etcd locally — no external
  cluster needed for the unit-test suite. This is materially better
  than mocking the controller-runtime client.
- **Stable CLI.** kubebuilder v4 is mature, well-documented, and
  pairs cleanly with the Microsoft Devcontainer base image PR13
  introduced (a single binary install via `go install` or release
  tarball).

Rejected:

- **operator-sdk.** A wrapper around kubebuilder plus
  Ansible-operator and Helm-operator runtimes. The Go-operator
  subset is structurally identical to kubebuilder, and the extra
  runtimes are not relevant to a Go-native operator that talks to a
  Rust engine. Adopting operator-sdk would impose its release cadence
  and conventions for no marginal benefit.
- **controller-runtime direct (no scaffolding).** Possible but loses
  the marker-driven generation tooling. A bespoke layout would also
  diverge from what the Kubernetes audience expects. Not worth
  reinventing.

### 2. ScrapeJob is the only CRD in PR14

Chosen: **ship `ScrapeJob` (singular) in PR14; defer `ScrapeFleet`
and `ScrapeSchedule` to PR16+.**

`ScrapeJob` represents one execution of a DSL job. It is the simplest
CRD that makes the operator useful and the foundation that future
CRDs build on. Future CRDs reference `ScrapeJob` semantics — they
either create `ScrapeJob` instances (`ScrapeFleet` for parallel
fan-out, `ScrapeSchedule` for cron-like recurrence) or template them.
The reverse direction is awkward: trying to extract a single-execution
CRD out of a fan-out wrapper after the fact creates schema churn.

Reasons:

- **Minimum viable scope.** One CRD, one reconciler, one state
  machine. The whole surface fits in a single PR review.
- **Composability.** Future fan-out and schedule CRDs delegate to
  `ScrapeJob`; the reverse is harder. Picking the right primitive
  first avoids retrofitting later.
- **Testability.** envtest with one CRD is straightforward; multiple
  CRDs multiply test surface and add cross-CRD interactions that
  PR14 cannot meaningfully exercise without a real workload.

Future CRDs sketched here without commitment, to make the scope
boundary explicit:

- **`ScrapeFleet`.** Fan-out wrapper that creates N `ScrapeJob`
  instances with parameter substitution (e.g. a list of URLs).
  Reconciler aggregates child status into a fleet-level summary.
- **`ScrapeSchedule`.** Cron-like recurrence that templates a
  `ScrapeJob` and re-creates it on a schedule, similar to the
  Kubernetes `CronJob` controller's relationship with `Job`.

Both are PR16+ work. PR14 does not anticipate their schemas in
`ScrapeJob` fields; v1alpha1 stays minimal.

### 3. Adapter execution model — subprocess inside operator pod

Chosen: **the operator runs as a single Pod. When a `ScrapeJob`
transitions to Running, the operator (in PR15+) spawns the engine
binary as a subprocess within its own Pod. The engine, in turn,
spawns the adapter as a subprocess within the same Pod.** Output
(JSONL) flows from adapter → engine → operator stdout through
stream piping. There are no per-adapter Pods, no `Job` objects, no
sidecar containers, and no service-mesh hops between operator and
adapter.

This is the most architecturally consequential decision in the PR.
It commits Phase 3 to the same "subprocess + protocol" pattern that
already shapes PR7 (engine launches adapter as subprocess) and the
conformance harness (Python pytest fixture launches each adapter as
a subprocess). PR14's reconciler ships a stub that sleeps; PR15
replaces the stub with a `SubprocessRunner` that invokes the
engine. The control plane becomes the third nested subprocess
layer, all inside one Pod.

Reasons in order of weight:

- **Architectural symmetry.** The project's load-bearing pattern is
  "subprocess + protocol." Engine launches adapters as subprocesses;
  the conformance suite launches adapters as subprocesses; the
  operator extends the pattern by running the engine as a
  subprocess. Three nested processes, one Pod, one process tree.
  Anyone who understands one layer understands the others.
- **Minimum operational complexity.** No Pod-orchestration logic in
  the operator. No image-pull-secret threading, no Pod-lifecycle
  state synchronisation between the reconciler and per-job Pods, no
  service mesh between operator and adapter. The operator deals in
  Go function calls; Kubernetes deals in one Pod.
- **Faster startup.** Spawning a process is milliseconds; spawning
  a Pod is seconds at best (image pull, scheduler, kubelet,
  container runtime). v1alpha1 targets interactive jobs where
  startup latency dominates total job time. The asymmetry matters.
- **Existing test infrastructure carries over.** The PR7 end-to-end
  integration test exercises exactly the code path the operator
  will exercise. PR15 reuses it. No new test architecture is needed
  for the engine-as-subprocess case; envtest covers the
  reconciler-as-state-machine case.

Trade-offs documented honestly:

- **Resource isolation.** All adapter executions share the
  operator Pod's resource budget. A runaway job competes with the
  reconciler for CPU and memory. PR15 will surface adapter
  timeouts and resource hints from `ScrapeJobSpec.Resources`, but
  enforcement at the OS-process level is best-effort. v1alpha2
  may revisit per-job Pods if multi-tenant pressure becomes real.
- **Horizontal scaling.** A single operator Pod handles all
  `ScrapeJob` reconciliations. Phase 3 follow-ups will add
  concurrent reconciliation with a worker pool inside the operator
  (controller-runtime's `MaxConcurrentReconciles`), which scales
  vertically. True horizontal scaling — multiple operator
  replicas, leader election for the reconciler, work distribution
  — is v1alpha2 territory. v1alpha2 may also add a
  `Job-per-ScrapeJob` mode behind a `ScrapeJobSpec.Mode` field if
  the demand emerges.
- **Adapter image flexibility.** The `adapterImage` field exists
  on `ScrapeJobSpec` (a string, not a typed reference), reserved
  for future per-Pod-per-job execution where users supply their
  own adapter image. v1alpha1 ignores the field entirely; the
  operator runs with whatever adapter binaries it was deployed
  with. Documented in the CRD schema and in the architecture guide.

Rejected:

- **Job-per-ScrapeJob (the obvious "Kubernetes way").** Each
  `ScrapeJob` creates a Kubernetes `Job` that runs the engine with
  the chosen adapter image. The reconciler watches the `Job` and
  mirrors its phase into `ScrapeJobStatus`.

  - Good, because it is the textbook pattern: per-job isolation,
    per-job resource limits, per-job image, horizontal scaling for
    free.
  - Bad, because Pod startup latency dominates job latency for
    interactive workloads, which is what v1alpha1 targets.
  - Bad, because the reconciler grows substantial Pod-lifecycle
    code (image pulls, image-pull-secret threading, Pod
    spec construction, container status interpretation, log
    streaming through `kubectl logs`-equivalent APIs). All of that
    is operational complexity that pays off only when v1alpha2
    needs the isolation it buys.
  - Bad, because the existing PR7 integration test does not
    exercise this path; PR15 would have to build new test
    infrastructure that simulates the kubelet's role.

  This is the strongest rejected alternative. ADR-0019's
  v1alpha2 follow-up may revisit it as an opt-in `Mode: Pod` on
  `ScrapeJobSpec`.

- **Pod-per-adapter sidecar model.** The operator creates a Pod
  with engine and adapter as sidecar containers; status flows via
  Pod conditions and shared-volume signalling. Even more complex
  than Job-per-ScrapeJob, and does not solve a real v1alpha1
  problem. Rejected without further consideration.

- **Custom CRI executor.** Out of scope. Writing a kubelet-
  equivalent for adapter execution would mean building a parallel
  container-runtime stack. Mentioned only to make the rejection
  explicit; not seriously considered.

The chosen model is right for v1alpha1. v1alpha2 may revisit any
of the three trade-offs above with a single targeted ADR.

> **Status update (PR16).** The §3 invariant (single Pod, three
> nested processes) held. PR16 added a `playwright-builder` stage
> to `core/control-plane/Dockerfile` and switched the runtime base
> to the official Microsoft Playwright image
> (`mcr.microsoft.com/playwright:v1.59.1-noble`, pinned by digest
> in `adapters/playwright/.playwright-base-image`). The Playwright
> adapter is installed at `/opt/spectre/adapters/playwright/`
> (compiled `dist/`, pruned production `node_modules/`, and
> `driver.yaml`); Chromium and its system dependencies come from
> the base image. The reconciler, the `JobRunner` interface, the
> runner unit tests, and the envtest suite are byte-for-byte
> unchanged from PR15 — the only Go diff is a two-line
> default-flag change in `cmd/main.go` (`--adapters-path` defaults
> to `/opt/spectre/adapters`). The bundled engine + adapter flow
> produced 30 JSONL rows from `hello-hackernews` end-to-end.
> SeleniumBase (PR17) and curl-impersonate (PR18) replicate the
> same builder-stage pattern; the v1alpha2 escape hatches above
> remain available unchanged.
>
> **Status update (PR17).** The §3 invariant held again with two
> adapters bundled. PR17 added a `seleniumbase-builder` stage that
> uses `uv` (pinned 0.5.11) to build the adapter's virtualenv at
> the FINAL runtime path (`/opt/spectre/adapters/seleniumbase/.venv`)
> so the venv's shebangs and `pyvenv.cfg` resolve unchanged after
> COPY. The runtime stage extends — does not replace — the
> Microsoft Playwright base from PR16 §4.1 with an apt overlay
> (Python 3.12, Google Chrome stable from `dl.google.com`, and a
> matching ChromeDriver provisioned via SeleniumBase's own
> installer). Chrome and Chromium coexist cleanly: Playwright
> launches Chromium via `PLAYWRIGHT_BROWSERS_PATH`, SeleniumBase
> launches `/usr/bin/google-chrome` via ChromeDriver. At any
> moment at most one runs (`MaxConcurrentReconciles=1` plus the
> DSL `driver:` field selects exactly one). The §5 invariant
> (reconciler / `JobRunner` / runner tests / envtest suite) held
> byte-for-byte; PR17 has zero Go changes. The only adapter
> source diff was a five-line patch to
> `_default_driver_factory` adding a
> `SPECTRE_SELENIUMBASE_CONTAINER` env-var knob that injects
> `--no-sandbox --disable-dev-shm-usage` for the `restricted`
> PodSecurityStandard; the env var is set in
> `config/manager/manager.yaml`, the memory limit there bumped
> from 2 GiB to 3 GiB for SeleniumBase headroom. curl-impersonate
> (PR18) replicates the pattern in its own stage; the v1alpha2
> escape hatches above remain available unchanged.
>
> **Status update (PR18).** The §3 invariant held a third time
> with all three reference adapters bundled. PR18 added a
> `curl-impersonate-builder` stage (Go, `CGO_ENABLED=0`) that
> regenerates the gitignored protocol bindings via `buf generate`
> + `tools/codegen/post-generate.sh` and produces a single static
> `bin/adapter`. The runtime stage extends — does not replace —
> the layered base from PR16/PR17 with the upstream
> `curl-impersonate-v${VERSION}.x86_64-linux-gnu.tar.gz` release
> tarball; the variant binaries (`curl_chrome116`, `curl_chrome110`,
> `curl_firefox117`, `curl_safari16_5`, …) are static (ADR-0016 §1)
> so they install-and-go on `/usr/local/bin/`. The version + SHA-256
> are pinned together in
> `adapters/curl-impersonate/.curl-impersonate-version` (one line:
> `VERSION SHA256`); the Dockerfile downloads the tarball and
> verifies the SHA-256 as defence-in-depth against tarball
> tampering. No Pod-spec change: curl-impersonate has no browser
> to sandbox, so no `--no-sandbox` knob, no `/dev/shm` sizing, no
> `SPECTRE_*_CONTAINER` env var. The §5 invariant (reconciler /
> `JobRunner` / runner tests / envtest suite) held byte-for-byte;
> PR18 has zero Go changes. ADR-0016 §1's subprocess-over-cgo
> contract held byte-for-byte too: the adapter still shells out
> to `curl_chrome116` per Navigate, no link against
> `libcurl-impersonate.so`. The kind smoke now runs all three
> samples sequentially (hello-hackernews → seleniumbase-extract →
> curl-impersonate-extract); all three reach Completed. PR18
> closes v1alpha1 adapter bundling. The v1alpha2 escape hatches
> above remain available unchanged.

### 4. ScrapeJob status as state machine, not condition arbiter

Chosen: **`ScrapeJobStatus.Phase` is the source of truth for the
job's lifecycle state, with strictly monotonic transitions:
`Pending → Running → Completed | Failed`.** Once a terminal phase
(`Completed` or `Failed`) is reached, no further transitions occur
and the reconciler returns without requeue.

`Status.Conditions` is present on the type for standard Kubernetes
condition entries (`Ready`, `Progressing`, etc.) used for diagnostic
purposes, but the phase enum — not the condition list — is the
authoritative lifecycle state. Tools like `kubectl wait
--for=jsonpath='{.status.phase}'=Completed` work directly against
`Phase`; condition entries are added incrementally as PR15+ surfaces
new diagnostic signals.

Reasons:

- **Predictability for users.** `kubectl get scrapejob` shows a
  single `PHASE` printer column. Users can reason about job state
  from one cell, not from a list of condition entries with
  overlapping semantics.
- **Simpler reconciler.** The reconciler is a `switch` on `Phase`
  with one case per state. Terminal phases short-circuit by
  returning `ctrl.Result{}` with no requeue.
- **Compatibility with `kubectl wait`.** The single-field jsonpath
  match is the supported idiom.
- **Condition explosion is a v1alpha2 problem, not a v1alpha1
  problem.** Adding conditions later does not break consumers that
  depend on `Phase`. Promoting conditions to authoritative state
  later would.

The phase-vs-conditions distinction is intentional. ADR-0019 does
not declare conditions deprecated — only that `Phase` is
authoritative. Future reconciler iterations may attach conditions
for `Validated`, `EngineLaunched`, `OutputDrained`, etc., as
diagnostic signals that complement (not replace) the phase.

### 5. The JobRunner interface boundary

Chosen: **the reconciler does not invoke the engine directly.
Instead, it depends on a `JobRunner` interface, defined in
`internal/runner/`.** PR14 ships two implementations: `StubRunner`
(used by the reconciler in PR14 and by all unit tests) and a
reserved seam for `SubprocessRunner` (the real engine-invoking
implementation that PR15 will introduce).

> **Status update (PR15).** `SubprocessRunner` landed in PR15
> without changes to the `JobRunner` signature, the reconciler
> control flow, the envtest suite, or the runner unit tests for
> `StubRunner`. The single in-tree edit to `internal/controller/`
> was a one-line writer swap (`io.Discard` → `os.Stdout`) so JSONL
> rows surface in `kubectl logs`. The §5 invariant held.

```go
type JobRunner interface {
    // Run executes the DSL job and writes JSONL rows to writer.
    // Returns total rows extracted on success, or error on any
    // failure.
    Run(ctx context.Context, jobDSL string, writer io.Writer) (int64, error)
}
```

This is the most important seam in PR14. The reconciler is wired
to a `JobRunner` via dependency injection (`main.go` constructs
the implementation; the reconciler accepts it as a struct field).
PR15's first action is to drop in `SubprocessRunner` without
touching the reconciler logic, the controller wiring, or the
envtest suite. If PR14 gets the signature wrong, PR15's "drop-in
replacement" becomes a refactor that also touches the tests.

Reasons:

- **The reconciler is testable today.** envtest with the stub
  exercises every transition end-to-end without needing the engine
  binary, the protocol bindings, or a Chromium install.
- **PR15 is mechanical.** Replace one constructor call in
  `main.go`; add `SubprocessRunner` next to `StubRunner`. The
  reconciler test suite continues to use the stub. New end-to-end
  tests for `SubprocessRunner` live next to it in the same
  package.
- **The signature reflects the protocol primitive.** `Run` takes
  the DSL document (a string) and a `Writer` for JSONL output —
  the same shape the engine binary uses. `int64` row count maps
  cleanly to `ScrapeJobStatus.RowsExtracted`. `error` covers
  validation, launch, runtime, and timeout failures uniformly;
  the reconciler does not need to discriminate between failure
  modes for v1alpha1.
- **Context is honoured.** `ctx` carries the timeout from
  `ScrapeJobSpec.TimeoutSeconds` (defaulting to 10 minutes). The
  stub honours `ctx.Done()` so timeout tests work without real
  blocking work.

Rejected alternatives:

- **Direct engine invocation in the reconciler.** Tightly couples
  the reconciler to the engine's CLI surface. Tests would need a
  built `spectre` binary on PATH, which envtest does not provide.
- **A richer interface (multiple methods, structured options).**
  PR15 may need to grow the surface (e.g. a `Stream` method that
  returns a channel of rows). v1alpha1 ships the smallest surface
  that supports the reconciler's needs; growing later is additive
  and does not break existing implementations.

### 6. The CRD's outputSink field accepts only "stdout" in PR14

Chosen: **`ScrapeJobSpec.OutputSink` is a string field whose
v1alpha1 grammar accepts only the literal value `"stdout"` (or
empty, treated as `"stdout"`).** The reconciler, when PR15 wires
real execution, writes JSONL output to the operator Pod's stdout
where it appears in `kubectl logs <operator-pod>`. Other sink
syntaxes (`s3://...`, `pvc://...`, etc.) are recognised by the
schema as valid string values but rejected by the reconciler's
validator with an explicit error in `Status.Error`.

The field exists on the spec today so that future sinks do not
require a CRD-schema change. v1alpha1 of the *spec field* is
forward-compatible with v1alpha2's expanded sink set; only the
reconciler's runtime validation tightens or loosens.

Future sinks sketched here for the design space:

- `s3://bucket/path/job-${name}.jsonl` — streaming upload via the
  AWS SDK; row buffering and multipart upload tuned per workload.
- `pvc://claim-name/path` — write to a mounted PersistentVolume;
  useful for large extractions where stdout-streaming becomes a
  log-pipeline stress test.
- `webhook://url` — POST batched rows to an HTTP endpoint;
  pairs well with downstream consumers that prefer push over poll.
- `kafka://broker/topic` — stream to Kafka; for adopters with
  established stream-processing pipelines.

These are v1alpha1 of the CRD *schema* (the field accepts the
syntax) and v1alpha2+ of the *runtime* (the reconciler implements
them one at a time with their own ADRs). The grammar is documented
in the architecture guide so contributors can grep for the
PR14-rejected sinks when adding support.

## Consequences

- Good, because the operator is recognisable to the Kubernetes
  audience: kubebuilder layout, idiomatic CRD with printer columns
  and subresource status, controller-runtime reconciler with envtest
  coverage. A senior reviewer can navigate the code in minutes.
- Good, because the project's "subprocess + protocol" thesis extends
  uniformly into Phase 3. The control plane is not a new
  architectural pattern; it is the existing pattern at a third
  level of nesting.
- Good, because the JobRunner interface isolates the reconciler from
  engine evolution. PR15 changes one file (`internal/runner/`) and
  one constructor call. The reconciler and its tests stay frozen.
- Good, because every decision can be revisited individually in a
  v1alpha2 ADR without unwinding the others. The execution model in
  particular has a clear v1alpha2 escape hatch (`Mode: Pod`).
- Bad, because the single-Pod execution model gives up per-job
  isolation that operators may eventually want. Documented;
  v1alpha2 has a path.
- Bad, because the stdout-only output sink in v1alpha1 is a real
  limit for non-trivial workloads. The CRD schema accommodates
  growth, but the reconciler does not until PR15+.
- Neutral, because shipping ScrapeJob alone defers fan-out and
  scheduling. The deferral is intentional — both build on
  `ScrapeJob` semantics that PR14 cements.

## Confirmation

The decision is working when:

1. `cd core/control-plane && make test` exits zero on Linux and
   macOS, exercising the reconciler's state machine end-to-end via
   envtest (apiserver + etcd binaries downloaded by setup-envtest).
2. `make install && make run` brings up the operator against the
   developer's current kubectl context; `kubectl apply -f
   config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml`
   produces the documented phase progression in `kubectl get
   scrapejob -w`.
3. The CI `operator` job is green on every PR that touches
   `core/control-plane/**`.
4. A grep for `// TODO(PR15)` in `core/control-plane/` returns the
   `StubRunner`-replacement site. PR15's first action finds the seam
   in seconds.
5. No PR1–PR13 invariant regresses. The conformance suite still
   passes against all three adapters; `spectre run examples/...`
   still produces JSONL.

The PR15 acceptance criterion that closes the loop on this ADR's
§5 (JobRunner) is: replacing `StubRunner` with `SubprocessRunner`
requires zero changes to `internal/controller/` and zero changes to
the envtest suite.

## More Information

- kubebuilder book: <https://book.kubebuilder.io/>
- controller-runtime: <https://pkg.go.dev/sigs.k8s.io/controller-runtime>
- envtest: <https://book.kubebuilder.io/reference/envtest.html>
- CRD versioning best practices:
  <https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/>
- Operator Pattern:
  <https://kubernetes.io/docs/concepts/extend-kubernetes/operator/>
- Related ADRs:
  [ADR-0001 Driver protocol as architectural primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0002 Polyglot language selection](0002-polyglot-language-selection.md),
  [ADR-0007 Protocol code generation](0007-protocol-code-generation.md),
  [ADR-0013 CLI as engine binary](0013-cli-as-engine-binary.md),
  [ADR-0018 Devcontainer and engine image](0018-devcontainer-and-engine-image.md).

## Update (R1.1, ADR-0020)

This ADR's decisions are partially superseded. Specifically:

- §1 (kubebuilder over operator-sdk or controller-runtime
  direct): preserved. The scaffold, the marker-driven CRD/RBAC
  generation, and the controller-runtime / envtest pairing carry
  forward.
- §2 (`ScrapeJob` as the only CRD in PR14): preserved. The CRD
  evolves to v1alpha2 in R3 with breaking-change semantics and
  no conversion webhook (no production users to migrate);
  `ScrapeFleet` and `ScrapeSchedule` remain deferred per the
  original deferral rationale.
- §3 (Adapter execution model — subprocess inside operator pod):
  superseded by
  [ADR-0020](0020-microservices-architecture-supersession.md).
  The single-Pod, three-nested-process model is replaced by
  service-per-component: the engine becomes a gRPC service the
  control plane dials, and adapters become services the engine
  dials. The bundled-operator-image pattern from PR16/PR17/PR18
  is retired in R6.
- §4 (`ScrapeJob` status as state machine, not condition
  arbiter): preserved. The strictly monotonic `Phase`
  progression carries forward into v1alpha2.
- §5 (`JobRunner` interface boundary): preserved and validated
  by the refactor. `EngineClientRunner` becomes the third
  implementation against the same interface (after `StubRunner`
  and the now-retired `SubprocessRunner`); the reconciler logic
  and the envtest suite stay frozen across the swap, as they did
  in PR15 / PR16.
- §6 (`OutputSink` field accepts only `"stdout"` in PR14):
  preserved as a CRD-schema commitment. The field's grammar
  remains forward-compatible; the runtime grows S3, webhook, and
  Kafka sinks under ADR-0024 in R5.

The refactor's phase R3 contains the implementation work that
deletes `SubprocessRunner` and lands `EngineClientRunner`. See
[ADR-0020](0020-microservices-architecture-supersession.md) §5
for the full phase sequence.
