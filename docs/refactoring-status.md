# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.3 — Devcontainer with Docker-in-Docker (operator + kind into the unified Compose stack) (complete on merge of this PR, 2026-04-29)** — **Phase R6 CLOSED**
Next PR: **R7.1 — ADR-0026 Helm chart**

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
- [x] **R4.2 — PostgreSQL integration end-to-end** *(merged 2026-04-28, PR #61)*
- [x] **R4.3 — Redis for adapter session cache** *(merged 2026-04-28)*
- [x] **R4.4 — Kafka producer (engine → topic)** *(merged 2026-04-28, PR #63 — closes Phase R4)*
- [x] **R5.1 — ADR-0024 output sinks (S3 + webhook)** *(merged 2026-04-28, PR #64 — closes Phase R5)*
- [x] **R6.1 — Per-service Dockerfiles (engine, control plane, three adapters) + `docker-bake.hcl` orchestration** *(merged 2026-04-29, PR #65 — opened Phase R6)*
- [x] **R6.2 — ADR-0025 Compose stack (application services + profile topology + 8090–8093 port migration)** *(merged 2026-04-29, PR #66)*
- [x] **R6.3 — Devcontainer with Docker-in-Docker (operator + kind into the unified Compose stack)** *(complete on merge of this PR, 2026-04-29 — closes Phase R6)*
- [ ] **R7.1 — ADR-0026 Helm chart** *(next — opens Phase R7)*
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.3)

The R6.3 PR's per-step checklist mirrors Section 7 of the phase
prompt. R6.3 places the control-plane operator inside the
unified Compose stack alongside a `kind` cluster running in the
devcontainer's Docker-in-Docker daemon. **Phase R6 closes with
this PR's merge.**

- [x] Step 1 — Inventory: R6.2 merge confirmed; ADR-0020 §85–91 (DinD commitment), ADR-0025 §6 (problem statement) + §9 (deferrals) re-read; `docker-compose.yml` (ten services + no top-level networks block) confirmed; `.devcontainer/{devcontainer.json,Dockerfile,post-create.sh}` read in full; `core/control-plane/cmd/main.go` flag block confirmed (`--engine-endpoint`, `--health-probe-bind-address`, `--metrics-bind-address`, `--leader-elect`); `core/control-plane/Makefile` `install` / `uninstall` targets confirmed (apply CRDs from `config/crd` via kustomize+kubectl)
- [x] Step 2 — `build/kind/cluster.yaml` (single-node `spectre-dev`) + `build/kind/.gitignore` (kubeconfig) created; `.gitignore` carve-out negation extended (`!/build/kind/`, `!/build/kind/**`)
- [x] Step 3 — justfile surgery: `op-run` deleted entirely; `op-install-crds` / `op-uninstall-crds` renamed `crds-install` / `crds-uninstall` and repointed at `build/kind/kubeconfig` via `KUBECONFIG=$(realpath ../../build/kind/kubeconfig) make install/uninstall`; old names kept as one-cycle deprecation aliases (echo + forward); new `kind-up` (idempotent; writes `--internal` kubeconfig with server URL `https://spectre-dev-control-plane:6443`), `kind-down`, `kind-status` recipes; `compose-up` and `compose-reset` comment blocks updated for the post-R6.3 eleven-service topology + kind/Compose lifecycle independence
- [x] Step 4 — `docker-compose.yml` extended: `control-plane` service (image `spectre-control-plane:dev` + `pull_policy: never`; depends on engine + postgres; joins `default` and external `kind` networks; reads-only mounts `build/kind/kubeconfig` at `/home/nonroot/.kube/config`; passes `--engine-endpoint=engine:8090 --health-probe-bind-address=:8081 --metrics-bind-address=0 --leader-elect=false`; profiles `app`, `full`); top-level `networks:` block declares `kind` as `external: true name: kind`. `_endpoint.yaml` sample updated `127.0.0.1:8090` → `engine:8090` for the post-R6.3 Compose-internal flow; `_hello-hackernews.yaml` comment corrected (Helm provisions the in-cluster Service; Compose dev uses Endpoint sample). Validated `docker compose --profile <name> config --services` for each profile (infra/core/adapters/app/full); local end-to-end smoke against host Docker daemon: `just kind-up && just compose-up` brings up eleven services; `kubectl apply -f` of an Endpoint-form ScrapeJob (curl-impersonate driver to avoid R6.1 Playwright runtime image version skew) reconciles Pending → Running → Completed in seconds; row visible in Postgres `jobs` table; operator container confirmed on both `kind` and `baas_default` networks via `docker network inspect`
- [x] Step 5 — `.devcontainer/devcontainer.json` rewritten with the official `docker-in-docker:2` feature (Moby + Compose v2), eleven `forwardPorts`, `portsAttributes` labels, `ms-azuretools.vscode-docker` extension, R6.3-aware comment block; `.devcontainer/Dockerfile` adds `KIND_VERSION=0.24.0` ARG + kind binary install block (after kubebuilder), bumps BUF_VERSION 1.45.0 → 1.55.1 (harmonised with `build/docker/versions.env`), refreshes the comment block to cite ADR-0018 §3a + ADR-0025 §6 R6.3 update; `.devcontainer/post-create.sh` expanded from 5 to 8 numbered steps (kind-up + crds-install precede sanity checks; kind/kubectl version checks added)
- [x] Step 7 — ADR amendments: ADR-0018 frontmatter status flips to "partially superseded; see status notes in §3, §4 and §5", new §3a "R6.3 evolution: Docker-in-Docker for the devcontainer" subsection appended (citing ADR-0020 §85–91 + ADR-0025 §6 R6.3 update; documenting first-build cost rise, two-kubeconfig dance, shared kind Docker network, BUF version harmonisation); ADR-0025 §6 gains "R6.3 update — resolution" subsection (recording dual-network join, kubeconfig mount path, `op-run` deletion, alias-then-remove plan for `op-install-crds` / `op-uninstall-crds`, four R6.3 decisions, end-to-end criteria); ADR-0025 §9 marked as "resolved in R6.3" with each deferral closed; `docs/adr/README.md` index status fields updated for both records
- [x] Step 8 — `docs/architecture/development-environment.md` rewritten for post-R6.3 unified flow (Reopen-in-Container as the supported path; "What runs where" table gains operator row; Operator dev flow rewritten Compose-side only; new "Kubernetes-in-Docker (kind)" subsection — recipe table + two-kubeconfig dance; new "DinD model" subsection — nesting / failure modes / network-not-found troubleshooting; toolchain prerequisites slimmed); `docs/architecture/control-plane.md` Phase 3 status table flips R6.3 row to shipped, deployment-shapes table widened to four columns with R6.3 marked current, "Host operator against a Compose-running engine" replaced by "Operator-as-Compose-service against a kind API server"; README quick-start updated to Reopen-in-Container + `just images && just compose-up`; host-process operator commands removed
- [x] Step 9 — `docs/refactor-audit.md` R6.3 row appended (this PR); `docs/refactoring-status.md` R6.3 → complete on merge, R7.1 → next, **Phase R6 marked CLOSED**; CHANGELOG Unreleased entry recording the DinD/kind/operator-in-Compose changes; ADR-0025 §6 + §9 + ADR-0018 §3a cross-references intact
- [ ] Step 10 — Final verification (devcontainer rebuild + end-to-end smoke; conformance regression unchanged)
- [ ] Step 11 — Open the PR

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
seven decisions for R6.3 (DinD over socket-mount; kind via
post-create + dedicated recipes, not as a Compose service;
dual-network join via Compose's standard mechanism;
`op-run` retired with `op-install-crds` one-cycle alias; no
`compose-up` auto-runs `kind-up`; no new ADR; conformance flow
unchanged) are settled by Section 4 of the phase prompt.

One small reframe surfaced during execution: the kind API
server's in-network DNS name is `spectre-dev-control-plane`
(not `kind-control-plane` as the phase prompt's spec sketched).
Kind names the API server node after the cluster
(`<cluster>-control-plane`); the kubeconfig the `--internal`
flag emits points at the cluster-named hostname. The phase
prompt's references to `kind-control-plane:6443` were corrected
in-line in the justfile + docker-compose.yml + ADR-0025 §6 R6.3
update so the audit trail records the actual hostname rather
than the prompt's sketch.

One pre-existing-issue note: the Playwright runtime image pinned
in `build/docker/versions.env` (`mcr.microsoft.com/playwright:v1.49.0-noble`)
is out-of-step with the npm `playwright` dep (`1.59.1`); a
ScrapeJob targeting the Playwright driver fails at adapter
launch ("Executable doesn't exist at /ms-playwright/…"). The
operator → engine → adapter chain works through that point —
visible in the failure message, which proves R6.3's networking
path. R6.3 does not bump the Playwright pin (out of scope per
phase prompt §10); R7.x picks up the pin sync alongside the
Helm chart.

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
