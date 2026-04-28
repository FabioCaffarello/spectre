---
status: accepted
date: 2026-04-28
deciders: [Fabio Caffarello]
---

# Output sinks: S3 and HTTP webhook

## §1 — Context and Problem Statement

R3.2 introduced the v1alpha2 `OutputSink` discriminated union
with four variants: `Stdout`, `Kafka`, `S3`, and `Webhook`
(`core/control-plane/api/v1alpha2/scrapejob_types.go` lines
99–123). The reconciler accepted only `Stdout` at admission;
the other three variants were rejected with explicit "not
yet implemented (R4.4 / R5.1)" errors so the schema landed
forward-compatible without committing implementation.

R4.4 wired Kafka end-to-end: the engine ships an `rdkafka`
producer, the reconciler unblocks Kafka admission, Compose
adds Apache Kafka in KRaft mode plus the Redpanda Console
UI. ADR-0023 §3 R4.4 addendum recorded the architecture.

R5.1 closes Phase R5 by wiring the remaining two sinks: S3
(via `aws-sdk-s3`) and HTTP webhook (via `reqwest`). After
R5.1 the `OutputSink` discriminated union is fully
behaviourally implemented and `validateOutputSink` accepts
every variant the schema admits.

This ADR records the architectural decisions that shape the
two new sinks. ADR-0023 §3 (Kafka) is the precedent; the new
sinks borrow its admission-gating pattern with a meaningful
asymmetry that §5 below names. ADR-0019 §5 (the JobRunner
seam) gains an R5.1 addendum recording the parameter
accumulation; this ADR cross-references that addendum but
does not reproduce it.

`TestFailedOnUnsupportedSink` in
`scrapejob_controller_test.go` is **deleted** as part of
R5.1: every variant in the v1alpha2 schema is now wired, so
the test's input set has gone to zero. Preserving it would
require fabricating an invalid sink (a fifth variant), which
is itself a schema violation. The deletion is recorded here
and in CHANGELOG.

## §2 — Decision summary

R5.1 commits the engine to:

- **AWS SDK for Rust (`aws-sdk-s3` 1.x)** for the S3 sink.
  Production-parity choice over `rust-s3`; supports MinIO /
  Cloudflare R2 / Wasabi via the `endpoint_url` builder
  parameter so dev and prod share the same client.
- **`reqwest` 0.12** for the webhook sink. De-facto Rust HTTP
  client; aligns rustls 0.23 with sqlx (no diamond conflict).
- **In-memory buffering** for S3: rows accumulate during the
  job, single `PutObject` at completion. Multipart streaming
  is a v1alpha2 concern (§8).
- **Per-row or batched POST** for webhook, controlled by
  `WebhookSink.BatchSize`. Default (`0`) is one HTTP request
  per row.
- **At-least-once delivery** for both sinks. Webhook adds
  bounded exponential-backoff retry (3 attempts) for transient
  errors; non-429 4xx responses are fatal on the first attempt.
- **Asymmetric admission gating** (§5). Kafka and S3 hold
  engine-level state validated at startup; webhook has no
  global state and gates per-job at runtime.
- **MinIO** in the Compose stack as the local-dev S3 endpoint,
  alongside a one-shot bucket-bootstrap container that
  pre-creates `spectre-rows`.

Out-of-scope for v1alpha1: multipart S3, S3 server-side
encryption, webhook authentication (HMAC, bearer tokens),
webhook DLQ, per-job credential override, mTLS to either.
§8 enumerates.

## §3 — S3 sink

**Library choice.** `aws-sdk-s3` 1.x — the official AWS SDK
for Rust. Production-parity: deployments against real S3 use
the same client model. Custom-endpoint support via
`Client::builder().endpoint_url(...)` covers MinIO, R2,
Wasabi, Backblaze B2 with no code branching.

`rust-s3` (third-party, smaller) was evaluated and rejected:
no AWS-team backing, less production exercise, awkward
endpoint-customisation model.

Feature flags:

```toml
aws-sdk-s3 = { version = "1", default-features = false, features = [
    "rt-tokio",
    "rustls",
    "behavior-version-latest",
] }
```

`default-features = false` skips the `default-tls`
(native-tls) chain that would pull a second TLS stack.
`rustls` aligns with sqlx's rustls 0.23 — single rustls in
the tree. `behavior-version-latest` opts into the SDK's
stable behaviour pinning so a 1.x minor bump cannot silently
shift auth precedence or retry policy.

