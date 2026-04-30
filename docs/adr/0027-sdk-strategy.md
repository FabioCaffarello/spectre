---
status: accepted
date: 2026-04-30
deciders: [Fabio Caffarello]
---

# Multi-language SDK strategy

## §1 — Context and Problem Statement

ADR-0026 reserves the `sdks/` top-level directory as the home for
per-language client libraries that wrap each protocol exposed
under `proto/`. ADR-0026 deliberately leaves the *internals* of
that category to this ADR: directory layout, codegen ownership,
versioning semantics, breaking-change policy, and what a SDK
package contains beyond raw generated bindings.

Today's protocol consumption model — the ground truth this ADR
evolves from — is captured by ADR-0007:

- **Generated code is never committed.** Protobuf sources are
  committed; outputs are reproducible.
- **Each language uses its ecosystem's idiom.** Rust's
  `tonic-build` materialises into `OUT_DIR` (cargo per-build
  artifact dir), invoked from each consumer's `build.rs`.
  Go, Python, and TypeScript run `buf generate` to land output
  in shared trees (`proto/gen/go/`, `proto/gen/python/`) or
  inside the consuming component (`adapters/playwright/src/proto/`).
- **`proto-generate` is a prerequisite of every consumer's
  bootstrap.** Every workflow — full bootstrap, single-component
  bootstrap, CI — produces a buildable tree.

This pattern works for one protocol with five consumers (the
engine, the operator, three adapters, the conformance harness).
It does not scale to the platform shape the master strategy
extends toward:

- **Multi-protocol consumers.** The engine in R7.x will consume
  the Driver Protocol *plus* a proxy-broker protocol *plus* a
  CAPTCHA-solver protocol. Each protocol multiplies the
  per-consumer codegen surface (each Dockerfile re-runs `buf
  generate`; each build script re-runs `tonic-build`; each
  consumer maintains its own client wrapper around the
  generated stubs). The R6.5.4 shared `buf-base` deduplicates
  the install step but not the generation step.
- **Hand-written wrappers don't have a home.** Above the
  generated stubs every consumer needs the same vocabulary —
  deadline defaults, retry policy, error mapping (gRPC →
  language-idiomatic), telemetry hooks, auth headers. Today
  each consumer rewrites this surface against its own
  generated stubs; the deviation surfaces in code review as
  per-PR judgement calls.
- **External SDK consumers are out of reach.** A community
  contributor writing a job script in Python that wants to
  submit a `RunJob` to the engine has no `pip install` path
  today. Even internally, a future CLI tool would have to
  re-run `buf generate` to get bindings.
- **Protocol-version migration is per-consumer churn.** When
  the Driver Protocol moves from `v1alpha1` to `v1alpha2`,
  every consumer migrates its own generation config, its own
  client wrapper, its own error-mapping. With SDKs, the
  per-version surface is a single package consumers swap.

ADR-0027 commits Phase R6.6 to the SDK shape that absorbs
those costs. The category exists — defined by ADR-0026 §3.6 —
and this ADR fills it with a contract: per-protocol SDK packages
in per-language workspaces, with codegen owned by each SDK
package (preserving ADR-0007's "never in version control"
posture), with a stable wrapper surface above the generated
stubs, with versioning and breaking-change discipline that
protects consumers across protocol version bumps.

This ADR does not yet *land* any SDK code. Phase R6.6's
restructure PR (ADR-0026 §4) creates `sdks/` as a placeholder
directory containing only this ADR's reference. The first SDK
materialises in R7.x or later when its first consumer migrates
or its first new protocol lands.

## §2 — Decision summary

R6.6 commits to:

- **Per-language workspace, per-protocol package, per-version
  directory.** SDKs land at `sdks/<lang>/<protocol>/<version>/`.
  Today's planned consumers cover Rust, Go, Python, TypeScript;
  future languages follow the same pattern. Each language root
  (`sdks/rust/`, `sdks/go/`, `sdks/python/`, `sdks/typescript/`)
  is a single workspace in that ecosystem's idiom (Cargo
  workspace, single Go module with sub-packages, uv workspace,
  pnpm workspace).
