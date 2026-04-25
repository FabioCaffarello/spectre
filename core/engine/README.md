# spectre-engine

The Spectre engine core. Parses and validates the DSL, plans
extraction jobs, and drives drivers via the
[Driver Protocol](../../proto/spectre/driver/v1alpha1).

> **Status:** v0.1.0-alpha.0 — skeleton only. The crate compiles
> cleanly and exposes identity constants, nothing more. Substantive
> functionality lands in Phase 1 of the
> [roadmap](../../docs/roadmap.md).

## Build

From the repository root:

```bash
just engine-build       # release build
just engine-test        # cargo test
just engine-lint        # cargo fmt --check + cargo clippy -D warnings
```

Or directly inside this directory:

```bash
cargo build --release
cargo test
cargo clippy --all-targets --all-features -- -D warnings
```

## What this crate will own

- **Lexer / parser** for the Spectre DSL.
- **Type checker** that validates jobs against a connected driver's
  declared `Capabilities`. Failures here block execution at compile
  time, not at runtime.
- **Planner** that lowers a validated job into a sequence of Driver
  Protocol RPCs.
- **Single-host scheduler** that executes the plan against one or
  more driver processes connected over gRPC.
- **Embedding API** (FFI) for SDKs in other languages: `napi-rs`
  bindings for Node, `pyo3` bindings for Python.

## What lives elsewhere

- The Kubernetes-native distributed scheduler is the
  [`core/control-plane`](../control-plane) Go module.
- Drivers live under [`adapters/`](../../adapters).
- Generated protocol bindings come from the workspace `buf.yaml` in
  the repository root; the engine generates Rust bindings with
  `tonic-build` once the build script is wired (deferred to Phase 1).

## Architectural references

- [Architecture overview](../../docs/architecture/overview.md)
- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [ADR-0001 Driver protocol as architectural primitive](../../docs/adr/0001-driver-protocol-as-architectural-primitive.md)
- [ADR-0002 Polyglot language selection](../../docs/adr/0002-polyglot-language-selection.md)
