# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-28
Current phase: **R4.1 — ADR-0023 stateful services architecture**
Next PR: **R4.1 — ADR-0023 stateful services architecture**

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
- [x] **R3.2 — `ScrapeJob` CRD v1alpha2 (breaking change, no conversion webhook)** *(complete on merge of this PR, 2026-04-28)*
- [ ] **R4.1 — ADR-0023 stateful services architecture** *(next)*
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

## Current PR checklist (R3.2)

The R3.2 PR's per-step checklist mirrors Section 7 of the phase
prompt. Updated each session that lands work on this PR.

- [x] Step 1 — Inventory: confirm R3.1 merged, v1alpha1 types/reconciler reviewed, six decisions in Section 4 cross-checked
- [x] Step 2 — Close R3.1 loose ends in status docs
- [x] Step 3 — Create v1alpha2 API: `api/v1alpha2/{groupversion_info.go,scrapejob_types.go,zz_generated.deepcopy.go}` with EngineRef + OutputSink discriminated unions; `make manifests` + `make generate`
- [x] Step 4 — CEL validation rules emitted in CRD YAML under `x-kubernetes-validations`
- [x] Step 5 — Reconciler updated for v1alpha2: EngineRef resolution, OutputSink enforcement, `ResolvedEngineEndpoint` status, per-reconcile `EngineClientRunner` construction
- [x] Step 6 — Reconciler tests updated: EngineRef resolution forms, OutputSink variants, `ResolvedEngineEndpoint` populated
- [x] Step 7 — `cmd/main.go` passes `DefaultEngineEndpoint` instead of `Runner`; flag preserved as fallback
- [x] Step 8 — Sample manifests rewritten under `_v1alpha2_*.yaml`; `_kafka_NOT_YET_IMPLEMENTED.yaml` documents the schema-ahead-of-functionality gap
- [x] Step 9 — `api/v1alpha1/` deleted; `PROJECT` and `config/crd/kustomization.yaml` updated
- [x] Step 10 — `docs/architecture/control-plane.md` rewritten for v1alpha2 with sink-status table and CEL explanation
- [x] Step 11 — ADR-0019 R3.2 addendum recording v1alpha2 as the only registered version
- [x] Step 12 — `docs/refactor-audit.md` R3.2 ticked; CHANGELOG Unreleased entry; this checklist
- [x] Step 13 — Final verification: `just check` green; `just conf-test` × 3 (44 passed, 13 skipped, byte-for-byte stable); `make build` produces a 73 MB operator binary
- [ ] Step 14 — Open the PR
- [ ] Step 15 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
six decisions for R3.2 (v1alpha2 as the only registered version;
Go discriminated union + CEL validation; EngineRef Service-or-
Endpoint; schema-ahead-of-functionality for non-stdout sinks;
per-reconcile `EngineClientRunner` construction; total v1alpha1
sample deletion) are settled by master strategy + maintainer
prior choices, recorded in the phase prompt's Section 4.

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
