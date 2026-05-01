# Driver Protocol

This directory contains the protobuf definitions for the Spectre
Driver Protocol — the contract between the engine and any
browser-automation runtime that implements it.

For the design rationale, read
[`docs/architecture/driver-protocol.md`](../docs/architecture/driver-protocol.md).
For the versioning rules, read
[ADR-0004](../docs/adr/0004-protocol-versioning-strategy.md).

## Status

`spectre.driver.v1alpha1` — unstable. Breaking changes are expected
until the three reference adapters all pass the conformance suite.

## Layout

The `buf.yaml` *workspace* configuration lives at the repository root
(see `../buf.yaml`). This directory contains only the protocol module
itself plus its codegen config.

```
proto/
├── buf.gen.yaml                              # codegen config
├── README.md
└── spectre/driver/v1alpha1/
    ├── driver.proto                          # Driver service + handshake + nav + screenshot
    ├── capabilities.proto                    # Capabilities message
    ├── errors.proto                          # DriverError envelope
    └── extraction.proto                      # Query, Extract, ElementRef, Field

# repo root
buf.yaml                                      # workspace: lists `proto` as a module
```

## Working with the protocol

From the repository root:

```bash
just proto-lint        # buf lint + format check
just proto-fmt         # rewrite in place
just proto-breaking    # compare against origin/main
```

Or directly (from anywhere inside the repo — buf walks up to find the
workspace `buf.yaml`):

```bash
buf lint
buf format -w
buf build              # parse and confirm the schema is well-formed
```

## Code generation

`buf.gen.yaml` produces typed bindings for downstream consumers. All
generator outputs are gitignored — regenerate locally or in CI
rather than checking generated code into the repository. The
contract and rationale are recorded in
[ADR-0007](../docs/adr/0007-protocol-code-generation.md).

```bash
just proto-generate            # writes Go, Python, and TypeScript
```

Or directly:

```bash
cd proto && buf generate       # then bash tools/codegen/post-generate.sh
```

Output paths:

| Language    | Path                                              | Consumed by                               |
|-------------|---------------------------------------------------|-------------------------------------------|
| Go          | `proto/gen/go/spectre/driver/v1alpha1/`           | `operators/control-plane`, `adapters/curl-impersonate` (via local `replace`) |
| Python      | `proto/gen/python/spectre/driver/v1alpha1/`       | `adapters/seleniumbase`, `tools/conformance` (via uv editable source) |
| TypeScript  | `adapters/playwright/src/proto/spectre/driver/v1alpha1/` | `adapters/playwright`                  |
| Rust        | cargo `OUT_DIR` (per-build artifact dir)          | `engines/engine` (via `tonic-build` in `build.rs`) |

Rust generation is lazy: it runs at `cargo build`/`cargo test` time
via `engines/engine/build.rs` rather than during `just proto-generate`.

## Conventions

- One service per directory, named `Driver`.
- Every RPC has a paired `*Request` and `*Response` message.
- Errors are surfaced via the `DriverError` envelope, never as
  transport-level exceptions or unstructured stderr output.
- Enums declare an explicit `*_UNSPECIFIED = 0` first value.
- Field numbering is append-only within a stable version.
