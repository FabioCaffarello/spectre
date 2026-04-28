# Refactoring status

This document is the canonical source of truth for the
microservices refactor's progress across sessions. It is read at
the start of every refactor-related session and updated at the
end of every session that produced work. The refactor's
architectural commitment is recorded permanently in
[ADR-0020](adr/0020-microservices-architecture-supersession.md);
this document tracks execution.

Last updated: 2026-04-28
Current phase: **R5.1 — ADR-0024 output sinks (S3 + webhook) (complete on merge of this PR, 2026-04-28)** — closes Phase R5
Next PR: **R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)**

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
- [x] **R5.1 — ADR-0024 output sinks (S3 + webhook)** *(complete on merge of this PR, 2026-04-28 — closes Phase R5)*
- [ ] **R6.1 — Per-service Dockerfiles (engine, control plane, three adapters)** *(next)*
- [ ] R6.2 — ADR-0025 Compose stack (six services + three stateful deps)
- [ ] R6.3 — Devcontainer with Docker-in-Docker
- [ ] R7.1 — ADR-0026 Helm chart
- [ ] R7.2 — Production smoke (Helm-installed cluster)
- [ ] R8.1 — Documentation refresh + narrative closing

## Current PR checklist (R5.1)

The R5.1 PR's per-step checklist mirrors Section 7 of the phase
prompt. R5.1 wires the engine's `aws-sdk-s3` uploader and
`reqwest` webhook client end-to-end, unblocks the v1alpha2
reconciler's S3 + Webhook admission rejections, extends the
Compose stack with MinIO + bucket bootstrapper, and introduces
ADR-0024. **Closes Phase R5** — the v1alpha2 `OutputSink`
discriminated union is fully behaviourally implemented.

