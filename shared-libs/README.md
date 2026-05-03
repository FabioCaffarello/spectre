# `shared-libs/`

Cross-cutting utilities used across modules within a single language.
Per-language sub-directories; cross-language sharing is impossible by
construction (different runtimes).

This directory is intentionally empty at v1alpha1 (Phase R6.6
reservation; refactor closed at R8.1). The category is
defined by **[ADR-0026 §3.8](../docs/adr/0026-platform-taxonomy.md)**.

Target layout:

```
shared-libs/
├── rust/<lib-name>/
├── go/<lib-name>/
├── python/<lib-name>/
└── typescript/<lib-name>/
```

Admission is lightweight (per ADR-0026 §6.2): no ADR required for new
libs. The gate is (a) at least two existing consumers, (b) genuinely
cross-cutting purpose (not "engine helpers"), (c) a stable public
surface or workspace-internal scope. A library-level changelog entry
suffices.

Dependencies: `proto/` (allowed), other `shared-libs/<same-lang>/`
(allowed if no cycle). May not depend on consumer categories
(`engines/`, `operators/`, `adapters/`, `infra-services/`,
`data-platform/`).
