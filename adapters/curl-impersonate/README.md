# spectre-curl-impersonate

Spectre's curl-impersonate driver adapter. Wraps the
[curl-impersonate](https://github.com/lwthiker/curl-impersonate)
binary as a per-request subprocess and exposes a gRPC Driver
server over a Unix domain socket.

> **Status:** v0.1.0-alpha.0 — Phase 2 in progress. PR11 implements
> `Initialize`, `Navigate`, and a thin `Close` (matching the
> [PR9 SeleniumBase precedent](../../docs/adr/0014-seleniumbase-adapter-and-cross-language-conformance.md)
> so the engine's executor can finish navigate-only plans). PR12
> will add the rich `Close`, `Query`, and `Extract` RPCs.
> `Screenshot` and `MODE_EVAL` will *never* be implemented for
> this adapter — see ADR-0016 §5.

## Module path

```
github.com/FabioCaffarello/spectre/adapters/curl-impersonate
```

## Run locally

From the repository root:

```bash
just curl-imp-build       # go build -o bin/adapter ./cmd/adapter
just curl-imp-test        # go test ./...
just curl-imp-lint        # go vet + golangci-lint
just curl-imp-run         # bind to /tmp/spectre-curl.sock
just curl-imp-conf-test   # run the curl-impersonate conformance suite
```

The adapter prints `ready unix:<path>` on stdout once it is
accepting connections. SIGTERM/SIGINT drains, removes every
session's cookie-jar file, and unlinks the socket before exit.

## Why subprocess invocation, not cgo

curl-impersonate ships both a C library and a forked `curl`
binary. Several integration shapes are possible; this adapter
chose **subprocess invocation** of the binary deliberately.
ADR-0016 §1 records the rationale, summarised here:

- **Architectural symmetry.** Spectre's adapters are subprocesses
  speaking gRPC over UDS (ADR-0008). This adapter is a subprocess
  that itself shells out to a subprocess. cgo would be the
  project's first architectural exception.
- **CI tractability.** Linux installs a static release tarball
  into `/usr/local/bin`. macOS is a manual download from the
  release page. cgo would require dev headers, dynamic linkage,
  and per-platform shared-object handling.
- **Cross-platform robustness.** Static binaries from the
  curl-impersonate releases work on any modern x86_64 Linux. cgo
  cross-compilation between Linux and macOS is well-known
  platform pain the project has not earned.
- **Performance is not the bottleneck for v1alpha1.** Each
  Navigate spawns one process: ~5-15ms overhead on Linux. For
  Phase 2's smoke-test scope, this is invisible. A long-running
  curl-impersonate via stdin/stdout pipes is a backwards-compatible
  v1alpha2 candidate if real throughput requirements surface.

## Variant override

The adapter invokes `curl_chrome116` by default. Operators can
pick a different curl-impersonate variant by setting
`SPECTRE_CURL_VARIANT` before launching the adapter:

```bash
SPECTRE_CURL_VARIANT=curl_firefox117 just curl-imp-run
```

Variant availability depends on the curl-impersonate release
that's installed on PATH. ADR-0016 §3 records the default
rationale and the override mechanism.

## Use cases

This adapter targets workflows where a full browser is
unnecessary but the request fingerprint must match a real
browser's TLS and HTTP/2 profile. Capabilities that require
JavaScript execution, DOM manipulation, or screenshots will not
be declared by this driver — jobs that depend on those
capabilities should select a browser-based adapter (Playwright,
SeleniumBase) at compile time. The engine's
`validate_capabilities` path rejects the plan before any
adapter launches.

## Generated code

Like the control plane, this module consumes the Driver Protocol
Go bindings from the gitignored
[`proto/gen/go/`](../../proto/gen/go) tree via a local `replace`
directive. Run `just proto-generate` (or `just curl-imp-bootstrap`,
which depends on it) before `go build`/`go test`. See
[ADR-0007](../../docs/adr/0007-protocol-code-generation.md).

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [curl-impersonate](https://github.com/lwthiker/curl-impersonate)
- [ADR-0016](../../docs/adr/0016-curl-impersonate-adapter-and-third-runtime-divergence.md)
  — subprocess-over-cgo, WaitCondition no-op, default variant,
  cookie-jar architecture, and the third capability divergence.
