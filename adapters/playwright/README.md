# spectre-driver-playwright

Spectre's reference Playwright driver adapter.

> **Status:** v0.1.0-alpha.0 — skeleton only. The package compiles
> cleanly and ships an `identity()` smoke function. The Driver
> Protocol implementation lands in Phase 1 of the
> [roadmap](../../docs/roadmap.md).

## Build

From the repository root:

```bash
just pw-bootstrap     # pnpm install --frozen-lockfile
just pw-typecheck     # tsc --noEmit
just pw-test          # vitest run
just pw-build         # tsc -> dist/
just pw-lint          # prettier --check .
```

Or directly:

```bash
pnpm install --frozen-lockfile
pnpm typecheck
pnpm test
pnpm build
```

## Layout

```
adapters/playwright/
├── src/
│   ├── index.ts          # public API surface (currently identity only)
│   └── index.test.ts     # vitest smoke tests
├── driver.yaml           # adapter manifest (transport, capabilities, runtime)
├── package.json          # ESM, pnpm-managed, vitest + prettier + tsc
├── tsconfig.json
└── README.md
```

## What this adapter will own

- A `Driver` server that listens on a Unix-domain-socket gRPC channel
  (transport configured in `driver.yaml`).
- Implementation of `Initialize`, `Navigate`, `Query`, `Extract`,
  `Screenshot`, and `Close` against the Playwright API.
- Capability declarations in `driver.yaml`, added incrementally as
  each capability passes the conformance suite.

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [Playwright](https://playwright.dev)
