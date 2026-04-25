---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Protocol versioning strategy

## Context and Problem Statement

The Driver Protocol is the contract between the Spectre engine and
every driver. Once external drivers exist, the protocol becomes
load-bearing infrastructure: a breaking change ripples to every
adapter in the ecosystem. We need a versioning strategy that allows
the protocol to evolve while protecting drivers that have already
shipped.

## Decision Drivers

- Drivers must be able to depend on a specific protocol version
  without fear that a `git pull` breaks them.
- Adding new messages or capabilities must not require a new major
  version.
- Breaking changes must be possible — but expensive enough that they
  are deliberate, not incidental.
- The strategy must work across the multiple languages used by
  drivers (ADR-0002).

## Considered Options

- **Option A — SemVer in package metadata**: bump major versions in
  `package.json` / `Cargo.toml` / `pyproject.toml`. The protobuf
  package name stays the same.
- **Option B — Path-based versioning**: encode the version into the
  protobuf package and directory path
  (`spectre.driver.v1`, `spectre.driver.v2`). New versions live
  alongside existing ones.
- **Option C — Single-version-with-deprecation**: one path, deprecate
  fields with annotations, never break.

## Decision Outcome

Chosen option: **Option B — Path-based versioning**.

This is the pattern Google, Kubernetes, and Bufbuild itself use for
public APIs:

- Path: `proto/spectre/driver/vNalphaM/`, `vNbetaM/`, `vN/`.
- Package: `spectre.driver.vNalphaM`, etc.
- Manifest: drivers declare `protocol_version: spectre.driver.v1` in
  their `driver.yaml`.
- Within a stable version (`v1`), messages are append-only. New fields
  use new field numbers. New RPCs may be added.
- Breaking changes require a new version path. `v1` and `v2` coexist.
  `v1` is removed only when no maintained driver still targets it.

### Pre-stable progression

Until the protocol has been validated by all three reference adapters
(Playwright/TS, SeleniumBase/Python, curl-impersonate/Go) running the
conformance suite, the protocol is unstable:

- `v1alpha1` — current state. Breaking changes expected. No
  compatibility guarantees.
- `v1beta1` — feature-frozen for a stabilization period. Breaking
  changes require strong justification.
- `v1` — stable. The append-only and side-by-side rules apply.

### Consequences

- Good, because drivers pin to a specific version and survive future
  protocol evolution untouched.
- Good, because reviewers can spot a breaking change at the directory
  level (a new path appears).
- Good, because tooling (`buf breaking`) can enforce the discipline:
  the stable-version path is checked breaking-against itself; new
  paths are not.
- Bad, because supporting two versions in the engine means the engine
  carries adapter code for both. Mitigated by removing old versions
  once their last driver migrates.
- Bad, because the file tree grows (one subtree per maintained
  version). Acceptable cost for the contract clarity it buys.

### Confirmation

- CI workflow `proto-check.yml` runs `buf breaking` on every PR
  touching `proto/`. Breaking changes inside a stable version path
  fail the check and block merge.
- Pre-`v1` development records breakages in CHANGELOG entries with
  migration notes.
- Promotion from `v1alphaN` to `v1betaN` to `v1` is itself recorded
  by ADRs that update this one.

## Pros and Cons of the Options

### Option A — SemVer in package metadata

- Good, because familiar to most contributors.
- Bad, because protobuf does not encode SemVer; the canonical schema
  source has no versioning, only the wrappers around it.
- Bad, because old drivers cannot coexist with new ones in the same
  build of the engine.

### Option C — Single-version-with-deprecation

- Good, because the file tree stays small.
- Bad, because real evolution sometimes requires breakage; this
  approach forces hacks (renamed messages, "v2" fields embedded in
  v1 messages) instead.
- Bad, because old fields accumulate forever.

## More Information

- Google API design guide on versioning: <https://cloud.google.com/apis/design/versioning>
- Kubernetes API versioning: <https://kubernetes.io/docs/reference/using-api/#api-versioning>
- Buf breaking rules: <https://buf.build/docs/breaking/rules/>
- Related: [ADR-0001](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003](0003-schema-transport-separation.md).
