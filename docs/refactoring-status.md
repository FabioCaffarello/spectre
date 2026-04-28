# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-28
Current phase: **R4.1 — ADR-0023 stateful services architecture (complete on merge of this PR, 2026-04-28)**
Next PR: **R4.2 — PostgreSQL for control-plane job state**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight phases. Order is fixed; phases cannot be reordered or
skipped. See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas.

- [x] **R1.1 — ADR-0020 supersession** *(merged 2026-04-27, PRs #26 + #27)*
- [x] **R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details** *(merged 2026-04-27, PR #28)*
- [x] **R2.2 — Adapter transport switch (UDS → TCP, all three adapters)** *(merged 2026-04-27, PR #29)*
- [x] **R2.3 — Engine transport + gRPC server (UDS client → TCP client)** *(merged 2026-04-27, PR #30)*
- [x] **R3.1 — `EngineClientRunner` replaces `SubprocessRunner`** *(merged 2026-04-27)*
- [x] **R3.2 — `ScrapeJob` CRD v1alpha2 (breaking change, no conversion webhook)** *(merged 2026-04-28)*
- [x] **R4.1 — ADR-0023 stateful services architecture** *(complete on merge of this PR, 2026-04-28)*
- [ ] **R4.2 — PostgreSQL for control-plane job state** *(next)*
- [ ] R4.3 — Redis for adapter session cache
- [ ] R4.4 — Kafka producer (engine → topic)
- [ ] R5.1 — ADR-0024 output sinks (S3 + webhook + Kafka)
- [ ] R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)
- [ ] R6.2 — ADR-0025 Compose stack (six services + three stateful deps)
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R4.1)

The R4.1 PR's per-step checklist mirrors Section 7 of the phase
prompt. R4.1 is documentation-only — the deliverable is
ADR-0023 (stateful services architecture) plus index updates.
Updated each session that lands work on this PR.

- [x] Step 1 — Inventory: confirm R3.2 merged, v1alpha2 schema reviewed, thirteen decisions in Section 4 cross-checked
- [x] Step 2 — Fix R4.1 status preamble
- [x] Step 3 — Draft ADR-0023 §1 (Context and problem)
- [x] Step 4 — Draft §2 (PostgreSQL)
- [x] Step 5 — Draft §3 (Kafka)
- [x] Step 6 — Draft §4 (Redis)
- [x] Step 7 — Draft §5 (Session externalization — most consequential paragraph)
- [x] Step 8 — Draft §6 (Required vs optional)
- [x] Step 9 — Draft §7 (Network topology)
- [x] Step 10 — Draft §8 (Library choices and pinning)
- [x] Step 11 — Draft §9 (Compose stack composition)
- [x] Step 12 — Draft §10 (Production deployment)
- [x] Step 13 — Draft §11 (Migration order across phases)
- [x] Step 14 — Draft §12 (Configuration via env vars)
- [x] Step 15 — Draft §13 (Migrations and schema evolution)
- [x] Step 16 — Update ADR index, refactor-audit, status, CHANGELOG; flip R4.1 → complete and R4.2 → next
- [x] Step 17 — Final verification: only `.md` files in `git diff main...HEAD --stat`; `just check` green (44 conformance pass / 13 skip; 13/12/6 invariant intact); ADR length 647 lines (target 500-600 — substantive, §5 narrative preserved)
- [ ] Step 18 — Open the PR
- [ ] Step 19 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
thirteen decisions for R4.1 (three stateful services scoped
together; Postgres / Kafka / Redis specifically; restart-
invalidation contract for sessions; required-vs-optional
deployment shape; library commitments per language; topic-per-
workspace partitioned by job; normalised Postgres schema;
per-service env-var configuration; migrations at engine
startup) are settled by master strategy + maintainer prior
choices, recorded in the phase prompt's Section 4.

## Known issues

The CRD evolution to v1alpha2 is a breaking change. Per master
strategy §3.3, no conversion webhook is implemented; v1alpha1
ScrapeJob CRs in clusters are orphaned on upgrade. The upgrade
procedure (documented in CHANGELOG and `control-plane.md`) is
`kubectl delete scrapejob --all` → install v1alpha2 CRD → apply
v1alpha2 CRs.

OutputSink schema is one step ahead of functionality: the
v1alpha2 schema includes `Kafka`, `S3`, and `Webhook` fields, but
the reconciler rejects them at admission with explicit "not yet
implemented" errors. R4.4 wires Kafka; R5.1 wires S3 and
Webhook. The schema is committed now to keep the CRD stable
through Phase 3.

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
