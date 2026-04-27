# spectre-curl-impersonate

Spectre's curl-impersonate driver adapter. Wraps the
[curl-impersonate](https://github.com/lwthiker/curl-impersonate)
binary as a per-request subprocess and exposes a gRPC Driver
server on a TCP listener. The R2.2 refactor retired the original
Unix-domain-socket transport in favour of TCP + the gRPC standard
health check (ADR-0021, ADR-0022).

> **Status:** v0.1.0-alpha.0 — Phase 2 closing. PR12 closes the
> v1alpha1 unary surface for this adapter: full `Close`, `Query`
> (CSS + XPath), and `Extract` (TEXT_CONTENT, INNER_TEXT,
> INNER_HTML, OUTER_HTML, ATTR), with the `MODE_EVAL` runtime
> gate from ADR-0010 §3 firing on every request that asks for it.
> Six capabilities declared (alphabetical:
> `extract_attribute`, `extract_html`, `extract_text`,
> `navigation`, `query_css`, `query_xpath`). `Screenshot`,
> `MODE_EVAL`, `query_text`, and `query_attribute` will *never*
> be implemented for this adapter — see ADR-0016 §5 and
> ADR-0017 §1 / §5.

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
just curl-imp-run         # bind 0.0.0.0:9093 (ADR-0021 §4)
just curl-imp-conf-test   # run the curl-impersonate conformance suite
```

The adapter logs a single `listening on 0.0.0.0:<port>` line on
stderr when ready. Readiness is signalled exclusively by the gRPC
standard health check (`grpc.health.v1.Health/Check`) returning
`SERVING`; there is no readiness banner on stdout. SIGTERM/SIGINT
drains in-flight RPCs, removes every session's cookie-jar file,
and exits zero. The bind port is read from
`SPECTRE_ADAPTER_GRPC_PORT` — the conformance harness allocates a
free ephemeral port at start time; production deployments use the
canonical 9093 reserved by ADR-0021 §4.

> The R6.2 Compose stack will replace `just curl-imp-run` as the
> canonical local-dev path. Until then this recipe survives as a
> convenience. The R2.2-R2.3 sequence breaks `spectre run`
> end-to-end because the engine still dials UDS — see
> `KNOWN_BREAKAGE.md` at the repo root.

## Why subprocess invocation, not cgo

curl-impersonate ships both a C library and a forked `curl`
binary. Several integration shapes are possible; this adapter
chose **subprocess invocation** of the binary deliberately.
ADR-0016 §1 records the rationale, summarised here:

- **Architectural symmetry.** Spectre's adapters are subprocesses
  speaking gRPC over TCP (ADR-0008 + ADR-0022). This adapter is a
  subprocess that itself shells out to a subprocess. cgo would be
  the project's first architectural exception.
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

## What this driver cannot do

The capability surface is six entries: `extract_attribute`,
`extract_html`, `extract_text`, `navigation`, `query_css`,
`query_xpath`. Seven capabilities are absent and will remain
absent in v1alpha1:

- **`js_execution` / `extract_eval`** — no JavaScript engine.
  `MODE_EVAL` fields trigger the runtime capability gate from
  ADR-0010 §3 and reject the entire `Extract` request with
  `CODE_CAPABILITY_MISSING`. The conformance suite's
  `test_curl_impersonate_extract_eval_returns_capability_missing`
  is the first conformance test in the project that exercises
  the negative path of the gate.
- **`screenshot_viewport` / `screenshot_full_page` /
  `screenshot_element`** — no rendering pipeline. The
  `Screenshot` RPC returns `codes.Unimplemented` permanently;
  ADR-0016 §5 documents the framing.
- **`query_text` / `query_attribute`** — goquery technically
  supports both, but Playwright's `getByText` (rendered visible
  text, case-insensitive substring) and goquery's
  `:contains()` (DOM text, case-sensitive substring) are
  semantically different searches. Declaring one capability
  with two interpretations defeats the cross-driver planning
  surface. **ADR-0017 §1** formalises the contract: capability
  declaration is a cross-driver semantic-equivalence promise,
  not a feasibility decision. Operators who hit
  `SELECTOR_KIND_TEXT` / `SELECTOR_KIND_ATTRIBUTE` against this
  adapter receive `CODE_INVALID_ARGUMENT` with a message that
  references ADR-0017.

`MODE_INNER_TEXT` is supported but is a documented semantic
approximation: with no layout engine, the adapter cannot exclude
hidden text the way browsers do. The fallback is the same output
as `MODE_TEXT_CONTENT`. Clients who need true visible-text
semantics should use `driver: playwright` or
`driver: seleniumbase`. **ADR-0017 §5** records the trade-off
and sketches a v1alpha2 capability split.

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
  cookie-jar architecture, and the third capability divergence
  (PR11).
- [ADR-0017](../../docs/adr/0017-curl-impersonate-extraction-and-final-capability-divergence.md)
  — `query_text` / `query_attribute` omission as the
  semantic-equivalence contract, goquery + htmlquery integration,
  ElementRef simplification, SelectorKind mapping, Field.Mode
  mapping for static HTML, and the MODE_EVAL conformance test
  (PR12).
