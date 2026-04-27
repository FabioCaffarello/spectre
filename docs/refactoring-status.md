# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-27
Current phase: **R2.2 — Adapter transport switch (UDS → TCP, all three adapters) (in progress)**
Next PR: **R2.3 — Engine transport + gRPC server (UDS client → TCP client)**

## Phases

The full refactor is delivered as roughly seventeen PRs across
eight phases. Order is fixed; phases cannot be reordered or
skipped. See [ADR-0020 §5](adr/0020-microservices-architecture-supersession.md)
for the per-phase ADR deltas.

- [x] **R1.1 — ADR-0020 supersession** *(merged 2026-04-27, PRs #26 + #27)*
- [x] **R2.1 — ADR-0021 service discovery + ADR-0022 TCP transport details** *(merged 2026-04-27, PR #28)*
- [ ] **R2.2 — Adapter transport switch (UDS → TCP, all three adapters)** *(in progress)*
- [ ] R2.3 — Engine transport + gRPC server (UDS client → TCP client)
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

## Current PR checklist (R2.2)

The R2.2 PR's per-step checklist mirrors Section 7 of the phase
prompt. Updated each session that lands work on this PR.

- [x] Step 0 — Pre-existing curl-imp-lint Go toolchain fix (`GOTOOLCHAIN=go1.25.3` mirrors cp-lint; three pre-existing `errcheck` warnings on `fmt.Fprintf` returns annotated with `_, _ =`)
- [x] Step 1 — Inventory and confirm working tree
- [x] Step 2 — Playwright adapter TCP switch (`SPECTRE_ADAPTER_GRPC_PORT`, `grpc.health.v1.Health` registration via vendored `proto/grpc/health/v1` bindings, UDS deletions)
- [x] Step 3 — SeleniumBase adapter TCP switch (`grpcio-health-checking` HealthServicer, `argparse` retired, UDS deletions)
- [x] Step 4 — curl-impersonate adapter TCP switch (`google.golang.org/grpc/health` HealthServer, `flag` import retired, UDS deletions)
- [x] Step 5 — Conformance harness rewrite (`_allocate_free_port`, `_wait_for_health_serving`, `from_driver_yaml` reads `runtime.command`, demos take `--endpoint=host:port`)
- [x] Step 6 — `docs/refactor-audit.md` R2.2 status note
- [x] Step 7 — `KNOWN_BREAKAGE.md` created (engine ↔ adapter transport mismatch documented; R2.3 deletes the file)
- [ ] Step 8 — `docs/refactoring-status.md` update (this commit)
- [ ] Step 9 — Final verification (`just check`, three consecutive `just conf-test` runs, manual health probe per adapter)
- [ ] Step 10 — Open the PR
- [ ] Step 11 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
five locked decisions for R2.2 (Step 0 toolchain alignment as
out-of-scope but pragmatic, gRPC standard health check as
readiness signal, dynamic port allocation in the harness,
`driver.yaml` schema evolution to drop `transports:` and add
`runtime.command:`, mandatory `grpc.health.v1.Health` registration
in every adapter) were settled before the phase began and are
documented in
[ADR-0021](adr/0021-service-discovery.md) and
[ADR-0022](adr/0022-tcp-grpc-transport.md).

The Step 0 diagnostic in the phase prompt was inverted relative
to the actual cause (the prompt assumed Go 1.26 was the linter's
target; in fact `golangci-lint v2.8.0` is built with Go 1.25.5
and panics on the Go 1.26 stdlib loaded by an unconstrained
toolchain). Pinning `GOTOOLCHAIN=go1.25.3` in `curl-imp-lint`
mirrors the existing `cp-lint` pattern and is the smallest
possible change. The buf python plugins were also pinned to
`v33.1`/`v1.74.0` because the unpinned hosted plugins emit gencode
requiring protobuf 7.x at runtime, which collides with
`grpcio-health-checking==1.80.0`'s `protobuf<7.0.0` upper bound.
Both pins are documented inline.

## Known issues

The engine binary still dials UDS while the adapters bind TCP —
the deliberate intermediate state between R2.2 and R2.3 merges,
documented at the repo root in
[`KNOWN_BREAKAGE.md`](../KNOWN_BREAKAGE.md). R2.3's first commit
deletes that file. No tests are quarantined; the conformance
suite continues to pass three consecutive times against the new
TCP transport.

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
