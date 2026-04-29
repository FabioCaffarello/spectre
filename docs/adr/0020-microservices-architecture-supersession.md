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

## Decision Outcome

Chosen option: **Option C — Full microservices**.

Spectre will refactor to a fully microservices architecture. Each
component — the engine, the control plane, and each of the three
reference adapters — becomes a standalone service with its own
Dockerfile, exposing gRPC over TCP. The system is backed by
stateful services (PostgreSQL for job state, Kafka for output
streaming, Redis for adapter session cache). Local development
happens via `docker compose up`; production deployment uses
Helm-installed Kubernetes manifests. The driver protocol
v1alpha1 wire contract is preserved unchanged; only the transport
address and the discovery model change. The `spectre` CLI mode
(ADR-0013) is retired completely; the operator and the Compose
stack are the two user-facing entry points.

### Consequences

- Good, because the deployed shape becomes coherent with the
  project's stated thesis. Documentation and architecture
  diagrams describing microservices are now factually accurate
  rather than aspirational.
- Good, because services scale independently. The engine, the
  control plane, and each adapter have their own resource
  budgets, replica counts, and failure domains.
- Good, because the protocol primitive (ADR-0001) becomes
  visibly load-bearing. A community-authored adapter ships as
  its own image and joins the topology without modifying any
  other service.
- Good, because state externalisation removes a class of failure
  the demonstrator could not handle. Pod restarts no longer mean
  lost in-flight work; adapter session caches survive engine
  restarts; output streaming is durable rather than ephemeral.
- Good, because local development mirrors production topology.
  Contributors who run `docker compose up` exercise the same
  service graph that runs in Kubernetes; "works on my machine"
  divergence shrinks to image-pull and resource-limit
  differences.
- Good, because the conformance suite's value compounds. The
  same 56 tests that ran against subprocess-spawned adapters in
  Phase 2 will run against TCP-dialled adapter services after
  Phase R2; the protocol contract holds across both transports.
- Bad, because operational complexity rises substantially. Six
  services plus PostgreSQL, Kafka, and Redis is a real
  distributed system; the failure modes (network partition,
  partial broker availability, Redis cache miss under churn)
  require engineering attention that the demonstrator did not.
- Bad, because Docker becomes a hard requirement for local
  development. There is no native fallback for contributors who
  cannot run Docker. The Devcontainer with Docker-in-Docker
  closes some of the gap but not all of it.
- Bad, because the bundled-operator-image pattern from PR16
  through PR18 is retired. The work was correct in its phase
  (it closed v1alpha1 adapter bundling and proved the pipeline
  end-to-end) but its outputs are not reusable in the new
  architecture. Three PRs' worth of Dockerfile staging moves
  from "load-bearing" to "historical record."
- Bad, because the CLI retirement breaks any contributor habits
  that depend on `spectre run`. The operator and the Compose
  stack cover the same surface but require Kubernetes or Docker
  respectively; there is no minimal shell-only invocation
  anymore.
- Neutral, because the refactor is bounded. Seventeen PRs across
  eight phases (see §5) deliver the new architecture; the
  refactor has a defined endpoint rather than being open-ended.
- Neutral, because the protocol freeze means adapter source
  trees see minimal change. The wire format does not move;
  adapter authors update their listening transport and their
  manifest discovery, not their RPC handlers.

### Confirmation

The refactor is complete when all of the following hold:

1. `git clone` followed by `docker compose up` produces a
   running stack of six application services (control-plane,
   engine, three adapters, plus PostgreSQL, Kafka, Redis).
2. `kubectl apply -f scrapejob.yaml` against a Helm-installed
   cluster produces JSONL output to the configured sink (S3,
   webhook, or Kafka topic).
3. The conformance suite passes against the running Compose
   stack with the same test count as pre-refactor, all green,
   no skipped tests.
4. Each adapter's declared capability list is byte-for-byte
   identical to its pre-refactor manifest (Playwright 13,
   SeleniumBase 12, curl-impersonate 6).
5. No legacy code paths remain. Specifically: no UDS transport
   support, no `SubprocessRunner`, no `spectre run` CLI, no
   bundled operator image with adapters inside.
6. The cross-driver demo runs the same `ScrapeJob` YAML against
   three different `driver:` values and produces equivalent
   results from the same engine service.

## Implementation phases

The refactor is delivered as roughly seventeen PRs across eight
phases. The sequence is fixed; phases cannot be reordered or
skipped. Each phase introduces or supersedes a defined set of
ADRs so the audit trail evolves alongside the code.