- **Codegen ownership moves into the SDK package.** Each SDK
  package owns its generation step (Rust: `build.rs` invoking
  `tonic-build`; Go: `go generate` directive invoking `buf
  generate`; Python: build hook invoking `buf generate`; TS:
  `prepare` script invoking `buf generate`). Generated output
  remains gitignored and lives inside the SDK package, not in
  shared trees. ADR-0007's "never in version control" posture
  is preserved without modification.
- **Consumers depend on SDKs, not on raw generated code.**
  Today's consumers wire `replace` directives, editable installs,
  or per-Dockerfile `buf generate` runs to import generated
  code directly. The migration target replaces those wirings
  with a single dependency on `sdks/<lang>/<protocol>/<version>`.
  Consumer Dockerfiles drop `buf generate` as a build step (the
  SDK package handles it).
- **SDK content boundary is normative.** SDK packages contain:
  generated stubs (gitignored, per-package), the hand-written
  client wrapper (typed client, deadlines, retries, error
  mapping, telemetry), and a small surface of convenience
  helpers. SDK packages do **not** contain: business logic
  specific to a consumer, vendor SDKs (BrightData/CapMonster/...
  are private to their host service in `infra-services/`), or
  duplicated cross-protocol code (that lives in `sdks/<lang>/common/`).
- **Versioning has two axes.** *Protocol version* is encoded in
  the package's path/name (`spectre-driver-v1alpha1-sdk-rust`).
  *SDK version* is the package's semver, tracking changes to
  the hand-written wrapper that don't break the protocol's wire
  format. Pre-stable protocols (`v<unstable>`) carry pre-stable
  SDKs (`0.x.x`).
- **ADR-0007 is preserved, not superseded.** ADR-0007's per-
  language generator choices (§1: tonic-build, protocolbuffers/go,
  protocolbuffers/python, protoc-gen-es) carry forward
  unchanged. ADR-0007's CI shape (§4: independent generation
  per language job) carries forward unchanged. Only ADR-0007
  §2's *output locations* are restructured: shared trees
  (`proto/gen/<lang>/`) and consumer-internal trees
  (`adapters/playwright/src/proto/`) are absorbed into per-SDK
  package output paths. ADR-0007 §3 (`proto-generate` as
  prerequisite of bootstraps) is restructured: per-SDK
  bootstrap recipes replace the centralised `proto-generate`,
  with the same idempotence and from-clean-clone guarantees.

## §3 — Directory layout

The canonical path is `sdks/<lang>/<protocol>/<version>/`. The
naming choices below are normative for first-party SDKs.

### §3.1 — Path components

- **`<lang>`** — language identifier matching the host
  ecosystem's conventional name. The fixed roster for v1alpha1:
  `rust`, `go`, `python`, `typescript`. Future languages
  (`java`, `kotlin`, `swift`, ...) follow the same pattern.
- **`<protocol>`** — protocol surface name, matching the proto
  package's middle segment under `spectre.<protocol>.v<version>`.
  The fixed entry for v1alpha1 is `driver` (matching
  `spectre.driver.v1alpha1`). Future protocols catalogued by
  ADR-0028 land as siblings: `proxy`, `captcha`, `fingerprint`,
  `session`, `rate-limit`.
- **`<version>`** — protocol version, matching the proto
  package's trailing segment. Today: `v1alpha1`. The path
  mirrors the proto-side path so a contributor reading
  `proto/spectre/driver/v1alpha1/driver.proto` knows
  immediately that the Rust SDK lives at
  `sdks/rust/driver/v1alpha1/`.

The mirror between `proto/spectre/<protocol>/<version>/` and
`sdks/<lang>/<protocol>/<version>/` is a discoverability
contract. A contributor migrating a consumer to a new protocol
version finds the SDK at the symmetric path without searching.

### §3.2 — Per-language workspace shape

Each `sdks/<lang>/` root is a single workspace in the
ecosystem's idiom. The choice is normative; alternatives are
rejected for monorepo-internal consumption.

