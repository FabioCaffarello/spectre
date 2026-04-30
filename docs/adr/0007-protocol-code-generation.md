---
status: accepted (partially evolved by ADR-0027 — see status notes in §2 and §3)
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Protocol code generation

## Context and Problem Statement

PR1 established the Driver Protocol schema at
`proto/spectre/driver/v1alpha1/` and skeletons for every consumer (the
Rust engine, the Go control plane, the three reference adapters, and
the Python conformance suite). Every consumer currently carries a
hard-coded `PROTOCOL_VERSION = "spectre.driver.v1alpha1"` string
constant. The schema is real; nothing imports it.

This ADR records the decisions that turn the schema into the single
source of truth for every component: which generators run for which
languages, where their output lands, how the build system wires them
together, and how CI exercises the pipeline.

## Decision Drivers

- A clean clone must produce a working `just bootstrap` and `just
  check` — no manual generation step. The schema is upstream of every
  build.
- Generated code must never live in version control. Protobuf sources
  are committed; outputs are reproducible.
- Each language's generator and output layout should follow that
  ecosystem's idiom rather than impose a uniform layout that fights
  every native build tool.
- The pipeline must be cheap enough to run on every CI job. Sharing
  generated artifacts across CI jobs is a possible optimisation; the
  default should not require it.
- Choices made here propagate to every future driver. Wrong defaults
  create churn for community contributors.

## Considered Options

The decisions cluster into four orthogonal axes (Section 4 of the
PR2 master prompt):

1. **Per-language generator selection.**
2. **Generated output locations.**
3. **Bootstrap order in `just`.**
4. **CI integration shape.**

Each axis is decided below with its own option set.

## Decision Outcome

### 1. Per-language generators

| Language    | Generator                                                                      | Rejected alternatives                                          |
|-------------|--------------------------------------------------------------------------------|----------------------------------------------------------------|
| Rust        | `tonic-build` + `prost`, invoked from `core/engine/build.rs`                   | `protobuf-codegen` (no gRPC), raw `grpcio` Rust bindings       |
| Go          | `buf.build/protocolbuffers/go` + `buf.build/grpc/go` (remote buf plugins)      | `connect-go` (premature; default gRPC pair fits today)         |
| Python      | `buf.build/protocolbuffers/python` + `buf.build/grpc/python` (remote)          | `betterproto` (smaller maintainer bandwidth), `mypy-protobuf`  |
| TypeScript  | `@bufbuild/protoc-gen-es` (run via local buf invocation)                       | `ts-proto` (different idioms; we already standardise on buf)   |

One concrete reason per choice:

- **Rust — tonic-build:** the canonical async gRPC stack for Rust;
  integrates with `prost` via a single `build.rs` step that every
  contributor already expects from a tonic-based crate.
- **Go — protocolbuffers/go + grpc/go:** the standard Google-maintained
  pair, hosted as remote buf plugins so contributors do not need a
  local `protoc` toolchain. `connect-go` is interesting once the
  control plane exposes HTTP-only clients, but that is post-PR2 work.
- **Python — protocolbuffers/python + grpc/python:** official, stable,
  and a known quantity. `betterproto` produces friendlier output but
  has a smaller maintainer pool, which matters for a contract layer
  the entire ecosystem depends on. `mypy-protobuf` may be added later
  for typed stubs; it is additive and does not block this PR.
- **TypeScript — protoc-gen-es:** buf's first-party generator. We
  already use buf for linting and breaking-change detection, so
  staying on the same toolchain keeps the contributor surface narrow.

### 2. Generated output locations

