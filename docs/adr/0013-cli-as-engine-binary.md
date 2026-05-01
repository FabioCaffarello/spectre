---
status: superseded by ADR-0019 (control-plane architecture) + ADR-0020 (microservices architecture supersession) — see status note in §1
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# CLI as engine binary

> **Supersession (R3.1).** The `spectre run <job.yaml>` CLI was
> retired in Phase R3.1: the operator now submits jobs to the
> engine via gRPC `RunJob` ([ADR-0019](0019-control-plane-architecture.md)
> §3 + [ADR-0020](0020-microservices-architecture-supersession.md)
> §3 + §4). The engine binary's CLI surface (`spectre --help`,
> `spectre version`) survives as a thin wrapper around the gRPC
> server; `spectre validate` and `spectre run` no longer exist.
> The `cargo install spectre-engine` distribution path described
> in §3 below is also retired. All deletion of CLI source files
> landed in R3.1; `engines/engine/src/bin/` retains only the
> service-mode binary entry point. The body of this ADR records
> the original PR8 decisions for historical context — see the
> "Update (R1.1, ADR-0020)" note further down for the full
> supersession argument.

## Context and Problem Statement

ADR-0002 set the language for every component in the project,
including a row that assigned the CLI to Go. The reasoning given was
"Go produces a static cross-platform binary; Rust produces a library."
At the time the ADR was written the engine crate at `core/engine/`
was a placeholder — it imported the generated bindings, exposed
`PROTOCOL_VERSION`, and did nothing else. The CLI, by contrast, was
expected to need a working static binary and was assigned to the
language that produced one most cheaply.

PR7 (ADR-0012) closed the engine. The crate now parses the DSL,
plans against driver capabilities, launches the Playwright adapter
as a subprocess, dials over a Unix domain socket, walks the full
RPC sequence, and writes JSONL. The example binary
`cargo run --example hello-hackernews` is a working static-linkable
Rust binary that takes a YAML file and produces real Hacker News
data.

The Phase 1 exit criterion in `docs/roadmap.md` says:

> a new contributor can `git clone && just bootstrap &&
> spectre run examples/hello-hackernews/job.yaml`

The substantive capability is delivered. The literal `spectre run`
command is not. PR8 closes that gap. In doing so it must answer a
question ADR-0002 did not: with the engine binary already real and
working, is the right `spectre` command a separate Go binary that
wraps it, or the engine binary itself with subcommands?

The remaining decisions are about the shape of the CLI: which crate
hosts it, how it reaches the engine, what its surface looks like,
which argument-parsing library it uses, and how it is built.

## Decision Drivers

- **The premise of the original CLI=Go choice has changed.** The
  engine is no longer a placeholder. Every reason ADR-0002 cited for
  Go (static binary, cross-platform install, simple deploy) is now
  true of the engine crate too. A decision driven by absent capability
  needs revisiting once the capability is present.
- **Architectural symmetry with the rest of the project.** The
  project's thesis is "subprocess + protocol" — drivers are
  subprocesses speaking gRPC. A CLI-as-subprocess-wrapper around the
  engine adds a separate inter-process boundary that exists for no
  protocol reason. Eliminating it removes a hop without losing a
  contract.
- **Compound simplicity.** A separate CLI binary needs a discovery
  mechanism (where does the CLI find the engine?), a version-sync
  story (which engine version does this CLI ship with?), and signal
  forwarding (Ctrl-C reaches the wrapper; does it propagate?). One
  binary has none of these.
- **`cargo install` is a credible install path.** A user runs
  `cargo install spectre-engine` and gets `spectre` on `$PATH`.
  Future release engineering can ship pre-built binaries; the
  Rust-only path is already complete.
- **Honest revision over silent drift.** ADR-0002 was a defensible
  decision with the information available in PR1. PR7's existence
  changed the information landscape. Recording the revision as an
  ADR with a `Superseded by` note on the relevant ADR-0002 row is
  more honest than quietly producing a Rust CLI binary and leaving
  ADR-0002's matrix asserting otherwise.