| Language     | Workspace shape                                                                                  | Rationale                                                                                      |
|--------------|--------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
| Rust         | Cargo workspace at `sdks/rust/Cargo.toml`; each `<protocol>/<version>/` is a workspace member.   | Single dependency resolution; `cargo build -p spectre-driver-v1alpha1-sdk` builds one SDK.    |
| Go           | Single Go module at `sdks/go/go.mod`; each `<protocol>/<version>/` is a Go package within.       | Simpler dependency tree for monorepo internal consumers; multi-module split is a future option (§9). |
| Python       | uv workspace at `sdks/python/pyproject.toml`; each `<protocol>/<version>/` is a workspace member.| `uv sync` resolves all SDK packages together; consumers in `adapters/seleniumbase/` reference via path. |
| TypeScript   | pnpm workspace at `sdks/typescript/pnpm-workspace.yaml`; each `<protocol>/<version>/` is a package.| `pnpm` resolves workspace packages locally; consumers in `adapters/playwright/` depend via `workspace:*`. |

Cross-language `sdks/common/` does not exist. Cross-protocol
shared code (error mapping, retry primitives, telemetry hooks
that *are* protocol-agnostic) lives at `sdks/<lang>/common/`
within each language root — see §5.3.

### §3.3 — Package naming

Per-language conventional naming, with protocol version embedded
in the package identity:

| Language     | Package name                                | Notes                                                  |
|--------------|---------------------------------------------|--------------------------------------------------------|
| Rust         | `spectre-driver-v1alpha1-sdk` (crate name)  | Hyphenated, matches Cargo convention.                  |
| Go           | `github.com/.../sdks/go/driver/v1alpha1`     | Path-based, matches Go module/package convention.      |
| Python       | `spectre-driver-v1alpha1-sdk` (PyPI name)    | Hyphenated; module name `spectre_driver_v1alpha1_sdk`. |
| TypeScript   | `@spectre/driver-v1alpha1-sdk`               | Scoped under `@spectre`; embeds protocol version.      |

Embedding the protocol version in the package name is the lever
that lets two protocol versions coexist in one consumer during
migration windows. A consumer can depend on
`spectre-driver-v1alpha1-sdk` and `spectre-driver-v1alpha2-sdk`
simultaneously; importing both is unambiguous; flipping over is
a single dependency swap when the consumer is ready.

## §4 — Codegen ownership

ADR-0007 §1 (per-language generators) and §4 (CI shape) are
preserved. ADR-0007 §2 (output locations) and §3 (bootstrap
order) are restructured to fit the SDK package boundary.

### §4.1 — Output locations (evolves ADR-0007 §2)

| Language   | Today (ADR-0007 §2)                        | Target (this ADR)                                                             |
|------------|--------------------------------------------|-------------------------------------------------------------------------------|
| Rust       | cargo's `OUT_DIR` per consumer crate       | cargo's `OUT_DIR` of the SDK crate (`sdks/rust/<protocol>/<version>/`)         |
| Go         | `proto/gen/go/` (shared, gitignored)       | `sdks/go/<protocol>/<version>/internal/generated/` (gitignored, per package)   |
| Python     | `proto/gen/python/` (shared, gitignored)   | `sdks/python/<protocol>/<version>/_generated/` (gitignored, per package)       |
| TypeScript | `adapters/playwright/src/proto/` (consumer-internal) | `sdks/typescript/<protocol>/<version>/src/_generated/` (gitignored)  |

Output remains gitignored across all four languages. ADR-0007's
"generated code never in version control" decision driver is
preserved without modification.

The Rust case is structurally unchanged — `tonic-build` writes
into `OUT_DIR`, included via `tonic::include_proto!`. The
difference is that the `OUT_DIR` belonging to the SDK crate
gets reused by every consumer that depends on the SDK crate;
the engine's own `build.rs` no longer runs `tonic-build`. This
removes per-consumer duplication of identical generated code in
parallel `OUT_DIR`s.