| Language    | Path                                                            | Rationale                                                        |
|-------------|-----------------------------------------------------------------|------------------------------------------------------------------|
| Rust        | `OUT_DIR` (cargo's per-build artifact directory)                | Idiomatic for tonic-build; included via `tonic::include_proto!`  |
| Go          | `proto/gen/go/` (single shared module, consumed via `replace`)  | Avoids two copies of identical code in two Go modules            |
| Python      | `proto/gen/python/` (single tree, consumed via uv editable src) | Single regeneration; both Python consumers reference one tree    |
| TypeScript  | `adapters/playwright/src/proto/`                                | Inside the consuming component; tsc/prettier see it as source    |

All four paths are gitignored. All four are produced reproducibly:
the same protobuf input yields byte-identical output, with no
embedded timestamps.

#### ADR-0027 evolution (Phase R6.6)

[ADR-0027](0027-sdk-strategy.md) §2 evolves §2's per-language
output locations once the first SDK migration lands. Per-language
generated bindings move into `sdks/<lang>/<protocol>/<version>/`
(per-language workspace, per-protocol-version package); each SDK
package owns its codegen invocation. The four locations recorded
above describe the pre-SDK pattern (every Dockerfile re-runs `buf
generate`) preserved at R6.6-close. ADR-0027 §2 is explicit that
ADR-0007 §1 (per-language generators) and ADR-0007 §4 (CI shape)
carry forward unchanged; only output locations and bootstrap
sequencing evolve. See ADR-0027 for the migration trajectory and
the gating on the first consumer-side adoption.

### 3. Bootstrap order

`proto-generate` is a prerequisite of every consumer's
`*-bootstrap` recipe. The aggregate `bootstrap` recipe lists
`proto-bootstrap` and `proto-generate` first; per-language bootstraps
follow.

The cost is a redundant generator invocation when a developer runs a
single component's bootstrap (for example `just sb-bootstrap`). The
benefit is that every workflow — full bootstrap, single-component
bootstrap, CI — guarantees the consuming component builds against
the current schema. The redundancy is acceptable: `buf generate` and
the post-generation steps complete in a few seconds.

Rust is a partial exception. `engine-bootstrap` runs `cargo fetch`,
which does not invoke `build.rs`; the actual Rust code generation
happens lazily on the first `cargo check` / `cargo build` /
`cargo test`. This means `just proto-generate` does not produce
Rust output until cargo is invoked. That is consistent with how
tonic-build is expected to integrate, and it means Rust generation
output never appears in `proto/gen/`.

#### ADR-0027 evolution (Phase R6.6)

[ADR-0027](0027-sdk-strategy.md) §3 evolves §3's bootstrap
sequencing in tandem with §2. Once an SDK package owns codegen for
its language and protocol version, the consumer's bootstrap
prerequisite shifts from `proto-generate` to a per-SDK-package
build/install step, and the per-Dockerfile `buf generate` invocation
is retired in that consumer's image. The migration is per-consumer
and gated; the §3 sequencing recorded above is the contract for
consumers that have not yet migrated. ADR-0027 §3 specifies the
migration order (engine first, adapters second) and the cutover
acceptance gates.

### 4. CI integration shape

CI runs `proto-generate` (or its language-specific equivalent)
**independently in each language job before its build step** —
Section 4.4 option (b).

Rejected alternatives:

- **(a) Single setup job + artifact upload.** Faster on paper, but
  the artifact wiring is more code to keep in sync, and the speedup
  is small once each job is already cached.

The chosen shape mirrors the local developer workflow exactly. A
contributor who runs `just check` locally exercises the same
generation path that CI runs, with no special-cases.

### Consequences

- Good, because every component has one source of truth for the
  protocol — the protobuf schema — and breaks visibly at
  generation time when the schema changes.
- Good, because each language uses its native conventions; new
  contributors are not forced to learn a project-specific layout
  before they understand the build.
- Good, because regeneration is fast and deterministic, so CI does
  not need shared-artifact orchestration.
- Bad, because the matrix of toolchain dependencies grows: a full
  local build now requires `buf`, `cargo`, `go`, `pnpm`, `uv`, and
  Python. Documented in component READMEs and in CONTRIBUTING.
- Bad, because `proto/gen/go/` exists as a Go module without a
  committed `go.mod`. The `proto-generate` recipe initialises it on
  first run; the directory is gitignored. Future contributors who try
  to consume the generated Go package without running
  `proto-generate` will see a clear error.
- Neutral, because Rust generation is lazy. This trade-off
  (idiomatic tonic-build versus uniform "everything is materialised
  by `proto-generate`") favours the Rust convention.

### Confirmation

- Acceptance criteria 1–9 of the PR2 master prompt are the
  verification checklist for this ADR. Each is binary and verifiable.
- A clean clone followed by `just bootstrap && just check` succeeds
  with no manual step.
- `just proto-generate` is idempotent: running it twice produces no
  diff in any of the four output directories.
- A grep for the literal `"spectre.driver.v1alpha1"` outside the
  protobuf sources, this ADR, and one re-export per language returns
  no hits.

## Pros and Cons of the Options

### Per-language generators

#### Rust — tonic-build (chosen)

- Good, because it is the standard async gRPC stack for Rust and
  integrates trivially with `prost` for message types.
- Good, because everything happens in `build.rs`; no out-of-band
  generation step is required for a Rust contributor.
- Bad, because cargo's `OUT_DIR` is build-specific, so `proto-generate`
  cannot materialise the output without invoking cargo. Documented.

#### Rust — `protobuf-codegen`

- Good, because the codegen story is simpler.
- Bad, because there is no gRPC story, which we will need in PR3.

#### Go — protocolbuffers/go + grpc/go (chosen)

- Good, because remote buf plugins remove the need for a local
  `protoc` toolchain.
- Good, because the generated package layout matches everything every
  Go reviewer expects.
- Bad, because the generated Go module needs a `go.mod` synthesised
  outside of buf; the `proto-generate` recipe handles it.

#### Go — connect-go

- Good, because it offers HTTP/1.1 transport for free.
- Bad, because nothing in PR2 needs HTTP-only clients; adopting it
  would commit the project to Connect's idioms before we have the
  evidence for that choice.

#### Python — protocolbuffers/python + grpc/python (chosen)

- Good, because the Google-maintained reference is stable across
  Python versions.
- Neutral, because the output is verbose and untyped; we accept this
  for now and can layer `mypy-protobuf` on top later without breaking
  consumers.

#### Python — betterproto

- Good, because the output is idiomatic Python with type hints.
- Bad, because the project's maintainer surface is small; for a
  contract layer this is a real risk.

#### TypeScript — protoc-gen-es (chosen)

- Good, because we already standardise on buf; one less toolchain.
- Good, because the output is ESM with full type information.
- Neutral, because `ts-proto` has wider community adoption; the
  buf-first choice is consistent with our existing tooling rather
  than driven by raw popularity.

### Generated output locations

#### Single shared trees (Go, Python; chosen)

- Good, because regeneration is cheap and the source of truth is
  obviously single-rooted.
- Bad, because cross-module references (Go `replace`, Python
  editable source) are slightly more setup than a fully self-contained
  per-module layout.

#### Per-consumer trees

- Good, because each component is self-contained.
- Bad, because identical generated code lives in two trees, which is
  noisy in code review and on disk.

### Bootstrap order

#### `proto-generate` as prerequisite of every `*-bootstrap` (chosen)

- Good, because every entry point — full bootstrap, single-component
  bootstrap, CI — produces a buildable tree.
- Bad, because the generator runs more often than strictly necessary.

#### Centralised in top-level `bootstrap` only

- Good, because the generator runs exactly once per workflow.
- Bad, because a contributor running `just sb-bootstrap` in isolation
  ends up with a tree that fails to build.

### CI integration shape

#### Independent generation per language job (chosen)

- Good, because the local and CI workflows are identical.
- Good, because no artifact-passing wiring is required.
- Neutral, because the generator runs per job; the cost is small
  relative to test time.

#### Setup job with artifact upload

- Good, because it amortises generation across language jobs.
- Bad, because it adds a coordination step that has to be kept in
  sync; the speedup does not justify the complexity for the current
  matrix.

## More Information

- Buf plugin registry: <https://buf.build/plugins>
- `tonic-build`: <https://docs.rs/tonic-build>
- `@bufbuild/protoc-gen-es`: <https://github.com/bufbuild/protobuf-es>
- `grpcio-tools`: <https://grpc.io/docs/languages/python/quickstart/>
- Related ADRs:
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md).
