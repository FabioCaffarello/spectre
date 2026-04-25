# spectre-conformance

The Spectre Driver Protocol conformance test suite.

> **Status:** v0.1.0a0 — skeleton only. The suite exposes one
> self-contained smoke test that validates the protocol-version
> target string. Real driver-exercising conformance tests land in
> Phase 1 of the [roadmap](../../docs/roadmap.md).

## Build

From the repository root:

```bash
just conf-bootstrap   # uv sync --all-extras --dev
just conf-lint        # ruff + mypy
just conf-test        # pytest
```

Or directly:

```bash
uv sync --all-extras --dev
uv run pytest
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

## Driver invocation (planned)

A future `--driver=PATH/TO/driver.yaml` pytest option will spawn the
driver under test and connect to it. Until that lands, the smoke
test is self-contained and does not require a running driver.

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
