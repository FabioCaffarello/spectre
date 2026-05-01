---
status: accepted
date: 2026-04-30
deciders: [Fabio Caffarello]
---

# Platform taxonomy and module categories

## §1 — Context and Problem Statement

Phase R6 closed with R6.3 (operator-in-Compose, Docker-in-Docker
devcontainer, kind cluster). Phase R6.5 closed with R6.5.4 (shared
codegen base, four-Dockerfile `buf` install dedup). The architecture
ADR-0020 commits to is now operationally complete for the v1alpha1
single-flow pipeline: a `ScrapeJob` CRD is reconciled by the
control-plane operator into a job submitted to the engine, the
engine executes the plan against one of three adapters (Playwright,
SeleniumBase, curl-impersonate), and the result lands in one of
four sinks (Stdout, Kafka, S3, Webhook).

The next phase pencilled in by the master strategy was R7.1 — Helm
chart packaging, multi-arch images, production smoke. R7.x is
production posture for the v1alpha1 surface that already exists.

What the master strategy did not yet capture is **what comes after
v1alpha1**. The platform vision now extends beyond a single
Driver-Protocol-shaped pipeline:

- **Ancillary infra services.** Web scraping at scale needs more
  than browser automation. Proxy management (BrightData, Oxylabs,
  rotating residential, datacenter pools) and CAPTCHA solving
  (CapMonster, 2Captcha, AntiCaptcha) are the two immediate
  pressures; fingerprinting, session storage, and rate-limit
  brokerage follow. Each is its own service with its own protocol
  contract and N pluggable provider implementations behind a
  single API surface, consumed from multiple languages.
- **Multi-language SDK clients.** The Driver Protocol is consumed
  today by Rust (engine), Go (curl-impersonate adapter,
  control-plane), Python (SeleniumBase adapter, conformance
  harness), and TypeScript (Playwright adapter). Each consumer
  generates protobuf bindings inside its own build (`prost-build`
  in Cargo, `buf generate` in each non-Rust Dockerfile). Adding
  every new infra-service protocol multiplies that work; without
  a designated home for SDK clients, every consumer reinvents
  client wiring (auth, retries, deadlines, telemetry).
- **Data platform.** The engine produces data; the platform
  beyond v1alpha1 will need to *process* it. File parsing across
  formats (HTML, JSON, PDF, XLSX, ...), transformation between
  lake layers (raw → bronze → silver → gold or equivalent), and
  per-layer DSLs that mirror the engine's job-DSL idiom but for
  downstream stages — these belong somewhere coherent.

The current monorepo has implicit categories that worked because
every component fit one of three roles (engine, adapter, operator):

| Today's directory       | What it holds                                    |
|-------------------------|--------------------------------------------------|
| `proto/`                | Driver Protocol — single protocol module         |
| `core/engine/`          | The Rust engine                                  |
| `core/control-plane/`   | The Go Kubernetes operator                       |
| `adapters/playwright/`  | TypeScript Playwright adapter                    |
| `adapters/seleniumbase/`| Python SeleniumBase adapter                      |
| `adapters/curl-impersonate/` | Go curl-impersonate adapter                 |
| `tools/`                | Build tooling, conformance harness, codegen      |
| `build/`                | Container build infrastructure                   |
| `docs/`                 | ADRs + architecture docs                         |
| `examples/`             | Usage examples                                   |

The implicit taxonomy held while the production surface was three
roles. It will not hold once the platform grows. Without a formal
taxonomy, the next several PRs face ad-hoc placement decisions
each time:

- Where does a proxy-broker service live? Under `core/`? At
  top level alongside `adapters/`? Under a new `services/`?
- Where do the proxy-broker's per-language client SDKs live?
  Under each consumer (duplicating)? Under a single `sdks/`
  shared root? Generated into `proto/gen/` (mixing generated and
  hand-written)?
- Where does HTML/PDF parsing live? Under `core/engine/`
  (mixing extraction and parsing)? Top-level under `parsers/`?
  Inside a new `data-platform/` umbrella?
- Where do cross-language utilities go when they emerge — a
  `shared/` folder that grows by gravity?

The cost of deferring the answer is well-understood from prior
monorepos: load-bearing decisions accrete as small, irreversible
PR-by-PR placements; six months later the structure no longer
maps to the platform's actual shape, and refactoring the layout
fights every active branch.

R6.6 — Platform Maturation closes this gap before R7.x feature
work resumes. Four ADRs and one restructure PR turn the implicit
taxonomy into a contract:

- **ADR-0026 (this) — Platform taxonomy and module categories**
- **ADR-0027 — Multi-language SDK strategy** (per-language SDK
  layout, codegen pipeline, versioning, breaking-change policy)
