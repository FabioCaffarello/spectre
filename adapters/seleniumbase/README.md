# spectre-seleniumbase

Spectre's reference SeleniumBase driver adapter.

> **Status:** v0.1.0a0 — skeleton only. The package imports cleanly,
> the smoke tests pass, and the entry point prints its identity. The
> Driver Protocol implementation lands in Phase 2 of the
> [roadmap](../../docs/roadmap.md).

## Build

From the repository root:

```bash
just sb-bootstrap   # uv sync --all-extras --dev
just sb-lint        # ruff check + ruff format --check + mypy
just sb-test        # pytest
just sb-fmt         # ruff format
```

Or directly:

```bash
uv sync --all-extras --dev
uv run ruff check .
uv run ruff format --check .
uv run mypy .
uv run pytest
```

## Layout

```
adapters/seleniumbase/
├── src/spectre_seleniumbase/
│   ├── __init__.py        # PROTOCOL_VERSION, __version__
│   └── adapter.py         # entry point: prints identity (placeholder)
├── tests/
│   └── test_smoke.py
├── driver.yaml            # adapter manifest
├── pyproject.toml         # PEP 621, hatchling backend, uv-managed
└── README.md
```

## What this adapter will own

- A `Driver` server that runs as a child process of the engine and
  speaks gRPC over a Unix domain socket.
- Implementation of `Initialize`, `Navigate`, `Query`, `Extract`,
  `Screenshot`, and `Close` against the SeleniumBase API, including
  its UC Mode (CDP-driven) where relevant.
- Capability declarations in `driver.yaml`, added incrementally as
  each capability passes the conformance suite.

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [SeleniumBase](https://seleniumbase.io)
