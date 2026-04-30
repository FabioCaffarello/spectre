# `sdks/`

Per-language client SDKs for each protocol the platform exposes.

This directory is intentionally empty in Phase R6.6. The category is
defined by **[ADR-0026 §3.6](../docs/adr/0026-platform-taxonomy.md)**;
the layout, codegen ownership, versioning, and breaking-change policy
are defined by **[ADR-0027](../docs/adr/0027-sdk-strategy.md)**.

Target layout:

```
sdks/
├── rust/
│   ├── Cargo.toml          (workspace)
│   └── <protocol>/<version>/
├── go/
│   ├── go.mod              (single module)
│   └── <protocol>/<version>/
├── python/
│   ├── pyproject.toml      (uv workspace)
│   └── <protocol>/<version>/
└── typescript/
    ├── pnpm-workspace.yaml
    └── <protocol>/<version>/
```

Each `<protocol>/<version>/` SDK package owns its codegen step (idiomatic
per language: `build.rs` + `tonic-build` for Rust, `go generate` for Go,
build hook for Python, `prepare` script for TS). Generated bindings stay
gitignored per [ADR-0007](../docs/adr/0007-protocol-code-generation.md);
ADR-0027 evolves only the output *locations*, not the codegen posture.

The first SDK materialises when its first non-trivial consumer migrates
or a new protocol lands. Per ADR-0027 §7, the natural first SDK is
`sdks/rust/driver/v1alpha1/` (the engine's consumption of the Driver
Protocol).
