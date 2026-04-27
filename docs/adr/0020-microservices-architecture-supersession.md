---
status: accepted
date: 2026-04-27
deciders: [Fabio Caffarello]
---

# Microservices architecture supersession

## Context and Problem Statement

Spectre's first eighteen PRs delivered the architectural primitive
the project was founded on: a driver-agnostic, polyglot, capability-
typed protocol with three reference adapters and a working
end-to-end pipeline. PR1 froze the protocol shape (ADR-0001). PR3
landed the first wire-level handshake over a Unix domain socket
(ADR-0008). PR4 through PR6 closed the Playwright reference
adapter at the v1alpha1 unary surface; PR9 through PR12 closed
SeleniumBase and curl-impersonate against the same conformance
suite (ADR-0014, ADR-0015, ADR-0016, ADR-0017). PR7 turned the
engine crate into a real orchestrator (ADR-0012); PR8 made the CLI
the engine binary (ADR-0013). PR13 introduced the Devcontainer and
a distroless engine image (ADR-0018). PR14 began the Kubernetes
operator with a `ScrapeJob` CRD and a state-machine reconciler
gated by a `JobRunner` interface (ADR-0019). PR15 wired
`SubprocessRunner`. PR16, PR17, and PR18 each bundled one of the
three reference adapters into the operator image so the
in-cluster smoke could exercise the full pipeline. Every one of
those decisions was defensible in isolation, and most carried
their own ADR.

The cumulative shape, however, drifted away from the project's
stated thesis. The Kubernetes operator image now bundles the
engine binary plus all three adapter runtimes (~1.95 GB), the
adapter-engine boundary still travels over a Unix domain socket
that ADR-0008 selected for local single-host development, the
control plane's `SubprocessRunner` shells out to the engine which
in turn spawns adapters as further subprocesses inside the same
Pod (ADR-0019 §3), and JSONL output flows up the pipe chain to
`kubectl logs`. This shape is a working demonstrator of the
protocol, but it is not coherent with the framing of Spectre as
a "disruptive microservices web scraping framework." The
architecture has accumulated localhost-isolated patterns that
the project's positioning specifically argues against. This ADR
records the maintainer's decision to refactor toward
microservices alignment, the four locked decisions that bound
the refactor, and the supersession status of the prior ADRs that
cement the patterns being retired. The project has no production
deployment to migrate; the refactor proceeds without backward-
compatibility shims.

## Decision Drivers

Four architectural drivers shape the refactor. Each is a
constraint the maintainer locked before any refactor PR opens, so
they bound the design space rather than becoming candidates for
revision during execution.

- **Microservices over subprocess-in-pod.** Spectre's positioning
  as a microservices framework requires microservices
  architecture. The subprocess-in-pod execution model (ADR-0019
  §3) was a pragmatic choice that minimised PR14's surface area,
  but it is not coherent with the project's stated identity. The
  refactor turns the engine, the control plane, and each
  reference adapter into independent services with their own
  Dockerfiles, container images, and process lifecycles.
- **TCP/gRPC over UDS.** Networked services require networked
  transport. The Unix-domain-socket transport selected in
  ADR-0008 served the project well as the lowest-friction option
  for local single-host development, but it is structurally
  incompatible with the service-per-component topology the
  refactor commits to. All adapter-engine and control-plane-
  engine traffic moves to gRPC over TCP, with explicit service
  discovery in place of subprocess spawning.
- **Stateful services included.** Job state, output streaming,
  and adapter session caching are externalised. PostgreSQL holds
  control-plane job state. Kafka carries output rows from engine
  to downstream sinks. Redis caches adapter session metadata
  across Pod restarts. Without these, the system remains an
  isolated demonstrator that cannot recover from any service's
  restart without losing in-flight work; with them, the system
  becomes a real distributed application.
- **Compose-only development.** Local development mirrors the
  production topology. `docker compose up` brings up the full
  six-service stack and is the supported path. There is no
  native fallback that runs the engine standalone, no
  adapter-as-subprocess developer mode, no Devcontainer-without-
  Docker variant. The Devcontainer ships with Docker-in-Docker
  enabled. The contribution barrier rises slightly; the
  architectural coherence of "what runs in development equals
  what runs in production" is the offsetting benefit.

These four drivers concretise into commitments later ADRs will
implement. ADR-0021 settles service discovery; ADR-0022 settles
TCP transport details; ADR-0023 settles stateful service
architecture; ADR-0024 settles output sinks; ADR-0025 settles
the Compose layout; ADR-0026 settles the Helm chart structure.
This ADR commits to the architectural direction; subsequent ADRs
fill in the specifics.

## Considered Options

Three architectural shapes were on the table once the cumulative
drift was acknowledged. Each is documented honestly so a future
reader can audit why the chosen direction was preferred.

