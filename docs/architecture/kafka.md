# Kafka — engine output streaming

R4.4 introduced Kafka as the streaming output destination for
ScrapeJobs that select `OutputSink.Kafka`. The architectural
commitment lives in
[ADR-0023 §3 / §6](../adr/0023-stateful-services-architecture.md);
this document is the operator-facing companion: how the engine
publishes rows, how the topic / partitioning / headers contract
works, what admission gating looks like, and how the local-dev
stack runs.

## Producer lifecycle

The engine binary builds **one** `KafkaProducer` at startup and
shares it across in-flight `RunJob` requests as
`Arc<KafkaProducer>`. `rdkafka::FutureProducer` is internally
reference-counted and thread-safe, so per-job producer creation
would be wasteful overhead.

Startup sequence (`engines/engine/src/bin/spectre.rs`):

1. Parse the listen port from `SPECTRE_ENGINE_PORT`.
2. Dial Postgres via `Database::from_env`. **Required** —
   startup fails on failure (ADR-0023 §6).
3. Apply migrations.
4. Dial the Kafka broker via `KafkaProducer::from_env`.
   **Optional** — startup logs a warning on failure and
   continues without the producer. ADR-0023 §6 commits Kafka
   as admission-gated.
5. Build the `EngineServiceImpl` carrying the
   `Option<Arc<KafkaProducer>>`.
6. Register the gRPC service + the `grpc.health.v1.Health`
   service and serve.

The `from_env` constructor reads `SPECTRE_KAFKA_BROKERS`
(required) and `SPECTRE_KAFKA_LINGER_MS` (optional, defaults
to 10), constructs the librdkafka client with the producer
config below, and validates broker reachability via
`fetch_metadata` with a 5-second timeout. Reachability failure
returns `KafkaError::Unreachable`; the binary maps it to a
`None` producer and a startup warning.

## Producer configuration

ADR-0023 §3 R4.4 addendum commits these defaults:

| Setting               | Value      | Why                                              |
|-----------------------|------------|--------------------------------------------------|
| `acks`                | `all`      | Full ISR durability                              |
| `enable.idempotence`  | `true`     | librdkafka idempotent producer (no intra-session duplicates) |
| `compression.type`    | `snappy`   | Moderate compression, low CPU overhead           |
| `linger.ms`           | `10` (env) | Small batch window for low-latency dev           |
| `message.timeout.ms`  | `30000`    | Matches `DELIVERY_TIMEOUT` in `publish_row`      |

The `linger.ms` value is tunable via `SPECTRE_KAFKA_LINGER_MS`
for production deployments that prefer batching over latency.

## Topic, partitioning, headers

Per ADR-0023 §3:

- **Topic name** is sourced from
  `ScrapeJob.Spec.OutputSink.Kafka.Topic` and forwarded to the
  engine via `RunJobRequest.kafka_topic` (engine.proto field 4
  added in R4.4, non-breaking).
- **Partition key** is the job UUID (from `ScrapeJob.UID`,
  forwarded as `RunJobRequest.job_id`). librdkafka's default
  partitioner sends every message with the same key to the same
  partition, so all rows for one job land on one partition in
  extraction order — a downstream consumer that orders by
  `(partition, offset)` sees rows in extraction order without
  any cross-partition coordination.
- **Headers** carry metadata so consumers can route or filter
  without parsing the JSONL body:

  | Key         | Type           | Example                              |
  |-------------|----------------|--------------------------------------|
  | `job_id`    | UUID string    | `9fda96c3-b1b8-40e4-b3ad-11353ce7f227` |
  | `row_index` | i64 string     | `0`, `1`, `2`, …                     |
  | `driver`    | string         | `playwright` / `seleniumbase` / `curl-impersonate` |
  | `timestamp` | ISO-8601 UTC   | `2026-04-28T22:24:09.027008+00:00`   |

  All values are encoded as UTF-8 bytes per Kafka convention.

- **Body** is the JSONL row exactly as the engine would have
  written to stdout — one Kafka message per row.

## Delivery semantics

**At-least-once.** The idempotent producer flag prevents
duplicate writes from intra-session retries (network blip,
leader failover, transient broker error). Inter-session
duplicates remain possible:

- Engine crash mid-job → partial Kafka state survives. If the
  reconciler re-drives the job (a new engine RunJob with a new
  UUID), the new run publishes the same logical rows again.
- Producer-restart-during-publish → librdkafka may republish
  in-flight messages on reconnection.

Consumer-side idempotency is the documented user
responsibility. The `(job_id, row_index)` pair is the natural
deduplication key: row N of job U is unique under this scheme,
so a consumer's "have I seen this already?" check is a single
lookup.

v1alpha2 may add Kafka transactions (exactly-once semantics)
if real workloads expose the cost of de-duplication.

## Admission gating

ADR-0023 §6 commits Kafka as OPTIONAL. The implementation is
**fail-fast at job-start**:

- Engine startup with an unreachable broker → `kafka` is `None`
  in the `EngineServiceImpl`. The engine continues to serve
  stdout-sinked jobs normally.
