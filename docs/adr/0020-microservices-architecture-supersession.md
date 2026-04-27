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
