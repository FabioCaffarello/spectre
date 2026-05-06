# Output sinks

> User-facing reference for the four destinations a `ScrapeJob`
> can route extracted rows to. Architectural decisions live in
> ADR-0023 §3 (Kafka) and ADR-0024 (S3 + Webhook); this page is
> the operator-facing companion.

The v1alpha2 `OutputSink` discriminated union admits four
variants; every one is behaviourally implemented post-R5.1. CEL
admission rules enforce "exactly one variant set" at apiserver
time so a misconfigured ScrapeJob is rejected before the
reconciler runs.

| Variant   | When to use                                                        | Wire shape          |
|-----------|--------------------------------------------------------------------|---------------------|
| `Stdout`  | Local-dev iteration, `kubectl logs` debugging, audit-trail CR     | (no fields)         |
| `Kafka`   | Streaming ingest, multi-consumer fanout, replay via offsets        | `brokers`, `topic`  |
| `S3`      | Long-term archival, batch downstream processing, reproducible runs | `bucket`, `key`, `endpoint`, `region` |
| `Webhook` | Real-time push to receivers, integration with SaaS endpoints       | `url`, `method`, `batchSize` |

## Stdout

The default sink. The engine streams every extracted row as a
`Row` event in the gRPC `RunJob` response. The control-plane
operator forwards each `Row.json_line` to its own stdout, so
`kubectl logs <operator-pod>` shows the JSONL as it arrives —
the same UX as the legacy `spectre run` CLI piped to a file.

Postgres carries an audit copy of every row in the `job_rows`
table (ADR-0023 §2). Per-row inserts are best-effort: a Postgres
write failure logs but does not abort the job. v1alpha2 may
offer an opt-out env to skip the audit table for high-volume
workloads.

```yaml
spec:
  outputSink:
    stdout: {}
```

No fields to configure. The CRD's empty `StdoutSink{}` marker
type carries the discriminated-union role.

## Kafka

The engine publishes one Kafka message per extracted row when
the sink is Kafka. ADR-0023 §3 R4.4 addendum is the load-bearing
record.

**Topic + message contract.** Topic from `spec.outputSink.kafka.topic`.
Partition key is the job UUID (in-order delivery within one
job). Headers carry `job_id`, `row_index`, `driver`,
`timestamp` (ISO-8601). Consumers route or filter without
parsing the body.

**Brokers.** The CRD's `brokers` field is informational in
v1alpha1 — the engine reads `SPECTRE_KAFKA_BROKERS` at startup,
not per-job. v1alpha2 may revisit if production deployments
need per-job broker selection.

**Admission gating.** Engine-level. `SPECTRE_KAFKA_BROKERS`
must point at a reachable broker at engine startup; failure
logs WARN and threads `None` through. Kafka jobs against `None`
fail fast at job-start with `error_code = "KAFKA_UNAVAILABLE"`.
Empty topic fails with `KAFKA_TOPIC_REQUIRED`. Postgres
acquires the `jobs` row regardless of sink so a reader can ask
"where did the output go?" — `job_rows` is skipped.

**Delivery semantics.** At-least-once. `enable.idempotence=true`
prevents intra-session duplicates; engine crash mid-job leaves
partial state that re-driving the ScrapeJob duplicates.
Consumer-side dedup on `(job_id, row_index)` is the documented
user responsibility.

**Local-dev path.** `docker compose up -d` brings up Apache
Kafka 3.7.1 in KRaft mode plus the Redpanda Console UI at
`http://localhost:8080`. The engine resolves the broker from
`SPECTRE_KAFKA_BROKERS=localhost:9092` (see `.env.example`).

**Production deployment.** R7.1's Helm chart at
[`build/helm/spectre/`](../../build/helm/spectre/) packages
Kafka via the Bitnami `kafka` 30.0.0 subchart per
[ADR-0030](../adr/0030-helm-chart-structure.md). For external
brokers, set `kafka.enabled: false` and point
`SPECTRE_KAFKA_BROKERS` at your bootstrap.

```yaml
spec:
  outputSink:
    kafka:
      brokers:
        - kafka.spectre-system.svc.cluster.local:9092
      topic: spectre.rows.default
```

`docs/architecture/kafka.md` carries the operational guide.

## S3

The engine uploads each job's full extracted output as a single
JSONL `PutObject` to the configured bucket / key. ADR-0024 §3 is
the load-bearing record.

**Buffering model.** Per-job in-memory accumulation, single
`PutObject` at job completion. A 1M-row × 1KB job buffers 1GB
in engine memory before the upload runs — v1alpha1 documents
this as a known limitation (ADR-0024 §8). Multipart streaming
is the v1alpha2 fix.

