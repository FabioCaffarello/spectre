# spectre-curl-impersonate

Spectre's curl-impersonate driver adapter. Wraps the
[curl-impersonate](https://github.com/lwthiker/curl-impersonate) C
library via cgo and exposes a gRPC Driver server.

> **Status:** v0.1.0-alpha.0 — skeleton only. The binary prints its
> build identity and exits. The cgo wrapper and gRPC server land in
> Phase 2 of the [roadmap](../../docs/roadmap.md).

## Module path

```
github.com/FabioCaffarello/spectre/adapters/curl-impersonate
```

## Build

From the repository root:

```bash
just curl-imp-build       # go build -o bin/adapter ./cmd/adapter
just curl-imp-test        # go test ./...
just curl-imp-lint        # go vet + golangci-lint
```

Or directly:

```bash
go build -o bin/adapter ./cmd/adapter
go test ./...
```

## Why a Go wrapper for a C library

curl-impersonate ships a C library plus a forked `curl` binary. Many
deployments only need the library — and need to call it from a
long-running server process. Go's cgo bindings give us:

- A static binary (no runtime Python/Node dependency).
- Easy integration with the gRPC Driver server.
- A predictable concurrency model for HTTP-only flows.

## Use cases

This adapter targets workflows where a full browser is unnecessary
but the request fingerprint must match a real browser's TLS and
HTTP/2 profile. Capabilities that require JavaScript execution,
DOM manipulation, or screenshots will not be declared by this driver
— jobs that depend on those capabilities should select a
browser-based adapter (Playwright, SeleniumBase) at compile time.

## Generated code

Like the control plane, this module consumes the Driver Protocol Go
bindings from the gitignored
[`proto/gen/go/`](../../proto/gen/go) tree via a local `replace`
directive. Run `just proto-generate` (or `just curl-imp-bootstrap`,
which depends on it) before `go build`/`go test`. See
[ADR-0007](../../docs/adr/0007-protocol-code-generation.md).

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [curl-impersonate](https://github.com/lwthiker/curl-impersonate)
