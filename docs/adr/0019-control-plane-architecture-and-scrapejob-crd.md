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
