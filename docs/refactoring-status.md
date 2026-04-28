# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-27
Current phase: **R2.3 — Engine transport + gRPC server (UDS client → TCP client) (in progress)**
Next PR: **R3.1 — `EngineClientRunner` replaces `SubprocessRunner`**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight phases. Order is fixed; phases cannot be reordered or
skipped. See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas.

- [x] **R1.1 — ADR-0020 supersession** *(merged 2026-04-27, PRs #26 + #27)*
- [x] **R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details** *(merged 2026-04-27, PR #28)*
- [x] **R2.2 — Adapter transport switch (UDS → TCP, all three adapters)** *(merged 2026-04-27, PR #29)*
- [ ] **R2.3 — Engine transport + gRPC server (UDS client → TCP client)** *(in progress)*
- [ ] R3.1 — `EngineClientRunner` replaces `SubprocessRunner`
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

## Current PR checklist (R2.3)

The R2.3 PR's per-step checklist mirrors Section 7 of the phase
prompt. Updated each session that lands work on this PR.

- [x] Step 0 — `KNOWN_BREAKAGE.md` deleted; R2.2 → R2.3 transitional break notice removed from the README
- [x] Step 1 — Inventory engine sources (launcher.rs, client.rs, engine.rs, bin/spectre.rs, justfile, ci.yml, refactor-audit R2.3 section)
- [x] Step 2 — `proto/spectre/engine/v1alpha1/engine.proto` introduced; Go bindings via `proto/buf.gen.engine.yaml`; Rust bindings via `core/engine/build.rs`; `engine_proto` module exported from the crate
- [x] Step 3 — `Client::dial` accepts `host:port` / `grpc://host:port`; UDS connector + `:authority=localhost` workaround retired; endpoint normalisation unit-tested
- [x] Step 4 — `AdapterRegistry` reads `SPECTRE_PLAYWRIGHT_ENDPOINT` / `SPECTRE_SELENIUMBASE_ENDPOINT` / `SPECTRE_CURL_IMPERSONATE_ENDPOINT` (defaults `127.0.0.1:909{1,2,3}`); `EngineError::UnknownDriver`
- [x] Step 5 — `Engine` rewired around the registry; `run_plan_with_sink` is the canonical entry point; `Engine::new` / `run_job` / `validate_only` retired; `tests/integration.rs` deleted
- [x] Step 6 — `core/engine/src/launcher.rs` deleted (628 lines); `EngineError::Launcher` removed; Cargo deps pruned (`nix`, `regex`, `uuid` for UDS, `tower`, `hyper-util` prod, `clap`, dev-only `hyper`, `http-body-util`, `tokio` `process` feature); `tonic-health` added
- [x] Step 7 — `server.rs` implements `spectre.engine.v1alpha1.Engine.RunJob` as a server-streaming RPC backed by `ChannelSink` + an unbounded mpsc; error-code mapping covers every `EngineError` variant; tonic-health registers `grpc.health.v1.Health`
- [x] Step 8 — `bin/spectre.rs` rewritten: parses `SPECTRE_ENGINE_PORT`, binds `0.0.0.0:port`, registers Engine + Health, handles SIGTERM/SIGINT; smoke-tested locally (binds, lsof confirms, SIGTERM exits 0)
- [x] Step 9 — `justfile` retires `spectre-version`/`spectre-validate`/`spectre-run`/`engine-run-hello`; adds `engine-run`/`engine-grpc-test`; CI `rust`, `engine-image`, `operator-image` jobs replace `spectre version` smokes with `test -x` checks; `operator-smoke-kind` gated `if: false` until R3.1
- [x] Step 10 — Example READMEs rewritten around the manual `grpcurl` flow; `seleniumbase-navigate` and `curl-impersonate-fetch` deleted (CLI-only demos with no post-R2.3 reason to exist); adapter README "R2.2-R2.3 sequence" notes cleaned up
- [x] Step 11 — `docs/architecture/engine.md` written; `overview.md` "Data flow" section rewritten around `RunJob`; `development-environment.md` engine-image rationale section dropped UDS-coloured passages
- [x] Step 12 — ADR-0012 status note for R2.3 supersession (§4 launcher contract retired; §§1-3, 5, 6 preserved); ADR index updated
- [ ] Step 13 — `docs/refactor-audit.md` R2.3 status note; `docs/refactoring-status.md` and `CHANGELOG.md` updated *(this entry)*
- [ ] Step 14 — Final verification: `just check`, `just conf-test` x3, manual end-to-end RunJob via grpcurl
- [ ] Step 15 — Open the PR
- [ ] Step 16 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
six locked decisions for R2.3 (CLI mode retirement is total;
engine protocol shape is minimum-viable single streaming RPC;
adapter discovery via env vars per ADR-0021 §5; streaming
backpressure is intentionally naive — unbounded channel; health
service registered the same way adapters do per ADR-0021 §6;
example migration via manual `grpcurl` flow until R6.2) were
settled before the phase began and are recorded in the phase
prompt's Section 4. The CI minimal-repair scope was discovered
during execution: `spectre version` / `spectre validate` smoke
steps in three jobs and the `operator-smoke-kind` end-to-end
job all reference the retired CLI surface and could not be
left untouched without breaking this PR's own CI run. The fix
is contained: the version smokes become `test -x` checks; the
end-to-end job is gated `if: false` with a single-line comment
that R3.1's `EngineClientRunner` re-enables.

## Known issues

The control plane's `SubprocessRunner` shells out to `spectre
run`, which the engine binary no longer accepts. R2.3 → R3.1 is
the new transitional window; the gap closes when R3.1's
`EngineClientRunner` lands. The operator's reconciler unit
tests use `StubRunner` and continue to pass; the CI's
`operator-smoke-kind` job is gated off (`if: false`) for the
duration of this window. No tests are quarantined; the
conformance suite (which dials adapters directly and never
went through the engine) continues to pass three consecutive
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
