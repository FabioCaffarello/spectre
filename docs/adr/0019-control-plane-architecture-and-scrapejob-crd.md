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