- **ADR-0028 — Ancillary infra services catalog** (which
  services exist as named slots, admission criteria for each
  to materialise, canonical shape: protocol + N providers + SDKs)
- **ADR-0029 — Data platform and lake DSLs** (lake-layer model,
  responsibilities per layer, when a layer warrants its own DSL)

A fifth PR, with no ADR, enacts the restructure: the directory
moves the taxonomy prescribes, plus documentation updates that
make the categories discoverable.

This ADR (0026) is the foundation. It defines the categories,
their dependency rules, their on-disk locations, and the
admission criteria that govern when new categories or modules
appear. It does not pre-empt the three sibling ADRs — those own
the details inside their categories. ADR-0026 is the schema;
ADR-0027 / 0028 / 0029 fill specific cells.

> **Note on numbering.** ADR-0025 §10 reserves "ADR-0026 (when
> drafted)" for Helm chart packaging at R7.1. That reservation
> was implicit, not authoritative — no draft existed. Phase R6.6
> takes 0026 because Platform Maturation precedes Helm packaging
> in the revised phase order. Helm packaging will pick up the
> next free number when R7.1 resumes.

## §2 — Decision summary

R6.6 commits to the following canonical taxonomy. Eight
production-code categories and four out-of-band categories,
each with a dedicated top-level directory.

### Production categories

| # | Category          | Path                            | Today's count | Future inhabitants                                       |
|---|-------------------|---------------------------------|---------------|----------------------------------------------------------|
| 1 | Protocol contracts| `proto/`                        | 1 module      | One module per protocol surface (Driver, proxy, captcha) |
| 2 | Engines           | `engines/`                      | 1 (`engine`)  | Job orchestrators                                        |
| 3 | Operators         | `operators/`                    | 1 (`control-plane`) | Kubernetes controllers / control planes            |
| 4 | Adapters          | `adapters/`                     | 3             | Driver Protocol implementations                          |
| 5 | Infra services    | `infra-services/`               | 0             | Proxy broker, CAPTCHA solver, fingerprint, session, rate-limit (ADR-0028 catalog) |
| 6 | SDK clients       | `sdks/`                         | 0             | Per-language clients for each protocol (ADR-0027 layout) |
| 7 | Data platform     | `data-platform/`                | 0             | Parsers, transforms, lake-layer DSLs (ADR-0029 layout)   |
| 8 | Shared libs       | `shared-libs/`                  | 0             | Cross-cutting utilities, per language                    |

### Out-of-band categories

| #  | Category   | Path        | Purpose                                       |
|----|------------|-------------|-----------------------------------------------|
| 9  | Tools      | `tools/`    | Build & internal tooling, conformance harness |
| 10 | Build      | `build/`    | Container build infrastructure                |
| 11 | Docs       | `docs/`     | ADRs + architecture documentation             |
| 12 | Examples   | `examples/` | Usage examples                                |

### Headline structural moves

- **`core/` is dissolved.** Its two members are promoted to
  top-level peers of `adapters/`. `core/engine/` becomes
  `engines/engine/`; `core/control-plane/` becomes
  `operators/control-plane/`. The `core/` umbrella was a
  pre-Phase-R6.6 grouping for "the runtime parts the project
  authors directly", but adapters fit that description equally
  well — the umbrella did not pull its weight, and dissolving it
  flattens the dependency-graph view (every category is a
  top-level directory).
- **Four new top-level directories are reserved**:
  `infra-services/`, `sdks/`, `data-platform/`, `shared-libs/`.
  R6.6's restructure PR materialises each as a directory
  containing only a `README.md` that points to the governing
  ADR. The slots are visible immediately; the contents land as
  R7.x and later phases produce inhabitants.
- **Adapter and out-of-band paths are unchanged.** `adapters/`,
  `tools/`, `build/`, `docs/`, `examples/` keep their current
  shape. `proto/` keeps its current shape (additional protocol
  modules will be added under `proto/spectre/<surface>/<version>/`,
  matching the existing `proto/spectre/driver/v1alpha1/`).

### Dependency direction

Categories form a directed acyclic graph. A category may depend
only on categories below it on the diagram. Cycles are
prohibited; cross-category source dependencies that violate the
direction are review-rejected.

```
                       proto                               (1)
                         │
                         ▼
                    shared-libs                            (8)
                         │
                         ▼
                       sdks                                (6)
                         │
        ┌────────────────┼────────────────┐
        ▼                ▼                ▼
    adapters     infra-services      data-platform        (4, 5, 7)
        └────────────────┬────────────────┘
                         ▼
                      engines                              (2)
                         │
                         ▼
                     operators                             (3)
```