**Key templating.** `spec.outputSink.s3.key` supports
`{{.JobID}}` substitution at upload time. Including the token
makes re-drives go to a fresh key (the new ScrapeJob has a
fresh UID); omitting it makes them overwrite. Both shapes are
supported. v1alpha2 may add `{{.Driver}}`, `{{.Timestamp}}`.

**Empty result.** A job whose executor produces zero rows still
uploads an empty (zero-byte) object — the post-job
presence-or-absence of the key is a reliable signal. Skipping
the upload would conflate "ran cleanly, extracted nothing" with
"didn't run".

**Content type.** `application/x-ndjson` per the JSON Lines
convention.

**Endpoint + region.** `S3Sink.Endpoint` (optional) overrides
the SDK's default endpoint resolution — set to MinIO / R2 /
Wasabi for non-AWS backends. `S3Sink.Region` defaults to
`us-east-1` (CRD kubebuilder default). The CRD field beats the
engine's `SPECTRE_S3_ENDPOINT` env var per-job, so a single
engine binary can serve mixed-endpoint workloads.

**Credentials.** `SPECTRE_S3_ACCESS_KEY_ID` +
`SPECTRE_S3_SECRET_ACCESS_KEY` env override the SDK's default
credential chain. Production deployments typically leave them
unset and rely on the chain (IAM role, AWS SSO, profile). The
engine logs INFO at startup when env is unset (BYO-credentials
mode is the production-typical shape) — distinct from Kafka's
WARN-level fallback.

**Admission gating.** Engine-level, soft. The engine starts
successfully even when `SPECTRE_S3_*` is unconfigured; jobs
against `None` fail fast at job-start with
`S3_UNAVAILABLE`. Empty bucket / key fails with
`S3_FIELD_REQUIRED`. ADR-0024 §5 records the asymmetry vs Kafka.

**Delivery semantics.** At-least-once. A successful PutObject
returns 200 OK with an ETag; the job is then durable. Re-driving
overwrites the same key (S3 PUT is replace-not-append) when the
key template doesn't include `{{.JobID}}`.

**Local-dev path.** `docker compose up -d` brings up MinIO at
`localhost:9000` (S3 API) + `localhost:9001` (web console)
plus a one-shot bucket-bootstrap container that pre-creates
`spectre-rows`. The engine resolves credentials from
`SPECTRE_S3_ACCESS_KEY_ID` + `SPECTRE_S3_SECRET_ACCESS_KEY`
(see `.env.example`).

**Production deployment.** R7.1's Helm chart at
[`build/helm/spectre/`](../../build/helm/spectre/) packages a
MinIO subchart (`minio.defaultBuckets=spectre-rows` by default)
plus the engine's S3 env-var surface per
[ADR-0030](../adr/0030-helm-chart-structure.md); set
`minio.enabled: false` to point at real AWS S3 / R2 / Wasabi
instead. Pre-create the destination bucket via Terraform /
Crossplane / console for non-MinIO targets; engine-side bucket
creation is a v1alpha2 concern (ADR-0024 §8).

```yaml
spec:
  outputSink:
    s3:
      bucket: spectre-rows
      key: scrapes/{{.JobID}}/rows.jsonl
      endpoint: http://minio.spectre-system.svc.cluster.local:9000
      region: us-east-1
```

## Webhook

The engine POSTs (or PUTs) rows to the configured URL — one
HTTP request per row by default, or batched when `batchSize > 0`.
ADR-0024 §4 is the load-bearing record.

**Per-row vs batched.** `spec.outputSink.webhook.batchSize` (CRD
default `0`) flushes after every row. Set `>0` to flush N rows
at a time; consumers split the body on `\n`. Batching trades
consumer simplicity for throughput; v1alpha1 prioritises
simplicity.

**Method.** `POST` (default) or `PUT`. Other methods rejected at
admission via the CRD's enum.

**Header schema.** Every request carries:

- `User-Agent: spectre-engine/<version>`
- `Content-Type: application/x-ndjson`
- `X-Spectre-Job-Id` — job UUID (matches `jobs.id`)
- `X-Spectre-Driver` — `playwright` / `seleniumbase` /
  `curl-impersonate`
- `X-Spectre-Batch-Size` — only when `batchSize > 0`
- `X-Spectre-Row-Count` — number of rows in this body

**Auth.** v1alpha1 ships **no authentication headers**. HMAC
signing, bearer tokens, signed-request envelopes are
v1alpha2 (ADR-0024 §8). Receivers requiring auth deploy behind
an ingress that adds the headers, or wait for v1alpha2.

**Retry policy.** Bounded exponential backoff: 3 attempts
(200 → 400 → 800 ms with jitter). Retryable on connection
refused, 5xx, 429. Fatal on first failure for other 4xx,
malformed URL. After exhaustion the job fails with
`WEBHOOK_POST_FAILED` carrying the last status code + body
excerpt.

