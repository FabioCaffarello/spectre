# spectre-controller

The Spectre control plane: a Kubernetes-native scheduler that
dispatches jobs to engine workers, applies retry and quota policy,
and exposes a control API for SDKs and CLIs.

> **Status:** v0.1.0-alpha.0 — skeleton only. The binary prints its
> build identity and exits. Substantive functionality lands in
> Phase 3 of the [roadmap](../../docs/roadmap.md).

## Module path

```
github.com/FabioCaffarello/spectre/core/control-plane
```

## Build

From the repository root:

```bash
just cp-build           # go build -o bin/controller ./cmd/controller
just cp-test            # go test ./...
just cp-lint            # go vet + golangci-lint
```

Or directly:

```bash
go build -o bin/controller ./cmd/controller
go test ./...
```

The compiled binary lives at `bin/controller`. `bin/` is gitignored
at the repo level. Plain `go build ./...` writes one binary per main
package into the current directory, which is why the recipe targets
`bin/` explicitly.

## Layout

```
core/control-plane/
├── cmd/
│   └── controller/         # main package: spectre-controller binary
├── internal/               # private packages, not importable downstream
└── go.mod
```

The `internal/` directory is currently empty; it will host the
scheduler, the Kubernetes operator, and the control API once those
land. Public packages (intended for downstream import by SDKs) will
live in a `pkg/` directory introduced when there is something to
export.

## Architectural references

- [Architecture overview](../../docs/architecture/overview.md)
- [ADR-0002 Polyglot language selection](../../docs/adr/0002-polyglot-language-selection.md)
- [Roadmap — Phase 3: Distributed execution](../../docs/roadmap.md#phase-3--distributed-execution)