- [x] Step 1 — Inventory: R4.4 merge confirmed; kafka module + server.rs dispatch + reconciler validateOutputSink + R3.2 OutputSink CRD types + ADR-0023 §3/§6 + ADR-0019 §5 read; Section 4 decisions confirmed
- [x] Step 2 — ADR-0024 (~530 lines, §1–§10): library choices, buffering models, retry/admission semantics, header schemas, Postgres + sink coexistence, library pinning, out-of-scope deferrals; ADR-0023 §6 cross-reference + ADR-0019 §5 R4.4/R5.1 addenda
- [x] Step 3 — `core/engine/Cargo.toml` adds `aws-sdk-s3` + `aws-config` + `aws-credential-types` (rustls + behavior-version-latest) and `reqwest` 0.12 (rustls-tls-native-roots + json + gzip); axum 0.7 dev-dep for in-process webhook test server; first clean build adds ~3 min (smaller than rdkafka's OpenSSL compile)
- [x] Step 4 — `core/engine/src/s3/{mod.rs,config.rs,uploader.rs}`: S3Config::from_env / from_lookup (HashMap-injected lookup avoids 2024-edition unsafe env::set_var that the unsafe_code = "forbid" lint would block); S3Uploader::from_env distinguishes NotConfigured (INFO) from IncompleteCredentials (WARN); upload_jsonl single PutObject; render_key {{.JobID}} substitution; 14 unit tests
- [x] Step 5 — `core/engine/src/webhook/{mod.rs,config.rs,client.rs}`: WebhookClient::new infallible; WebhookSession per-job batching (auto-flush at batch_size threshold); 3-attempt exponential-backoff retry (200/400/800ms with jitter); ADR-0024 §4 header schema; 15 unit tests
- [x] Step 6 — `engine.proto`: nested `S3SinkConfig` (field 5, bucket/key/endpoint/region) + `WebhookSinkConfig` (field 6, url/method/batch_size); `kafka_topic` (field 4) preserved for R4.4 wire compat. `proto-generate` regenerates Rust + Go + Python bindings
- [x] Step 7 — `bin/spectre.rs`: S3Uploader::from_env three-arm match (Ok → INFO; NotConfigured → INFO BYO-credentials mode; other → WARN); WebhookClient::new infallible single line. EngineServiceImpl + engine_server factory grow to take the new sink-level handles
- [x] Step 8 — Engine `server.rs` `RunJob` dispatches on `output_sink_kind`: pre-flight checks fail fast with S3_UNAVAILABLE / S3_FIELD_REQUIRED / WEBHOOK_FIELD_REQUIRED; per-row S3 buffers JSONL in memory, webhook hands rows to WebhookSession; end-of-job: S3 single PutObject with rendered key, webhook session.finalise(). sink_publish_error widens to (code, message) tuple; new EngineError::SinkPublish variant; 82 unit tests
- [x] Step 9 — `core/engine/tests/{s3,webhook}_integration.rs`: 6 webhook tests (in-process axum, no #[ignore]) cover per-row + batched + retry + fatal-4xx + PUT-method; 3 S3 tests (#[ignore]-gated) cover round-trip + empty-upload + key-template against Compose MinIO. justfile recipes `engine-s3-test`, `engine-webhook-test`
- [x] Step 10 — Reconciler `validateOutputSink` unblocks S3 + Webhook with defence-in-depth on bucket / key / URL emptiness; `outputSinkS3Config` / `outputSinkWebhookConfig` helpers parallel R4.4's kafkaTopic helper; reconciler running phase forwards both via 7-param `jr.Run(...)`. `JobRunner.Run` interface evolves; StubRunner + EngineClientRunner + errorRunner test stub follow. Tests: rejection→accepted renames, new RejectsEmpty* + RunningTransition cases, **TestFailedOnUnsupportedSink deleted** (input set went to zero); `make test` green at 84.7% controller coverage
- [x] Step 11 — ADR-0019 §5 R4.4 + R5.1 addenda: full JobRunner.Run parameter accumulation (R4.2 jobID + outputSinkKind, R4.4 kafkaTopic, R5.1 s3Config + webhookConfig — now 7 params); RunRequest struct refactor deferred to v1alpha2 with rationale (master prompt §4.4); §6 stub fully retired
- [x] Step 12 — `spectre_v1alpha2_scrapejob_s3.yaml` + `..._webhook.yaml` sample manifests with documented preambles (admission gating, local-dev paths, v1alpha1 deferrals); kustomization.yaml lists both
- [x] Step 13 — `docker-compose.yml` extended with `minio` (single-node embedded console at port 9001) and one-shot `minio-bootstrap` that pre-creates `spectre-rows`; `.env.example` carries `SPECTRE_S3_*` block (env-unset arm documented as production-typical); justfile recipes `minio-console`, `minio-ls`. All five stateful services + 2 UIs healthy
- [x] Step 14 — `tools/conformance/tests/test_s3_sink.py` (1 test against Compose MinIO via boto3) + `test_webhook_sink.py` (2 tests against in-process aiohttp server, no Compose dep). `boto3>=1.35` + `aiohttp>=3.10` added to conformance deps with mypy overrides. Full conformance suite at 50 passed / 14 skipped (vs R4.4's 47 / 14 — the +3 are the new tests)
- [x] Step 15 — `docs/architecture/output-sinks.md` (~250 lines per-sink reference: when to use, wire shape, buffering / delivery / admission, local-dev path, production deployment); `docs/architecture/control-plane.md` table updated (S3 / Webhook from schema-only to shipped)
- [x] Step 16 — This entry; CHANGELOG `Unreleased` block; refactor-audit R5.1 row + Phase R5 CLOSED note; README quick-start mention of MinIO
- [ ] Step 17 — Final verification (`just check` + Compose stack + manual transcript)
- [ ] Step 18 — Open the PR

## Surfaced decisions

No open architectural questions awaiting maintainer input. The
eight decisions for R5.1 (aws-sdk-s3 over rust-s3; reqwest over
hyper-direct; nested config messages in engine.proto; JobRunner.Run
keeps growing rather than struct-refactor; no OutputSinkAdapter
trait; S3 admission soft-not-warn; webhook per-job admission;
two engine-level conformance tests) are settled by master
strategy + maintainer prior choices, recorded in the phase
prompt's Section 4.

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