## Considered Options

The decisions cluster into five axes:

1. **CLI binary location and language.**
2. **Status of ADR-0002.**
3. **Subcommand surface.**
4. **Argument-parsing library.**
5. **Release target and static linking.**

Each is decided below.

## Decision Outcome

### 1. The CLI binary is the engine binary

Chosen: **the `spectre` CLI is produced by the `core/engine/` crate
as a binary target at `src/bin/spectre.rs`.** There is no separate
`core/cli/` directory, no Go binary, no FFI layer, no subprocess
wrapping between CLI and engine. The crate `spectre-engine` produces
the binary `spectre` — the same convention as `cargo` (crate
`cargo`, binary `cargo`) and `ripgrep` (crate `ripgrep`, binary
`rg`).

The CLI calls into the same `Engine` API the example binary used in
PR7: `Engine::parse_job`, `Engine::plan_job`, `Engine::run_job`. No
new public engine API is added beyond a thin `validate_only` helper
that composes parse + plan + capability check (Section 3 below).

Rationale, in order of weight:

1. The original Go-CLI premise no longer holds. ADR-0002's CLI row
   reasoned "Rust produces a library, so Go for the binary." The
   engine is now a binary too.
2. The protocol architecture's value is in the engine ↔ driver
   boundary. A CLI ↔ engine boundary that exists only to honour
   ADR-0002's matrix is friction without offsetting benefit.
3. Future PRs benefit. There is no engine-version-CLI-version
   coordination story, no signal forwarding edge case, no
   discovery mechanism, no FFI surface to keep stable.
4. Distribution is straightforward. `cargo install spectre-engine`
   today; pre-built release binaries via cargo-dist or equivalent
   later (out of scope for this PR).

Rejected:

- **Go subprocess wrapper around the Rust engine.** Added complexity
  for no architectural benefit. The protocol-level argument for
  separate processes (the engine and a driver run in different
  languages and need a stable wire contract) does not apply between
  the CLI and the engine — they are the same process by design.