The Go, Python, and TypeScript cases consolidate today's shared
or consumer-internal trees into per-SDK-package
gitignored directories. Consumers depend on the SDK package and
import the generated symbols re-exported by the package
boundary; they do not import the gitignored generated path
directly.

### §4.2 — When generation runs (evolves ADR-0007 §3)

Each SDK package owns its generation step. Generation happens
as a build-time hook of the SDK package, in the host
ecosystem's native mechanism:

- **Rust** — `build.rs` invokes `tonic-build` + `prost-build`.
  Triggered by `cargo build` / `cargo check`. No separate
  `proto-generate` invocation needed.
- **Go** — `//go:generate buf generate ...` directive at the
  SDK package root, invoked by `go generate ./...` from the
  SDK module root. The `sdks/go/` Makefile or justfile recipe
  wraps this. (Go does not have an automatic build-time codegen
  hook; explicit `go generate` keeps it discoverable.)
- **Python** — uv build hook (`tool.hatch.build.hooks.custom`
  or equivalent) runs `buf generate` during `uv sync`. The hook
  also runs on `uv pip install -e` for editable installs.
- **TypeScript** — `prepare` script in `package.json` runs
  `buf generate`. pnpm runs `prepare` automatically on
  `pnpm install` for workspace packages.

The aggregate `just bootstrap` recipe is restructured:

- Pre-R6.6 shape: `proto-bootstrap`, `proto-generate`, then per-
  language bootstraps.
- Post-R6.6 shape: `proto-bootstrap`, then per-SDK bootstraps
  (`sdk-rust-bootstrap`, `sdk-go-bootstrap`, `sdk-python-bootstrap`,
  `sdk-typescript-bootstrap`), then per-consumer bootstraps that
  depend on their respective SDKs.

The `proto-generate` recipe is retired — generation is no longer
a separable step but a build-time concern of each SDK package.
Idempotence per ADR-0007 §3's confirmation criterion is
preserved: each SDK's build is deterministic; running twice
produces no diff.

This restructure lands incrementally as each protocol's SDK
materialises, not all at once in R6.6. The R6.6 restructure PR
documents the target shape; per-protocol SDK PRs (R7.x and
later) enact the migration one protocol at a time.

### §4.3 — CI shape (preserves ADR-0007 §4)

ADR-0007 §4's "independent generation per language job before
its build step" is preserved. After SDK migration, the per-
language CI job's "generation" step is whatever the SDK package
declares (Rust's `cargo build` triggers `build.rs`; Go's
`go generate ./...`; Python's `uv sync`; TS's `pnpm install`).

The CI matrix gains per-SDK bootstrap jobs that run before
consumer-build jobs in the same language. The CI graph stays
shallow — each language column generates its own SDKs, then
builds its consumers.

## §5 — SDK content boundary

The hand-written surface above the generated stubs is the SDK's
reason to exist. This section is normative — what goes in, what
stays out, where the line falls.

### §5.1 — In scope: the wrapper surface

Each SDK package contains, beyond gitignored generated stubs:

- **Typed client struct.** Idiomatic per language: `Client` in
  Rust holding `tonic::transport::Channel`; `Client` in Go
  holding `*grpc.ClientConn`; `Client` class in Python holding
  the gRPC channel; `Client` class in TypeScript holding the
  Connect/gRPC transport. The struct exposes one method per
  protocol RPC.
- **Construction helpers.** `Client::connect(endpoint: &str)` /
  `NewClient(endpoint string)` / `Client(endpoint: str)` /
  `new Client({ endpoint })`. Defaults: TLS from env when set,
  plaintext otherwise; deadlines per RPC class (per ADR-0022 §4
  posture); reasonable retry policy.
- **Error mapping.** gRPC status → language-idiomatic error
  type. Rust: `thiserror`-derived `Error` enum mapping
  well-known codes (UNAVAILABLE, FAILED_PRECONDITION, ...) to
  variants; consumers match. Go: typed error returns honouring
  `errors.Is` / `errors.As`. Python: exception hierarchy
  rooted in `SpectreError`. TS: typed `Error` subclasses.