| Phase | PRs    | Focus                                            | ADRs introduced / superseded                                       |
|-------|--------|--------------------------------------------------|--------------------------------------------------------------------|
| R1    | 1      | Architectural supersession (this ADR)            | +ADR-0020. Updates ADR-0008, ADR-0009, ADR-0013, ADR-0019 status.  |
| R2    | 3      | TCP transport + service discovery                | +ADR-0021, +ADR-0022. Supersedes ADR-0008 §2 (UDS).                |
| R3    | 2      | Control plane refactor + ScrapeJob CRD v1alpha2  | Supersedes ADR-0019 §3 (subprocess-in-pod) and §5 (`SubprocessRunner`). |
| R4    | 4      | Stateful services (PostgreSQL, Kafka, Redis)     | +ADR-0023. Updates ADR-0010 (session state location).              |
| R5    | 1      | Output sinks (S3, webhook, Kafka topic)          | +ADR-0024.                                                          |
| R6    | 3      | Per-service Dockerfiles + Compose stack + Devcontainer | +ADR-0025. Retires PR16-PR18 bundled-image pattern.           |
| R6.5  | 4      | Quality & hardening (sub-phase insertion: stale-references sweep + R6.1 leftovers, CI hardening, Docker Hub registry wiring + multi-arch, shared codegen base) | None. Hygiene work; addresses drift accumulated across the long refactor. |
| R7    | 2      | Helm chart + production smoke                    | +ADR-0026.                                                          |
| R8    | 1      | Documentation refresh + narrative closing        | None (docs only).                                                   |

The driver protocol v1alpha1 directory at
`proto/spectre/driver/v1alpha1/` is treated as read-only across
every phase. Capability lists per adapter (Playwright 13,
SeleniumBase 12, curl-impersonate 6) are preserved byte-for-byte.
The conformance suite's semantics are preserved; only the
transport layer it dials changes.

### Inter-phase dependencies

- **R1 must merge before R2.** This ADR establishes the
  architectural commitment that every subsequent phase PR
  references as upstream context.
- **R2 must complete before R3.** Transport must be TCP before
  the control plane can dial the engine as a service.
- **R3 must complete before R4.** The structural shape (engine
  as service, control plane as gRPC client) must be stable
  before stateful dependencies are added.
- **R4 must complete before R5.** Output sinks beyond stdout
  presume Kafka exists.
- **R6 may begin in parallel with R5.** Per-service Dockerfiles
  do not depend on which sinks exist.
- **R7 requires all of R2–R6.** The Helm chart packages
  everything.
- **R8 is the closing PR** — documentation refresh after the
  refactored architecture has settled.

### Phase-level invariants

At every phase boundary, four invariants must hold:

1. The conformance suite passes with the same test count and
   the same capability assertions as pre-refactor.
2. Each adapter's declared capability list is byte-for-byte
   identical to its pre-refactor manifest.
3. No legacy code path coexists with its replacement after the
   phase that introduces the replacement merges. Per the no-
   legacy principle, the old path is deleted in the same PR
   that lands the new path.
4. The ADR index in `docs/adr/README.md` accurately reflects
   the status of every ADR. Partial supersessions are documented
   per the ADR-0002 / ADR-0013 pattern (Section 6 below describes
   the expected end state).

## ADR status changes

The table records every ADR's pre-refactor status (immediately
after PR18 merged) and its post-R1.1 status. The post-R1.1
status reflects the supersession recorded by this ADR's adoption,
not the implementation work that subsequent phase PRs will
deliver. Concretely: this ADR records that ADR-0019 §3 is
superseded; the actual `SubprocessRunner` deletion lands in
phase R3.