**Admission gating.** **Per-job, runtime.** Webhook has no
engine-level state — every job dials its own URL. No startup
log line; failures surface mid-job after the executor runs
the first POST attempt. ADR-0024 §5 contrasts this with
Kafka / S3's startup-gate model.

**Delivery semantics.** At-least-once. A 2xx response
acknowledges acceptance. Receivers responding 2xx after
duplicate writes must implement consumer-side dedup
(`X-Spectre-Job-Id` + row position is the dedup key).

**Local-dev path.** No Compose dep. Run
`python3 -m http.server 8088` (or any HTTP receiver) and point
`spec.outputSink.webhook.url` at it. The engine integration
test (`webhook_integration.rs`) and conformance test
(`test_webhook_sink.py`) both spin up an in-process HTTP
server in the test process.

**Production deployment.** The webhook sink has no chart-side
infrastructure requirement beyond network reachability;
operators configure receivers externally and point
`spec.outputSink.webhook.url` at them. R7.2's production-smoke
gate exercises the path against a `mendhak/http-https-echo`
mock receiver (digest-pinned), proving the engine's POST shape
end-to-end.

```yaml
spec:
  outputSink:
    webhook:
      url: https://receiver.example.com/spectre
      method: POST
      batchSize: 0
```

## Choosing a sink

Pick the variant that matches the downstream system's storage
shape:

- **Multiple consumers fan out from a single ingest** → Kafka.
  Replayable from offsets, partition-keyed for
  in-job-order delivery.
- **Long-term archival or batch reprocessing** → S3. Durable,
  cheap, key namespacing per-job.
- **Real-time push to a single receiver** → Webhook. Low
  latency, no infrastructure to deploy.
- **Local-dev iteration or audit-trail debugging** → Stdout.
  Zero configuration, surfaces in `kubectl logs`.

Mixing sinks across ScrapeJobs is supported — a single engine
binary serves all four variants concurrently. Mixing within a
single ScrapeJob is not — the discriminated union admits exactly
one variant per CR (CEL-enforced at admission).

## Reference materials

- ADR-0019 (control plane + ScrapeJob CRD) — §5 R5.1 addendum
  records the JobRunner.Run interface evolution.
- ADR-0023 (stateful services) — §3 Kafka R4.4 addendum,
  §6 admission gating asymmetry, §8 library pinning,
  §12 env vars.
- ADR-0024 (output sinks) — S3 + Webhook architectural record;
  load-bearing for §3 / §4 / §5 of this page.
- `docs/architecture/control-plane.md` — operator-facing CRD
  overview; §"Output sinks" cross-references this page.
- `docs/architecture/kafka.md` — Kafka-specific operational
  guide.
- `docs/architecture/postgres.md` — `jobs` + `job_rows` schema
  reference.

## v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe
> v1alpha1's four output sinks (stdout / Kafka / S3 /
> Webhook). Phase R9 layers schema validation on top of the
> sink contract while preserving the four-sink set; this
> subsection clarifies what changes and what does not.*

The four v1alpha1 sinks remain the v1alpha2 set —
`OutputSink.Stdout`, `OutputSink.Kafka`, `OutputSink.S3`,
`OutputSink.Webhook` per [ADR-0024](../adr/0024-output-sinks.md).
The discriminated union, the per-sink failure semantics
(engine-level admission-gating for Kafka / S3; per-job
runtime failure for Webhook), and the JSONL row format are
all preserved.

What v1alpha2 adds, layered **before** sink dispatch:

- **Schema validation** per
  [ADR-0034](../adr/0034-output-schema-validation.md) —
  every emitted row validates against the ScrapeJob's
  declared schema (engine-cached per job; in-process JSON
  Schema validation). Failed rows drop / fail per the
  configurable failure policy; sinks ship only validated
  rows.
- **Post-extraction enrichment** per
  [ADR-0036 §3.4](../adr/0036-microservices-catalog-expansion.md)
  (slot 10, `enricher`) — geocoded coordinates, classified
  labels, embeddings added pre-emission at Wave 10.
- **Pre-emit deduplication** per ADR-0036 §3.4 (slot 11,
  `dedup-service`) — bloom-filter membership check; new
  rows emit, duplicates drop. Wave 10.

Webhook authentication remains deferred per ADR-0024 §8 +
[ADR-0032 §7](../adr/0032-service-to-service-mtls.md) — the
v1alpha2 phase does not commit a specific mechanism (HMAC /
bearer / mTLS-for-receivers); a follow-up ADR settles when
real consumer demand surfaces.

v1beta1 may add a Mongo-as-L0-sink option per
[ADR-0039 §7.1](../adr/0039-mongodb-third-storage-tier.md);
that is not in v1alpha2 scope.

For the engine's per-row pipeline (validate → enrich →
dedup → emit), see
[`engine-orchestrator.md`](engine-orchestrator.md) §2.
