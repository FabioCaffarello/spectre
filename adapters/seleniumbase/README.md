# spectre-seleniumbase

Spectre's reference SeleniumBase driver adapter.

> **Status:** v0.1.0a0 — Phase 2 in progress. PR9 implements
> `Initialize`, `Navigate`, and a thin `Close` over gRPC on a Unix
> domain socket. Capabilities declared: `["navigation"]`. The other
> three RPCs (`Query`, `Extract`, `Screenshot`) respond
> `UNIMPLEMENTED`; PR10/PR11 will fill them in.
> See [ADR-0014](../../docs/adr/0014-seleniumbase-adapter-and-cross-language-conformance.md)
> for the PR9 decisions and the
> [roadmap](../../docs/roadmap.md) for the full Phase 2 picture.

## Build

From the repository root:

```bash
just sb-bootstrap                # uv sync --all-extras --dev
just sb-install-chromedriver     # SeleniumBase fetches a matching ChromeDriver
just sb-lint                     # ruff check + ruff format --check + mypy
just sb-test                     # pytest (unit tests, no browser launch)
just sb-fmt                      # ruff format
```

`sb-install-chromedriver` is required before any RPC that drives a
browser (currently `Navigate`). The recipe is idempotent and skips
when the matching driver is already on PATH. Google Chrome must
also be installed locally; the adapter raises `CODE_INTERNAL` with
a hint if the Chrome binary cannot be located on the first
`Navigate`.

Or directly:

```bash
uv sync --all-extras --dev
uv run ruff check .
uv run ruff format --check .
uv run mypy .
uv run pytest
```

## Run the adapter locally

```bash
# Terminal A — start the adapter
just sb-run                                  # uses /tmp/spectre-sb.sock
# or pick your own:
just sb-run -- --socket=/tmp/my.sock
```

The server prints `ready unix:<path>` on stdout once it accepts
connections; diagnostics go to stderr. Send `SIGTERM` (or press
Ctrl-C) to stop the gRPC server, tear down any launched Chrome
sessions, unlink the socket, and exit zero. The shutdown deadline
is 5 seconds; in-flight RPCs that exceed it are aborted.

```bash
# Terminal B — exercise it from the conformance suite
just sb-conf-test                            # SeleniumBase tests only
just conf-test                               # Playwright + SeleniumBase
```

You can also drive the adapter through `spectre run`:

```bash
just spectre-build
just spectre-run examples/seleniumbase-navigate/job.yaml --verbose
```

### Constraints

- **Absolute socket paths only.** Pass `--socket=/abs/path.sock` or
  set `SPECTRE_DRIVER_SOCKET=/abs/path.sock`. Relative paths are
  rejected; the harness anchors short paths under `/tmp` to fit
  macOS' 104-character AF_UNIX limit (ADR-0008).
- **Chrome and ChromeDriver are required.** The adapter does not
  bundle them. `seleniumbase install chromedriver` fetches the
  matching driver for the local Chrome install. If either is
  missing, `Navigate` surfaces `CODE_INTERNAL` with a hint
  pointing at the recipe (ADR-0014 §3).
