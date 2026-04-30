# spectre-engine

The Spectre engine core. Parses and validates the DSL, plans
extraction jobs, launches a driver subprocess, dials it over a Unix
domain socket, and drives the
[Driver Protocol](../../proto/spectre/driver/v1alpha1) RPC sequence to
produce JSON Lines output.

> **Status:** v0.1.0-alpha.0 — first working end-to-end execution.
> The DSL surface is the v1alpha1 minimum (one `navigate` plus one
> `extract` with a small field-spec table); the driver list is
> hardcoded to `playwright`. See
> [ADR-0012](../../docs/adr/0012-engine-dsl-and-execution-pipeline.md)
> for the design,
> [ADR-0013](../../docs/adr/0013-cli-as-engine-binary.md) for why
> the CLI lives in this crate, and the
> [roadmap](../../docs/roadmap.md) for the project's current phase.

## Build the binary

The crate produces a `[[bin]]` named `spectre` (ADR-0013). From
the repository root:

```bash
just spectre-build
# → engines/engine/target/release/spectre
```

`cargo install spectre-engine` installs the same binary on
`$PATH`. Pre-built release artifacts are out of scope for this PR
(see ADR-0013 §5).

## Run the hello-hackernews job

```bash
just spectre-build
just spectre-run examples/hello-hackernews/job.yaml --verbose
```

Output: one JSON row per story, written to
`examples/hello-hackernews/stories.jsonl` relative to the job
file (or to stdout when `output.path` is `-` or `--output=-` is
passed).

The `--verbose` flag prints the compiled `Plan` to stderr before
execution. Use it to see exactly which RPCs the engine will issue.
Use `spectre validate examples/hello-hackernews/job.yaml` to do
the same parse + plan + capability check without launching the
driver — useful when iterating on YAML.

## Subcommands

| Subcommand            | Purpose                                                                |
|-----------------------|------------------------------------------------------------------------|
| `spectre run JOB`     | Parse, plan, launch the driver, execute, and write JSONL.              |
| `spectre validate JOB`| Parse, plan, check declared capabilities; print the plan; no launch.   |
| `spectre version`     | Print engine and protocol versions.                                    |

`run` accepts `--verbose`, `--output=<path>` (use `-` for stdout),
and `--adapters-path=<path>` to override the default adapters
directory resolution.

## DSL surface

```yaml
spectre: v1alpha1
driver: playwright

steps:
  - navigate: <url>
  - extract:
      selector: <css-selector>
      fields:
        <name>: <field-spec>

output:
  format: jsonl
  path: ./<file>          # or `-` for stdout
```

`<field-spec>` is one of: `textContent`, `innerText`, `innerHTML`,
`outerHTML`, `href`, `src`, or `attr:<name>`. `MODE_EVAL` is not
exposed in v1alpha1.

The DSL is deliberately higher-level than the protocol: a single
`extract` step compiles to `Query` plus a per-element `Extract` loop.
See ADR-0012 §1 for the rationale.

## Build

From the repository root:

```bash
just engine-build              # release build (library + binary)
just spectre-build             # release build of just the spectre binary
just engine-test               # cargo test (unit tests; integration is gated)
just engine-lint               # cargo fmt --check + cargo clippy -D warnings
just spectre-run JOB           # spectre run <JOB>
just spectre-validate JOB      # spectre validate <JOB>
just spectre-version           # spectre version (smoke test)
```

Or directly inside this directory:

```bash
cargo build --release --bin spectre
cargo test                                            # unit tests
PLAYWRIGHT_AVAILABLE=1 cargo test -- --ignored        # integration test
./target/release/spectre run ../../examples/hello-hackernews/job.yaml --verbose
```

The integration test is `#[ignore]` by default — it builds the
Playwright adapter and launches Chromium. CI runs it in a dedicated
job with the browser cache populated; locally, run it explicitly only
when you have Chromium available.

## Developer workflow

When iterating on the engine itself, `cargo run --bin spectre` is
faster than the release build:

```bash
cd engines/engine
cargo run --bin spectre -- validate ../../examples/hello-hackernews/job.yaml
cargo run --bin spectre -- run ../../examples/hello-hackernews/job.yaml --verbose
```

`spectre validate` is the right tool for editing YAML without
paying for a browser launch on every attempt. The release binary
is what users install; the debug binary is what engine maintainers
iterate against.

## Static Linux build

Per ADR-0013 §5, the documented Linux release target is
`x86_64-unknown-linux-musl` for a fully static binary that runs on
any Linux without a glibc dependency:

```bash
# Requires `musl-tools` (apt) or `cargo zigbuild` on the build host.
cargo build --release --target x86_64-unknown-linux-musl --bin spectre
```

macOS uses the default target (`cargo build --release --bin spectre`),
which links to ABI-stable `libSystem`. Windows is deferred (ADR-0008
on UDS portability).

## Module layout

| Module       | Responsibility                                                      |
|--------------|---------------------------------------------------------------------|
| `dsl`        | YAML parsing and validation. Produces a `Job`.                      |
| `plan`       | Compiles `Job` → `Plan` (data); resolves required capabilities.     |
| `launcher`   | Spawns the driver subprocess, waits for the readiness line, owns    |
|              | the SIGTERM-on-shutdown contract. Mirrors the Python harness.       |
| `client`     | gRPC client over UDS, hides tonic specifics.                        |
| `executor`   | Walks the `Plan` against a `Client`, writes one row per element.    |
| `output`     | `JsonlFileSink` and `StdoutSink`, streaming with per-row flush.     |
| `error`      | `EngineError` taxonomy.                                             |

## Generated code

`build.rs` invokes `tonic-build` (which delegates message generation
to `prost-build`) on the protocol files at
[`proto/spectre/driver/v1alpha1/`](../../proto/spectre/driver/v1alpha1).
The generated Rust output lands in cargo's `OUT_DIR` and is included
into the `proto` module via `tonic::include_proto!`. `PROTOCOL_VERSION`
is sourced from a sibling file written by the same build script, so
the constant has the same provenance as the types it qualifies. See
[ADR-0007](../../docs/adr/0007-protocol-code-generation.md).

## Architectural references

- [Architecture overview](../../docs/architecture/overview.md)
- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [ADR-0001 Driver protocol as architectural primitive](../../docs/adr/0001-driver-protocol-as-architectural-primitive.md)
- [ADR-0008 Driver handshake and conformance harness](../../docs/adr/0008-driver-handshake-and-conformance-harness.md)
- [ADR-0010 Element lifecycle and capability gating](../../docs/adr/0010-element-lifecycle-and-capability-gating.md)
- [ADR-0012 Engine DSL surface, planner architecture, and execution pipeline](../../docs/adr/0012-engine-dsl-and-execution-pipeline.md)