- **Deadline and retry defaults.** Per-RPC-class budget tables
  matching the protocol's intent. Idempotent reads (Capabilities,
  Health) get longer retries; mutating RPCs (RunJob, AcquireSession)
  get tight deadlines and no auto-retry.
- **Telemetry hooks.** Pluggable interfaces for metrics,
  tracing, logging. Language-idiomatic: Rust's `tracing` crate,
  Go's `slog`, Python's `logging`, TS's `console`/structured
  logger. Default implementations are no-ops; consumers wire
  their own when needed.
- **Re-exported message types.** Generated message types
  re-exported from the SDK's public surface so consumers don't
  reach into the gitignored generated path.

### §5.2 — Out of scope

The SDK does **not** contain:

- **Business logic specific to a consumer.** The engine's job-
  planning algorithm is in `engines/engine/src/plan.rs`, not in
  the Driver SDK. The operator's reconciliation loop is in
  `operators/control-plane/internal/controller/`, not in the
  Driver SDK.
- **Vendor SDKs.** `aws-sdk-s3`, `rdkafka`, `BrightData
  client SDK`, `CapMonster client SDK` — vendor libraries are
  private to the service that wraps them (the engine for
  AWS/Kafka; future infra-services for proxy/CAPTCHA vendors).
- **High-level orchestration.** "Run a job that uses Playwright,
  retries on failure, falls back to SeleniumBase" — that's a
  consumer's behaviour, not a SDK concern. SDKs are primitive
  client wrappers, not workflow engines.
- **Cross-protocol compositions.** "Acquire a proxy, pass it to
  a Driver Protocol session" — the composition lives in the
  consumer (the engine), not in either SDK. The SDKs are
  per-protocol primitives; composition is consumer concern.
- **Config schema.** Configuration of consumers (env vars,
  flags) is the consumer's surface, not the SDK's. SDKs
  consume already-resolved config (an endpoint string, a token
  string).

### §5.3 — `sdks/<lang>/common/` for cross-protocol concerns

Some primitives are protocol-agnostic but SDK-shaped: a generic
retry policy, a generic error-classification helper, telemetry
hook interfaces, a deadline-budget builder. These live at
`sdks/<lang>/common/` per language, depended on by the
per-protocol SDK packages.

`sdks/<lang>/common/` is a workspace member like any other; it
follows the same dependency rules (may depend on `proto/`,
`shared-libs/<lang>/`; may not depend on consumer categories).

A common-package addition does not require an ADR. Library-
level changelog entries and reviewer judgement suffice. The
admission threshold: at least two SDK packages would otherwise
duplicate the same code; the duplicated code is genuinely
protocol-agnostic.

## §6 — Versioning and breaking-change policy

Two axes, decoupled.

### §6.1 — Protocol version (encoded in path / package name)

Protocol versions follow ADR-0004's path-based scheme:
`spectre.<protocol>.v<version>`. Today: `spectre.driver.v1alpha1`.
Future progression: `v1alpha2` (when wire-format-breaking
changes accumulate), `v1` (first stable), `v2` (next stable
cycle).

The SDK path mirrors the proto path:
`sdks/<lang>/<protocol>/<version>/`. Two protocol versions of
the same protocol are independent SDK packages; a consumer
during migration depends on both and migrates RPC sites one at
a time.

