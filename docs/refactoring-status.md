# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.1 — Per-service Dockerfiles + bake orchestration (complete on merge of this PR, 2026-04-29)** — opens Phase R6
Next PR: **R6.2 — ADR-0025 Compose stack (application services + three stateful deps)**

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
- [x] **R6.1 — Per-service Dockerfiles (engine, control plane, three adapters) + `docker-bake.hcl` orchestration** *(complete on merge of this PR, 2026-04-29 — opens Phase R6)*
- [ ] **R6.2 — ADR-0025 Compose stack (application services + three stateful deps)** *(next)*
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.1)

The R6.1 PR's per-step checklist mirrors Section 7 of the phase
prompt. R6.1 introduces per-service Dockerfiles for the three
reference adapters, harmonises the two existing Dockerfiles
(engine + control-plane) under a uniform `docker buildx bake`
orchestrator, and consolidates toolchain version pins into
`build/docker/versions.env`. **Opens Phase R6** — R6.2 wires
these images into the Compose stack; R6.3 revisits the
Devcontainer.

- [x] Step 1 — Inventory: R5.1 merge confirmed; existing engine + control-plane Dockerfiles read; ADR-0018 (§3 distroless / §4 deferred / §5 single-arch) re-read; `proto/buf.gen.yaml` + `tools/codegen/post-generate.sh` re-read; adapter source paths confirmed; ten Section 4 decisions settled
- [x] Step 2 — `build/docker/versions.env` (RUST 1.88, GO 1.25, NODE 20, PYTHON 3.12, PROTOC 27.2, BUF 1.55.1, UV 0.5.11, PLAYWRIGHT 1.49.0, CURL_IMPERSONATE_IMAGE pin, CHROME_VERSION reference); `.gitignore` widened with `/build/docker/` carve-out so the file survives the existing `build/` Node/TS exclusion
- [x] Step 3 — `docker-bake.hcl` (~220 lines): five targets (engine / control-plane / curl-impersonate / playwright / seleniumbase), three groups (default / core / adapters), eleven variables (TAG / REGISTRY + nine toolchain pins + VCS_REF / BUILD_DATE), two functions (`image()` for registry-aware naming, `labels()` for the OCI annotation schema); HCL parses clean; `bake --print` resolves all five targets with the correct args + labels
- [x] Step 4 — `adapters/curl-impersonate/Dockerfile`: three-stage (codegen → builder → runtime). **Runtime-base deviation** from R6.1 §4.3 sketch: distroless/base-debian12 sketched, but the variant binaries are POSIX shell wrappers and distroless ships no shell; the upstream `lwthiker/curl-impersonate:0.6-chrome` Alpine image is used as the runtime base directly. Final image 22 MB (target <80 MB). Smoke green ("SPECTRE_ADAPTER_GRPC_PORT is required" canonical exit-1)
- [x] Step 5 — `adapters/playwright/Dockerfile`: two-stage (builder → runtime). Builder uses `node:20-bookworm-slim` + corepack-managed pnpm, regenerates TS proto bindings via buf, runs `pnpm install --frozen-lockfile` + `pnpm run build`, prunes devDeps. Runtime is `mcr.microsoft.com/playwright:v1.49.0-noble` — pinned in lock-step with the npm `playwright` dep. Image 877 MB (above the 650 MB target — Microsoft image carries Chromium + Firefox + WebKit; documented in `container-images.md`). Smoke green ("redis ping failed" canonical exit-1)
- [x] Step 6 — `adapters/seleniumbase/Dockerfile`: two-stage (builder → runtime) using `python:3.12-slim-bookworm` + uv (added UV_VERSION=0.5.11 to `versions.env`). Builder builds the venv at the FINAL runtime path `/opt/spectre/adapters/seleniumbase/.venv` because uv hard-codes absolute paths; proto python bindings staged at `/opt/spectre/proto/gen/python` so the editable source resolves identically in builder + runtime. Runtime adds Google Chrome stable (apt) + ChromeDriver via `seleniumbase install chromedriver` at build time. SPECTRE_SELENIUMBASE_CONTAINER=1 baked. Final image 323 MB (target <850 MB). Smoke green
- [x] Step 7 — `core/engine/Dockerfile` + `core/control-plane/Dockerfile` refactored: removed inline ARG defaults so bake supplies `RUST_VERSION` / `PROTOC_VERSION` / `GO_VERSION` / `BUF_VERSION` from `versions.env`; header comments updated to reference `docker buildx bake`; control-plane Dockerfile gains `ARG GO_VERSION` so the `FROM golang:${GO_VERSION}` line consumes the bake-supplied pin. **`RUST_VERSION` bumped from 1.85 → 1.88** because aws-sdk-sts 1.94 (transitive dep added by R5.1) requires rustc 1.88
- [x] Step 8 — `.dockerignore` consolidated to deny-by-default + negate-include shape: `*` denies everything, `!proto/`, `!core/`, `!adapters/`, `!tools/codegen/`, `!build/docker/` re-include the directories any image needs, then per-pattern denies for generated trees (`proto/gen/`, `**/node_modules/`, `**/target/`, `**/.venv/`, etc.)
- [ ] Step 9 — Justfile umbrella recipes (`images`, `images-smoke`, `images-clean`, `images-list`); per-adapter `pw-image` / `sb-image` / `curl-imp-image` + `*-image-smoke` recipes; `engine-image` + `op-build-image` refactored to wrap `docker buildx bake`
- [ ] Step 10 — `docs/architecture/container-images.md` (~280 lines: overview, build context, toolchain pinning, per-service notes including the curl-impersonate runtime-base deviation rationale, bake orchestration, OCI label schema, smoke testing, forward references); ADR-0018 status frontmatter updated to "accepted (partially superseded)"; ADR-0018 §4 status note "retired by R6.1"; ADR-0018 §5 status note "reaffirmed for R6.1; revisited in R7.1"
- [ ] Step 11 — This entry; CHANGELOG `Unreleased` block; refactor-audit R6.1 row + Phase R6 OPEN note; README quick-start mention of `just images`
- [ ] Step 12 — Final verification: `just check` green; `docker buildx bake default` builds all five; `docker images "spectre-*"` sizes within targets (one documented exception); `just images-smoke` green; OCI labels + nonroot users present on every image; Compose stack still boots cleanly; conformance suite count unchanged
- [ ] Step 13 — Open the PR

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
ten decisions for R6.1 (Dockerfile co-location with services;
bake as orchestrator; runtime-base matrix; OCI labels via bake;
single repo-root .dockerignore; CI work deferred; no HEALTHCHECK
in Dockerfiles; versions.env as plain shell; no multi-arch;
binary-exists / canonical-error smokes) are settled by Section 4
of the phase prompt. The curl-impersonate runtime-base deviation
from §4.3 (Alpine upstream image rather than distroless) is
documented in `container-images.md` and reflects an infeasibility
discovered during execution: the variant binaries are shell
wrappers, distroless ships no shell.

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
