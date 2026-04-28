# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-27
Current phase: **R3.1 — `EngineClientRunner` replaces `SubprocessRunner` (in progress)**
Next PR: **R3.2 — `ScrapeJob` CRD v1alpha2 (breaking change, no conversion webhook)**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight phases. Order is fixed; phases cannot be reordered or
skipped. See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas.

- [x] **R1.1 — ADR-0020 supersession** *(merged 2026-04-27, PRs #26 + #27)*
- [x] **R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details** *(merged 2026-04-27, PR #28)*
- [x] **R2.2 — Adapter transport switch (UDS → TCP, all three adapters)** *(merged 2026-04-27, PR #29)*
- [x] **R2.3 — Engine transport + gRPC server (UDS client → TCP client)** *(merged 2026-04-27, PR #30)*
- [ ] **R3.1 — `EngineClientRunner` replaces `SubprocessRunner`** *(in progress)*
- [ ] R3.2 — `ScrapeJob` CRD v1alpha2 (breaking change, no conversion webhook)
- [ ] R4.1 — ADR-0023 stateful services architecture
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

## Current PR checklist (R3.1)

The R3.1 PR's per-step checklist mirrors Section 7 of the phase
prompt. Updated each session that lands work on this PR.

- [x] Step 1 — Inventory: confirm R2.3 merged, runner / cmd / engine bindings reviewed, decisions in Section 4 cross-checked
- [x] Step 2 — `core/control-plane/internal/runner/engine_client.go` implementing `JobRunner` over `spectre.engine.v1alpha1.Engine.RunJob`; per-Run dial, ctx-respect, insecure transport per ADR-0022 §6
- [x] Step 3 — `engine_client_test.go` with bufconn-backed mock server; six tests cover row streaming, Failed event, ctx cancellation, empty-endpoint guard, dial failure, writer error
- [x] Step 4 — `cmd/main.go` instantiates `EngineClientRunner`; `--engine-binary` / `--adapters-path` flags replaced with `--engine-endpoint` reading `SPECTRE_ENGINE_ENDPOINT` (default 127.0.0.1:9090)
- [x] Step 5 — `subprocess.go`, `subprocess_test.go`, `testdata/fake_spectre.go` deleted; doc strings touched up to describe `EngineClientRunner`; justfile `op-run` rewired around the new flag
- [x] Step 6 — Dockerfile rewritten as a two-stage Go-static build on `gcr.io/distroless/static:nonroot` (~50 MB); engine + adapter bundling stages removed; `op-build-image` / `op-image-smoke` slimmed to a single manager smoke; CI `operator-image` job updated, `operator-smoke-kind` retired (returns in R6.2 / R7.2); `hack/smoke-kind.sh` deleted
- [x] Step 7 — `docs/architecture/control-plane.md` rewritten around the unbundled image and the engine-endpoint contract
- [x] Step 8 — ADR-0019 §5 vindication addendum committed
- [ ] Step 9 — `docs/refactor-audit.md` R3.1 closure markers; this checklist; CHANGELOG entry *(this entry)*
- [ ] Step 10 — Final verification: `make test`, `make build`, image build + smoke, conformance suite
- [ ] Step 11 — Open the PR
- [ ] Step 12 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
five locked decisions for R3.1 (per-Run dial; ctx-deadline
propagation, no internal retry; `SPECTRE_ENGINE_ENDPOINT` env var
+ flag override per ADR-0021 §5; insecure transport for v1alpha1
per ADR-0022 §6; bufconn-based test pattern) were settled before
the phase began and are recorded in the phase prompt's Section 4.

A small in-scope CI cleanup surfaced during execution: the
operator-image job's bundled-asset smokes assume the retired
execution model and would have failed on this PR's own CI run.
The job is reduced to a single `--help` smoke; the gated
`operator-smoke-kind` end-to-end suite is removed and reintroduced
in R6.2 / R7.2 against the Compose stack and the Helm chart.

## Known issues

R3.1 closes the R2.3 → R3.1 transitional window. After this PR
merges, `kubectl apply -f scrapejob.yaml` against an operator
running with `--engine-endpoint=<engine>` produces JSONL output
end-to-end again. No tests are quarantined; the conformance
suite (which dials adapters directly and never went through the
engine or the operator) continues to pass three consecutive
times.

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
