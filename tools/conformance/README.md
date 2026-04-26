# spectre-conformance

The Spectre Driver Protocol conformance test suite.

> **Status:** v0.1.0a0 — exercises every implemented unary RPC
> against the Playwright adapter over gRPC on a Unix domain
> socket: `Initialize`, `Navigate`, `Close`, `Query`, and
> `Extract`. `Screenshot` is the only remaining unimplemented
> unary RPC; the negative test cements its `UNIMPLEMENTED` status
> until it ships. Transport equivalence and per-capability tests
> follow in Phase 1 of the [roadmap](../../docs/roadmap.md).

## Build

From the repository root:

```bash
just conf-bootstrap   # uv sync --all-extras --dev
just conf-lint        # ruff + mypy
just conf-test        # builds the playwright adapter, then pytest
```

`just conf-test` depends on `just pw-build` so the live tests always
run against fresh artifacts. Run directly if you've already built:

```bash
uv sync --all-extras --dev
uv run pytest
```

## Run the conformance suite

The Playwright handshake test launches the adapter as a subprocess
on a per-test Unix domain socket, dials it as a gRPC client, sends
`InitializeRequest`, and validates the response:

```bash
just conf-test
```

Skip behaviour: if the Playwright `dist/` is not built, the
Playwright fixture skips with a hint to run `just pw-build`. The CI
job builds the adapter unconditionally before running pytest.

## Layout

```
tools/conformance/
├── src/spectre_conformance/
│   ├── __init__.py
│   ├── capabilities.py     # canonical capability-name constants
│   ├── demo_navigate.py    # manual demo: dial a running adapter, navigate once
│   ├── demo_full_cycle.py  # manual demo: Initialize → Navigate → Query → Extract → Close
│   ├── harness.py          # DriverHarness — subprocess + grpc.Channel
│   └── http_fixture.py     # local HTTP server for deterministic Navigate tests
├── tests/
│   ├── conftest.py        # pytest fixtures (playwright_adapter, local_http_server, …)
│   ├── test_close.py
│   ├── test_extract.py
│   ├── test_initialize.py
│   ├── test_navigate.py
│   ├── test_query.py
│   └── test_unimplemented.py
├── pyproject.toml
└── README.md
```

## What this suite will own

When complete, the suite exercises every Driver Protocol RPC and
every capability declared by the driver under test. Its
responsibilities:

1. **Handshake conformance.** Verify that the driver returns a
   well-formed `InitializeResponse` with a non-empty `session_id`,
   declares a protocol version path the engine speaks, and lists its
   capabilities.
2. **Capability assertions.** For each capability the driver
   declares, run the corresponding test path. A driver that declares
   `screenshot_full_page` but fails the screenshot test cannot be
   accepted into the ecosystem registry.
3. **Error envelope shape.** Drivers must surface errors as
   `DriverError`. The suite induces several failure modes (timeout,
   missing element, malformed input) and verifies the envelope is
   shaped correctly.
4. **Transport equivalence.** When a driver supports both gRPC and
   JSON-RPC transports, the suite asserts they yield identical
   results.

## Driver invocation

`spectre_conformance.harness.DriverHarness.from_driver_yaml(<path>)`
reads the manifest, picks a fresh per-instance socket path under
`/tmp` (short enough to fit macOS' 104-character UDS limit),
launches the subprocess with `--socket=<path>` (also exported as
`SPECTRE_DRIVER_SOCKET`), waits for the driver's `ready unix:<path>`
line on stdout, and exposes a configured `grpc.Channel` via
`harness.dial()`. Use it as a context manager — the subprocess is
terminated and the socket is cleaned up on exit, even when an
assertion fails.

A future `--driver=PATH/TO/driver.yaml` pytest CLI option will
allow running the same suite against any conforming driver. The
harness API is the seed; the CLI wraps it.

## Manual full-cycle demo

`demo_full_cycle.py` drives the complete RPC sequence
(`Initialize → Navigate → Query → Extract → Close`) against an
already-running adapter and prints each response. It is the
canonical human smoke test for PR5; the conformance suite covers
the same surface automatically.

```bash
just pw-build
just pw-install-browsers
just pw-run -- --socket=/tmp/spectre-demo.sock

# in a second terminal:
uv --project tools/conformance run python -m \
    spectre_conformance.demo_full_cycle \
    --socket=/tmp/spectre-demo.sock \
    --url=https://example.com \
    --selector="h1"
```

## Local HTTP fixture

`Navigate` tests point at an in-process HTTP server rather than the
public internet. The `local_http_server` session-scoped fixture (see
`tests/conftest.py`) yields a started instance of
`spectre_conformance.http_fixture.LocalHttpServer`, which binds to
`127.0.0.1` on a random port and serves four routes:

| Route                | Behaviour                                             |
|----------------------|-------------------------------------------------------|
| `GET /ok`            | 200 with body `ok`.                                   |
| `GET /redirect`      | 302 to `/ok`.                                         |
| `GET /status/<code>` | The given status code with body `<code>`. 100–599.    |
| `GET /slow`          | Sleeps 5 s, then 200. Used for timeout testing.       |
| `GET /elements`      | Stable HTML used by Query/Extract tests.              |
| `GET /elements-2`    | Different HTML used to test post-Navigate ref staleness. |

Adding a route: edit `_LocalHandler.do_GET` in
`http_fixture.py`. The fixture is stdlib-only (no extra
dependencies). The choice is deliberate — public-internet calls in
conformance tests are flaky by definition. See
[ADR-0009](../../docs/adr/0009-navigate-and-session-lifecycle.md).

## Generated code

The conformance suite imports the Driver Protocol Python bindings
from
[`proto/gen/python/spectre/driver/v1alpha1/`](../../proto/gen/python)
via a uv editable source — the same mechanism used by the
SeleniumBase adapter. The smoke test sources
`PROTOCOL_VERSION_TARGET` from the generated `FileDescriptor`
rather than declaring it as a literal. Run `just proto-generate` (or
`just conf-bootstrap`, which depends on it) before `uv sync`. See
[ADR-0007](../../docs/adr/0007-protocol-code-generation.md).

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [ADR-0004 Protocol versioning strategy](../../docs/adr/0004-protocol-versioning-strategy.md)