### Option A — Status quo (subprocess-in-pod)

Keep the architecture PR14 through PR18 produced. The operator
image bundles the engine plus all three adapter runtimes; the
operator shells out to the engine; the engine spawns adapters as
further subprocesses; output flows up the pipe chain. Continue
the v1alpha2 follow-up plan that ADR-0019 sketched (an opt-in
`Mode: Pod` field on `ScrapeJobSpec`).

- Good, because it requires no refactor — the cumulative work of
  PR14 through PR18 stays in place.
- Good, because the local development story remains the lightest
  possible: a single binary plus subprocess spawn.
- Bad, because the misalignment with the project's stated
  microservices positioning persists. Every README paragraph that
  describes Spectre as a "microservices framework" remains
  contradicted by the deployed shape.
- Bad, because the bundled image at ~1.95 GB is not coherent
  with a microservices narrative regardless of how the runtime
  trees are nested. The size by itself signals monolith.
- Bad, because the v1alpha2 escape hatches (`Mode: Pod`,
  per-adapter Pods) remain hypothetical while the load-bearing
  shape stays subprocess-in-pod.

Rejected.

### Option B — Partial microservices (engine as service, adapters as subprocess)

Split the engine into its own service exposing gRPC over TCP, but
keep adapters as subprocesses spawned by the engine inside the
engine's own Pod. The control plane becomes a gRPC client of the
engine; adapter discovery stays subprocess-based.

- Good, because it preserves the lowest-friction adapter
  development workflow. Authors of new adapters keep the existing
  subprocess-launch contract from ADR-0008 and ADR-0019.
- Good, because the refactor surface is smaller — the protocol
  freeze (no proto changes) plus a single transport switch
  on the control plane / engine boundary, with adapters
  unchanged.
- Bad, because the architectural story becomes mixed. Two
  patterns coexist (service-per-component on one boundary,
  subprocess-spawn on the other) with no semantic reason for the
  asymmetry. The "what about a hybrid?" question becomes a
  recurring review prompt rather than being answered once.
- Bad, because the engine's bundled image still carries every
  adapter runtime. Image-size and operational-complexity
  benefits of microservices are deferred indefinitely.
- Bad, because the no-legacy principle (strategy prompt §2.2)
  is harder to honour. Subprocess-launching code in the engine
  becomes a permanent feature rather than a transitional shim.

Rejected by maintainer; documented here so the rejection is
visible rather than silent.

### Option C — Full microservices (chosen)

Each of the engine, the control plane, and the three reference
adapters becomes a standalone service with its own Dockerfile,
exposing gRPC over TCP. The system is backed by stateful
services (PostgreSQL, Kafka, Redis). Local development is
`docker compose up`; production deployment is Helm-installed
Kubernetes. The driver protocol v1alpha1 wire contract is
preserved; only the transport address (UDS path → TCP host:port)
and the discovery mechanism (subprocess spawn → service DNS)
change.

- Good, because the deployed shape matches the stated thesis.
  Every README paragraph describing Spectre as microservices is
  now consistent with the architecture.
- Good, because each service scales independently. An adapter
  under load adds replicas without affecting the engine; the
  engine's request budget is independent of any single adapter.
- Good, because the protocol's value as an architectural
  primitive becomes verifiable. A community-authored adapter
  ships as its own image and joins the topology without touching
  any other service.
- Good, because state is externalised into purpose-built
  services. Pod restart no longer means lost in-flight work.
- Bad, because operational complexity is the highest of the
  three options. Six services plus three stateful dependencies
  is a real distributed system, with the failure modes that
  implies.
- Bad, because Docker becomes a hard requirement for local
  development. Contributors who cannot run Docker cannot work
  locally on the project.
- Bad, because the bundled-operator-image pattern from PR16
  through PR18 is retired. Three PRs of work move from
  "load-bearing infrastructure" to "historical artifact."

Accepted.

#### A note on "what about a hybrid that keeps the CLI?"

A natural review prompt is whether the `spectre` CLI mode
(ADR-0013) could survive as a third entry point alongside the
operator and the Compose stack. The answer is no, for two
linked reasons. First, after the refactor the engine binary
exists only as a gRPC service; a CLI that wraps a service is a
different shape from a CLI that wraps an in-process pipeline,
and rebuilding it would mean writing a thin gRPC client that
duplicates what the operator already does. Second, the no-
legacy principle (strategy prompt §2.2) forbids three coexisting
entry points whose responsibilities overlap. The operator (in
Kubernetes) and Compose (locally) cover the user-facing surface
the CLI used to occupy. Keeping `spectre run` as a third path
would dilute the architecture without adding capability the
other two cannot deliver. ADR-0013 is therefore retired in full
rather than partially preserved.
