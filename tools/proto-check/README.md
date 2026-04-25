# proto-check

This directory hosts protocol-validation tooling beyond what `buf`
provides out of the box.

> **Status:** placeholder. The directory currently exists only to
> reserve the path. The first concrete tools land in Phase 2 of the
> [roadmap](../../docs/roadmap.md).

## Why a separate directory

The [`proto/`](../../proto) directory holds the schema source of
truth and is managed by `buf` (lint, format, breaking-change
detection). `tools/proto-check/` is for project-specific validators
that operate on the schema *plus* repository state — outside `buf`'s
remit.

## Tools that may land here

- A linter that verifies every adapter's `driver.yaml` declares a
  `protocol_version` that the schema actually exposes (i.e. there is
  a `proto/spectre/driver/<version>/` directory for it).
- A capability-string registry validator that ensures driver-declared
  capabilities are either standard names or properly namespaced
  (`<driver>.<capability>`).
- A documentation drift checker that fails CI if
  `docs/architecture/driver-protocol.md` references an RPC that no
  longer exists in the schema.
- An ecosystem-registry generator that compiles a JSON index of
  community drivers from their `driver.yaml` manifests.

## See also

- [`.github/workflows/proto-check.yml`](../../.github/workflows/proto-check.yml)
  runs `buf breaking` on every PR that touches `proto/`.
- [Driver Protocol design](../../docs/architecture/driver-protocol.md).