**Buffering model.** Per-job in-memory accumulation, single
`PutObject` at job completion. The engine appends each
extracted row to a `Vec<u8>` buffer formatted as JSON Lines
(one row per line, trailing newline). When the executor
finishes, the engine renders the object key from the
template, calls `S3Uploader::upload_jsonl(bucket, key,
body)`, and the job terminates with `Completed` (or `Failed`
if the upload errored).

A job that produces 1M rows × 1KB each = 1GB in engine
memory before `PutObject` runs. v1alpha1 documents this as a
known limitation (§8). Multipart streaming
(`UploadPart` / `CompleteMultipartUpload`) is the v1alpha2
fix.

**Object key templating.** `S3Sink.Key` supports
`{{.JobID}}` template substitution at upload time. The
engine implements substitution by hand — no `tera` /
`handlebars` / `minijinja` dependency. Only `{{.JobID}}` is
recognised in v1alpha1; v1alpha2 may add `{{.Driver}}`,
`{{.Timestamp}}`, etc., per real-deployment demand.

Example: `Key = "scrapes/{{.JobID}}/rows.jsonl"` renders to
`scrapes/3f2504e0-4f89-11d3-9a0c-0305e82c3301/rows.jsonl`
for a job with that UUID.

**Empty-result handling.** A job whose executor produces
zero rows still uploads an empty (zero-byte) object. The
post-job presence-or-absence of the object is then a
reliable signal — present-empty means "job ran cleanly,
extracted nothing", absent means "job did not complete".
Skipping the upload would conflate the two states.