The diagram reads top-to-bottom as "depended on by". `proto`
depends on nothing. `engines` depend on protocol contracts plus
SDK clients plus shared libs, and they call adapters, infra
services, and data-platform components *via* the protocols
defined in `proto/`, never via direct source imports. Operators
manage engines through Kubernetes — not through engine source
imports — preserving operator/engine independence.

The DAG enforces three structural invariants:

1. **`proto` is the architectural primitive.** ADR-0001's
   commitment carries forward: the contract surface is the wire
   format, not language-specific code. Every other category
   depends on `proto`, directly or transitively.
2. **No category depends on its callees' source.** An engine
   does not import adapter source code; it dials adapter gRPC
   endpoints whose contract is in `proto/`. An operator does
   not import engine source code; it submits jobs through the
   engine's gRPC API. This is what ADR-0020's microservices
   architecture commits to; the taxonomy makes the discipline
   visible at the directory level.
3. **`shared-libs` is a leaf, not a god module.** Shared libs
   may depend on `proto` and on each other within their own
   language; they may not depend on any consumer category. A
   shared lib that needs to know about adapter or engine
   internals is not a shared lib — it belongs inside its
   consumer.

## §3 — Category definitions

One subsection per category. Each defines the category's
purpose, on-disk location, language scope, dependency posture,
and admission criteria for new members within the category.

### §3.1 — `proto/` (Protocol contracts)

**Purpose.** Single source of truth for every gRPC + protobuf
contract the platform exposes. Today's `proto/spectre/driver/v1alpha1/`
is one such contract; future protocols (proxy broker,
captcha solver, ...) land as siblings under `proto/spectre/`.

**Location.** `proto/<workspace>/<surface>/<version>/*.proto`.
The workspace is `spectre/` for first-party protocols. The
existing `proto/grpc/health/v1/health.proto` is a vendored
canonical contract and stays where it is, with the existing
buf-lint exemption (see ADR-0021 §6).

**Dependencies.** None. `proto/` is the root of the DAG.

**Buf module composition.** `buf.yaml` at repo root declares the
modules. R6.6's restructure does not change `buf.yaml`. Future
protocols can be added in two shapes:

- **Single-module composition** (default): new protocols become
  new packages inside the existing `proto/` module
  (`proto/spectre/proxy/v1alpha1/...`). Lint rules and breaking
  rules apply uniformly.
- **Multi-module composition** (deferred): if a future protocol
  needs different lint exemptions or different breaking-change
  semantics, `buf.yaml` grows a second module entry under
  `modules:`. The existing comment in `buf.yaml` already
  anticipates this.

The choice between shapes is per-protocol and is an ADR-0027
detail (SDK strategy depends on it).

**Admission of a new protocol.** A new protocol surface
requires (a) an ADR justifying why an existing protocol cannot
be extended, (b) versioning aligned with ADR-0004, (c) a
package path matching `spectre.<surface>.v<version>`, (d) at
least one consumer in the same PR (no orphan protocols), and
(e) the SDK-client implications resolved per ADR-0027.

### §3.2 — `engines/` (Job orchestrators)

**Purpose.** Long-running services that consume `proto/`
contracts and execute jobs. The engine is the glue between the
operator's submitted `RunJob` and the adapters / infra-services
that fulfil the plan. Today's single Rust engine fills the role
end to end; the category is plural to accommodate future
specialised engines (e.g., a stream-engine for continuous
collection vs. the current batch-engine for one-shot jobs).

**Location.** `engines/<engine-name>/`. Today: `engines/engine/`
(moved from `core/engine/`). The naming is awkward in the
single-engine case — "engine" inside "engines" — but renaming
to something more specific (`engines/batch/`, `engines/orchestrator/`)
is scope creep until a second engine forces disambiguation. The
restructure PR carries forward the existing crate name (`spectre-engine`).

**Language.** Rust today. Future engines could land in any
runtime; the category does not prescribe a language.

