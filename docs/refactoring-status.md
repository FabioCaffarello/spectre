# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-30
Current phase: **R6.6 — Platform Maturation (CLOSES on merge of this PR, 2026-04-30; ADRs 0026–0029 accepted; restructure enacted; fossil sweep + doc refresh complete)**
Next PR: **R7.1 — Helm chart (opens Phase R7)**

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
- [x] **R6.5.3 — Docker Hub registry wiring + multi-arch readiness** *(merged 2026-04-29, PR #70)*
- [x] **R6.5.4 — Dockerfile deduplication via shared codegen base image stage** *(merged 2026-04-29, PR #71 — closed Phase R6.5)*

**Phase R6.6 — Platform Maturation** *(inserted between R6.5
close and R7.1 open; four new ADRs 0026–0029; recorded in
ADR-0020 §5 R6.6 sub-row)*

- [x] **R6.6 — Platform Maturation: ADRs 0026–0029 + repository restructure + fossil sweep + doc refresh** *(complete on merge of this PR, 2026-04-30 — closes Phase R6.6)*

- [ ] **R7.1 — Helm chart packaging** *(next; opens Phase R7)*
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.6)

The R6.6 PR is structured as **eight commit clusters** (A → B →
C → D → E → F → H + final validation G). One PR, large by
design. Each cluster is a focused commit; CI passes at every
commit boundary; the maintainer can pause review at any
cluster. ADRs 0026–0029 are the foundation; the restructure
they prescribe enacts the platform taxonomy ahead of R7.1's
production posture.

- [x] Step 1 — Inventory and confirm: R6.5.4 merge confirmed
  (`1aca726 Merge pull request #71`). Eight Section 4
  decisions settled (single PR; ADR-0026 §4 enacted exhaustively;
  accepted ADRs 0001–0025 immutable; fossil sweep unconditional;
  master strategy + audit + status doc kept through R7.x;
  ADR-0007 amended in place; path-based dependency rule
  enforcement deferred).
- [x] Step 2 — Verify the four ADRs match the in-progress
  branch. ADR-0026 (~750 lines), ADR-0027 (~580 lines),
  ADR-0028 (~620 lines), ADR-0029 (~590 lines) — frontmatter
  correct (status: accepted; date: 2026-04-30; deciders:
  [Fabio Caffarello]); titles match the prompt.
- [x] Step 3 — Cluster A: four ADRs (single commit covering
  all four).
- [x] Step 4 — Cluster B: restructure sub-commits. (B.1)
  rename `core/engine/` → `engines/engine/` via `git mv`;
  (B.2) rename `core/control-plane/` → `operators/control-plane/`;
  (B.3) Go module path flip + import flips across operator
  source; (B.4) infrastructure path flips
  (`docker-bake.hcl`, `docker-compose.yml`, `justfile`,
  `.devcontainer/`, `.github/workflows/`, `.gitignore`,
  `.dockerignore`, `.pre-commit-config.yaml`,
  `build/docker/README.md`, `proto/buf.gen*.yaml`); (B.5)
  `README.md` + `CONTRIBUTING.md` flips; (B.6) four
  placeholder dirs materialised (`infra-services/`, `sdks/`,
  `data-platform/{parse,transform,aggregate}/`,
  `shared-libs/`); (B.7) empty `core/` dir cleanup.
- [x] Step 5 — Cluster C: ADR amendments. (C.1) ADR-0007
  frontmatter `accepted (partially evolved by ADR-0027)` +
  §2 / §3 brief evolution subsections; (C.2) ADR-0013
  frontmatter `superseded by ADR-0019 + ADR-0020` + §1
  "Supersession (R3.1)" subsection; (C.3)
  `docs/adr/README.md` index updates + breadcrumb note about
  pre-R6.6 vs post-R6.6 path citation.
- [x] Step 6 — Cluster D: fossil sweep. `docs/MASTER_PROMPT.md`
  deleted via `git rm`; repo-root `MEMORY.md`,
  `/memory/spectre_pr*.md`, `.claude/scheduled_tasks.lock`
  removed from disk (already gitignored). `.gitignore`
  unchanged (already covered the patterns).
- [x] Step 7 — Cluster E: documentation refresh.
  `docs/architecture/overview.md` rewritten (~400 lines);
  `docs/roadmap.md` rewritten (~270 lines); JSON-RPC stripped
  from `docs/architecture/driver-protocol.md` and
  `docs/guides/writing-a-driver.md`; `CONTRIBUTING.md`
  Driver Path snippet refreshed for gRPC-over-TCP. Subprocess
  language preserved where it describes current behaviour
  (conformance harness; curl-impersonate cgo wrapper;
  R-series past-tense).
- [x] Step 8 — Cluster F: bookkeeping. R6.6 row appended to
  `docs/refactor-audit.md` (under a new "### Phase R6.6 —
  Platform Maturation (CLOSED)" subsection); this status
  doc advanced; CHANGELOG Unreleased extended to cover all
  eight clusters.
- [x] Step 9 — Cluster H: master strategy `§9` post-R6.6
  amendment (~30 lines).
- [ ] Step 10 — Cluster G: final local validation
  (`cargo check`, `cargo test --lib`, `go build ./...`,
  `go test ./...`, `just check`, `just images`,
  `just images-smoke`, `just check-versions`,
  `just conf-test`, eleven-service Compose stack with sample
  ScrapeJob reaching `Completed`).
- [ ] Step 11 — Open the PR.

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
eight decisions for R6.6 (single PR with eight commit clusters;
ADR-0026 §4 enacted exhaustively; accepted ADRs 0001–0025
immutable + breadcrumb in `docs/adr/README.md`; fossil sweep
unconditional; master strategy + audit + status doc kept
through R8.1; ADR-0007 amended in place per ADR-0027 §2's
preservation stance; path-based dependency rule enforcement
deferred per ADR-0026 §9) are settled by Section 4 of the
phase prompt.

One pre-existing-issue note carried over from R6.3 / R6.5: the
Playwright runtime image pinned in `build/docker/versions.env`
(`v1.49.0`) is out-of-step with the npm `playwright` dep
(`1.59.1`). R7.x or a separate maintenance PR picks up the
sync; R6.6 doesn't bump pins (path-only refactor).

Two reservations clarified by R6.6 ahead of R7.1:

- **ADR-0026 §1 reassignment.** ADR-0025 §10's implicit
  reservation of "ADR-0026 for Helm" was never authoritative;
  ADR-0026 §1 explicitly notes that the reservation was
  implicit and now goes to the Platform taxonomy. R7.1's
  Helm chart will pick up the next available ADR number when
  it lands.
- **`build/helm/spectre/` as the chart's home.** ADR-0026
  §3.9 doesn't reserve a `helm/` category because Helm
  artifacts are out-of-band; R7.1 makes the final location
  call against the Compose-equivalence criterion.

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
