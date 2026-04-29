# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.5 — Quality & Hardening (in progress; R6.5.3 complete on merge of this PR, 2026-04-29)**
Next PR: **R6.5.4 — Dockerfile deduplication via shared codegen base image stage**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight master-plan phases, plus a four-PR **R6.5 Quality &
Hardening** sub-phase inserted between R6.3 (Phase R6 close) and
R7.1 (Helm chart) to address drift accumulated across the long
refactor. Order is fixed; phases cannot be reordered or skipped.
See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas and §4 for the R6.5 insertion's
audit-trail row.

- [x] **R1.1 — ADR-0020 supersession** *(merged 2026-04-27, PRs #26 + #27)*
- [x] **R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details** *(merged 2026-04-27, PR #28)*
- [x] **R2.2 — Adapter transport switch (UDS → TCP, all three adapters)** *(merged 2026-04-27, PR #29)*
- [x] **R2.3 — Engine transport + gRPC server (UDS client → TCP client)** *(merged 2026-04-27, PR #30)*
- [x] **R3.1 — `EngineClientRunner` replaces `SubprocessRunner`** *(merged 2026-04-27)*
- [x] **R3.2 — `ScrapeJob` CRD v1alpha2 (breaking change, no conversion webhook)** *(merged 2026-04-28)*
- [x] **R4.1 — ADR-0023 stateful services architecture** *(merged 2026-04-28)*
- [x] **R4.2 — PostgreSQL integration end-to-end** *(merged 2026-04-28, PR #61)*
- [x] **R4.3 — Redis for adapter session cache** *(merged 2026-04-28)*
- [x] **R4.4 — Kafka producer (engine → topic)** *(merged 2026-04-28, PR #63 — closes Phase R4)*
- [x] **R5.1 — ADR-0024 output sinks (S3 + webhook)** *(merged 2026-04-28, PR #64 — closes Phase R5)*
- [x] **R6.1 — Per-service Dockerfiles (engine, control plane, three adapters) + `docker-bake.hcl` orchestration** *(merged 2026-04-29, PR #65 — opened Phase R6)*
- [x] **R6.2 — ADR-0025 Compose stack (application services + profile topology + 8090–8093 port migration)** *(merged 2026-04-29, PR #66)*
- [x] **R6.3 — Devcontainer with Docker-in-Docker (operator + kind into the unified Compose stack)** *(merged 2026-04-29 — closed Phase R6)*

**Phase R6.5 — Quality & Hardening** *(inserted between R6.3
close and R7.1 open; no new ADRs; recorded in ADR-0020 §4)*

- [x] **R6.5.1 — Stale-references sweep + R6.1 leftovers (`build/docker/README.md`, `tools/build/check-versions-coherent.sh`)** *(merged 2026-04-29 — opened Phase R6.5)*
- [x] **R6.5.2 — CI hardening: image-build matrix completion, bake unification in CI, full-stack gate** *(merged 2026-04-29, PR #69)*
- [x] **R6.5.3 — Docker Hub registry wiring + multi-arch readiness** *(complete on merge of this PR, 2026-04-29)*
- [ ] **R6.5.4 — Dockerfile deduplication via shared codegen base image stage** *(next)*

- [ ] **R7.1 — ADR-0026 Helm chart** *(opens Phase R7 once R6.5 closes)*
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.5.3)

The R6.5.3 PR's per-step checklist mirrors Section 7 of the
phase prompt. R6.5.3 makes the publish flow operational:
Docker Hub `fabiocaffarello/spectre-<name>` flat namespace,
multi-arch (linux/amd64 + linux/arm64) for control-plane +
playwright, three deferrals (engine, seleniumbase,
curl-impersonate) with explicit unblock criteria.

- [x] Step 1 — Inventory: R6.5.2 merge confirmed
  (`4f19951 Merge pull request #69`). docker-bake.hcl,
  ADR-0018 §5, the five Dockerfiles, ci.yml `changes` job,
  container-images.md, and the per-Dockerfile arch
  case-statements reviewed. The six §4 decisions settled.
- [x] Step 2 — Forward-readiness for the deferred three
  (engine, seleniumbase, curl-impersonate): TARGETPLATFORM /
  TARGETARCH ARGs declared at the top of each Dockerfile;
  R7.x deferral comment blocks above each blocker referencing
  ADR-0018 §5 R6.5.3 update; buf/protoc-arch case-statements
  audit confirms aarch64 already covered (no extension
  needed).
- [x] Step 3 — Multi-arch enablement for control-plane +
  playwright: explicit `# Multi-arch ready (R6.5.3): linux/
  amd64 + linux/arm64` comment markers near the top of each
  Dockerfile; both already had arm64-aware buf-arch case
  statements + TARGETARCH plumbing from R6.1.
- [x] Step 4 — `docker-bake.hcl` comment block updates: the
  R7.1/ghcr.io narrative replaced with R6.5.3/Docker Hub
  reality; the REGISTRY variable annotation rewritten; the
  `image()` function logic unchanged (registry-agnostic).
  `git grep -n 'ghcr\.io' docker-bake.hcl` is empty.
- [x] Step 5 — `.github/workflows/publish.yml` shipped:
  `workflow_dispatch` only; three inputs (`tag`, `targets`,
  `multi_arch`); QEMU + buildx + just setup; tag resolution
  from VERSION file; Docker Hub login via `DOCKERHUB_TOKEN`
  secret; `docker buildx bake --push` with per-target
  platform overrides for the two ready images; final
  `imagetools inspect` step. `actionlint` clean.
- [x] Step 6 — `publish-dry-run` job in `ci.yml`: `changes`
  job grows a `publish_dry_run` filter; new job builds
  multi-arch (control-plane + playwright) without `--push`
  or `--load`; `ci-summary`'s `needs:` and report block
  extended.
- [x] Step 7 — ADR-0018 §5 amended in place: status note at
  the heading; new "R6.5.3 update — Docker Hub registry +
  multi-arch reality" subsection (~120 lines) covering the
  pivot rationale, the 5-image multi-arch table, the per-
  image unblock criteria, the per-target-platform-set-at-
  publish-time decision, the `workflow_dispatch`-only
  posture, the deliverables list, and the maintainer
  DOCKERHUB_TOKEN prerequisite. `container-images.md`
  updated: header forward-references; REGISTRY variable
  description; new "Multi-arch status" subsection.
- [x] Step 8 — `docs/architecture/releases.md` shipped
  (~250 lines): operator-facing runbook (Overview · Image
  registry · Multi-arch status · Publish flow · What's
  deferred · Operator runbook with three numbered procedures
  · CI dry-run · Forward references). Cross-referenced from
  README, ADR-0018 §5 R6.5.3 update, and container-images.md.
- [x] Step 9 — `refactor-audit.md` ticks R6.5.3;
  `refactoring-status.md` advances to R6.5.4-next; CHANGELOG
  Unreleased entry; README quick-start one-liner.
- [ ] Step 10 — Final verification (`just check`,
  conformance suite, multi-arch dry-run locally,
  workflow lint).
- [ ] Step 11 — Open the PR.

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
five decisions for R6.5.2 (single matrix job over five separate
jobs; bake invocation matches local byte-for-byte; per-target
`changes` outputs preserve selectivity; full-stack gate
verifies Level 3 / sample ScrapeJob `Completed`; gate placement
`needs: [changes, images]`) are settled by Section 4 of the
phase prompt.

One scope adjustment surfaced during execution: the prompt's
Cluster B sketch hardcoded `images-smoke` recipe names that
mostly differed from the project's actual conventions
(`op-image-smoke`, `curl-imp-image-smoke`, `pw-image-smoke`,
`sb-image-smoke`, `engine-image-run`). The matrix's
`include:` form carries `smoke_recipe:` per entry to absorb
that asymmetry; the prompt's Decision 4.1 anticipated the
asymmetry and chose `include:` for exactly this reason.

One pre-existing-issue note carried over from R6.3: the
Playwright runtime image pinned in `build/docker/versions.env`
(`v1.49.0`) is out-of-step with the npm `playwright` dep
(`1.59.1`). R7.x or a separate maintenance PR picks up the
sync; R6.5.2 doesn't bump pins.

## Known issues

The CRD evolution to v1alpha2 is a breaking change. Per master
strategy §3.3, no conversion webhook is implemented; v1alpha1
ScrapeJob CRs in clusters are orphaned on upgrade. The upgrade
procedure (documented in CHANGELOG and `control-plane.md`) is
`kubectl delete scrapejob --all` → install v1alpha2 CRD → apply
v1alpha2 CRs.

OutputSink schema is fully implemented post-R5.1: every variant
(Stdout, Kafka, S3, Webhook) has runtime support. The
"schema-only" entries in earlier known-issues are retired.

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
