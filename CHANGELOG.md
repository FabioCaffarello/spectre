# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Devcontainer with Docker-in-Docker; kind cluster managed by
  `kind-up` / `kind-down`; control-plane operator added as a
  Compose service; closes Phase R6 (R6.3).** R6.3 places the
  operator inside the unified Compose stack alongside a local
  `spectre-dev` kind Kubernetes cluster running in the
  devcontainer's Docker-in-Docker daemon (ADR-0025 §6 R6.3
  update + ADR-0018 §3a R6.3 evolution). The
  `.devcontainer/devcontainer.json` adds the official
  `ghcr.io/devcontainers/features/docker-in-docker:2` feature
  (Moby variant + Compose v2), populates `forwardPorts` for
  eleven application + stateful service ports, attaches human
  labels via `portsAttributes`, and adds
  `ms-azuretools.vscode-docker`. The Dockerfile pins
  `KIND_VERSION=0.24.0` and harmonises `BUF_VERSION` 1.45.0 →
  1.55.1 with `build/docker/versions.env`; the post-create
  script grows from five to eight numbered steps (kind cluster
  creation + CRD install + version-print sanity precede the
  ready banner). New `build/kind/cluster.yaml` (single-node
  `spectre-dev` config) + `build/kind/.gitignore`; the
  `.gitignore` carve-out is extended to track `/build/kind/`
  alongside `/build/docker/`. `docker-compose.yml` gains a
  `control-plane` service (image `spectre-control-plane:dev` +
  `pull_policy: never`; depends on engine + postgres-healthy;
  joins both the Compose default network and the external
  `kind` Docker network; mounts `build/kind/kubeconfig` read-only
  at `/home/nonroot/.kube/config`; passes
  `--engine-endpoint=engine:8090 --health-probe-bind-address=:8081
  --metrics-bind-address=0 --leader-elect=false`; profiles
  `app`, `full`); top-level `networks:` block declares `kind` as
  `external: true name: kind`. New justfile recipes:
  `kind-up` (idempotent — writes `kind get kubeconfig --internal`
  to `build/kind/kubeconfig` with server URL
  `https://spectre-dev-control-plane:6443`), `kind-down`,
  `kind-status`, `crds-install`, `crds-uninstall`. The
  R6.2 host-process `op-run` recipe is **deleted** entirely (no
  legacy paths per master strategy §2.2);
  `op-install-crds` / `op-uninstall-crds` are renamed
  `crds-install` / `crds-uninstall` and repointed at
  `build/kind/kubeconfig`, with one-cycle deprecation aliases
  preserving the old names (removed in R7.1). Sample-manifest
  endpoints updated: `_endpoint.yaml` flips
  `127.0.0.1:8090` → `engine:8090` (the Compose-internal
  hostname the operator container resolves);
  `_hello-hackernews.yaml` comment corrected (Helm provisions
  the in-cluster Service; Compose dev uses the Endpoint
  sample). ADR-0018 frontmatter status flipped to
  "partially superseded; see status notes in §3, §4 and §5";
  new §3a "R6.3 evolution: Docker-in-Docker for the
  devcontainer" subsection records the audit trail (citing
  ADR-0020 §85–91 master commitment, the two-kubeconfig dance,
  the shared `kind` Docker network, and BUF version
  harmonisation). ADR-0025 §6 gains "R6.3 update — resolution"
  subsection; §9 deferrals each marked "(resolved in R6.3)".
  `docs/architecture/development-environment.md` rewritten for
  the post-R6.3 unified flow (Reopen-in-Container as the
  supported path; new "Kubernetes-in-Docker (kind)" + "DinD
  model" subsections; toolchain prerequisites slimmed to "Docker
  on the host"). `docs/architecture/control-plane.md` Phase 3
  status table flips the R6.3 row to shipped; deployment-shapes
  table widened with R6.3 marked current; the host-operator
  subsection replaced by the post-R6.3 "Operator-as-Compose-
  service against a kind API server" walkthrough. README
  quick-start rewritten. **Phase R6 CLOSED** with this PR's
  merge — the master-strategy §2.5 promise ("what runs in
  development equals what runs in production") holds for
  application services and their direct dependencies; v1alpha1
  deferrals (mTLS, multi-arch images, Helm chart) are Phase R7
  work. **No new ADR** — ADR-0020 §4 refactor table locks Phase
  R6 to ADR-0025 only. **No source-code changes** beyond
  sample-manifest endpoint updates (topology). Conformance suite
  unchanged per ADR-0025 §5. Capability invariant 13/12/6 holds
  byte-for-byte.
- **Unified Compose stack with application services; profile-based
  topology; ADR-0025 introduced (R6.2).** `docker-compose.yml`
  gains four application services — engine, playwright-adapter,
  seleniumbase-adapter, curl-impersonate-adapter — alongside the
  six stateful services from R4.x and R5.1, on a single Compose
  network. Services consume locally-built images via
  `image: spectre-<name>:dev` + `pull_policy: never` (no `build:`
  directives — bake from R6.1 is the canonical build path,
  ADR-0025 §8). Five profiles (`infra`, `core`, `adapters`,
  `app`, `full`) cover the common subset use cases; the
  documented default is `--profile full` (aliased as
  `just compose-up`). The application port range moves from
  9090–9093 to **8090–8093** (ADR-0021 §4 implementation note —
  the plan was right since R2.1; the implementation was lazy)
  to free `localhost:9092` for Kafka under the unified Compose
  stack. ADR-0025 records the topology, profile design, port
  migration, conformance subprocess-harness rationale, and
  operator-outside-Compose deferral. **Healthcheck strategy is
  asymmetric per runtime base** (ADR-0025 §3): engine has none
  (distroless static ships no shell or probe binary); Playwright
  uses bash `/dev/tcp` redirect; SeleniumBase uses Python
  `socket`; curl-impersonate uses busybox `nc -z`. SeleniumBase
  service sets `shm_size: 1gb` for Chrome's `/dev/shm` need.
  **Operator stays a host process for R6.2** (ADR-0025 §6) —
  `just op-run` continues to dial the Compose-running engine via
  the host-port mapping `127.0.0.1:8090`. R6.3 (Devcontainer
  with Docker-in-Docker) brings the operator into the unified
  shape alongside a Compose-managed `kind` cluster. **Phase R6
  remains open**; R6.3 is next.
- **Per-service container images for the engine, control plane,
  and three reference adapters; `docker buildx bake` orchestration;
  `build/docker/versions.env` single-source-of-truth for toolchain
  pins (R6.1).** Three new Dockerfiles
  (`adapters/{curl-impersonate,playwright,seleniumbase}/Dockerfile`)
  bring the service-per-image story to all five components. The
  curl-impersonate runtime base is the upstream
  `lwthiker/curl-impersonate:0.6-chrome` Alpine image used directly
  (the variant binaries are POSIX shell wrappers and the R6.1 §4.3
  sketch's distroless base ships no shell — see
  [container-images.md](docs/architecture/container-images.md));
  the Playwright runtime is the canonical
  `mcr.microsoft.com/playwright:v1.49.0-noble` (Microsoft-maintained,
  Chromium pre-baked, version locked in step with the npm dep);
  the SeleniumBase runtime is `python:3.12-slim-bookworm` with
  Chrome stable + ChromeDriver provisioned at image build time
  and `SPECTRE_SELENIUMBASE_CONTAINER=1` baked as a default ENV.
  `docker-bake.hcl` at the repo root declares five targets, three
  groups (default / core / adapters), and two functions (`image()`
  for registry-aware naming, `labels()` for the OCI annotation
  schema injected uniformly across every image).
  `build/docker/versions.env` consolidates `RUST_VERSION` (1.85
  → 1.88; aws-sdk-sts 1.94 transitive dep MSRV), `GO_VERSION`,
  `NODE_VERSION`, `PYTHON_VERSION`, `PROTOC_VERSION`,
  `BUF_VERSION`, `UV_VERSION`, `PLAYWRIGHT_VERSION`,
  `CURL_IMPERSONATE_IMAGE`, and `CHROME_VERSION` into one POSIX
  shell-sourceable file every consumer reads. Existing engine +
  control-plane Dockerfiles refactored to drop inline `ARG`
  defaults and `LABEL` directives (bake supplies both); engine
  Dockerfile gains apt installs for `cmake`, `g++`,
  `libcurl4-openssl-dev` plus a `x86_64-linux-musl-g++` symlink
  and a curl-headers copy into musl's sysroot so librdkafka 2.12
  builds under the musl target. `.dockerignore` consolidated to
  deny-by-default + negate-include shape so the build context
  shrinks below 50 MB. ADR-0018 status frontmatter updated to
  "accepted (partially superseded)"; §4 (per-adapter Dockerfile
  deferral) **retired**; §5 (single-arch + no-publish)
  **reaffirmed for R6.1; revisited in R7.1**. New justfile
  umbrella recipes: `images`, `images-smoke`, `images-clean`,
  `images-list`; per-adapter `*-image` + `*-image-smoke`
  recipes; `engine-image` + `op-build-image` refactored to wrap
  `docker buildx bake`. **Phase R6 opens with this PR** (R6.2 wires
  the images into Compose; R6.3 revisits the Devcontainer; R7.1
  adds release-engineering — multi-arch matrix, ghcr.io
  publishing, signing). Capability invariant 13/12/6 holds
  byte-for-byte; conformance suite count unchanged at 50/14
  (no behavioural tests added).
- **S3 + webhook output sinks; `OutputSink.S3` + `OutputSink.Webhook`
  unblocked; ADR-0024 introduced (R5.1).** ADR-0024 documents
  the engine's `aws-sdk-s3` 1.x uploader (rustls features,
  custom-endpoint support for MinIO/R2/Wasabi,
  `behavior-version-latest` pinning) and `reqwest` 0.12 webhook
  client (rustls-tls-native-roots aligned with sqlx 0.23). The
  S3 sink buffers extracted rows in memory as JSON Lines and
  uploads as a single PutObject at job completion (multipart
  streaming deferred to v1alpha2); the object key supports
  `{{.JobID}}` template substitution; empty-result jobs upload
  zero-byte objects so the presence-or-absence of the key
  remains a reliable post-job signal; content type is
  `application/x-ndjson`. The webhook sink POSTs (or PUTs) rows
  to the configured URL with bounded exponential-backoff retry
  on transient errors (3 attempts, 200/400/800 ms with jitter,
  retryable on connection-refused / 5xx / 429, fatal on first
  attempt for other 4xx); per-row when `BatchSize=0` (CRD
  default) or batched at N-row threshold otherwise. Every
  request carries the `User-Agent: spectre-engine/<version>`,
  `X-Spectre-Job-Id`, `X-Spectre-Driver`, `X-Spectre-Row-Count`
  header schema (auth deferred to v1alpha2). **Admission gating
  asymmetry** (ADR-0024 §5): Kafka and S3 hold engine-level
  state validated at startup (S3's env-unset arm logs INFO, not
  WARN — BYO-credentials mode covers IAM-role / SSO / profile,
  the production-typical shape); Webhook has no global state
  and gates per-job at runtime. Engine-side errors:
  `S3_UNAVAILABLE` / `S3_FIELD_REQUIRED` / `S3_UPLOAD_FAILED` /
  `WEBHOOK_FIELD_REQUIRED` / `WEBHOOK_POST_FAILED`. `engine.proto`
  evolves non-breakingly with nested `S3SinkConfig` (field 5,
  bucket/key/endpoint/region) and `WebhookSinkConfig` (field 6,
  url/method/batchSize) messages; `kafka_topic` (field 4) stays
  as a flat string for R4.4 wire compat. The reconciler's
  `validateOutputSink` unblocks both variants (defence-in-depth
  on per-variant required fields); new helpers
  `outputSinkS3Config` / `outputSinkWebhookConfig` parallel
  R4.4's `outputSinkKafkaTopic`. The Compose stack adds **MinIO**
  at `localhost:9000` (S3 API) + `localhost:9001` (web console)
  plus a one-shot bucket-bootstrap container that pre-creates
  `spectre-rows`. `.env.example` carries the `SPECTRE_S3_*`
  block. Justfile recipes: `engine-s3-test`,
  `engine-webhook-test`, `minio-console`, `minio-ls`. Two new
  sample manifests (`spectre_v1alpha2_scrapejob_s3.yaml`,
  `..._webhook.yaml`). The conformance suite gains
  `test_s3_sink.py` (one test against Compose MinIO via boto3) +
  `test_webhook_sink.py` (per-row + batched against an
  in-process aiohttp server, no Compose dep) — full suite at
  50 passed, 14 skipped (vs R4.4's 47 / 14 — the +3 are the
  new tests). The 13 / 12 / 6 capability invariant holds
  byte-for-byte. **Phase R5 closes with this PR — every
  v1alpha2 `OutputSink` variant is behaviourally implemented.**

### Changed

- **Devcontainer toolchain `BUF_VERSION` harmonised 1.45.0 →
  1.55.1 (R6.3 — `build/docker/versions.env` is the single
  source of truth).** ADR-0018 §3a records the harmonisation;
  Go / Node / Python / protoc were already aligned.
- **`spectre_v1alpha2_scrapejob_endpoint.yaml` sample (R6.3).**
  The `engineRef.endpoint` field flips
  `127.0.0.1:8090` → `engine:8090` to reflect the post-R6.3
  topology where the operator runs as a Compose service and
  resolves engine via the Compose default network's DNS. The
  comment block describes the unified `just kind-up && just
  compose-up` flow that replaces the R6.2 multi-terminal
  `op-run` walkthrough. The `_hello-hackernews.yaml`
  Service-form sample's comment block is corrected: Helm (R7.1)
  provisions the in-cluster Service; the post-R6.3 dev flow
  uses the Endpoint sample.
- **Application port range migrated from 9090–9093 to 8090–8093
  (R6.2 — ADR-0021 §4 / ADR-0025 §7).** The engine's bind port
  default flips 9090 → 8090; adapter Dockerfile `EXPOSE` /
  `ENV SPECTRE_ADAPTER_GRPC_PORT` defaults flip 9091/9092/9093
  → 8091/8092/8093; `core/control-plane/cmd/main.go`'s
  `defaultEngineEndpoint` flips to `127.0.0.1:8090`; the
  v1alpha2 `EngineServiceRef.Port` kubebuilder default flips to
  8090 (regenerated CRD updated); `.env.example`, every sample
  ScrapeJob manifest, the architecture docs, every example
  README, and the conformance demo CLI help text follow. Kafka's
  9092 broker port stays unmolested — the migration's reason
  for being. The native-binary `pw-run` / `sb-run` /
  `curl-imp-run` recipes are retired (no fallback —
  master-strategy §2.2 forbids "temporary" legacy fallbacks);
  `just engine-run` is renamed `just engine-run-native` and
  preserved as a debugging escape hatch with a comment block
  pointing at `compose-up`. `op-build-image` is renamed
  `op-image` for naming consistency with the other
  `<service>-image` recipes.
- **`JobRunner.Run` signature evolves to seven parameters
  (R5.1).** `s3Config *enginev1alpha1.S3SinkConfig` and
  `webhookConfig *enginev1alpha1.WebhookSinkConfig` join the
  R4.2 / R4.4 parameters. ADR-0019 §5 R5.1 addendum documents
  the trade-off: a `RunRequest` struct refactor is the right
  v1alpha2 shape but doing it inside an R5.1 PR that already
  adds two new sinks would double the reviewable surface area;
  the refactor lands as its own PR in v1alpha2.

### Removed

- **`just op-run` recipe (R6.3 — ADR-0025 §6 R6.3 update).** The
  R6.2 host-process operator flow is gone; the operator runs as
  the `control-plane` Compose service. No fallback path
  survives — master strategy §2.2 forbids "temporary" legacy
  fallbacks during refactor. `op-install-crds` /
  `op-uninstall-crds` are renamed `crds-install` /
  `crds-uninstall`; the old names are kept as one-cycle
  deprecation aliases (removed in R7.1).
- **`TestFailedOnUnsupportedSink` deleted (R5.1).** Every
  v1alpha2 `OutputSink` variant is now wired; the test's input
  set (a sink the reconciler rejects) has gone to zero.
  Preserving it would require fabricating an invalid sink
  (a fifth variant), which is itself a schema violation. The
  defence-in-depth `RejectsEmpty*` tests in
  `scrapejob_controller_test.go` continue to cover the
  remaining negative-path surface. ADR-0024 §1 records the
  deletion.

- **Kafka producer integration; `OutputSink.Kafka` unblocked
  (R4.4).** ADR-0023 §3 R4.4 addendum implements the engine's
  `rdkafka` producer end-to-end. The engine binary builds one
  shared `KafkaProducer` at startup via `KafkaProducer::from_env`
  and threads it through `EngineServiceImpl` as
  `Option<Arc<KafkaProducer>>`. Kafka admission gating follows
  ADR-0023 §6's optional-service pattern: an unreachable broker
  at startup logs a warning and the engine continues without
  Kafka; subsequent `RunJob`s with `output_sink_kind = "kafka"`
  fail fast at job-start time with `error_code = "KAFKA_UNAVAILABLE"`
  (or `"KAFKA_TOPIC_REQUIRED"` for an empty topic) — equivalent UX
  to admission rejection without a custom validating webhook.
  Kafka-sinked jobs publish one message per extracted row to the
  topic from `ScrapeJob.Spec.OutputSink.Kafka.Topic`,
  partition-keyed by job UUID so all rows for a job land on a
  single partition in extraction order, with headers `job_id` /
  `row_index` / `driver` / `timestamp` (ISO-8601). Producer
  config: `acks=all`, `enable.idempotence=true`,
  `compression.type=snappy`, `linger.ms=10` (tunable via
  `SPECTRE_KAFKA_LINGER_MS`). Delivery semantics:
  **at-least-once**; consumer-side idempotency on
  `(job_id, row_index)` is the documented user responsibility.
  `engine.proto` evolves non-breakingly with `kafka_topic` field
  (number 4); the control-plane reconciler unblocks the Kafka
  branch of `validateOutputSink` and forwards the topic via the
  evolved `JobRunner` interface (ADR-0019 §5 addendum). The
  `_NOT_YET_IMPLEMENTED` Kafka sample manifest is renamed to
  `spectre_v1alpha2_scrapejob_kafka.yaml` — a functional
  example. The Compose stack gains **Apache Kafka 3.7.1 in KRaft
  mode** (production parity with R7.1's Strimzi target,
  superseding the original §3 Redpanda single-binary mention)
  and **Redpanda Console** as the topic / offset / message-browser
  UI at <http://localhost:8080>. `.env.example` carries
  `SPECTRE_KAFKA_BROKERS`. Justfile recipes:
  `engine-kafka-test`, `kafka-console`, `kafka-topics`,
  `kafka-consume`. Conformance suite gains
  `tools/conformance/tests/test_kafka_sink.py` — one
  engine-level E2E test (the kafka path is engine behaviour,
  not driver-level capability) that spawns the engine binary +
  Playwright adapter, submits a `RunJob` with the kafka sink,
  drains the topic via `confluent_kafka.Consumer`, and asserts
  partition keys + headers. The 13 / 12 / 6 capability
  invariant holds byte-for-byte. **Phase R4 closes with this PR.**
  rdkafka 0.36 with `cmake-build + ssl-vendored + tokio`
  features adds 10-15 minutes to the first clean engine build
  (OpenSSL compile from source) for forward-compat with
  v1alpha2 SASL/mTLS; cached thereafter. The OpenSSL stack
  vendored with librdkafka is *deliberately* separate from
  sqlx's rustls 0.23 — the two TLS stacks coexist without
  conflict because they are different libraries (C vs
  Rust-native).
- **Redis adapter session externalization with restart
  invalidation (R4.3).** ADR-0023 §4's keyspace lands across all
  three reference adapters: each adapter writes session metadata
  to `session:<adapter>:<session_id>` at `Initialize` with a
  1-hour idle TTL refreshed on every successful non-Initialize
  RPC. Each adapter process generates a UUID at startup
  (overridable via `SPECTRE_ADAPTER_INSTANCE_ID` for the
  conformance suite only) and stamps it on the metadata
  document; non-Initialize RPCs read the metadata, compare the
  stored `adapter_instance_id` against the live process value,
  and surface foreign-instance sessions as gRPC `UNAVAILABLE`
  with the message _"session belongs to a different adapter
  instance; client must re-Initialize"_ — the §5
  restart-invalidation contract documented in the new ADR-0023
  §5 R4.3 addendum. `Initialize` awaits the Redis write before
  responding so the local registry never drifts ahead of Redis;
  `Close` validates first, then evicts locally and best-effort
  deletes the Redis key (TTL is the safety net per phase prompt
  §4.6). Per-language libraries: Playwright uses
  `ioredis` + `ioredis-mock`; SeleniumBase uses `redis>=5.0` +
  `fakeredis`; curl-impersonate uses `go-redis/v9` +
  `redismock/v9` + `miniredis/v2`. Each adapter PINGs Redis at
  startup and exits non-zero when unreachable (ADR-0023 §6 —
  Redis required). The Compose stack gains `redis:7-alpine`
  (AOF + LRU eviction); `.env.example` carries
  `SPECTRE_REDIS_URL`. Conformance suite gains
  `tools/conformance/tests/test_session_restart_invalidation.py`
  (one test per adapter) exercising the contract via parallel
  adapter instances with distinct `instance_id_overrides`. The
  13 / 12 / 6 capability invariant holds byte-for-byte. Engine
  and control plane are unchanged operationally — ADR-0023 §7
  reserves Redis access to adapters only.
- **PostgreSQL integration end-to-end (R4.2).** ADR-0023 §2's
  schema lands as a versioned, immutable migration file in
  `core/engine/migrations/`. The engine gains an `sqlx`-backed
  `db` module — connection pool, embedded migration runner, four
  typed query functions — and writes a `jobs` row at status
  `'running'` on every admitted `RunJob`, appends `job_rows`
  audit rows for stdout-sinked jobs, and persists the terminal
  `mark_completed` / `mark_failed` UPDATE. The control plane
  gains a `pgx/v5` + pgxpool wrapper and a reconciler that reads
  `jobs` by `ScrapeJob.UID` on Running-phase entry, syncing
  terminal status from Postgres without re-running.
  `engine.proto` evolves non-breakingly with a new
  `output_sink_kind` field (proto3 default empty, engine treats
  empty as `'stdout'`). The JobRunner interface (ADR-0019 §5)
  evolves to accept `jobID uuid.UUID` and
  `outputSinkKind string`; the abstraction is preserved per the
  §5 evolution rule, with the addendum recording the breakage of
  the R3.1 byte-for-byte vindication. A `docker-compose.yml` at
  the repo root brings up `postgres:16-alpine` for local dev;
  `.env.example` documents the env var set; the justfile gains
  `compose-{up,down,logs,reset}` recipes. The
  `SPECTRE_POSTGRES_URL` env var is required at startup for both
  engine and operator; ADR-0023 §6's "no Postgres-less mode"
  holds.
- Architectural commitment to a microservices refactor recorded
  in [ADR-0020](docs/adr/0020-microservices-architecture-supersession.md).
  No code changes in this release; subsequent phase PRs (R2–R8)
  deliver the implementation. Live progress is tracked in
  [`docs/refactoring-status.md`](docs/refactoring-status.md).
- [ADR-0023](docs/adr/0023-stateful-services-architecture.md)
  records the stateful-services architecture for Phase R4
  (R4.1, documentation-only). Three services land together:
  PostgreSQL for job state and the audit `job_rows` table
  (R4.2; engine-side `sqlx` and control-plane-side `pgx/v5`),
  Kafka for the `OutputSink.Kafka` streaming surface (R4.4;
  `rdkafka` producer publishing one message per JSONL row to
  topic `spectre.rows.<workspace>`, partitioned by job UUID
  with `job_id` / `row_index` / `driver` / `timestamp`
  headers), Redis for adapter session metadata (R4.3; `ioredis`
  / `redis-py` / `go-redis/v9` per language at the
  `session:<adapter>:<session_id>` keyspace with a 1-hour idle
  TTL). The ADR commits the deployment-shape matrix (Postgres
  + Redis required everywhere, Kafka admission-gated when an
  operator runs it), the env-var configuration convention
  extending ADR-0021 §5 (`SPECTRE_POSTGRES_URL`,
  `SPECTRE_KAFKA_BROKERS`, `SPECTRE_REDIS_URL`), the
  per-service network topology, and the migration discipline
  (sqlx forward-only versioned SQL applied at engine startup).
  ADR-0023 §5 commits the *restart-invalidation* contract for
  adapter sessions: clients hold session_ids only for the
  lifetime of the adapter Pod that allocated them; sticky
  sessions and warm recovery were evaluated and rejected.
- Initial repository structure and foundational documents
- Driver Protocol skeleton at v1alpha1
- Skeleton implementations for three reference adapters
- gRPC standard health check (`grpc.health.v1.Health`) registration
  in every adapter (R2.2, ADR-0021 §6). The conformance harness
  polls `Check` until SERVING as the readiness signal; production
  deployments wire the same endpoint into Compose / Kubernetes
  readiness probes.
- `proto/grpc/health/v1/health.proto` vendored verbatim from the
  canonical gRPC source so the Playwright TS adapter can produce
  Connect-RPC bindings; the Go and Python adapters consume their
  ecosystem libraries (`google.golang.org/grpc/health` and
  `grpcio-health-checking`) directly. Buf lint exempts the
  vendored file so it stays byte-identical to the upstream source.
- `KNOWN_BREAKAGE.md` documented the R2.2 → R2.3 engine ↔ adapter
  transport mismatch. R2.3's first commit deleted the file as the
  engine-side TCP dial landed.
- Internal `spectre.engine.v1alpha1.Engine` service contract
  (R2.3) at `proto/spectre/engine/v1alpha1/engine.proto`. A
  single streaming RPC, `RunJob`, takes an inline DSL document
  and streams `Row` events followed by a terminal `Completed` or
  `Failed`. Cancellation is gRPC stream cancellation; status,
  metrics, and listing are control-plane responsibilities.
  Bindings are generated for Rust (via `core/engine/build.rs`)
  and Go (via `proto/buf.gen.engine.yaml`); Python and TS are
  intentionally not generated.
- Engine becomes a stateless gRPC service (R2.3, ADR-0020 §3).
  The binary at `core/engine/src/bin/spectre.rs` registers
  `spectre.engine.v1alpha1.Engine` and `grpc.health.v1.Health`
  on a single TCP listener (default `0.0.0.0:9090`, override
  via `SPECTRE_ENGINE_PORT`) and shuts down cleanly on
  SIGTERM/SIGINT. Adapter discovery flows through
  `AdapterRegistry`, which reads
  `SPECTRE_PLAYWRIGHT_ENDPOINT` /
  `SPECTRE_SELENIUMBASE_ENDPOINT` /
  `SPECTRE_CURL_IMPERSONATE_ENDPOINT` (defaults
  `127.0.0.1:909{1,2,3}`).
- Engine architecture document at
  `docs/architecture/engine.md` describing the service contract,
  discovery model, health-check registration, CLI-retirement
  rationale, and v1alpha1 statelessness invariant.
- Control plane is now a thin gRPC client of the engine service
  (R3.1, ADR-0020 §5). The new
  `core/control-plane/internal/runner/engine_client.go` implements
  the `JobRunner` interface by dialling
  `spectre.engine.v1alpha1.Engine.RunJob` per invocation and
  forwarding every `Row.json_line` event into the supplied
  writer. ADR-0019 §5's interface seam is vindicated at the
  second substitution: three implementations (`StubRunner`,
  `SubprocessRunner`, `EngineClientRunner`) share one signature
  and the reconciler is unaware of which is wired. The R2.3 → R3.1
  transitional window (operator broken at runtime) closes here.
- Operator startup honours `--engine-endpoint=<host:port>` and
  `SPECTRE_ENGINE_ENDPOINT` (default `127.0.0.1:9090`) — Compose
  (R6.2) and Helm (R7.1) renderings will inject the service-
  network address. Plain-text gRPC for v1alpha1 per ADR-0022 §6;
  TLS / mTLS deferred to v1alpha2.
- `ScrapeJob` CRD evolved to `spectre.io/v1alpha2` (R3.2). New
  fields:
  - `spec.engineRef` (optional, CEL-validated): per-job engine
    selection via Kubernetes Service reference (rendered as
    `<name>.<namespace>.svc.cluster.local:<port>`) or direct
    host:port endpoint. Nil falls back to the operator's
    startup-time `SPECTRE_ENGINE_ENDPOINT` configuration.
  - `spec.outputSink` (required, CEL-validated): discriminated
    union over `stdout`, `kafka`, `s3`, `webhook` variants. R3.2
    wires only `stdout` end-to-end; the other three variants are
    schema-only — the reconciler rejects them at the
    `Pending → Running` boundary with explicit "not yet
    implemented (R4.4 / R5.1)" errors. Schema-ahead-of-
    functionality is intentional and documented in
    `config/samples/spectre_v1alpha2_scrapejob_kafka_NOT_YET_IMPLEMENTED.yaml`.
  - `status.resolvedEngineEndpoint`: records the host:port the
    operator actually dialed (debug aid for `EngineRef`
    resolution).
  CEL `XValidation` rules (stable in Kubernetes 1.25+) enforce
  the discriminated-union shapes at admission, removing the
  operational overhead of custom validating webhooks.
- `core/control-plane/config/samples/spectre_v1alpha2_*.yaml`
  (R3.2): five samples covering Service `EngineRef` for the
  three reference adapters, an `EngineRef.Endpoint` variant for
  ad-hoc local testing, and one schema-only Kafka sample
  (`_kafka_NOT_YET_IMPLEMENTED.yaml`) documenting the schema-
  ahead-of-functionality gap.

### Changed

- ADR-0019 (control plane and ScrapeJob CRD) gains an "Update
  (R3.2)" addendum recording v1alpha2 as the only registered
  version: §1, §2, §4 carry forward unchanged; §3 (subprocess
  execution model) was already superseded by ADR-0020 in R1.1;
  §5 (JobRunner interface) preserved through a construction-
  site refactor (per-Reconcile runner construction via a
  `RunnerFactory` closure) — the interface signature is byte-
  for-byte unchanged; §6 (OutputSink stdout-only commitment)
  honoured at the runtime level (the discriminated union now
  carries Kafka / S3 / Webhook field shapes, but the reconciler
  rejects them at admission until R4.4 / R5.1 wire them).
- ADR-0008 (UDS transport), ADR-0009 (session lifecycle),
  ADR-0019 (subprocess-in-pod) carry "Update (R1.1, ADR-0020)"
  notes recording per-section supersession. ADR-0019 §5
  (`JobRunner` interface) gains an "Update (R3.1, vindication)"
  addendum recording the seam's stability across three
  implementations. ADR-0013 (CLI as engine binary) is superseded
  in full. ADR-0012 (engine DSL + execution pipeline) carries an
  "Update (R2.3, ADR-0020)" note recording the launcher-contract
  supersession; §§1-3, 5, 6 are preserved unchanged. The ADR
  index reflects these changes.
- Adapter transport for all three reference adapters (Playwright,
  SeleniumBase, curl-impersonate) and the conformance harness
  switched from Unix-domain-socket gRPC to TCP gRPC (R2.2,
  ADR-0021 + ADR-0022). Each adapter binds `0.0.0.0:<port>` where
  the port is read from `SPECTRE_ADAPTER_GRPC_PORT`; the
  conformance harness allocates a free localhost port and injects
  it into the subprocess env. The wire-level driver protocol
  contract is unchanged. The 13 / 12 / 6 capability lists for
  Playwright / SeleniumBase / curl-impersonate are preserved
  byte-for-byte.
- `driver.yaml` schema for every adapter retires the `transports:`
  block; the spawn directive moves into `runtime.command`
  (transitional — R6.2's Compose stack supersedes the
  harness-spawn flow entirely). The conformance harness reads
  `runtime.command` rather than `transports[0].command`.
- `just <adapter>-run` recipes take a port argument (default
  matching the canonical port from ADR-0021 §4) instead of a
  socket path. The recipes survive R2.2 as developer
  conveniences and are scheduled for retirement in R6.2.
- Engine `Client::dial` (R2.3) accepts `host:port` or
  `grpc://host:port` endpoints and connects via
  `tonic::transport::Endpoint`'s TCP path. The
  `tower::service_fn` UDS connector and the
  `:authority=localhost` Node-http2 workaround are gone.
- Engine binary (R2.3) is service-only. The `run`, `validate`,
  and standalone `version` subcommands the CLI exposed via clap
  are gone with ADR-0013's CLI surface (ADR-0020 §3). The binary
  has no flags beyond `--help`/`--version` and its single
  responsibility is starting the gRPC service.
- `justfile` and `.github/workflows/ci.yml` (R2.3) drop the
  `spectre version` / `spectre validate` smoke steps. The
  release-binary smoke becomes "the binary built / exists at the
  canonical path"; deeper start-and-probe smokes are deferred to
  the Compose stack (R6.2). The `operator-smoke-kind` CI job is
  gated `if: false` until R3.1 lands `EngineClientRunner`.
- Example READMEs (R2.3) document the manual `grpcurl` flow
  honestly. The `seleniumbase-navigate` and
  `curl-impersonate-fetch` directories are deleted; they
  existed to demonstrate the legacy CLI's "minimum viable
  adapter run" and the `*-extract` examples cover the same
  adapters with richer functionality.
- `curl-imp-lint` pinned to `GOTOOLCHAIN=go1.25.3` mirroring the
  existing `cp-lint` pattern; without the pin `golangci-lint
  v2.8.0` (built with go1.25.5) panics on the Go 1.26 stdlib
  loaded by an unconstrained toolchain.
- `proto/buf.gen.yaml` pins the python plugins to revisions that
  emit gencode compatible with the protobuf 6.x runtime
  (`grpcio-health-checking==1.80.0` caps protobuf at <7.0.0).

### Removed

- **BREAKING**: `core/control-plane/api/v1alpha1/` (R3.2) — the
  ScrapeJob CRD's first version. Per master strategy §3.3, the
  v1alpha1 → v1alpha2 migration is a breaking change without a
  conversion webhook (no production users to migrate); v1alpha1
  ScrapeJob CRs in clusters on upgrade are orphaned. Upgrade
  procedure: `kubectl delete scrapejob --all` → install v1alpha2
  CRD → apply v1alpha2 CRs (see
  `docs/architecture/control-plane.md` and the ADR-0019 R3.2
  addendum). The retired surface includes
  `api/v1alpha1/{groupversion_info.go,scrapejob_types.go,
  zz_generated.deepcopy.go}`, the
  `config/samples/spectre_v1alpha1_scrapejob_*.yaml` set, and
  the v1alpha1 entry from `core/control-plane/PROJECT`.
- The Unix-domain-socket transport across all three reference
  adapters and the conformance harness (R2.2). No fallback path
  survives — strategy prompt §2.2 forbids "temporary" legacy
  fallbacks during refactor. The retired surface includes
  `resolveSocketPath` / `resolve_socket_path` resolvers, the
  `--socket` CLI flag, the `SPECTRE_DRIVER_SOCKET` env var, the
  stale-socket unlink logic, the `ready unix:<path>` stdout
  readiness banner, and the `transports:` block in every
  `driver.yaml`.
- `core/engine/src/launcher.rs` (R2.3) — 628 lines of subprocess
  management. The engine no longer spawns adapters; it dials
  them as long-running services via `AdapterRegistry`.
  `LauncherError` and `EngineError::Launcher` go with it.
- `core/engine/tests/integration.rs` (R2.3) — required
  `PLAYWRIGHT_AVAILABLE=1` and Chromium and exercised the same
  engine → adapter loop the conformance suite already covers
  across all three adapters.
- `Engine::new(adapters_path)`, `Engine::run_job(yaml, job_dir)`,
  `Engine::run_plan(plan, job_dir)`, `Engine::validate_only`
  (R2.3). The legacy CLI-shaped API gives way to
  `Engine::from_env` / `Engine::with_registry` and a single
  `Engine::run_plan_with_sink(plan, sink)` entry point that the
  gRPC server's `RunJob` handler drives.
- Engine Cargo.toml deps no longer used (R2.3): `nix` (SIGTERM
  helper), `regex` (readiness-line matching), `tower` (UDS
  connector via `service_fn`), `hyper-util` (production —
  `TokioIo` UDS wrapper), `clap` (CLI subcommand parser),
  dev-only `hyper` and `http-body-util` (integration-test
  fixture HTTP server). `tokio`'s `process` feature is dropped.
  `tonic-health` is added in their place.
- `examples/seleniumbase-navigate/` and
  `examples/curl-impersonate-fetch/` (R2.3) — navigate-only CLI
  demos; the `*-extract` siblings cover the same adapters.
- `core/control-plane/internal/runner/subprocess.go` (R3.1) —
  shelled out to the engine's retired `spectre run` CLI surface
  and bundled the JSONL scanner. `EngineClientRunner` replaces
  it with a gRPC stream consumer; `subprocess_test.go` and the
  `testdata/fake_spectre.go` fixture binary go with it.
- The operator image's bundled engine binary
  (`/usr/local/bin/spectre`) and three adapter trees
  (`/opt/spectre/adapters/{playwright,seleniumbase,curl-impersonate}/`)
  retire with the bundled-image execution model (R3.1). The
  Microsoft Playwright runtime base, the apt overlay for Google
  Chrome + ChromeDriver, the curl-impersonate release tarball
  download, and the per-adapter builder stages are gone. The new
  image is a Go static binary on
  `gcr.io/distroless/static:nonroot` (~50 MB on disk).
- `core/control-plane/hack/smoke-kind.sh` and the gated
  `operator-smoke-kind` CI job (R3.1) — drove the bundled-image
  in-cluster smoke. The multi-service end-to-end smoke returns
  with the Compose stack (R6.2) and the Helm production smoke
  (R7.2).
