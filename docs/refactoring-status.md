# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-05-02
Current phase: **R7.2 — Production smoke (CLOSES on merge of this PR, 2026-05-02; production-smoke CI gate ships; Phase R7 CLOSED — no new ADR; ADR-0030 §9 already deferred R7.2's territory)**
Next PR: **R8.1 — Documentation refresh + narrative closing (refactor's final PR)**

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

- [x] **R7.1 — Helm chart packaging** *(merged 2026-05-02, PR #82 — opened Phase R7; ADR-0030; first real Docker Hub publish)*
- [x] **R7.2 — Production smoke (Helm-installed cluster)** *(complete on merge of this PR, 2026-05-02 — closes Phase R7; production-smoke CI gate; no new ADR)*
- [ ] R8.1 — Documentation refresh + narrative closing *(next — refactor's final PR)*

## Current PR checklist (R7.1)

The R7.1 PR is structured as **eight commit clusters** (A → B →
C → D → E → F → G → H). One PR per ADR-0020 §5; each cluster is
focused enough to review independently; CI passes at every
commit boundary. ADR-0030 is the structural commitment;
build/helm/spectre/ is the artifact; the helm-lint CI gate
guards future drift.

**R6.6 PR checklist (historical, completed 2026-04-30):**

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
- [x] Step 11 — Open the PR.

**R7.1 PR checklist (current, in flight 2026-05-02):**

- [x] Step 1 — Inventory and confirm. R6.6 merge confirmed
  (`ea278ff Merge pull request #72`); helm 3.19 present;
  `gh workflow run publish.yml` dispatched as part of R7.1
  development with `tag=0.1.0-alpha.0 multi_arch=true`. Two
  pre-condition gaps surfaced and were addressed: (a)
  `publish.yml` missing `environment: ci` declaration so
  `secrets.DOCKERHUB_TOKEN` resolved empty (fixed in this
  PR's first commit); (b) Dependabot's controller-runtime
  0.24.0 bump (#75) merged immediately before with go.mod →
  Go 1.26 but no toolchain bump and an incomplete go.sum,
  red-lining `main` — addressed by the separate hotfix
  PR #81 (`fix(versions): bump GO_VERSION 1.25 → 1.26 for
  operator track` + `go mod tidy` reconciliation). R7.1's
  feat branch was rebased onto green main once #81 merged.
- [x] Step 2 — Cluster A: ADR-0030 (~600 lines, 10
  sections); docs/adr/README.md index updated.
- [x] Step 3 — Cluster B: chart skeleton (Chart.yaml,
  Chart.lock, .helmignore, .gitignore) + Bitnami subchart
  pinning (postgresql 16.0.0, redis 19.6.0, kafka 30.0.0,
  minio 14.7.0); root .gitignore extended to carve out
  /build/helm/.
- [x] Step 4 — Cluster C: values.yaml (~325 lines, fully
  commented) + values.schema.json (JSON Schema draft-07,
  ~225 lines).
- [x] Step 5 — Cluster D: seven templates — _helpers.tpl
  (named templates), engine.yaml, control-plane.yaml,
  rbac.yaml, three adapter templates, NOTES.txt.
- [x] Step 6 — Cluster E: chart's crds/scrapejob.yaml is a
  byte-for-byte copy of the operator's source CRD.
- [x] Step 7 — Cluster F: build/helm/spectre/README.md
  (~150 lines), docs/architecture/helm-chart.md (~250
  lines), top-level README "Deploying to Kubernetes"
  section, docs/architecture/releases.md "R7.1 Helm chart
  as a release artifact" section.
- [x] Step 8 — Cluster G: CI helm-lint job + 5 justfile
  recipes (chart-sync-crds, chart-check-crd-sync,
  chart-deps, chart-lint, chart-install-smoke); `just check`
  extends to gate chart-CRD drift; deprecated R6.2 aliases
  (`op-install-crds`, `op-uninstall-crds`) removed per the
  one-cycle deprecation commitment.
- [x] Step 9 — Cluster H: this section + audit row +
  ADR-0020 §5/§6 + roadmap §2 + CHANGELOG.
- [ ] Step 10 — Final verification (kind smoke).
- [ ] Step 11 — Open the PR.

**R7.2 PR checklist (current, in flight 2026-05-02):**

- [x] Step 1 — Inventory and confirm. R7.1 merge confirmed
  (`f8923a1 feat(deploy): Helm chart for v1alpha1 stack`).
  Eight Section 4 decisions settled (three triggers in one
  workflow file; build images on the runner; in-cluster
  MinIO + Kafka; `mendhak/http-https-echo:31` for mock
  receiver; three sinks playwright-driver only; no new ADR;
  workflow standalone; ADR-0020 §5 third post-R6.6 audit-
  table edit). Receiver image digest resolved
  (`sha256:0fefe04350131d7bb28355e3bf037062643e45f4a8a32f23679529e1b09d8ce4`);
  Bitnami pod naming verified via `helm template`
  (`spectre-kafka-controller-0`, `spectre-minio` Deployment,
  `spectre-postgresql-0`, `spectre-redis-master-0`).
- [x] Step 2 — Cluster A: mock webhook receiver
  (`build/helm/test/mock-webhook-receiver.yaml`,
  digest-pinned).
- [x] Step 3 — Cluster B: CI values overrides
  (`build/helm/test/values-ci.yaml` — pullPolicy=Never,
  ephemeral storage, redis standalone,
  `defaultBuckets: spectre-rows`).
- [x] Step 4 — Cluster C: CI samples + sync invariant
  (`build/helm/test/samples/{kafka,s3,webhook}.yaml`,
  `tools/test/sync-ci-samples.sh`,
  `tools/test/check-ci-samples-sync.sh`). s3 endpoint
  flipped to `spectre-minio.spectre-system...`; webhook URL
  flipped to in-cluster mock receiver; kafka byte-identical.
- [x] Step 5 — Cluster D: three sink verifiers
  (`tools/test/verify-{kafka,s3,webhook}-sink.sh`); all
  shellcheck-clean; idempotent with bounded internal
  timeouts.
- [x] Step 6 — Cluster E:
  `.github/workflows/production-smoke.yml` (three
  triggers: workflow_dispatch + path-filtered pull_request +
  schedule daily 06:00 UTC); actionlint-clean.
- [x] Step 7 — Cluster F: five justfile recipes
  (`chart-smoke-sync-samples`, `chart-smoke-check-samples`,
  `chart-smoke-up`, `chart-smoke-test`, `chart-smoke-down`);
  `just check` extends to gate the CI sample drift
  invariant.
- [x] Step 8 — Cluster G: `docs/architecture/production-smoke.md`
  (~316 lines); `helm-chart.md §9` split into §9.1/§9.2;
  chart `README.md` Verification section added.
- [x] Step 9 — Local lightweight verification: chart-CRD
  sync invariant green; CI sample sync invariant green;
  `helm lint --strict` green; `helm template` with CI
  values renders 2217 lines (6 Deployments, 3 StatefulSets);
  shellcheck on five test scripts green; actionlint green.
  Heavy `chart-smoke-up`/`-test` flow defers to CI's amd64
  runners (Apple Silicon `kind` nodes are arm64 and the
  chart's `nodeSelector: kubernetes.io/arch: amd64` for
  three services would leave them Pending — see ADR-0030
  §6.4).
- [x] Step 10 — Cluster H: this section + audit row +
  ADR-0020 §5/§6 + roadmap §2 + CHANGELOG.
- [ ] Step 11 — Open the PR.

## Surfaced decisions

No open architectural questions awaiting maintainer input.
ADR-0030 §2's eight decisions are settled.

One pre-existing-issue note carried over from R6.3 / R6.5: the
Playwright runtime image pinned in `build/docker/versions.env`
(`v1.49.0`) was out-of-step with the npm `playwright` dep
(`1.59.1`); resolved by `4a435ac` between R6.6 and R7.1. The
post-R7.1 follow-up backlog now includes:

- `.devcontainer/Dockerfile`'s `ARG GO_VERSION=1.25.3` —
  unchanged when R7.1 bumped versions.env; rebuild only
  picks it up; deferred to a small hygiene PR.
- `operators/control-plane/Makefile`'s
  `GOLANGCI_LINT_VERSION ?= v2.8.0` — works for the
  current operator source under Go 1.26 but should bump
  alongside any future code that exercises 1.26-only
  syntax.

Resolved by R7.1 itself:

- **The Helm chart's ADR number.** R6.6 deferred this to
  R7.1; ADR-0030 was assigned (the next free number after
  ADR-0029).
- **`build/helm/spectre/` as the chart's home.** ADR-0030
  §3.1 makes the location call (no new top-level category
  needed; out-of-band per ADR-0026 §3.9). The directory
  name is `spectre/` (not `chart/`) so future per-infra-
  service chart fragments per ADR-0028 §6 land as siblings
  under `build/helm/`.

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