A protocol version's SDK package is **frozen** when the
protocol version is frozen (per ADR-0004's freeze semantics).
Bug fixes to a frozen protocol's SDK wrapper are allowed (they
don't change the wire format); breaking changes to the wrapper
of a frozen protocol's SDK require a new SDK semver major
within the same protocol-version package. Wrapper-breaking
changes are extremely rare in practice.

### §6.2 — SDK version (semver of the wrapper)

Each SDK package carries a semver per ecosystem convention:

- **Rust** — `Cargo.toml` `version = "0.x.y"`. Pre-stable
  during protocol's `v<unstable>` phase.
- **Go** — module path embeds major version per Go module
  convention; semver tags on the `sdks/go` module cover the
  Go-side bumps.
- **Python** — `pyproject.toml` `version = "0.x.y"`. Pre-stable
  during protocol's `v<unstable>` phase.
- **TypeScript** — `package.json` `version: "0.x.y"`. Pre-stable
  during protocol's `v<unstable>` phase.

Semver discipline is conventional: major bump for wrapper-API
breaking change, minor for additive change, patch for
non-breaking fixes. Pre-`v1.0.0` SDKs may break on minor bumps;
`v1.0.0` lands when the underlying protocol stabilises.

### §6.3 — Breaking-change policy for wrappers

The wrapper surface is more flexible than the wire format. A
consumer's compile-time errors on a wrapper change are caught
in CI; a wire-format change may pass CI and break runtime
across version skew.

For SDK wrappers:

- **Pre-stable (SDK `0.x.x`).** Breaking changes allowed on
  minor bumps. PR title states `BREAKING:` in the wrapper
  scope. CHANGELOG entry under `### Changed` flags the break.
- **Stable (SDK `>=1.0.0`).** Breaking changes require a major
  bump. The previous major's SDK package coexists in the
  workspace under a versioned package name (`...-sdk-v1` and
  `...-sdk-v2`) until consumers migrate. A deprecation cycle
  of one major version is the convention.

Breaking changes to the wrapper that propagate from a wire-
format change in the underlying protocol are not "wrapper
breaks" — they are protocol-version migrations and live in a
new protocol-version SDK package per §6.1.

## §7 — Migration sequence

This ADR is contract-only. R6.6's restructure PR (ADR-0026 §4)
materialises `sdks/` as a placeholder directory containing only
a `README.md` that references this ADR. No SDK code lands in
R6.6.

The materialisation sequence after R6.6 closes, recorded here
so the next phase's planner inherits the order:

### §7.1 — First SDK: Driver Protocol, Rust

The natural first migration is the Rust engine's consumption of
the Driver Protocol. Today's `core/engine/build.rs` runs
`tonic-build` to generate Driver bindings into the engine's
`OUT_DIR`; the engine's `src/proto/` re-exports them. Migration:

1. Create `sdks/rust/Cargo.toml` (workspace).
2. Create `sdks/rust/driver/v1alpha1/` (workspace member).
3. Move `tonic-build` invocation from `engines/engine/build.rs`
   to `sdks/rust/driver/v1alpha1/build.rs`.
4. Hand-write the Rust client wrapper per §5.1.
5. Add `spectre-driver-v1alpha1-sdk` as a dependency in
   `engines/engine/Cargo.toml`.
6. Replace `engines/engine/src/proto/` with re-exports from the
   SDK or direct imports of SDK types.

This is one protocol × one language = one PR, scoped tightly.

### §7.2 — Subsequent SDKs

Order is consumer-driven, not centrally prescribed:

- The next protocol consumer that lands in a phase carries the
  SDK migration for its (protocol, language) pair.
- A new infra service (per ADR-0028) lands its protocol's SDKs
  in the same PR or in a directly preceding PR — the protocol
  has no consumers without SDKs.
- Protocol-version migrations (`v1alpha1` → `v1alpha2`) land
  the new-version SDK package alongside the consumer migration
  PR.

R6.6 declines to enumerate the full sequence — the order
depends on Phase R7.x's feature priorities, which are out of
scope here.

## §8 — Confirmation

The SDK strategy is working when:

- **No consumer Dockerfile runs `buf generate`** after all
  protocols have migrated to SDK packages. The shared
  `buf-base` Dockerfile (R6.5.4) becomes vestigial for proto
  codegen; it may survive for kubebuilder's `controller-gen`
  in the operator if relevant, or be removed.
- **A new protocol consumer** in a new PR adds one SDK
  dependency line to its manifest and imports the SDK's
  `Client` type. No `replace` directives, no editable installs,
  no per-Dockerfile codegen.
- **A protocol-version bump** is a multi-PR migration that
  begins with a new SDK package and proceeds consumer by
  consumer; no consumer is forced to migrate before it's
  ready.
- **The hand-written wrapper surface** (deadlines, retries,
  error mapping, telemetry hooks) is consistent across all
  SDKs in a language because they share `sdks/<lang>/common/`.
  Reviewers stop catching per-PR drift in those primitives.

A signal that the strategy needs revision: more than one PR
ad-hoc bypasses the SDK package and reaches into generated
code directly. That's evidence the SDK boundary is missing
something a consumer needs; the response is to expand the
wrapper surface in the SDK, not to legitimise the bypass.

## §9 — What's deferred / out of scope

R6.6 declines these deliberately. Each is a real concern; each
belongs to a later phase.

- **External SDK publishing.** Publishing
  `spectre-driver-v1alpha1-sdk` to crates.io / PyPI / npm /
  Go module proxy is a v1alpha1+ concern. Internal monorepo
  consumption is the R6.6 contract. External publishing
  requires per-language packaging (license headers, docs,
  release workflow) that is out of scope.
- **Multi-module Go SDK split.** Today's normative shape is one
  Go module at `sdks/go/`. If external publishing or
  independent versioning per protocol becomes a need, the
  module splits into per-protocol modules
  (`sdks/go/driver/`, `sdks/go/proxy/`). The split is a future
  ADR amendment.
- **Connect-RPC alternative transport.** ADR-0007 §1 noted
  Connect-Go as deferred. SDKs may add Connect transport in
  the future as an additional surface alongside gRPC; today
  only gRPC is in scope.
- **Generated-code commit policy reversal.** The "never in
  version control" decision driver from ADR-0007 carries
  forward. If a future need (offline builds, supply-chain
  attestation) forces a reversal, that is a new ADR
  superseding ADR-0007 §2 — out of scope for R6.6.
- **`mypy-protobuf` for typed Python stubs.** ADR-0007 §1
  noted as additive future work. Adding it is a change to the
  Python SDK package's build hook, not to the strategy
  here.
- **SDK feature flags.** SDKs are not the place for protocol-
  surface feature flags. Feature flags belong to the protocol
  itself (per-RPC, per-message) under ADR-0004 versioning, not
  to the SDK wrapper.
- **Wire-level codec choices** (proto2 vs proto3, JSON encoding
  for HTTP transports, ...) — protocol layer, not SDK layer.
- **The `apps/` category** mentioned in ADR-0026 §9 — when an
  end-user CLI emerges, it depends on the SDKs like any other
  consumer. No special handling.

## §10 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive. SDKs are the language-idiomatic
  consumption surface for that primitive.
- [ADR-0003](0003-schema-transport-separation.md) — Schema vs.
  transport. SDKs encapsulate the transport choice; consumers
  see a typed wrapper, not a transport handle.
- [ADR-0004](0004-protocol-versioning-strategy.md) — Path-based
  protocol versioning. §6.1's protocol-version-in-package-name
  is this ADR's commitment made consumer-visible.
- [ADR-0007](0007-protocol-code-generation.md) — Codegen
  decisions. This ADR evolves §2 (output locations) and §3
  (bootstrap order) without superseding §1 (per-language
  generators) or §4 (CI shape).
- [ADR-0022](0022-tcp-grpc-transport.md) — gRPC transport. The
  SDK wrapper's deadlines, retries, and dial defaults align
  with ADR-0022 §4's gRPC client posture.
- [ADR-0026](0026-platform-taxonomy.md) — Platform taxonomy.
  This ADR fills the `sdks/` cell (§3.6 of ADR-0026).
- Cargo workspaces:
  <https://doc.rust-lang.org/cargo/reference/workspaces.html>
- Go modules with internal packages:
  <https://go.dev/ref/mod>
- uv workspaces:
  <https://docs.astral.sh/uv/concepts/workspaces/>
- pnpm workspaces:
  <https://pnpm.io/workspaces>
- buf generate:
  <https://buf.build/docs/generate/usage/>
