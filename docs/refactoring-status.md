# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-28
Current phase: **R4.4 — Kafka producer (engine → topic) (complete on merge of this PR, 2026-04-28)** — closes Phase R4
Next PR: **R5.1 — ADR-0024 output sinks (S3 + webhook)**

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
- [x] **R4.4 — Kafka producer (engine → topic)** *(complete on merge of this PR, 2026-04-28 — closes Phase R4)*
- [ ] **R5.1 — ADR-0024 output sinks (S3 + webhook)** *(next)*
- [ ] R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)
- [ ] R6.2 — ADR-0025 Compose stack (six services + three stateful deps)
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R4.4)

The R4.4 PR's per-step checklist mirrors Section 7 of the phase
prompt. R4.4 wires the engine's `rdkafka` producer end-to-end,
unblocks the v1alpha2 reconciler's Kafka admission rejection, and
extends the Compose stack with Apache Kafka KRaft + Redpanda
Console. Closes Phase R4 — the full ADR-0023 stateful-services
architecture (Postgres + Redis + Kafka) is operational.

- [x] Step 1 — Inventory: R4.3 merge confirmed; ADR-0023 §3 / §6 + ADR-0019 read; reconciler's R3.2 `validateOutputSink` and engine `server.rs` mapped; runner / engine_client `JobRunner` shape understood
- [x] Step 2 — `core/engine/Cargo.toml` adds `rdkafka 0.36` with `cmake-build + ssl-vendored + tokio` features; comment block documents the build-time / TLS-stack trade-offs; `chrono` added for ISO-8601 header timestamps; first clean build pays ~10–15 min for OpenSSL compile (cached thereafter)
- [x] Step 3 — `core/engine/src/kafka/{mod.rs,config.rs,producer.rs}`: `KafkaConfig::from_env` reads `SPECTRE_KAFKA_BROKERS` + `SPECTRE_KAFKA_LINGER_MS`; `KafkaProducer::from_env` builds the `FutureProducer` with `acks=all` + `enable.idempotence=true` + `compression.type=snappy` + `linger.ms` and validates broker reachability via `fetch_metadata` (5s timeout); `publish_row` formats §3 headers (`job_id`, `row_index`, `driver`, `timestamp`) and partition-keys by `job_id`; `lib.rs` exports the module
- [x] Step 4 — `bin/spectre.rs` calls `KafkaProducer::from_env` after Postgres init; on success threads `Some(Arc<KafkaProducer>)`, on failure logs warning + threads `None`; ADR-0023 §6 admission-gating realised
- [x] Step 5 — Engine `server.rs` `RunJob` dispatches on `output_sink_kind`: pre-flight check fails fast with `KAFKA_UNAVAILABLE` / `KAFKA_TOPIC_REQUIRED`; per-row publishes via `publish_row`; kafka publish error overrides executor outcome with terminal `Failed`. `engine.proto` adds `kafka_topic` field (number 4, non-breaking); `proto-generate` regenerates Rust + Go + Python bindings; control-plane `JobRunner.Run` signature evolves with `kafkaTopic string`; `EngineClientRunner` forwards it via `RunJobRequest.KafkaTopic`
- [x] Step 6 — `core/engine/tests/kafka_integration.rs`: `#[ignore]` E2E tests (`cargo test --test kafka_integration -- --ignored`) against `SPECTRE_KAFKA_BROKERS`, mirroring R4.2's `db_integration.rs` pattern; round-trips payload + headers and asserts partition-key colocation; both green against the Compose broker. `engine-kafka-test` justfile recipe added
- [x] Step 7 — Reconciler `validateOutputSink` accepts the Kafka branch (rejects empty topic for defence-in-depth); `outputSinkKafkaTopic` helper extracts the topic and the reconciler forwards it to the runner; envtest gains `TestValidateOutputSink_KafkaAccepted`, `TestValidateOutputSink_KafkaRejectsEmptyTopic`, and `TestRunningTransition_KafkaSinkAccepted`; the previous `TestFailedOnUnsupportedSink` switches its example sink from Kafka to S3; `make test` green
- [x] Step 8 — `git mv` `spectre_v1alpha2_scrapejob_kafka_NOT_YET_IMPLEMENTED.yaml` → `spectre_v1alpha2_scrapejob_kafka.yaml`; manifest content updated to a working example; `kustomization.yaml` updated
- [x] Step 9 — `docker-compose.yml` extended with `kafka` (`apache/kafka:3.7.1` KRaft, single broker + controller, 8 partitions default, dual-listener `PLAINTEXT://kafka:9092` for compose-internal + `HOST://localhost:9092` for native binaries) and `kafka-console` (`docker.redpanda.com/redpandadata/console:latest` at <http://localhost:8080>); `.env.example` carries `SPECTRE_KAFKA_BROKERS`; justfile recipes `kafka-console`, `kafka-topics`, `kafka-consume <topic>`; all four services healthy in `docker compose ps`
- [x] Step 10 — `tools/conformance/tests/test_kafka_sink.py`: one engine-level E2E test (kafka behaviour is engine-level, not driver-level — driver is incidental) that spawns the release `spectre` binary as a subprocess, the Playwright adapter via `DriverHarness`, submits a `RunJob` with `output_sink_kind="kafka"` against `/elements`, drains the topic via `confluent_kafka.Consumer`, asserts row count + partition keys = job UUID + headers (`job_id`, `row_index`, `driver=playwright`, `timestamp`); `confluent-kafka>=2.5` added to conformance deps; `buf.gen.engine.yaml` extended with the Python plugins so the test can import the engine bindings; full conformance suite at 47 passed, 14 skipped (vs R4.3's 46 passed, 14 skipped — the +1 is `test_kafka_sink`)
- [x] Step 11 — ADR-0023 §3 R4.4 addendum: KRaft + Redpanda Console choice (superseding the original §3 Redpanda single-binary mention); fail-fast admission gating pattern; producer-config rationale; at-least-once delivery semantics + consumer-side idempotency on `(job_id, row_index)`; engine.proto field 4 evolution; build-time cost note; conformance test pattern reference
- [x] Step 12 — `docs/architecture/kafka.md`: producer lifecycle, configuration table, topic / partitioning / headers contract, delivery semantics, admission-gating UX, Postgres + Kafka coexistence, local-dev stack with justfile recipes, production-deployment cross-reference to R7.1 / Strimzi
- [x] Step 13 — This entry; CHANGELOG `Unreleased` Kafka block; refactor-audit R4.4 row + Phase R4 CLOSED note; README quick-start mention of Kafka
- [x] Step 14 — Final verification: engine clippy + cargo fmt --check + 52 unit tests green; control-plane `make test` green (envtest 85.6 %, runner 81.6 %); conformance ruff + mypy + 3× pytest runs at 47 passed / 14 skipped (skips are curl-impersonate when `curl_chrome116` is not on PATH locally — CI runs all three). 13 / 12 / 6 capability invariant holds byte-for-byte (no driver.yaml or capability touches in this PR). Engine kafka integration tests green against the local Compose broker (2 tests round-trip payload + headers and verify partition-key colocation). Manual transcript: `docker compose ps` shows postgres / redis / kafka / kafka-console all healthy; `just kafka-topics` lists the conformance topics; `kafka-console-consumer.sh --property print.headers=true` shows three messages per kafka job with `job_id=<UUID>`, `row_index=0/1/2`, `driver=playwright`, `timestamp=ISO-8601` headers and `{"text":"first|second|third"}` bodies; Postgres `jobs` table shows kafka jobs at `status=completed, rows_extracted=3` and `job_rows` count = 0 for kafka jobs (ADR-0023 §2 contract verified)
- [ ] Step 15 — Open the PR
- [ ] Step 16 — Summary report

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