- **Headless by default.** PR9's factory builds `Driver(browser="chrome",
  headless=True, uc=False)`. UC (undetected) mode is a v1alpha2
  capability candidate.
- **Windows is not supported.** Inherited from ADR-0008.

## Layout

```
adapters/seleniumbase/
├── src/spectre_seleniumbase/
│   ├── __init__.py        # PROTOCOL_VERSION, __version__
│   ├── adapter.py         # entry point — argv + signals + lifecycle
│   ├── server.py          # gRPC service implementation
│   ├── sessions.py        # SessionManager + lazy Driver factory
│   ├── capabilities.py    # declared capability list + coherence check
│   └── errors.py          # Selenium-to-DriverError mapping
├── tests/
│   ├── test_smoke.py
│   ├── test_sessions.py
│   ├── test_capabilities.py
│   └── test_errors.py
├── driver.yaml            # adapter manifest (transport, capabilities, runtime)
├── pyproject.toml         # PEP 621, hatchling backend, uv-managed
└── README.md
```

## What this adapter owns

- A `Driver` server that listens on a Unix-domain-socket gRPC
  channel (transport configured in `driver.yaml`). Built on the
  `grpcio` Python runtime; see ADR-0008 for the framework
  rationale carried forward.
- Implementations of `Initialize`, `Navigate`, and `Close` against
  the SeleniumBase API. Other unary RPCs return `UNIMPLEMENTED`
  and ship in PR10/PR11.
- Capability declarations in `driver.yaml`, added incrementally as
  each capability passes the conformance suite. The declared list
  must match `src/spectre_seleniumbase/capabilities.py` exactly —
  the conformance suite asserts this at runtime.

### Navigate semantics

- **Lazy Chrome launch.** Chrome is launched on the first
  `Navigate` for a given session, not at `Initialize` time. An
  adapter on a host without Chrome installed will `Initialize`
  successfully and surface the missing-browser failure on
  `Navigate`. ADR-0009 set this contract for Playwright; ADR-0014
  carries it to SeleniumBase unchanged.
- **Session reuse.** Each `session_id` is backed by a dedicated
  `Driver` instance reused across navigations. Cookies, localStorage,
  and other residual state persist across `Navigate` calls in the
  same session.
- **Strict `session_id`.** A `Navigate` with an unknown id returns
  `CODE_INVALID_ARGUMENT`. `Initialize` must precede every other
  RPC.
- **Timeout default.** `30_000` ms when `timeout` is omitted.
  Selenium's page-load timeout is set to that value before
  `driver.get()` runs.
- **HTTP status is data, not error.** A 4xx or 5xx response is a
  successful navigation. Selenium does not surface the HTTP status
  directly; the adapter probes
  `performance.getEntriesByType('navigation')[0].responseStatus`
  and returns `0` when the timing API is unavailable. The
  conformance suite tolerates either value for the happy path
  until PR10's Extract is available to inspect the page directly.
- **Selenium error mapping.** `TimeoutException` →
  `CODE_TIMEOUT`; `net::ERR_*` messages → `CODE_TARGET_UNREACHABLE`;
  `SessionNotCreatedException` and chromedriver-missing patterns →
  `CODE_INTERNAL` with an install hint; everything else collapses
  to `CODE_INTERNAL` with the original Selenium message preserved.
  The full table is in
  [ADR-0014 §3](../../docs/adr/0014-seleniumbase-adapter-and-cross-language-conformance.md).

### Capability coherence

The startup invariant `assert_capability_coherence` rejects a
declared list with `extract_eval` but not `js_execution`. The
SeleniumBase adapter's PR9 list (`["navigation"]`) satisfies the
rule trivially; the assertion is in place so a future maintainer
who declares a contradictory list sees the error at module load
rather than as a confusing first-RPC failure. ADR-0010 introduced
this invariant for Playwright; ADR-0014 carries it forward.

## Generated code

The Driver Protocol Python bindings live at
[`proto/gen/python/spectre/driver/v1alpha1/`](../../proto/gen/python) —
a gitignored, generated tree produced by `just proto-generate`. The
adapter consumes it via a uv editable source declared in
`pyproject.toml`. Run `just proto-generate` (or `just sb-bootstrap`,
which depends on it) before `uv sync`. See
[ADR-0007](../../docs/adr/0007-protocol-code-generation.md).

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [SeleniumBase](https://seleniumbase.io)
- ADRs: [0008](../../docs/adr/0008-driver-handshake-and-conformance-harness.md),
  [0009](../../docs/adr/0009-navigate-and-session-lifecycle.md),
  [0010](../../docs/adr/0010-element-lifecycle-and-capability-gating.md),
  [0014](../../docs/adr/0014-seleniumbase-adapter-and-cross-language-conformance.md)
