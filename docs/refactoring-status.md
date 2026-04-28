# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-28
Current phase: **R4.2 — PostgreSQL integration end-to-end (complete on merge of this PR, 2026-04-28)**
Next PR: **R4.3 — Redis for adapter session cache**

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
- [x] **R4.1 — ADR-0023 stateful services architecture** *(merged 2026-04-28)*
- [x] **R4.2 — PostgreSQL integration end-to-end** *(complete on merge of this PR, 2026-04-28)*
- [ ] **R4.3 — Redis for adapter session cache** *(next)*
- [ ] R4.4 — Kafka producer (engine → topic)
- [ ] R5.1 — ADR-0024 output sinks (S3 + webhook + Kafka)
- [ ] R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)
- [ ] R6.2 — ADR-0025 Compose stack (six services + three stateful deps)
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R4.2)

The R4.2 PR's per-step checklist mirrors Section 7 of the phase
prompt. R4.2 is the first implementation PR of Phase R4: engine
writes job state to Postgres, control plane reads for restart
recovery, and a Compose stack at the repo root brings up
postgres:16-alpine for local dev. Updated each session that
lands work on this PR.

- [x] Step 0 — Skipped: repo's go 1.25.3 pin in core/control-plane/go.mod is deliberate (commit 9168b32, controller-runtime v0.23.3 requires it). pgx/v5 v5.6.0+ supports Go 1.21+ so the toolchain bump is unnecessary for R4.2's stated goals.
- [x] Step 1 — Inventory: R4.1 merge confirmed; ADR-0023 §2 / §13 read; engine + control-plane + proto current state mapped
- [x] Step 2 — Engine migration file (`<timestamp>_initial_schema.sql` per ADR-0023 §13; both tables + indexes from §2)
- [x] Step 3 — Engine `db` module: sqlx 0.8 with explicit features, Database wrapper, run_migrations, four typed query functions; .sqlx/ offline cache committed
- [x] Step 4 — Engine startup wires Postgres dial + migrations before the gRPC service registers
- [x] Step 5 — Engine RunJob persists state on every transition (insert_job → record_job_row per stdout row → mark_completed/mark_failed); 5 #[ignore] integration tests; new just engine-db-test recipe
- [x] Step 6 — engine.proto RunJobRequest gains output_sink_kind (field 3, non-breaking)
- [x] Step 7 + 8 — Control-plane pgx/v5 dependency + db package (Pool interface, Database wrapper, GetJob / CountJobRows, pgxmock unit tests)
- [x] Step 9 — JobRunner evolves to accept jobID + outputSinkKind; StubRunner + EngineClientRunner + reconciler + tests update in lockstep
- [x] Step 10 — Reconciler reads Postgres on Running phase entry for restart recovery; main.go wires db.FromEnv at startup; four pgxmock-driven envtest cases cover the recovery branches
- [x] Step 11 — docker-compose.yml at repo root (postgres:16-alpine, healthchecked); .env.example; .gitignore (compose.override.yml); justfile compose-{up,down,logs,reset} recipes
- [x] Step 12 — README quick-start replaced (the prior `just spectre-run` flow was retired in R2.3; new flow uses Compose + multi-process gRPC stack)
- [x] Step 13 — docs/architecture/postgres.md: schema, migration discipline, connection lifecycle, unavailability semantics, local dev, tests
- [x] Step 14 — ADR-0019 R4.2 addendum: §5 JobRunner evolution + §4 Running-phase recovery; ADR index updated
- [x] Step 15 — This entry; CHANGELOG; refactor-audit R4.2 row
- [ ] Step 16 — Final verification (just check + Compose smoke + conformance ×3)
- [ ] Step 17 — Open the PR
- [ ] Step 18 — Summary report

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
