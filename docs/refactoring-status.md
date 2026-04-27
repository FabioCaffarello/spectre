# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-27
Current phase: **R1.1 — ADR-0020 supersession (in progress)**
Next PR: **R2.1 — ADR-0021 (service discovery) + ADR-0022 (TCP transport details)**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight phases. Order is fixed; phases cannot be reordered or
skipped. See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas.

- [ ] **R1.1 — ADR-0020 supersession** *(in progress)*
- [ ] R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details
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

## Current PR checklist (R1.1)

The R1.1 PR's per-step checklist mirrors Section 7 of the phase
prompt. Updated each session that lands work on this PR.

- [x] Step 1 — Inventory and confirm working tree
- [x] Step 2 — Draft ADR-0020 §1 (Context and problem statement)
- [x] Step 3 — Draft ADR-0020 §2 (Decision drivers)
- [x] Step 4 — Draft ADR-0020 §3 (Considered options)
- [x] Step 5 — Draft ADR-0020 §4 (Decision outcome)
- [x] Step 6 — Draft ADR-0020 §5 (Implementation phases)
- [x] Step 7 — Draft ADR-0020 §6 (ADR status changes table)
- [x] Step 8 — Status notes on superseded ADRs (0008, 0009, 0013, 0019)
- [x] Step 9 — Update `docs/adr/README.md` index
- [x] Step 10 — Create `docs/refactoring-status.md` *(this file)*
- [x] Step 11 — Update top-level docs (`roadmap.md`, `README.md`, `CHANGELOG.md`)
- [x] Step 12 — Final verification (diff scope confirmed: 10 `.md` files only; `just check` blocked by pre-existing `curl-imp-lint` Go-toolchain mismatch unrelated to R1.1)
- [ ] Step 13 — Open the PR
- [ ] Step 14 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. All
four locked decisions for the refactor (microservices over
subprocess-in-pod, TCP over UDS, stateful services included,
Compose-only development) were settled before R1.1 began and are
recorded in
[ADR-0020 §2](adr/0020-microservices-architecture-supersession.md).

## Known issues

None. R1.1 is documentation-only; no code paths are quarantined,
no tests skipped, no regressions introduced.

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
