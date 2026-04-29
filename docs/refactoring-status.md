# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.2 — ADR-0025 Compose stack (application services + profile topology + 8090–8093 port migration) (complete on merge of this PR, 2026-04-29)** — Phase R6 still open
Next PR: **R6.3 — Devcontainer with Docker-in-Docker (operator + kind cluster join the unified Compose stack)**

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
- [x] **R6.2 — ADR-0025 Compose stack (application services + profile topology + 8090–8093 port migration)** *(complete on merge of this PR, 2026-04-29)*
- [ ] **R6.3 — Devcontainer with Docker-in-Docker (operator + kind into the unified Compose stack)** *(next)*
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.2)

The R6.2 PR's per-step checklist mirrors Section 7 of the phase
prompt. R6.2 wires the five service images R6.1 produced into a
unified `docker-compose.yml` with profile-based topology, enacts
the ADR-0021 §4 port plan (8090–8093), retires the native-binary
adapter run recipes, and introduces ADR-0025. **Phase R6 still
open** — R6.3 brings the operator + a kind cluster into the
unified Compose stack via Docker-in-Docker.

- [x] Step 1 — Inventory: R6.1 merge confirmed; ADR-0021 §4 + §6, ADR-0023 §6 + §9, ADR-0024 §3 re-read; existing `docker-compose.yml` (six stateful services) and `docker-bake.hcl` (five build targets) re-read; engine binary `SPECTRE_ENGINE_PORT` env var name confirmed (the prompt's `SPECTRE_ENGINE_GRPC_PORT` sketch deviates from the binary; R6.2 uses what the binary reads); adapter Dockerfile bases verified for healthcheck-tool availability (Playwright Ubuntu Noble has bash; SeleniumBase python-slim has python but not nc; curl-impersonate Alpine has busybox nc; engine distroless static has nothing); six Section 4 decisions settled
- [x] Step 2 — ADR-0025 (~470 lines: §1 context · §2 decision summary · §3 service topology + healthcheck strategy · §4 profiles · §5 conformance subprocess-harness rationale · §6 operator deferral to R6.3 + precise R6.3 problem statement · §7 port migration · §8 image-source policy · §9 R6.3 deferrals · §10 out of scope · §11 references); ADR-0021 §4 R6.2 implementation note added; ADR-0023 §9 cross-reference updated to point at ADR-0025; ADR-0019 §3 R6.2 forward-link to ADR-0025 §6; `docs/adr/README.md` indexed
- [x] Step 3 — `docker-compose.yml` extended: four application services (engine, playwright-adapter, seleniumbase-adapter, curl-impersonate-adapter) added with `image:` + `pull_policy: never` + asymmetric per-base healthchecks + `depends_on` on stateful deps; SeleniumBase service sets `shm_size: 1gb`; existing six stateful services tagged with profile membership (`infra`, `core`, `adapters`, `app`, `full`); kafka-console deliberately excluded from `app`. Validated via `docker compose --profile <name> config --services` for each profile
- [x] Step 4 — Port migration sweep 9090–9093 → 8090–8093 across `core/engine/src/bin/spectre.rs` (DEFAULT_PORT + module docs), `core/engine/src/server.rs` (module docs), `core/engine/src/registry.rs` (PLAYWRIGHT/SELENIUMBASE/CURL_IMPERSONATE_DEFAULT_ENDPOINT + module docs + tests), `core/engine/src/client.rs` (test fixtures), `core/control-plane/cmd/main.go` (defaultEngineEndpoint + flag help text), `core/control-plane/internal/runner/engine_client.go` (doc comment), `core/control-plane/internal/runner/engine_client_test.go` (dial-failure test endpoints), `core/control-plane/internal/controller/scrapejob_controller.go` (defaultEnginePort + comment), `core/control-plane/internal/controller/scrapejob_controller_test.go` (10 occurrences across resolveEngineEndpoint tests), `core/control-plane/internal/db/db_test.go` (Service-FQDN endpoint), `core/control-plane/api/v1alpha2/scrapejob_types.go` (kubebuilder default), `core/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml` (regenerated CRD's port default), `core/control-plane/README.md` (operator port doc), eight `core/control-plane/config/samples/spectre_v1alpha2_scrapejob_*.yaml` manifests, `adapters/playwright/Dockerfile` + `adapters/seleniumbase/Dockerfile` + `adapters/curl-impersonate/Dockerfile` (EXPOSE + ENV defaults), three adapter READMEs (rewrote pw-run/sb-run/curl-imp-run sections to point at compose-up), `adapters/seleniumbase/src/spectre_seleniumbase/adapter.py` (module docstring), three adapter test fixtures (Playwright TS, SeleniumBase Python, curl-impersonate Go), `tools/conformance/src/spectre_conformance/demo_navigate.py` + `demo_full_cycle.py` (CLI help text), three example READMEs (hello-hackernews, seleniumbase-extract, curl-impersonate-extract — quick-start blocks rewritten for compose-up), `.env.example` (six default values + comment block referencing ADR-0021 §4 / ADR-0025 §7). Kafka 9092 broker + Kafka container internal listener ports 9092/9093/9094 untouched
- [x] Step 5 — justfile recipe surgery: deleted `pw-run` / `sb-run` / `curl-imp-run` (no fallback); renamed `engine-run` → `engine-run-native` (debugging escape hatch with comment block pointing readers at compose-up); renamed `op-build-image` → `op-image` (parallel to engine-image / pw-image / sb-image / curl-imp-image); `compose-up` body becomes `docker compose --profile full up -d`; `compose-reset` chains `--profile full`; `compose-logs` accepts an optional SERVICE argument; new `compose-up-app` / `compose-up-core` / `compose-up-adapters` / `compose-up-infra` recipes; new `compose-restart SERVICE` and `compose-rebuild SERVICE` recipes (the latter chains `bake SERVICE && docker compose up -d --no-deps SERVICE` with bake-target-name → Compose-service-name mapping for adapters). `engine-grpc-test` default port flips 9090 → 8090. `just --list` confirms recipe set
- [x] Step 6 — `docs/architecture/development-environment.md` rewritten (~150 lines: quick start, profiles, what runs where, port reference, conformance-suite flow, operator dev flow, devcontainer, native-binary debugging, related ADRs); `docs/architecture/control-plane.md` updated (Phase 3 status table flips Compose-stack row to "shipped"; engine port 0.0.0.0:8090; engine endpoint resolution defaults; operator-image deployment-shapes table added; "Host operator against a Compose-running engine" section replaces the multi-terminal native-binary flow); `docs/architecture/engine.md` adapter discovery defaults flipped 8091/8092/8093; `docs/architecture/redis.md` and `postgres.md` adapter run examples updated for compose-up
- [x] Step 7 — `docs/refactor-audit.md` R6.2 row appended; `docs/refactoring-status.md` R6.2 → complete on merge, R6.3 → next; CHANGELOG Unreleased entries (Added: Compose stack + ADR-0025; Changed: port migration + recipe surgery); README quick-start rewritten for `just images && just compose-up`
- [ ] Step 8 — Final verification: build images via `just images`; bring up `--profile full`; eleven services healthy; submit ScrapeJob via grpcurl against 127.0.0.1:8090; profile selectivity (infra / core / adapters / full); operator dev flow via `just op-run` against external kind; conformance suite passes unchanged
- [ ] Step 9 — Open the PR

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
six decisions for R6.2 (operator outside Compose for R6.2;
8090–8093 port migration enacted; conformance stays subprocess-
based; single-file profile-based Compose; asymmetric per-base
healthchecks; `image:` + `pull_policy: never`) are settled by
Section 4 of the phase prompt. One pre-existing implementation
discrepancy was discovered during inventory: the engine binary
reads `SPECTRE_ENGINE_PORT` (not `SPECTRE_ENGINE_GRPC_PORT` as
ADR-0021 §4's table documents). R6.2 uses the env var name the
binary actually reads in the Compose env block; correcting the
binary or the ADR table would be source-modification beyond
R6.2's port-default-only scope (ADR-0025 §10) and is left for a
future small PR.

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