**Dependencies.** `proto`, `sdks` (consuming), `shared-libs`
(within the engine's language). May call adapters, infra
services, and data-platform components via protocol RPCs only —
never via source imports.

**Admission of a new engine.** Requires a dedicated ADR
explaining why the existing engine cannot serve the use-case
(extension vs. parallel-engine cost analysis). Renaming the
existing engine on second-engine landing is expected.

### §3.3 — `operators/` (Kubernetes controllers)

**Purpose.** Control-plane services that reconcile CRDs against
the engine layer. Today's single Go operator (`control-plane`)
reconciles `ScrapeJob` v1alpha2 CRDs into engine `RunJob` calls;
future operators may handle separate CRD families
(`PromptJob`, `ParseJob`, ...) without sharing the
control-plane's reconciliation loop.

**Location.** `operators/<operator-name>/`. Today:
`operators/control-plane/` (moved from `core/control-plane/`).
The crate name (`control-plane` Go module) and Kubernetes
metadata (group, version) are preserved by the rename.

**Language.** Go (kubebuilder convention). Operators in other
languages are out of scope of this ADR.

**Dependencies.** `proto`, `sdks/go/<protocol>` (consuming).
Manages engines through Kubernetes APIs, not through engine
source imports — matching ADR-0020's microservices
commitment.

**Admission of a new operator.** Requires a dedicated ADR.
Operators should be sparse: a second operator only emerges when
the CRD family it reconciles is unrelated to existing CRDs and
sharing reconcilers would entangle two unrelated lifecycles.

### §3.4 — `adapters/` (Driver Protocol implementations)

**Purpose.** Per-runtime implementations of the Driver Protocol
defined in `proto/spectre/driver/v<version>/`. The category is
ADR-0001's architectural primitive made concrete. Each adapter
runs as its own gRPC server; the engine dials them on demand.

**Location.** `adapters/<adapter-name>/`. Today:
`adapters/playwright/`, `adapters/seleniumbase/`,
`adapters/curl-impersonate/`. **No restructure changes.**

**Language.** One adapter per (runtime, language) pair: TS for
Playwright, Python for SeleniumBase, Go for curl-impersonate.
Future adapters land in whatever language fits the underlying
runtime.

**Dependencies.** `proto`, `sdks/<lang>/driver`, optionally
`shared-libs/<lang>/<lib>`. May not depend on `engines`,
`operators`, or other adapters.

**Admission of a new adapter.** ADR is required (see ADR-0014,
ADR-0016 for prior precedent). The set is intentionally
bounded: each adapter carries non-trivial maintenance load
(image, conformance, capability divergence). The R6.6 phase
does not add adapters; ADR-0001's discipline holds.

### §3.5 — `infra-services/` (Ancillary infra services)

**Purpose.** Services that the engine and adapters consume to
solve cross-cutting infrastructure concerns. Each service
exposes a single protocol contract (in `proto/`) and routes to
N pluggable provider implementations behind that contract.
ADR-0028 will catalog the named slots; the canonical examples
are proxy broker (BrightData, Oxylabs, ...) and CAPTCHA solver
(CapMonster, 2Captcha, ...).

**Location.** `infra-services/<service-name>/`. The directory
is **created empty** (a placeholder `README.md` only) by R6.6's
restructure PR. First inhabitant lands in a later phase
governed by ADR-0028.

**Language.** Each service picks the runtime that best fits
its provider integrations and concurrency profile. The default
language is not prescribed by this ADR.

**Dependencies.** `proto`, `sdks/<service-lang>/<service-protocol>`,
`shared-libs/<service-lang>/<lib>`. May not depend on engines,
operators, or specific provider SDKs (those vendor SDKs are
internal to the service's provider-adapter layer; not exposed).

**Admission criteria for materialising a slot.** ADR-0028
will catalog slots such as `proxy-broker`, `captcha-solver`,
`fingerprint-broker`, `session-store`, `rate-limit-broker`. A
slot is *named* when ADR-0028 lists it; a slot becomes *built*
when (a) at least one engine or adapter PR has a concrete need
for it, (b) at least two provider integrations are planned
(otherwise the abstraction is premature), (c) the service's
protocol is added to `proto/` in the same PR, (d) the service's
SDK clients are added per ADR-0027 in the same PR.

### §3.6 — `sdks/` (Multi-language SDK clients)

**Purpose.** Per-language client libraries for each protocol
the platform exposes. Today every protocol consumer generates
bindings inside its own build (Cargo's `prost-build`, each
non-Rust Dockerfile's `buf generate`). The pattern works for
one protocol but multiplies as protocols accumulate. ADR-0027
defines the SDK layout — whether `sdks/<lang>/<protocol>/` or
`sdks/<protocol>/<lang>/`, codegen ownership (centralised in
`sdks/` vs. distributed in consumers), versioning of the SDK
relative to the protocol, and breaking-change policy.

**Location.** `sdks/`. The directory is **created empty**
(placeholder `README.md` only) by R6.6's restructure PR.
ADR-0027 lands the first inhabitants. Sub-structure is
ADR-0027's call.

**Languages.** Today's protocol consumers cover Rust, Go,
Python, TypeScript. Future languages (Java if a JVM consumer
emerges, Kotlin, Swift, ...) follow the same pattern.

**Dependencies.** `proto` (every SDK depends on a protocol
contract). Optional: `shared-libs/<lang>/<lib>` for
cross-protocol concerns (auth, retries, telemetry). May not
depend on consumer categories.

**Admission of a new SDK.** A new (protocol, language) pair
becomes an SDK when the first non-trivial consumer in that
language lands. ADR-0027 governs the gate.

### §3.7 — `data-platform/` (Data lake processing)

**Purpose.** The categories above produce data; the data
platform consumes it. File parsing across formats, transforms
between lake layers, and per-layer DSLs that mirror the engine's
job-DSL idiom for downstream stages. ADR-0029 will define the
lake-layer model (raw / bronze / silver / gold or equivalent),
the responsibilities per layer, and when a new layer warrants
its own DSL.

**Location.** `data-platform/`. The directory is **created
empty** (placeholder `README.md` only) by R6.6's restructure
PR. ADR-0029 lands the first inhabitants. Sub-structure is
ADR-0029's call.

**Languages.** Likely Rust (performance-critical parsers),
Python (analytics-adjacent layers), or both. ADR-0029 picks.

**Dependencies.** `proto` (consumes engine-output schemas plus
its own parse/transform contracts), `sdks/<lang>/<protocol>`,
`shared-libs/<lang>/<lib>`. May call infra-services through
their protocols. May not depend on engines or operators
sources.

**Admission of a new data-platform module.** ADR-0029 sets the
gate. The bar is high: each module is a long-lived component,
not a one-off script.

### §3.8 — `shared-libs/` (Cross-language shared libs)

**Purpose.** Utilities used across multiple modules within a
language. Cross-cutting concerns that don't justify their own
service (logging conventions, retry primitives, error mapping,
configuration parsing) accumulate here when they would
otherwise be copy-pasted between consumers.

**Location.** `shared-libs/<language>/<lib-name>/`. The
directory is **created empty** (placeholder `README.md` only)
by R6.6's restructure PR. First inhabitants emerge organically
when copy-paste pressure crosses a threshold.

**Languages.** Per-language sub-directories. Cross-language
shared code is impossible by construction (different runtimes).

**Dependencies.** `proto`, possibly `sdks` for SDK-adjacent
utilities. May not depend on any consumer category. May depend
on other `shared-libs` within the same language as long as no
cycle forms.

**Admission of a new shared lib.** Lightweight — a library-level
changelog entry suffices; no ADR required for the addition. The
gate is: (a) two or more existing consumers, (b) the lib is
genuinely cross-cutting (not "engine helpers"), (c) the lib
has a stable public surface or is internal to one language's
ecosystem (workspace-level visibility).

### §3.9 — Out-of-band categories

`tools/`, `build/`, `docs/`, `examples/` keep their current
shape and purpose. They are not part of the production
dependency DAG; they are not deployed; they exist for
contributor support. The taxonomy lists them only to document
their scope.

- **`tools/`** — internal tooling. Build helpers, conformance
  harness, codegen scripts (`tools/conformance/`,
  `tools/build/`, `tools/proto-check/`).
- **`build/`** — container build infrastructure. Dockerfiles
  for shared bases (`build/docker/buf-base.Dockerfile`),
  `versions.env`, `kind/` cluster manifests.
- **`docs/`** — ADRs (`docs/adr/`) and architecture
  documentation (`docs/architecture/`). User-facing guides.
- **`examples/`** — usage examples for end-users learning the
  platform.

## §4 — Restructure plan (today vs target)

R6.6's restructure PR enacts the following moves. The PR
contains no business-logic changes; it is a path-only refactor
plus directory placeholders.

### Renames

| From                         | To                                  | Rationale                                                                       |
|------------------------------|-------------------------------------|---------------------------------------------------------------------------------|
| `core/engine/`               | `engines/engine/`                   | Promote engines to a top-level category; dissolve `core/` umbrella.             |
| `core/control-plane/`        | `operators/control-plane/`          | Promote operators to a top-level category; symmetric to engines.                |

After both renames, `core/` is empty and is removed.

### Net-new placeholder directories

| Path                  | Initial contents                                                       |
|-----------------------|------------------------------------------------------------------------|
| `infra-services/`     | `README.md` referencing ADR-0026 §3.5 and ADR-0028 (when drafted).     |
| `sdks/`               | `README.md` referencing ADR-0026 §3.6 and ADR-0027 (when drafted).     |
| `data-platform/`      | `README.md` referencing ADR-0026 §3.7 and ADR-0029 (when drafted).     |
| `shared-libs/`        | `README.md` referencing ADR-0026 §3.8.                                 |

### Path-reference updates the restructure PR must enact

The renames touch path references across the repository. Every
reference must be updated atomically to keep the build green.
Inventory captured here so the restructure PR's reviewer has
the full surface up front:

- **Cargo workspace.** `Cargo.toml` workspace `members:` if any
  references `core/engine`; the engine crate's own `Cargo.toml`
  is path-internal and unaffected.
- **Go modules.** `core/control-plane/go.mod`'s module path is
  internal but its consumer references (controller imports,
  test fixtures) are checked. The Go module path itself
  (`github.com/FabioCaffarello/spectre/control-plane` or
  similar) is preserved by the rename if path-based, or
  unaffected if domain-based.
- **`docker-bake.hcl`.** Every target's `context = "..."` and
  `dockerfile = "..."` referencing `core/engine` or
  `core/control-plane` flips to the new paths.
- **`docker-compose.yml`.** Service `build:` directives
  (none today — R6.2 dropped them) and any `volumes:` that
  bind-mount source paths.
- **`justfile`.** Every recipe that `cd core/engine` or
  references `core/control-plane` flips.
- **`.devcontainer/`.** Mount paths, post-create scripts.
- **`.github/workflows/`.** Path filters in `paths:` triggers,
  Cargo cache keys, working-directory directives.
- **`docs/architecture/*.md`.** Every reference to
  `core/engine/...` or `core/control-plane/...`. ADR-0018,
  ADR-0019, ADR-0023, ADR-0024, ADR-0025 mention these paths
  in prose; **ADR text is immutable once accepted** — the
  restructure does not edit accepted ADRs. Future ADRs cite
  the new paths; older ADRs continue to read against their
  era's paths (the path-based citation becomes a historical
  record of when the category dissolved). The restructure PR
  adds a one-paragraph note in `docs/adr/README.md` flagging
  the rename so a new contributor reading ADR-0018 with `core/engine/`
  prose follows the breadcrumb.
- **`README.md` (top-level), `CONTRIBUTING.md`,
  `build/docker/README.md`.** Every path reference flips.
- **`CHANGELOG.md`.** New entry under `[Unreleased]` records
  the restructure scope.

### What stays unchanged

- **`adapters/`** at top level. The taxonomy keeps adapters
  where they are.
- **`proto/`** at top level. No internal moves.
- **`tools/`, `build/`, `docs/`, `examples/`** at top level.
  Out-of-band categories keep their current shape.
- **Go module path of the operator.** ADR-0019's path is
  preserved across the rename so the CRD's
  `apiVersion: spectre.io/v1alpha2` and Kubernetes object
  identity stay byte-identical. If the Go module path
  literally embeds `core/control-plane`, the rename surfaces
  as a `go.mod` `module` directive update; the rename PR
  records which.
- **Container image names.** `spectre-engine:dev`,
  `spectre-control-plane:dev`, the three `spectre-<adapter>:dev`
  images keep their names. The bake target context paths flip;
  the produced image names are unchanged.
- **Compose service names** (`engine`, `control-plane`,
  `playwright-adapter`, `seleniumbase-adapter`,
  `curl-impersonate-adapter`). Topology stays.

## §5 — Dependency rules

A category may import / call / link only from categories above
it on the DAG (§2 above). The rules are normative:

1. **`proto` imports nothing.** Every other category may depend
   on `proto`.
2. **`shared-libs/<lang>/<lib>`** may depend on `proto` and on
   other `shared-libs/<lang>/<other-lib>` within the same
   language. May not depend on consumer categories.
3. **`sdks/<lang>/<protocol>`** may depend on `proto` and
   `shared-libs/<lang>/<lib>`. May not depend on consumer
   categories.
4. **`adapters/<adapter>`** may depend on `proto`, `sdks/<adapter-lang>/driver`,
   `shared-libs/<adapter-lang>/<lib>`. May not depend on other
   adapters, engines, operators, or `infra-services` source.
   Adapters call engines and infra-services *only* through
   their respective `proto` contracts.
5. **`infra-services/<service>`** may depend on `proto`,
   `sdks/<service-lang>/<protocol>`,
   `shared-libs/<service-lang>/<lib>`. May not depend on
   adapters, engines, operators, data-platform, or other
   infra-services source. Provider integrations (vendor SDKs)
   are private to each service and not exposed beyond it.
6. **`data-platform/<module>`** may depend on `proto`, `sdks/<lang>/<protocol>`,
   `shared-libs/<lang>/<lib>`. May call infra-services through
   their protocols. May not depend on engines or operators
   source.
7. **`engines/<engine>`** may depend on `proto`,
   `sdks/<engine-lang>/<protocol>`,
   `shared-libs/<engine-lang>/<lib>`. Calls adapters,
   infra-services, and data-platform components only through
   their protocols. May not depend on operators source.
8. **`operators/<operator>`** may depend on `proto`,
   `sdks/<operator-lang>/<protocol>`,
   `shared-libs/<operator-lang>/<lib>`. Manages engines through
   the Kubernetes APIs the engines expose; does not import
   engine source.

Cross-category cycles are prohibited by construction — the DAG
admits none. Source-level violations (an `engines/` crate
importing an `adapters/` crate, an `operators/` package
importing an `engines/` package) are review-rejected and SHOULD
be caught by per-language tooling where practical (Cargo
workspace boundaries, Go-module boundaries, lint rules).

## §6 — Admission criteria

Two levels: admitting a new *category* (changes the taxonomy);
admitting a new *module* within an existing category.

### §6.1 — Admitting a new category

A new top-level category requires:

1. **Distinct responsibility** not covered by an existing
   category. Restating an existing category's purpose with a
   different name is not sufficient.
2. **Dedicated ADR** (numbered after this one) that justifies
   the new category, defines its purpose, and locates it on the
   DAG. The new ADR amends ADR-0026's §2 table by superseding
   that section (the new ADR's frontmatter cites ADR-0026 in
   `superseded-by`-equivalent prose; the §2 table moves to the
   new ADR).
3. **No dependency cycle** introduced by the new category. The
   ADR proves cycle-freedom by re-drawing the DAG.
4. **At least one intended inhabitant** — the category cannot
   be purely speculative.

### §6.2 — Admitting a new module within an existing category

| Category         | Admission gate                                                                                                                                   |
|------------------|--------------------------------------------------------------------------------------------------------------------------------------------------|
| `proto`          | New protocol surface: ADR justifying separation from existing protocols + ADR-0004 versioning + at least one consumer in the same PR.            |
| `engines`        | ADR justifying why the existing engine cannot serve the use-case. Renaming the existing singleton on second-engine landing is expected.          |
| `operators`      | ADR justifying the new CRD family's reconciliation independence.                                                                                 |
| `adapters`       | ADR (precedent: ADR-0014, ADR-0016). Adapters are bounded; each addition carries documented maintenance cost.                                    |
| `infra-services` | ADR-0028 catalog entry pre-existing the build PR; build PR satisfies §3.5 admission criteria (≥1 consumer need + ≥2 providers + proto + SDKs).   |
| `sdks`           | First non-trivial consumer in the target language landing in the same or prior PR. ADR-0027 governs.                                             |
| `data-platform`  | ADR-0029 governs. Each module is long-lived, not a one-off script.                                                                               |
| `shared-libs`    | Lightweight: ≥2 existing consumers + cross-cutting purpose + stable public surface. CHANGELOG entry suffices; no ADR required.                   |

## §7 — Confirmation

The taxonomy is working when the following hold across at least
two phases of feature work after R6.6 closes:

- New PRs that add modules name their target category
  explicitly in the PR description, citing this ADR's §3
  subsection.
- A new contributor reading `docs/architecture/overview.md`
  (updated by R6.6's restructure PR) can locate the category a
  new module belongs in without asking, given the module's
  purpose.
- Path-based dependency violations in PRs are flagged at review
  by reviewers citing this ADR's §5 rules.
- The number of "where does this go?" judgement calls per PR
  trends toward zero as the team internalises the DAG.

A signal that the taxonomy needs revision: more than one PR in
a phase invents an *ad-hoc* placement that doesn't fit any
category cleanly. That's evidence the category set is
incomplete; the response is a successor ADR adding the missing
slot, not a one-off folder under the closest existing category.

## §8 — Phase R6.6 roadmap

Five PRs, in order. Each ADR is an independent PR (single ADR
per PR keeps review surface tight, matches the project's
ADR-0001 / ADR-0014 / ADR-0016 precedent of one architectural
commitment per PR).

| #  | PR                                       | ADR        | Scope                                                                                       |
|----|------------------------------------------|------------|---------------------------------------------------------------------------------------------|
| 1  | Platform taxonomy                        | ADR-0026   | This document. Defines categories, DAG, admission criteria. No code.                        |
| 2  | Multi-language SDK strategy              | ADR-0027   | SDK layout, codegen ownership, versioning, breaking-change policy. No SDK code yet.         |
| 3  | Ancillary infra services catalog         | ADR-0028   | Named slots (proxy broker, captcha solver, ...), canonical shape, admission criteria. No service code yet. |
| 4  | Data platform & lake DSLs                | ADR-0029   | Lake-layer model, per-layer responsibilities, DSL admission criteria. No data-platform code yet. |
| 5  | Restructure                              | none       | Enact the renames in §4 above. Materialise the four placeholder directories. Update path references and architecture docs. **Closes Phase R6.6.** |

R7.1 (Helm chart packaging, multi-arch images, production
smoke) resumes after R6.6 closes. The R7.x feature work
inherits an explicit taxonomy.

## §9 — What's deferred / out of scope

R6.6 declines these deliberately. Each is a real concern; each
belongs to a later phase or to a sibling ADR.

- **SDK layout details** — `sdks/<lang>/<protocol>/` vs.
  `sdks/<protocol>/<lang>/`, codegen ownership, etc. ADR-0027.
- **Catalog of infra services** — which services exist as named
  slots, in what order they materialise. ADR-0028.
- **Lake-layer model** — raw/bronze/silver/gold or alternative
  naming, per-layer DSL design, parser language choices.
  ADR-0029.
- **Renaming the existing engine** — `engines/engine/` is
  awkward but stable. A future second-engine ADR is the
  natural place to rename.
- **`apps/` category** — end-user-facing CLIs separate from the
  engine binary. No CLI exists today distinct from the engine
  binary; the slot is not reserved by R6.6. A successor ADR can
  add it when the first such app emerges.
- **Per-language workspace setup** for `sdks/`, `shared-libs/`
  (Cargo workspace files, pnpm workspaces, uv workspace, Go
  multi-module). ADR-0027 (for SDKs) and per-emergence
  decisions (for shared-libs) handle it.
- **Tooling for path-based dependency rule enforcement**
  (linter / pre-commit hook / CI check that catches forbidden
  imports). The rules are normative in §5; enforcement is
  desirable but is a follow-up after R6.6 lands. A lightweight
  tracking note in `docs/architecture/overview.md` records the
  follow-up.
- **Migrating existing prose in accepted ADRs** that cites
  `core/engine/` paths. Accepted ADRs are immutable; the
  restructure PR adds a flag note in `docs/adr/README.md` and
  every future ADR cites the new paths.
- **Renaming Compose service names**, image names, or proto
  package names. The taxonomy is about source paths; runtime
  identities are stable.
- **Adding new ADRs for the existing five top-level
  categories' current behaviour.** ADR-0001, ADR-0012,
  ADR-0019, ADR-0021, ADR-0022, ADR-0023, ADR-0025 already
  govern the existing categories' shape; ADR-0026 references
  them rather than restating.

## §10 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol as primitive. The taxonomy's `proto/` root
  is this ADR's prescription made structural.
- [ADR-0004](0004-protocol-versioning-strategy.md) — Protocol
  versioning. Constraints on `proto/`'s admission criteria
  (§3.1, §6.2).
- [ADR-0007](0007-protocol-code-generation.md) — Protocol code
  generation. Codegen ownership across `sdks/` and consumer
  builds is ADR-0027's concern; ADR-0007 is the prior art.
- [ADR-0012](0012-engine-dsl-and-execution-pipeline.md) —
  Engine DSL and execution. Defines what `engines/` (§3.2)
  contains today.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — Control plane and ScrapeJob CRD. Defines what `operators/`
  (§3.3) contains today. ADR-0019 §3 supersession is preserved
  by the rename (path moves, prose stays).
- [ADR-0020](0020-microservices-architecture-supersession.md)
  — Microservices architecture. The DAG's "no source
  cross-category dependencies" rule (§5) is this ADR's
  commitment made path-level.
- [ADR-0021](0021-service-discovery.md) — Service discovery.
  Adapter and infra-service endpoint resolution will live atop
  ADR-0021's env-var + DNS pattern; the taxonomy does not
  alter it.
- [ADR-0022](0022-tcp-grpc-transport.md) — TCP/gRPC transport.
  Every cross-category call in the DAG (§4) flows over
  ADR-0022's transport.
- [ADR-0023](0023-stateful-services-architecture.md) —
  Stateful services. The Postgres / Redis / Kafka / S3
  posture is unchanged by R6.6; the taxonomy points
  data-platform consumers (§3.7) at ADR-0023's services.
- [ADR-0024](0024-output-sinks.md) — Output sinks. Sinks today
  live inside the engine; whether they migrate to
  `data-platform/` (ADR-0029) or stay in-engine is ADR-0029's
  call.
- [ADR-0025](0025-compose-stack.md) — Compose stack. R6.3
  closed Phase R6 with operator-in-Compose; ADR-0025's §10
  reservation of "ADR-0026" for Helm packaging is reassigned
  by §1 of this ADR.
- MADR template: <https://adr.github.io/madr/>
- `buf.yaml` (repo root) — already anticipates multi-module
  composition for future protocol surfaces.
