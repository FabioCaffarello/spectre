# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-27
Current phase: **R2.1 — ADR-0021 (service discovery) + ADR-0022 (TCP transport) (in progress)**
Next PR: **R2.2 — Adapter transport switch (UDS → TCP, all three adapters)**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight phases. Order is fixed; phases cannot be reordered or
skipped. See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas.

- [x] **R1.1 — ADR-0020 supersession** *(merged 2026-04-27, PRs #26 + #27)*
- [ ] **R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details** *(in progress)*
- [ ] R2.2 — Adapter transport switch (UDS → TCP, all three adapters)
- [ ] R2.3 — Engine transport + gRPC server (UDS client → TCP client)
- [ ] R3.1 — `EngineClientRunner` replaces `SubprocessRunner`
- [ ] R3.2 — `ScrapeJob` CRD v1alpha2 (breaking change, no conversion webhook)
- [ ] R4.1 — ADR-0023 stateful services architecture
- [ ] R4.2 — PostgreSQL for control-plane job state
- [ ] R4.3 — Redis for adapter session cache
- [ ] R4.4 — Kafka producer (engine → topic)
- [ ] R5.1 — ADR-0024 output sinks (S3 + webhook + Kafka)
- [ ] R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)
- [ ] R6.2 — ADR-0025 Compose stack (six services + three stateful deps)
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R2.1)

The R2.1 PR's per-step checklist mirrors Section 7 of the phase
prompt. Updated each session that lands work on this PR.

- [x] Step 1 — Inventory and confirm working tree
- [x] Step 2 — Scan current code for UDS usage (grep-based inventory)
- [x] Step 3 — Draft ADR-0021 §1–§3 (context, drivers, discovery model)
- [x] Step 4 — Draft ADR-0021 §4–§5 (port allocation, env vars)
- [x] Step 5 — Draft ADR-0021 §6–§8 (healthcheck, exclusions, alternatives)
- [x] Step 6 — Draft ADR-0022 §1–§4 (context, drivers, contract, lifecycle)
- [x] Step 7 — Draft ADR-0022 §5 (removal targets inventory)
- [x] Step 8 — Draft ADR-0022 §6–§8 (security, exclusions, migration)
- [x] Step 9 — Append update note to ADR-0008 (R2.1 supersession of §2 / §4)
- [x] Step 10 — Generate `docs/refactor-audit.md` (tabular inventory)
- [x] Step 11 — Update `docs/adr/README.md` index, status tracker, roadmap
- [ ] Step 12 — Final verification (`just check`, diff scope)
- [ ] Step 13 — Open the PR
- [ ] Step 14 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
five locked decisions for R2.1 (port allocation, env-var
discovery, eager-fail dial, gRPC standard healthcheck, mTLS
deferred to v1alpha2) were settled before the phase began and
are documented in
[ADR-0021](adr/0021-service-discovery.md) and
[ADR-0022](adr/0022-tcp-grpc-transport.md).

## Known issues

None. R2.1 is documentation-only; no code paths are quarantined,
no tests skipped, no regressions introduced. The pre-existing
`curl-imp-lint` Go-toolchain mismatch noted under R1.1 is still
present (unrelated to the refactor) and is tracked separately.

## How to read this document

- **At session start:** identify the current phase, the in-progress
  PR, and the next un-completed step in the PR checklist. Resume
  from there.
- **At session end (if work landed):** update the checklist
  checkboxes, the "last updated" date, and — if the PR closed —
  flip the phase entry to `[x]` and shift the "current phase" /
  "next PR" pointers.
- **At phase boundary:** confirm the phase-level invariants from
  [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
  hold (conformance suite green, capability lists byte-identical,
  no legacy paths surviving alongside replacements, ADR index
  accurate).
