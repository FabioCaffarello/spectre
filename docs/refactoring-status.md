# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-29
Current phase: **R6.5 — Quality & Hardening (CLOSES on merge of this PR, 2026-04-29; R6.5.4 closes the four-PR sub-phase)**
Next PR: **R7.1 — ADR-0026 Helm chart (opens Phase R7)**

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
- [x] **R6.5.4 — Dockerfile deduplication via shared codegen base image stage** *(complete on merge of this PR, 2026-04-29 — closes Phase R6.5)*

- [ ] **R7.1 — ADR-0026 Helm chart** *(next; opens Phase R7)*
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R6.5.4)

The R6.5.4 PR's per-step checklist mirrors Section 7 of the
phase prompt. R6.5.4 deduplicates the buf install across four
Dockerfiles via a single shared codegen base image stage
consumed through bake's `contexts:` feature. ~30 lines net
reduction; engine unchanged; image sizes unchanged. Phase
R6.5 closes with this PR.

- [x] Step 1 — Inventory: R6.5.3 merge confirmed
  (`b42a0f3 Merge pull request #70`). The four buf install
  RUN blocks audited (control-plane uses `aarch64` and
  `${TARGETARCH:-amd64}`; the other three use `aarch_64` —
  the wrong asset name — and `$(uname -m)`). Verified buf
  release naming via direct fetch:
  `buf-Linux-aarch64` is `302`, `buf-Linux-aarch_64` is `404`.
  `docker-bake.hcl` target structure, `build/docker/README.md`,
  and `tools/build/check-versions-coherent.sh` reviewed. The
  five §4 decisions settled.
- [x] Step 2 — `build/docker/buf-base.Dockerfile` shipped
  (~25 lines on `debian:12-slim`; `ARG BUF_VERSION` +
  `ARG TARGETARCH`; arch case echoes `aarch64` per buf's
  actual asset name). Standalone build verified
  (`docker buildx build` with `BUF_VERSION=1.55.1` produces
  an image whose `buf --version` reports `1.55.1`).
- [x] Step 3 — `docker-bake.hcl` `target "buf-base"` block
  added (`output = ["type=cacheonly"]`; `BUF_VERSION` arg
  passed through). Verified via
  `docker buildx bake --print buf-base`.
- [x] Step 4 — `contexts: { buf-base = "target:buf-base" }`
  declared on the four buf-using consumer targets
  (control-plane, curl-impersonate, playwright,
  seleniumbase). Engine target intentionally untouched —
  Rust uses `prost-build`. `docker buildx bake --print` shows
  the four `contexts:` declarations.
- [x] Step 5a — Go consumers (control-plane,
  curl-impersonate): buf install RUN block deleted;
  `COPY --from=buf-base /usr/local/bin/buf` added before
  the `COPY proto/` line; `ARG BUF_VERSION` preserved with a
  cache-key comment. Both built green via bake; both smokes
  (op-image, curl-imp-image) pass.
- [x] Step 5b — playwright: buf install RUN block deleted;
  `COPY --from=buf-base` added; minimal
  `apt-get install ca-certificates` retained because
  `node:<version>-bookworm-slim` ships no trust store and
  `buf generate` reaches the BSR over TLS for remote plugins
  (caught during build verification). `pw-image-smoke`
  passes.
- [x] Step 5c — seleniumbase: interleaved buf+uv RUN split;
  buf-specific commands deleted; uv install preserved
  verbatim; `COPY --from=buf-base` added above the apt-get
  RUN; `ARG BUF_VERSION` preserved with cache-key comment.
  `sb-image-smoke` passes.
- [x] Step 6 — `tools/build/check-versions-coherent.sh`
  extended with a `dockerfile_arg_declared()` helper plus an
  `ARG_DECLARED_CHECKS` list covering
  `build/docker/buf-base.Dockerfile`. `just check-versions`
  reports 0 mismatches; intentional drift reproduces a
  non-zero exit.
- [x] Step 7 — `build/docker/README.md` "Shared codegen base
  (R6.5.4)" section added (multi-arch propagation, BUF_VERSION
  bump procedure, why engine stays out). ADR-0018 §3 gains
  an "R6.5.4 update — shared codegen base" subsection.
- [x] Step 8 — `docs/refactor-audit.md` ticks R6.5.4 and
  marks Phase R6.5 CLOSED; `docs/refactoring-status.md`
  advances to R7.1-next with R6.5.4 ticked; CHANGELOG
  Unreleased entry.
- [ ] Step 9 — Final verification (`just check`, all five
  images via bake, image sizes unchanged, conformance suite,
  multi-arch dry-run locally).
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
