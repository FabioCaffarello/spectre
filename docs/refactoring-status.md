# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.5 — Quality & Hardening (in progress; R6.5.1 complete on merge of this PR, 2026-04-29)**
Next PR: **R6.5.2 — CI hardening: image-build matrix completion + bake unification + full-stack gate**

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

- [x] **R6.5.1 — Stale-references sweep + R6.1 leftovers (`build/docker/README.md`, `tools/build/check-versions-coherent.sh`)** *(complete on merge of this PR, 2026-04-29 — opens Phase R6.5)*
- [ ] **R6.5.2 — CI hardening: image-build matrix completion, bake unification in CI, full-stack gate** *(next)*
- [ ] R6.5.3 — Docker Hub registry wiring + multi-arch readiness
- [ ] R6.5.4 — Dockerfile deduplication via shared codegen base image stage

- [ ] **R7.1 — ADR-0026 Helm chart** *(opens Phase R7 once R6.5 closes)*
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.5.1)

The R6.5.1 PR's per-step checklist mirrors Section 7 of the
phase prompt. R6.5.1 sweeps stale `PR<N>` references out of
live code, ships R6.1's two leftovers (`build/docker/README.md`
and `tools/build/check-versions-coherent.sh`), and wires the
coherence script into `just check` and CI's `proto` job.
**Phase R6.5 opens with this PR's merge.**

- [x] Step 1 — Inventory: R6.3 merge confirmed; the inventory
  grep located stale `PR<N>` references in live code (excluding
  `docs/adr/**`, `docs/MASTER_STRATEGY_REFACTOR.md`, and similar
  history-bearing strategy docs). Section 4 decisions settled.
- [x] Step 2 — `build/docker/README.md` authored (~155 lines):
  versions.env contract, two-consumer model (bake + direct
  Dockerfile builds), bump procedure, pin-inventory table
  covering every variable in `versions.env` plus the
  `core/engine` + `core/control-plane` no-default ARG split.
- [x] Step 3 — `tools/build/check-versions-coherent.sh` authored
  (~190 lines, executable, bash 3.2 compatible): sources
  versions.env, verifies docker-bake.hcl variable defaults,
  verifies adapter Dockerfile ARG defaults, sanity-checks the
  bake `labels()` schema. Pass-path returns 0; intentional
  drift returns non-zero with per-mismatch detail. `tools/build/`
  carved out of the unanchored `build/` gitignore rule.
- [x] Step 4 — Wired into `just check` via a top-level
  `check-versions` recipe; `check` chain becomes
  `check-versions lint test`. CI's `proto` job runs the script
  as its first step (after checkout); R6.5.2 may promote it to a
  dedicated job.
- [x] Step 5 — Stale-reference sweep, batch 1 — curl-impersonate
  Go source: nine files (Pattern A); one test rename
  `TestNamesReturnsPR12List` → `TestNamesMatchesCapabilityManifest`.
  Capability invariant 6 unchanged byte-for-byte; tests pass.
- [x] Step 6 — Stale-reference sweep, batch 2 — SeleniumBase
  Python source: eight files (Pattern A + B). Capability
  invariant 12 unchanged byte-for-byte; tests pass.
- [x] Step 7 — Stale-reference sweep, batch 3 — Playwright TS
  source: five files (Pattern A); the capabilities test spec
  name updates `"declares the thirteen PR6 capabilities"` →
  `"declares the thirteen v1alpha1 capabilities"`. Capability
  invariant 13 unchanged byte-for-byte; 88/88 tests pass.
- [x] Step 8 — Stale-reference sweep, batch 4 — engine +
  control-plane: dsl.rs (Pattern B), plan.rs (Pattern A),
  runner.go + engine_client.go (Pattern C), manager.yaml
  (Pattern A on PR# refs + Pattern D R7.1 deferral annotations
  on resource limits / runAsUser / terminationGracePeriodSeconds
  that were sized for the pre-R3.1 bundled adapter Pod).
  cargo test --lib 82/82; go vet clean.
- [x] Step 9 — Stale-reference sweep, batch 5 — conformance
  suite: 15 files (Pattern A across capabilities.py,
  demo_full_cycle.py, README.md, conftest.py, test docstrings).
  Pytest collection holds at 64 tests (test bodies unchanged).
- [x] Step 10 — Stale-reference sweep, batch 6 — build infra:
  justfile, .github/workflows/ci.yml, .devcontainer/Dockerfile.
- [x] Step 11 — Stale-reference sweep, batch 7 — module READMEs:
  core/engine/README.md (one-line) and core/control-plane/README.md
  (status block + runner.go bullet + multi-stage description +
  "What this does not do (yet)" block, kept narrowly scoped to
  PR# removal + the bundled-image staleness that was directly
  reachable from the PR# wording).
- [x] Step 12 — Final stale-ref grep clean (live-code paths
  return zero hits); ADRs / strategy docs retain their refs.
- [x] Step 13 — `docs/refactor-audit.md` ticks R6.5.1 + opens the
  R6.5 phase block; this `docs/refactoring-status.md` advances
  to R6.5; ADR-0020 §4 gains the R6.5 audit row; CHANGELOG
  Unreleased entry recording the sweep + R6.1 leftovers.
- [ ] Step 14 — Final verification (`just check`, `just
  check-versions`, conformance suite, image build).
- [ ] Step 15 — Open the PR.

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
three decisions for R6.5.1 (Pattern A/B/C taxonomy for stale
PR# refs; pin inventory in README, not in source comments;
coherence script wired into `just check` + the existing CI
`proto` job, not a new dedicated job) are settled by Section 4
of the phase prompt.

One scope adjustment surfaced during execution: the inventory
grep's actual count (~125 hits across live code) was higher
than the prompt's estimate of 102. The Playwright adapter — which
the prompt said happened to be clean — actually had five
references; module READMEs (which the prompt's Section 5 didn't
enumerate) carried twenty-three more. Both were swept under the
prompt's Pattern A guidance because criterion 2 (zero hits in
live code) is unconditional.

One pre-existing-issue note carried over from R6.3: the
Playwright runtime image pinned in `build/docker/versions.env`
(`v1.49.0`) is out-of-step with the npm `playwright` dep
(`1.59.1`). R6.5.1 documents the pinning contract but does not
bump pins (Section 10 of the phase prompt). R7.x or a separate
maintenance PR picks up the sync.

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