- **FFI via cgo + Rust dylib.** Cross-platform fragility (dylib
  search paths, ABI versioning), async-runtime boundary problems
  (Rust's tokio cannot call back into Go cleanly), distribution
  complexity (the dylib must travel with the binary). The engine's
  API is async-tokio-flavoured throughout; an FFI exposure would
  require a synchronous shim and a parallel bridge of every error
  variant.
- **A separate Rust CLI crate that depends on `spectre-engine`.**
  Possible, but adds a workspace member, a second `Cargo.toml`,
  and a duplicate `[[bin]]` declaration with no offsetting
  isolation benefit. The engine's public API is what the CLI needs;
  putting them in the same crate is the simpler arrangement.

### 2. ADR-0002 is partially superseded

Chosen: **ADR-0013 supersedes only the CLI row of ADR-0002's
language-selection matrix.** The rest of ADR-0002 — control plane
in Go, curl-impersonate adapter in Go (cgo wrapper), SeleniumBase
adapter in Python, intelligence layer in Python, SDKs in TS /
Python / Go, and the Rust assignments for engine and compatibility
core — remains in force.

ADR-0002's overall status remains `accepted`. The relevant section
gains an "Update (PR8, ADR-0013)" note pointing to this ADR. The
ADR README index lists this ADR with an explicit "supersedes
ADR-0002 (CLI row only)" notation so a reader scanning the index
sees the partial nature of the change.

Rationale: a full supersession of ADR-0002 would mis-represent the
scope of the change. Eight of nine rows are unaffected. Partial
supersession with a marginal note is the honest record of what
actually changed. This is a soft pattern recommendation for future
ADRs in the project: when a later decision refines a piece of an
earlier one without invalidating the whole, the partial-supersession
shape is clearer than a rewrite.

Rejected:

- **Mark ADR-0002 fully superseded.** Inaccurate. Most of the
  document's decisions are still load-bearing.
- **Edit ADR-0002 in place to change the CLI row.** Violates the
  immutability of accepted ADRs. ADRs are append-only history; the
  proper revision mechanism is a new ADR plus a forward reference.
- **Leave ADR-0002 alone and document the change only here.** A
  reader of ADR-0002 alone would see Go for the CLI and look for a
  Go CLI binary that does not exist. The forward reference is
  necessary.

### 3. Subcommand surface

Chosen: **three subcommands at v1alpha1, no more.**

- `spectre run <job.yaml> [--verbose] [--output=<path>]
  [--adapters-path=<path>]` — runs the job. Same behaviour as
  PR7's example binary, with the flag set carried over for
  continuity.
- `spectre validate <job.yaml> [--adapters-path=<path>]` — parses,
  plans, checks the plan's required capabilities against the
  declared list in the driver manifest, and prints the compiled
  `Plan` to stdout. Exits 0 on success, non-zero on validation
  failure. No subprocess is spawned; no browser is launched.
- `spectre version` — prints two lines: `spectre <ENGINE_VERSION>`
  and `protocol <PROTOCOL_VERSION>`. Both are already exported
  from the engine crate.

The flag set on `run` mirrors PR7's example binary:

- `--verbose` enables the same plan-printing-to-stderr behaviour.
- `--output=<path>` overrides the YAML's `output.path`. The literal
  `-` writes to stdout, matching the PR7 `OutputSink` contract
  (ADR-0012 §5).
- `--adapters-path=<path>` overrides the resolved adapters
  directory (engine's `Engine::new` constructor argument; defaults
  cascade through `SPECTRE_ADAPTERS_PATH` env var down to the
  workspace-relative `adapters/`).

`validate` is the killer debugging affordance for users authoring
jobs: they see exactly what their YAML compiles to before paying
the cost of running it. The output is plain text — a header
(`Driver: playwright`, the required capability set), then the
`PlanStep`s numbered. No JSON, no boxes, no ANSI. A reader can
copy a section into a bug report without filtering escape codes.

Rejected:

- `spectre init`, `spectre new`, `spectre scaffold`. Out of scope.
  Not adoption-blocking; users can write a YAML by hand or copy
  the example.
- `spectre run --watch`, `spectre repl`. Out of scope. Phase 1 is
  one-shot job execution.
- A configuration file at `~/.spectre/config.yaml` for default
  flags. Out of scope. The flag set is short enough that
  per-invocation specification is fine.
- Bundling adapters into the binary. Adapters are subprocesses;
  the binary launches them. PR4-PR6 work, deliberately separate.

### 4. `clap` derive macros for argument parsing

Chosen: **`clap` 4.x with the `derive` feature** for top-level CLI
parsing and subcommand dispatch. The hand-rolled argv parser from
PR7's example binary is removed in the same change.

Rationale:

- `clap` is the de facto standard in the Rust CLI ecosystem.
- Derive macros produce excellent help text automatically. Each
  subcommand and flag gets `--help` rendering, version output, and
  consistent error messaging without per-flag code.
- First-class subcommand support. `Subcommand` derive handles the
  three-way dispatch with a single match.
- `--help` and `--version` behave consistently with every other
  Rust CLI a user runs.
- Integrates cleanly with `tracing-subscriber` for the
  `--verbose` path.

The trade-off is compile-time cost from derive-macro codegen.
Measured against the existing engine build (which already pulls
`tonic`, `prost`, `serde`, etc.), the additional cost is in the
noise. If compile time becomes a real issue later, switching to
`clap`'s builder API is a mechanical refactor.

Rejected:

- **`argh`.** Lighter, but lacks subcommand ergonomics at the
  level we want.
- **`pico-args`.** Minimal, but means writing the dispatch and
  help text by hand. The example binary's hand-rolled parser was
  fine for one binary; it is the wrong shape for three subcommands.
- **Hand-rolled parser.** What PR7 had. Acceptable for one binary;
  unnecessary friction for three subcommands.

### 5. Static linking, release target, and platform support

Chosen: **`x86_64-unknown-linux-musl` is the documented Linux
release target**, producing a fully static binary that runs on any
Linux without a glibc dependency. macOS uses the default target
with standard `libSystem` linkage (ABI-stable on macOS by
contract). Windows is deferred (out of scope per ADR-0008).

The README documents the build commands:

- Linux (static): `cargo build --release --target
  x86_64-unknown-linux-musl --bin spectre`. Requires
  `musl-tools` on the build host (or `cargo zigbuild` as an
  alternative that bundles the toolchain).
- macOS: `cargo build --release --bin spectre`. Standard linkage;
  no extra toolchain.
- Windows: not built or tested. Same scope as ADR-0008.

PR8 does not ship release binaries. A future release-engineering
PR will wire `cargo-dist` (or equivalent) into a GitHub Releases
workflow, with build matrices for Linux x86_64 musl, Linux aarch64
musl, macOS x86_64, and macOS aarch64. The PR8 scope is to make
that future PR a one-off configuration change rather than a CLI
rearchitecture.

Rejected:

- **glibc Linux as the release target.** Loses the
  any-Linux-distribution property that ADR-0002 originally cited
  as Go's advantage. Replacing it would forfeit one of the original
  motivations even after the language change.
- **Ship release binaries in PR8.** Conflates two PRs. Release
  engineering deserves its own ADR (signing, checksums, release
  cadence, version tags); folding it into the CLI introduction
  delays both.
- **Promise Windows support.** ADR-0008 deferred Windows on
  transport grounds (UDS portability) and the same applies here.
  An honest "deferred" is preferable to a tentative claim.

## Confirmation

- Acceptance criteria 1–12 of the PR8 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap`, `just spectre-build`,
  `just check`, `just spectre-validate examples/hello-hackernews/job.yaml`,
  and `just spectre-run examples/hello-hackernews/job.yaml`
  succeeds on Linux and macOS.
- `target/release/spectre version` prints the engine version and
  protocol version, each on its own line.
- `target/release/spectre validate examples/hello-hackernews/job.yaml`
  prints the compiled plan and exits 0 without launching a
  subprocess.
- `target/release/spectre run examples/hello-hackernews/job.yaml`
  produces the same JSONL output as the PR7 example binary did.
- The CI Rust job builds the binary and asserts that
  `spectre version` and `spectre validate` exit 0 against the
  example YAML.
- ADR-0002's CLI row carries a `Superseded by ADR-0013` note. The
  ADR README index reflects ADR-0013 with an explicit notation
  about partial supersession.

## Consequences

- Good, because the project's user-facing entry point now exists
  under the name the roadmap promised. Phase 1 closes.
- Good, because the architecture is now uniformly "subprocess +
  protocol" — the only inter-process boundaries are the ones the
  protocol contract justifies.
- Good, because there is one fewer language toolchain to keep
  current in CI and developer environments. ADR-0002 anticipated
  five toolchains; the active set is now four for first-party
  code (Rust, Go, TypeScript, Python).
- Good, because `validate` exists as a first-class debugging
  affordance. Users can iterate on YAML without paying for a
  browser launch on every attempt.
- Neutral, because the binary inherits PR7's adapters-path
  resolution: workspace-relative by default, overridable via
  `--adapters-path` or `SPECTRE_ADAPTERS_PATH`. Users running the
  binary from outside the workspace need to pass the override; the
  README documents this.
- Neutral, because `clap` adds one direct dependency. The
  ergonomic and consistency wins outweigh the dependency cost; the
  engine crate already depends on a non-trivial number of
  third-party crates (`tonic`, `prost`, `serde`, `tokio`, etc.).
- Bad, because some readers may expect a separate `core/cli/`
  directory based on a quick reading of ADR-0002. The README, the
  engine README, and the ADR-0002 update note exist to make the
  arrangement obvious; the index notation flags the supersession
  for anyone scanning ADRs.
- Bad, because Windows users have one fewer realistic install
  path than they would have had under a Go CLI (Go cross-compiles
  to Windows trivially; Rust does too but UDS is the load-bearing
  problem). The honest position is the one ADR-0008 already took:
  Windows is deferred.

## Pros and Cons of the Options

### CLI binary location and language

#### CLI is the engine binary (chosen)

- Good, because there is no inter-process boundary between the CLI
  and the engine. One binary, one process, one error path.
- Good, because no version-sync story is required: the CLI and
  the engine are the same artifact.
- Good, because `cargo install` works out of the box.
- Bad, because readers familiar with ADR-0002's matrix may briefly
  expect a Go binary. Mitigated by ADR-0002's update note and the
  index notation.

#### Go subprocess wrapper around the engine

- Good, because it preserves ADR-0002's matrix verbatim.
- Bad, because it adds a process boundary the protocol does not
  justify.
- Bad, because signal forwarding, version pinning, and engine
  discovery become real engineering problems that a single binary
  does not have.

#### Rust CLI crate depending on `spectre-engine`

- Good, because it gives the CLI its own crate-level metadata and
  README.
- Bad, because it adds a workspace member with no isolation
  benefit; the engine's public API is what the CLI needs anyway.
- Neutral: easy to extract later if a real reason emerges.

### Argument parsing

#### `clap` derive (chosen)

- Good, because it is the ecosystem standard.
- Good, because help text, version output, and subcommand
  dispatch come for free.
- Bad, because derive-macro codegen adds compile-time cost. Below
  the noise floor against the engine's existing dependency graph.

#### Hand-rolled

- Good, because zero dependency cost.
- Bad, because three subcommands and a help text are real work
  to do well.

### Release target

#### Linux musl static + macOS default + Windows deferred (chosen)

- Good, because the resulting Linux binary runs anywhere without
  glibc concerns.
- Good, because macOS' default linkage is ABI-stable.
- Bad, because building musl statically requires `musl-tools` (or
  `cargo zigbuild`) on the build host.

#### Linux glibc default

- Good, because no extra toolchain needed.
- Bad, because the resulting binary is tied to the glibc version
  of the build host.

## More Information

- `clap` 4.x derive guide:
  <https://docs.rs/clap/latest/clap/_derive/index.html>
- Static musl builds:
  <https://doc.rust-lang.org/cargo/reference/profiles.html>
- `cargo install` install path:
  <https://doc.rust-lang.org/cargo/commands/cargo-install.html>
- Partial-supersession pattern (informal):
  <https://adr.github.io/madr/>
- Related ADRs:
  [ADR-0002 Polyglot language selection](0002-polyglot-language-selection.md)
  (this ADR supersedes the CLI row only),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md)
  (Windows deferral, UDS as transport),
  [ADR-0012 Engine DSL surface, planner architecture, and execution pipeline](0012-engine-dsl-and-execution-pipeline.md)
  (the engine pipeline the CLI consumes).

## Update (R1.1, ADR-0020)

This ADR is superseded by
[ADR-0020](0020-microservices-architecture-supersession.md) in
full. The `spectre` CLI (`spectre run`, `spectre validate`,
`spectre version`), the engine binary's standalone execution
mode, and the `cargo install spectre-engine` distribution path
are all retired. After the refactor, the engine binary exists
only as a gRPC service binary; the user-facing entry points are
the Kubernetes operator (in production) and the `docker compose
up` stack (locally). ADR-0002's CLI row, originally superseded
by this ADR (Rust replaces Go), reverts to "no CLI" — there is
no CLI binary in any language.

The argument for retirement is in
[ADR-0020](0020-microservices-architecture-supersession.md) §3
(the "what about a hybrid that keeps the CLI?" sub-bullet) and
§4 (the decision and its consequences). The refactor's no-legacy
principle (recorded in the strategy prompt that governs every
phase PR) forbids three coexisting entry points whose
responsibilities overlap. The phase that lands the actual deletion
of CLI source files is R3.