**Content-Type.** `application/x-ndjson` per the
[NDJSON / JSON Lines convention](https://github.com/ndjson/ndjson-spec).
Set on the `PutObject` call so consumers that introspect
`HeadObject` see the format type without parsing.

**Region and endpoint.** `S3Sink.Region` defaults to
`us-east-1` via the CRD's kubebuilder default. MinIO accepts
any string. `S3Sink.Endpoint` (optional) overrides the SDK
default — set to `http://minio:9000` (in-cluster reference)
or `http://localhost:9000` (Compose-native) for local dev,
left unset for real S3. The CRD field beats the engine's
`SPECTRE_S3_ENDPOINT` env var per-job, so a single engine
binary can serve mixed-endpoint jobs.

**Credentials.** Environment-driven keys
(`SPECTRE_S3_ACCESS_KEY_ID` / `SPECTRE_S3_SECRET_ACCESS_KEY`)
override the SDK's default credential chain. When the env
keys are absent, the SDK uses its default chain
(IAM role, AWS SSO, profile, etc.). v1alpha1 has **no
per-job credential override**: the engine binary holds one
credential set; mixed-credential workloads are a v1alpha2
concern.

**Delivery semantics.** **At-least-once.** A successful
`PutObject` returns 200 OK with an ETag — the job is then
durable. An error mid-upload (network blip, 503, throttling)
fails the job with `S3_UPLOAD_FAILED` and the operator
re-drives by recreating the ScrapeJob. Re-driving overwrites
the same key (S3 PUT is replace-not-append), so duplicate
content is bounded to "the last attempt's full output" — no
multi-write divergence.

The CRD's `Key` template is the operator's responsibility:
including `{{.JobID}}` makes re-drives go to a fresh key
(because the new ScrapeJob has a fresh UID); omitting it
makes them overwrite. Both shapes are supported.

## §4 — Webhook sink

**Library choice.** `reqwest` 0.12 — de-facto Rust HTTP
client, builds on `hyper`. Single TLS stack
(`rustls-tls-native-roots`) aligned with sqlx's rustls
0.23, picks up the OS trust store so corporate proxies /
private CAs work without env gymnastics.

```toml
reqwest = { version = "0.12", default-features = false, features = [
    "rustls-tls-native-roots",
    "json",
    "gzip",
] }
```

`hyper` direct was evaluated — lower-level than the engine's
needs; reqwest's connection pool and error model save code.
`surf` was evaluated — smaller but smol/async-std-oriented,
diverges from the engine's tokio commitment.

**Per-row vs batched.** `WebhookSink.BatchSize` controls the
flush model:

- `BatchSize = 0` (CRD default): one HTTP request per row.
  Body is the row's JSON Lines string + `\n`. Lower
  throughput, simpler consumer logic.
- `BatchSize > 0`: rows accumulate in memory until N rows
  are collected, then flushed as a single request. Body is
  N JSON Lines lines concatenated. Higher throughput,
  consumer must split on `\n`.

The default is per-row because v1alpha1 prioritises consumer
simplicity. Production deployments expecting high throughput
override.

**Retry policy.** Bounded exponential backoff for transient
errors:

- 3 attempts total
- Base delay 200 ms; doubled per attempt (200 / 400 / 800 ms)
  with jitter
- Retryable: connection refused, 5xx status codes, 429 status
- Fatal on first failure: 4xx other than 429, malformed URL,
  body-encoding errors

After retries exhaust, the job terminates with
`WEBHOOK_POST_FAILED` and the matching error message
(status code + truncated body excerpt). The pattern mirrors
Kafka's `KAFKA_PUBLISH_FAILED` from ADR-0023 §3.

The retry policy is hard-coded in v1alpha1; an env var to
override is a v1alpha2 concern (§8). Real deployments that
need different policy override at the receiver-side
infrastructure layer (load balancer retry, CDN edge caching
of writes).

**Method.** `POST` (default) or `PUT` per `WebhookSink.Method`.
The CRD enforces the enum; the engine validates
defence-in-depth.

**Headers.** Every webhook request carries:

- `User-Agent: spectre-engine/<version>` — engine version
- `Content-Type: application/x-ndjson` — single row or batch
- `X-Spectre-Job-Id` — job UUID
- `X-Spectre-Driver` — `playwright` / `seleniumbase` /
  `curl-impersonate`
- `X-Spectre-Batch-Size` — only when `BatchSize > 0`
- `X-Spectre-Row-Count` — number of rows in this request's
  body

Auth headers (`Authorization`, `X-Hub-Signature`, etc.) are
**not** added — auth is v1alpha2 (§8).

**Delivery semantics.** **At-least-once.** A 2xx response
acknowledges acceptance. The engine retries 5xx / 429 with
backoff; receivers that respond 2xx after duplicate writes
must implement consumer-side dedup
(`X-Spectre-Job-Id` + row position is the
deduplication key — the same shape as Kafka's
`(job_id, row_index)`).

A network failure mid-batch fails the job after retries;
re-driving the ScrapeJob produces a fresh job with fresh
headers. Receivers must handle inter-job duplicates the
same way Kafka consumers do.

## §5 — Admission gating asymmetry

The cross-cutting decision. Kafka (R4.4) gates at engine
startup: `KafkaProducer::from_env` validates broker
reachability via `fetch_metadata`; on success the engine
holds `Some(Arc<KafkaProducer>)` and accepts kafka-sinked
jobs, on failure it holds `None` and Kafka jobs fail fast
at job-start with `KAFKA_UNAVAILABLE` (ADR-0023 §3 R4.4
addendum).

R5.1 generalises the pattern with one asymmetry: **S3 gates
at startup like Kafka, webhook gates at runtime per job.**

**S3 — startup gate, soft fail.**
`S3Uploader::from_env` parses `SPECTRE_S3_*` env vars and
constructs an `aws_sdk_s3::Client`. Three startup arms in
the engine binary:

- **Ok**: log INFO "s3 uploader ready: endpoint=…"; thread
  `Some(Arc<S3Uploader>)` through to `EngineServiceImpl`.
- **Err(NotConfigured)**: log INFO "s3 uploader not
  configured: SPECTRE_S3_* env unset. OutputSink.S3 jobs
  will fail fast with S3_UNAVAILABLE." Thread `None`.
- **Err(other)**: log WARN with the underlying error.
  Thread `None`.

Note the asymmetry vs Kafka's R4.4 pattern: the env-unset
branch is **INFO**, not WARN. Production deployments
typically rely on the AWS default credential chain
(IAM role, SSO, profile) without explicit
`SPECTRE_S3_*` env vars; "env unset" is the *expected*
production shape, not a configuration mistake. WARN would
be misleading.

S3 jobs against `None` fail fast at job-start with
`S3_UNAVAILABLE`. Empty bucket / key fails with
`S3_FIELD_REQUIRED`. The pattern matches Kafka's
pre-flight model exactly — same UX without an admission
webhook.

**Webhook — per-job gate, runtime fail.**
`WebhookClient::new()` is **infallible**. It builds a single
shared `reqwest::Client` (connection-pooled, kept alive for
the engine's lifetime). The startup logs do not mention
webhook — there is no global state to validate.

The `Arc<WebhookClient>` field on `EngineServiceImpl` is
always `Some(...)`. Admission happens at the executor when
the first POST attempt connects (or fails to). A job with
an unreachable URL surfaces as `WEBHOOK_POST_FAILED` after
retries exhaust — no special pre-flight branch.

The architectural distinction: **Kafka and S3 hold
engine-level state** (broker connection / S3 client
configured against an endpoint). **Webhook is fully per-job**
— every URL is its own dial; the engine has no
"webhook unavailable" state to surface at startup.

This shapes where errors materialise. Kafka and S3 fail at
job-start (before the executor runs) when their dependency
is misconfigured. Webhook can only fail mid-job, after the
adapter has produced rows. The trade-off is acceptable for
v1alpha1 — webhook is the per-job-config sink by design.

A future fifth sink contributor (GCS, Azure Blob, Pub/Sub,
Datadog Logs) consults this section to decide which model
applies. Engine-level state with a startup probe? Use the
S3 pattern. No global state, every job is its own dial?
Use the webhook pattern.

## §6 — Postgres + sink coexistence

ADR-0023 §2 commits Postgres as the durable store for `jobs`
state and (for stdout-sinked jobs only) the `job_rows`
audit table. ADR-0023 §3 R4.4 addendum extended this:
**Kafka-sinked jobs write to `jobs` but skip `job_rows`** —
Kafka itself is the data destination, not an audit aside.

R5.1 generalises: **S3 and Webhook also skip `job_rows`.**
The data lives in S3 (the object body) or at the consumer
endpoint (the receiver's persistence layer); a Postgres
audit copy would double-write without serving a query the
sink itself does not.

The `jobs` table records `output_sink_kind` for every job.
A Postgres reader can answer "what sinks did we run today"
with:

```sql
SELECT output_sink_kind, COUNT(*)
FROM jobs
WHERE created_at > now() - interval '1 day'
GROUP BY output_sink_kind;
```

The CHECK constraint on `output_sink_kind` from ADR-0023 §2
already admits the four canonical values (`stdout`, `kafka`,
`s3`, `webhook`). No schema migration in R5.1.

## §7 — Library pinning

| Library                | Version | Feature flags                                        |
|------------------------|---------|------------------------------------------------------|
| `aws-sdk-s3`           | 1.x     | `rt-tokio`, `rustls`, `behavior-version-latest`      |
| `aws-config`           | 1.x     | `rt-tokio`, `rustls`, `behavior-version-latest`      |
| `aws-credential-types` | 1.x     | `hardcoded-credentials`                              |
| `reqwest`              | 0.12    | `rustls-tls-native-roots`, `json`, `gzip`            |
| `axum` (dev only)      | 0.7     | `http1`, `tokio`                                     |

`behavior-version-latest` is critical for the AWS SDK: it
opts into the SDK's stable behaviour pinning so a 1.x minor
bump cannot silently shift auth precedence, retry policy,
or signing version. Cargo's lockfile carries the exact 1.x
patch; `behavior-version-latest` is a feature flag that
*selects which behaviour group the major-version SDK
exposes*, independent of the patch.

`aws-credential-types`'s `hardcoded-credentials` feature
exposes the
`Credentials::from_keys(access_key, secret_key, token)`
constructor used when `SPECTRE_S3_ACCESS_KEY_ID` /
`SPECTRE_S3_SECRET_ACCESS_KEY` are set. Without the feature,
the SDK only accepts credentials from its default chain.

`reqwest`'s `rustls-tls-native-roots` aligns with sqlx's
rustls 0.23. `default-tls` (native-tls) would pull a second
TLS stack and break the single-rustls invariant.

`axum` is a dev-dependency only — used by
`webhook_integration.rs` to spin up an in-process test
server. The engine crate already pulls hyper transitively
via tonic 0.13, so axum's added cost is small. `wiremock`
was evaluated — heavier abstraction, less control over the
exact status sequence the retry test needs.

## §8 — Out of scope

These are recorded as v1alpha2 concerns. Implementing any
of them in R5.1 would push the PR past the cohesion
threshold §2.4 of the master strategy commits.

- **S3 multipart upload** — single `PutObject` per job in
  v1alpha1. Jobs >5GB exceed S3's PutObject limit.
  Multipart (`UploadPart` / `CompleteMultipartUpload`) is
  the fix.
- **S3 server-side encryption parameters** — SSE-KMS / SSE-C
  arguments on `PutObject`. Security-audit-level decision;
  v1alpha2.
- **Per-job S3 credentials** — access key / secret key in
  the CRD spec. Security audit (where do the keys live in
  etcd, how are they rotated, who can read them); v1alpha2.
- **Webhook authentication** — HMAC signing
  (X-Hub-Signature-256), bearer tokens, signed-request
  envelopes. Auth header schema decision; v1alpha2.
- **Webhook DLQ** — failed-payload archival for re-drive.
  Storage shape decision (Kafka topic? S3 bucket?
  Postgres table?); v1alpha2.
- **Webhook URL whitelist** at admission — prevent
  exfiltration to arbitrary endpoints. Policy decision;
  v1alpha2.
- **Per-row retries that vary per error class** —
  v1alpha1 ships a single bounded-exponential-backoff
  policy; finer control needs a real workload to design
  against.
- **Configurable webhook retry count / backoff** —
  hard-coded `(3 attempts, 200ms base, exp backoff)` in
  v1alpha1. v1alpha2 may surface as env vars or per-job
  CRD fields.
- **Per-job webhook headers** — currently every request
  carries the same fixed schema (§4). Custom headers are a
  v1alpha2 surface.
- **`OutputSinkAdapter` trait abstraction** — see master
  prompt §4.5. The four sinks have meaningfully different
  semantics; a unifying trait either trivialises (forces
  S3 to fake per-row) or gets ahead of the design. v1alpha2
  may revisit when a fifth or sixth sink shows up.
- **mTLS** to S3 endpoints or webhook receivers. Fits the
  same v1alpha2 batch as adapter mTLS (ADR-0022 §6).

## §9 — Migration order across phases

R5.1 fits in Phase R5 of the refactor (master strategy §3).
It depends on:

- **R4.2** (`jobs` table with `output_sink_kind` column —
  ADR-0023 §2). The S3 / webhook job rows go into the same
  table.
- **R4.4** (Kafka producer — ADR-0023 §3 R4.4 addendum).
  S3 reuses the engine.proto field-addition pattern (field 4
  was Kafka; fields 5 + 6 are S3 + webhook nested messages).
  S3 reuses the startup-gate-with-soft-fail pattern.
- **R3.2** (v1alpha2 `OutputSink` discriminated union
  schema). The S3 + Webhook variant types (`S3Sink`,
  `WebhookSink`) already exist in
  `core/control-plane/api/v1alpha2/scrapejob_types.go`; R5.1
  unblocks `validateOutputSink` and forwards the per-sink
  config to the engine.

R5.1 unblocks Phase R5 entirely — there is no R5.2.
Subsequent phases are about packaging:

- **R6.1** — per-service Dockerfiles
- **R6.2** — Compose stack moves engine + adapters +
  control plane in
- **R7.1** — Helm chart
- **R8.1** — closing documentation refresh

## §10 — Reference materials

- **Predecessor ADRs.** ADR-0019 (control plane + ScrapeJob
  CRD; §5 evolves with R5.1 addendum), ADR-0020 (microservices
  supersession), ADR-0023 (stateful services — §2 Postgres,
  §3 Kafka R4.4 addendum, §6 admission gating, §8 library
  pinning).
- **AWS SDK for Rust.**
  <https://github.com/awslabs/aws-sdk-rust>;
  <https://docs.rs/aws-sdk-s3>.
- **MinIO Rust + endpoint compatibility.**
  <https://min.io/docs/minio/linux/developers/rust.html>.
- **reqwest.** <https://github.com/seanmonstar/reqwest>;
  <https://docs.rs/reqwest>.
- **axum (test server).**
  <https://github.com/tokio-rs/axum>.
- **boto3 custom endpoint.**
  <https://boto3.amazonaws.com/v1/documentation/api/latest/guide/configuration.html>
  — used by the conformance S3 test against Compose MinIO.
- **aiohttp test utilities.**
  <https://docs.aiohttp.org/en/stable/testing.html> —
  used by the conformance webhook test.
- **JSON Lines / NDJSON spec.** <https://jsonlines.org/>.
- **`docs/architecture/output-sinks.md`** — user-facing
  per-sink reference companion to this ADR.
