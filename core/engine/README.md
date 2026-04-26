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
> for the design and the
> [roadmap](../../docs/roadmap.md) for what is still outstanding in
> Phase 1.

## Run the example

`hello-hackernews` is a runnable example binary that exercises the
full pipeline against the live Hacker News front page:

```bash
cd core/engine
cargo run --example hello-hackernews -- --verbose
```

Output: one JSON row per story, written to
`examples/hello-hackernews/stories.jsonl` relative to the job file
(or to stdout when `output.path` is `-`).

The `--verbose` flag prints the compiled `Plan` to stderr before
execution. Use it to see exactly which RPCs the engine will issue.

The Go CLI (`spectre run`) is deferred to PR8 — until then, the
example binary is the user-visible entry point.

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
just engine-build       # release build
just engine-test        # cargo test (unit tests; integration test is gated)
just engine-lint        # cargo fmt --check + cargo clippy -D warnings
just engine-run-hello   # cargo run --example hello-hackernews
```

Or directly inside this directory:

```bash
cargo build --release
cargo test                                            # unit tests
PLAYWRIGHT_AVAILABLE=1 cargo test -- --ignored        # integration test
cargo run --example hello-hackernews -- --verbose
```

The integration test is `#[ignore]` by default — it builds the
Playwright adapter and launches Chromium. CI runs it in a dedicated
job with the browser cache populated; locally, run it explicitly only
when you have Chromium available.

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