- A `RunJob` with `output_sink_kind = "kafka"` enters the
  pre-flight check: `kafka.is_none()` short-circuits to a
  terminal `Failed` event with `error_code =
  "KAFKA_UNAVAILABLE"` and the message _"kafka producer is
  not available; set SPECTRE_KAFKA_BROKERS and restart engine
  to enable OutputSink.Kafka jobs"_. The job's `jobs.id`
  Postgres row transitions to `failed` accordingly.
- An empty `kafka_topic` short-circuits to `Failed{
  KAFKA_TOPIC_REQUIRED }`.

User experience: a ScrapeJob with the Kafka sink fails
immediately with a clear error message and a resolution hint.
Equivalent UX to admission rejection without the
implementation cost of a custom validating webhook (TLS-cert
plumbing, failure-policy decisions, webhook-deployment
lifecycle).

## Postgres + Kafka coexistence

Per ADR-0023 §2:

- The `jobs` table is written for **every** job regardless of
  sink. Status transitions, error tracking, `output_sink_kind`,
  `rows_extracted`. A Postgres reader can answer "where did the
  output go?" without joining anywhere else.
- The `job_rows` table is written **only for stdout-sinked
  jobs**. Kafka jobs bypass the audit table — Kafka itself is
  the data store, the audit, and the source of truth for that
  job's rows.

`jobs.rows_extracted` reflects the actual count. For Kafka
jobs, it equals the number of messages successfully published
to Kafka; a divergence between Postgres count and consumer
count signals an ops problem (engine crash mid-publish,
broker-side retention dropping messages, etc.).

## Local-dev stack

`docker-compose.yml` brings up the broker + observability UI
alongside Postgres + Redis:

```bash
docker compose up -d
docker compose ps   # postgres, redis, kafka, kafka-console healthy
```

Services:

- **Apache Kafka 3.7.1** (`apache/kafka:3.7.1`) in KRaft mode
  on `localhost:9092` (host) and `kafka:9092` (Compose
  network). Single broker, single controller. Auto-creation of
  topics is enabled for dev convenience; production
  deployments (R7.1's Helm chart) disable it.
- **Redpanda Console** (`docker.redpanda.com/redpandadata/console:latest`)
  at <http://localhost:8080>. Web UI for topic browsing,
  consumer-offset inspection, and message preview. Despite
  the name, it works against any Kafka API broker.

The engine connects via `SPECTRE_KAFKA_BROKERS=localhost:9092`
(see `.env.example`).

### Justfile recipes

```bash
just kafka-console            # open http://localhost:8080
just kafka-topics             # docker exec list of topics
just kafka-consume <topic>    # docker exec stream of messages
```

The engine integration test runs against the local broker via
`SPECTRE_KAFKA_BROKERS`:

```bash
just compose-up
just engine-kafka-test        # cargo test --test kafka_integration -- --ignored
```

The conformance kafka-sink test exercises the full E2E
(control plane DSL → engine → Playwright adapter → engine →
Kafka → consumer):

```bash
just compose-up
just spectre-build pw-build   # prerequisites
just conf-test                # 47+ tests including kafka-sink
```

## Production deployment

R7.1's Helm chart packages Kafka via the Bitnami `kafka` 30.0.0
subchart pinned in
[`build/helm/spectre/Chart.yaml`](../../build/helm/spectre/Chart.yaml)
per [ADR-0030](../adr/0030-helm-chart-structure.md). ADR-0023 §10
sketched a Strimzi-managed shape; ADR-0030 §4 chose Bitnami for
parity with the other stateful subcharts (postgresql, redis,
minio). Highlights:

- StatefulSet KRaft topology (single controller in CI; clusters
  size up via subchart values), PVC for log directories,
  `Service` for the bootstrap, `Secret` for SASL credentials
  when enabled.
- `kafka.enabled: false` accepts an external broker via
  `SPECTRE_KAFKA_BROKERS` resolved from a `valueFrom.secretKeyRef`.
- Topic provisioning is admin-driven (Bitnami exposes a
  `provisioning.topics` array) or external. Auto-
  creation is disabled in production.
- mTLS / SASL deferred to v1alpha2 — v1alpha1 trusts the Pod
  network per ADR-0022 §6.

## References

- [ADR-0023 §3 + R4.4 addendum — Kafka architecture](../adr/0023-stateful-services-architecture.md)
- [ADR-0023 §6 — required vs optional](../adr/0023-stateful-services-architecture.md)
- [ADR-0023 §12 — env-var configuration](../adr/0023-stateful-services-architecture.md)
- rdkafka 0.36: <https://github.com/fede1024/rust-rdkafka>
- librdkafka producer config:
  <https://github.com/confluentinc/librdkafka/blob/master/CONFIGURATION.md>
- Apache Kafka KRaft mode:
  <https://kafka.apache.org/documentation/#kraft>
- Redpanda Console:
  <https://docs.redpanda.com/current/manage/console/>
