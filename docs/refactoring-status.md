# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-28
Current phase: **R4.3 — Redis for adapter session cache (complete on merge of this PR, 2026-04-28)**
Next PR: **R4.4 — Kafka producer (engine → topic)**

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
- [x] **R4.3 — Redis for adapter session cache** *(complete on merge of this PR, 2026-04-28)*
- [ ] **R4.4 — Kafka producer (engine → topic)** *(next)*
- [ ] R5.1 — ADR-0024 output sinks (S3 + webhook + Kafka)
- [ ] R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)
- [ ] R6.2 — ADR-0025 Compose stack (six services + three stateful deps)
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R4.3)

The R4.3 PR's per-step checklist mirrors Section 7 of the phase
prompt. R4.3 externalises adapter session metadata to Redis
across all three reference adapters (Playwright, SeleniumBase,
curl-impersonate) and materialises the §5 restart-invalidation
contract via the `adapter_instance_id` mechanism. The most
operationally risky PR of the refactor; per-adapter commits
provide stable resumption points.

- [x] Step 1 — Inventory: R4.2 merge confirmed; ADR-0023 §4/§5 + ADR-0010 read; per-adapter session managers + conformance harness mapped
- [x] Step 2 — Compose stack extension: `redis:7-alpine` service with AOF + LRU eviction; `.env.example` extended with `SPECTRE_REDIS_URL` and the testing-only `SPECTRE_ADAPTER_INSTANCE_ID` knob (committed pre-step-3)
- [x] Step 3 — Playwright adapter: `ioredis` + `ioredis-mock` deps; `redis.ts` wrapper; `SessionManager` accepts `RedisClient` + `instanceId`; `register` writes Redis, `validate` reads + refreshes TTL, `closeSession` best-effort deletes; `server.ts` gates non-Initialize RPCs on validate, throws `ConnectError(Code.Unavailable)` on instance mismatch / Redis errors; `index.ts` resolves env, PINGs Redis, exits non-zero on failure; 88 vitest cases green
- [x] Step 4 — SeleniumBase adapter: `redis>=5.0` + `fakeredis>=2.0` (dev) deps; `redis_client.py` mirrors the TS shape; `SessionManager` mirrors the lifecycle integration; `server.py` gates each RPC via `_gate_session` and aborts with `grpc.StatusCode.UNAVAILABLE` on mismatch / Redis errors; `adapter.py` resolves env, PINGs Redis, exits non-zero on failure; 79 pytest cases green; mypy + ruff green
- [x] Step 5 — curl-impersonate adapter: `go-redis/v9` + `redismock/v9` + `miniredis/v2` (test) deps; `internal/redis/redis.go` mirrors the TS / Python shape; `Manager` accepts `*redis.Client` + `instanceID`; `Validate` returns typed kinds; gRPC handlers use `gateSession` and return `status.Error(codes.Unavailable, ...)` on mismatch / Redis errors; `cmd/adapter/main.go` resolves env, PINGs Redis, exits non-zero on failure; all `go test ./...` green
- [x] Step 6 — Conformance harness: `DriverHarness.instance_id_override` exports `SPECTRE_ADAPTER_INSTANCE_ID` into the spawned subprocess; `redis>=5.0` added to conformance deps for test-side verification; existing 56-test suite still passes (44 passed, 13 skipped — env-gated; unchanged from R4.2)
- [x] Step 7 — Restart-invalidation conformance test: `tools/conformance/tests/test_session_restart_invalidation.py` with three tests (one per adapter) following Section 4.4 parallel-instances pattern; full suite three consecutive runs at 46 passed, 14 skipped (curl-impersonate test skips with the rest of the curl-impersonate suite when `curl_chrome116` is not on PATH locally)
- [x] Step 8 — ADR-0023 §5 R4.3 addendum: `adapter_instance_id` mechanism, why hostname-based identification was rejected, per-RPC failure semantics, conformance test pattern; ADR index updated
- [x] Step 9 — `docs/architecture/redis.md`: keyspace, lifecycle table per RPC, instance_id mechanism, restart-invalidation contract, local-dev + production deployment notes
- [x] Step 10 — This entry; CHANGELOG; refactor-audit R4.3 row
- [x] Step 11 — Final verification: per-component lint + tests green (Playwright 88 vitest cases, SeleniumBase 79 pytest, curl-impersonate `go test ./...` across 9 packages including the new `internal/redis`); full conformance suite three consecutive runs at 46 passed, 14 skipped (the +2 vs R4.2's 44 are the Playwright + SeleniumBase restart-invalidation tests; the curl-impersonate restart-invalidation test skips with the rest of the curl-impersonate suite when `curl_chrome116` is not on PATH locally — CI runs all three). Capability invariant 13 / 12 / 6 holds byte-for-byte. The Connect-RPC adapter does not expose gRPC reflection so grpcurl-based manual smoke against the Playwright adapter would need proto descriptors; the conformance test exercises the same code path so the manual transcript is deferred to maintainer review.
- [ ] Step 12 — Open the PR
- [ ] Step 13 — Summary report

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
thirteen decisions for R4.1 (three stateful services scoped
together; Postgres / Kafka / Redis specifically; restart-
invalidation contract for sessions; required-vs-optional
deployment shape; library commitments per language; topic-per-
workspace partitioned by job; normalised Postgres schema;
per-service env-var configuration; migrations at engine
startup) are settled by master strategy + maintainer prior
choices, recorded in the phase prompt's Section 4.

## Known issues

The CRD evolution to v1alpha2 is a breaking change. Per master
strategy §3.3, no conversion webhook is implemented; v1alpha1
ScrapeJob CRs in clusters are orphaned on upgrade. The upgrade
procedure (documented in CHANGELOG and `control-plane.md`) is
`kubectl delete scrapejob --all` → install v1alpha2 CRD → apply
v1alpha2 CRs.

OutputSink schema is one step ahead of functionality: the
v1alpha2 schema includes `Kafka`, `S3`, and `Webhook` fields, but
the reconciler rejects them at admission with explicit "not yet
implemented" errors. R4.4 wires Kafka; R5.1 wires S3 and
Webhook. The schema is committed now to keep the CRD stable
through Phase 3.

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