| ADR    | Title                                                                       | Pre-refactor                              | Post-R1.1                                                            |
|--------|-----------------------------------------------------------------------------|-------------------------------------------|----------------------------------------------------------------------|
| 0001   | Driver protocol as architectural primitive                                  | accepted                                  | accepted (preserved — load-bearing primitive)                        |
| 0002   | Polyglot language selection                                                 | accepted (CLI row superseded by ADR-0013) | accepted (CLI row retired entirely; see ADR-0020 §3)                 |
| 0003   | Schema-transport separation                                                 | accepted                                  | accepted (preserved)                                                 |
| 0004   | Protocol versioning strategy                                                | accepted                                  | accepted (preserved — reinforced by the protocol-freeze invariant)   |
| 0005   | Licensing (Apache 2.0)                                                      | accepted                                  | accepted (preserved)                                                 |
| 0006   | Build orchestration (Just)                                                  | accepted                                  | accepted (preserved)                                                 |
| 0007   | Protocol code generation                                                    | accepted                                  | accepted (preserved)                                                 |
| 0008   | Driver handshake and conformance harness                                    | accepted                                  | accepted (§1 handshake + §3 conformance preserved; §2 UDS superseded by ADR-0022 in R2)        |
| 0009   | Navigate, session lifecycle, and the driver error mapping                   | accepted                                  | accepted (lazy-launch contract preserved; session lifecycle revisited under ADR-0023 in R4)    |
| 0010   | Element lifecycle and capability gating                                     | accepted                                  | accepted (preserved — capability divergence chain stays intact)      |
| 0011   | Screenshot RPC, scope mapping, and payload boundaries                       | accepted                                  | accepted (preserved — read-only contract unchanged)                  |
| 0012   | Engine DSL surface, planner architecture, and execution pipeline            | accepted                                  | accepted (substantially revised — engine gains gRPC server, Postgres, Kafka in R3/R4)         |
| 0013   | CLI as engine binary                                                        | accepted                                  | superseded by ADR-0020 (CLI retired completely)                      |
| 0014   | SeleniumBase adapter and cross-language conformance                         | accepted                                  | accepted (preserved — capability progression discipline stays)        |
| 0015   | SeleniumBase element lifecycle and screenshot coverage                      | accepted                                  | accepted (preserved)                                                 |
| 0016   | curl-impersonate adapter and third-runtime divergence                       | accepted                                  | accepted (preserved)                                                 |
| 0017   | curl-impersonate extraction and final capability divergence                 | accepted                                  | accepted (preserved — semantic-equivalence contract stays)           |
| 0018   | Devcontainer and engine image                                               | accepted                                  | accepted (Devcontainer revisited under ADR-0025 in R6; engine image pattern retired alongside) |
| 0019   | Control plane architecture and ScrapeJob CRD                                | accepted                                  | accepted (§3 subprocess-in-pod superseded by ADR-0020; §5 `JobRunner` preserved; §1/§2/§4/§6 preserved) |

### Explicitly preserved baselines

Four ADRs anchor architectural commitments the refactor must not
disturb. They are listed here so future readers do not assume
"everything is up for grabs" during the refactor:

- **ADR-0001** — the driver protocol as architectural primitive.
  The protocol is the project's central asset; the refactor
  changes its transport, not its shape.
- **ADR-0010** — element lifecycle and capability gating. The
  strict-invalidation contract, the UUID registry, and the
  capability-roles split are unchanged. Only the storage location
  of session state moves (to Redis in R4); the in-memory contract
  remains the source of truth at the adapter boundary.
- **ADR-0014** — cross-language conformance and capability
  progression. The "declared = tested" discipline applies to
  every adapter that joins the topology before and after the
  refactor.
- **ADR-0017** — capability semantic equivalence. The
  cross-driver contract that capability declaration is about
  semantic equivalence, not implementation feasibility, is the
  project's most architecturally sophisticated decision. The
  refactor does not touch it.

### Substantially revised, not retired

Three ADRs see substantial revision through R2 / R3 / R4 without
being superseded as wholes. ADR-0008 keeps its handshake and
conformance-harness decisions but loses its UDS choice; ADR-0009
keeps its lazy-launch and session-strictness decisions but moves
session-state ownership to Redis; ADR-0012 keeps its DSL and
planner shape but gains a gRPC server, a PostgreSQL backing
store, and a Kafka producer. Each gets a per-section status note
in the phase that lands its revision (R2 for ADR-0008, R3 for
ADR-0019, R4 for ADR-0009 and ADR-0010).

### Retired

One ADR is retired in full: ADR-0013 (CLI as engine binary). The
refactor's no-legacy principle and the two-entry-point model
(operator + Compose) make a third entry point untenable; see §3
"what about a hybrid that keeps the CLI?" for the full reasoning.

## More Information

- The strategy prompt for the refactor (carried in session
  context, not in the repository) records the principles, the
  locked decisions, and the resumption protocol that govern
  every phase PR.
- Related ADRs:
  [ADR-0001 Driver protocol as architectural primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate, session lifecycle, and the driver error mapping](0009-navigate-and-session-lifecycle.md),
  [ADR-0013 CLI as engine binary](0013-cli-as-engine-binary.md),
  [ADR-0019 Control plane architecture and ScrapeJob CRD](0019-control-plane-architecture-and-scrapejob-crd.md).
- Forward references (ADRs introduced by subsequent phases):
  ADR-0021 (service discovery, R2.1), ADR-0022 (TCP transport
  details, R2.1), ADR-0023 (stateful services, R4.1), ADR-0024
  (output sinks, R5.1), ADR-0025 (Compose layout, R6.2),
  ADR-0026 (Helm chart structure, R7.1).
