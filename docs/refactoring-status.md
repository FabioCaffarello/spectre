# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.5 — Quality & Hardening (in progress; R6.5.2 complete on merge of this PR, 2026-04-29)**
Next PR: **R6.5.3 — Docker Hub registry wiring + multi-arch readiness**

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
- [x] **R6.5.2 — CI hardening: image-build matrix completion, bake unification in CI, full-stack gate** *(complete on merge of this PR, 2026-04-29)*
- [ ] **R6.5.3 — Docker Hub registry wiring + multi-arch readiness** *(next)*
- [ ] R6.5.4 — Dockerfile deduplication via shared codegen base image stage

- [ ] **R7.1 — ADR-0026 Helm chart** *(opens Phase R7 once R6.5 closes)*
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.5.2)

The R6.5.2 PR's per-step checklist mirrors Section 7 of the
phase prompt. R6.5.2 routes every CI image build through the
canonical `docker buildx bake` orchestrator, replaces the two
ad-hoc `engine-image` / `operator-image` jobs with a single
matrix `images` job, and adds a new `full-stack` end-to-end
gate that exercises the unified Compose flow against a sample
ScrapeJob.

- [x] Step 1 — Inventory: R6.5.1 merge confirmed; the existing
  CI workflow, `docker-bake.hcl`, `docker-compose.yml`,
  `build/kind/cluster.yaml`, and the five smoke recipe names
  reviewed. Section 4 decisions settled.
- [x] Step 2 — Smoke recipe parameterization: each of the five
  smoke / run recipes (`engine-image-run`, `op-image-smoke`,
  `curl-imp-image-smoke`, `pw-image-smoke`, `sb-image-smoke`)
  gains a positional `TAG='dev'` argument; recipe bodies use
  `spectre-<name>:{{TAG}}` instead of hardcoded `:dev`. Local
  default unchanged.
- [x] Step 3 — `changes` job filters extended: legacy
  `engine_image` output removed; six new outputs added
  (`image_engine`, `image_control_plane`,
  `image_curl_impersonate`, `image_playwright`,
  `image_seleniumbase`, `full_stack`). Each `image_<name>`
  filter triggers on its source, the proto schema, the
  per-Dockerfile `.dockerignore`, `docker-bake.hcl`,
  `build/docker/**`, or the workflow.
- [x] Step 4 — Matrix `images` job added: five matrix entries
  (one per bake target), each carrying `target` + `changed` +
  `smoke_recipe` via `include:` form; `if: matrix.changed ==
  'true'` skips unchanged targets within a dispatched matrix;
  build step calls `set -a; source build/docker/versions.env;
  set +a; docker buildx bake --load <target>` (canonical
  invocation byte-for-byte aligned with `just images`); CI sets
  `VCS_REF`, `BUILD_DATE`, `TAG=ci`. Legacy jobs preserved in
  this commit.
- [x] Step 5 — `full-stack` job added: bake builds at `TAG=dev`,
  Compose `--profile full` up, kind via `helm/kind-action@v1`
  (`cluster_name: spectre-ci`), kubeconfig regen with
  `--internal`, `make install` for v1alpha2 CRD, hello-hackernews
  sample apply, 5-minute polling loop on `status.phase ==
  Completed`, always-run operator + engine + adapter log tails,
  unconditional `compose down -v` cleanup.
- [x] Step 6 — Legacy `engine-image` and `operator-image` jobs
  removed; `ci-summary` `needs:` and report block updated to
  drop the legacy names and add `images` + `full-stack`.
- [x] Step 7 — `docs/architecture/container-images.md` gains a
  "CI shape" subsection (matrix table, full-stack gate steps,
  filter map, when-each-job-runs table). `build/docker/README.md`
  pin inventory gains a footer noting CI consumes the same pins
  via the same bake invocation.
- [x] Step 8 — `docs/refactor-audit.md` ticks R6.5.2;
  `docs/refactoring-status.md` advances to R6.5.3; CHANGELOG
  Unreleased entry recording the CI restructuring.
- [ ] Step 9 — Final verification (`just check`, smoke
  parameterization tests, conformance suite).
- [ ] Step 10 — Open the PR.

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
