---
status: accepted
date: 2026-04-28
deciders: [Fabio Caffarello]
---

# Stateful services architecture

## §1 — Context and Problem Statement

Six PRs into the refactor, the architecture Spectre commits to in
ADR-0020 is operationally complete at the service-mesh layer.
ADR-0021 / ADR-0022 retired the subprocess-in-pod transport in
favour of TCP gRPC with env-var discovery (R2.1–R2.3); ADR-0019
+ ADR-0020 §5 reshaped the control plane into a thin gRPC client
of the engine (R3.1); ADR-0019's R3.2 addendum evolved the
ScrapeJob CRD to v1alpha2 with EngineRef and a four-variant
OutputSink discriminated union (R3.2). Every component that
needs to talk to every other component now does so over the
network. What remains is what the network has, so far, been
asked to carry: nothing durable. Job state lives in the engine's
Tokio task and on the operator's `Status` subresource — both
volatile across Pod restart. Output rows leave the engine via
stdout and reach `kubectl logs` as soon as the operator buffers
them, with no streaming surface for downstream consumers. Adapter
session metadata — the UUIDs the conformance suite already exercises
under ADR-0010, the cookie-jar paths curl-impersonate emits,
the per-session generation counter Playwright uses for stable-
node tracking — lives entirely in the adapter's process memory.
A single Pod restart on any of the three layers loses every job
in flight and every session a client thought it held.

R4 closes that gap. Three stateful services land together in the
architecture: PostgreSQL for job state and audit, Kafka for
output streaming, Redis for adapter session metadata. The PRs
that wire them — R4.2 (Postgres), R4.3 (Redis), R4.4 (Kafka) —
land in series, but the architectural commitment is one
decision, not three, because the three services interlock.
Postgres alone cannot recover a job whose adapter session was
lost on Redis-less Pod restart. Kafka alone cannot publish rows
the engine never persisted. Redis alone externalises session
metadata that no other service knows how to index. Introducing
them piecemeal would leave the system in a half-stateful state
no operator can reason about: some workloads recoverable, some
not, with the matrix depending on which PR landed when. This
ADR records the full commitment up front so the implementation
PRs can each ship a coherent slice of one architecture rather
than three negotiations of an emerging one. The companion
[`docs/refactor-audit.md`](../refactor-audit.md) tracks the
per-PR work plan; this ADR is the architectural reference R4.2
/ R4.3 / R4.4 each implement against.

## §2 — PostgreSQL

PostgreSQL is the durable store for job state. The engine writes
one row to a `jobs` table on every job admission, updates the
status column as the job transitions, and (for `OutputSink.Stdout`
jobs only) appends each row of extracted data to a `job_rows`
table as a JSONB document. The control plane reads the same
tables to populate the `ScrapeJob` `Status` subresource and to
serve historical queries — "show me every Failed job in the last
hour", "show me the rows for job X" — without round-tripping
through the engine. The two services share one database; the
schema is normalised, owned by the engine, and migrated through
sqlx-cli at engine startup (see §13 for the migration discipline).

### Schema

```sql
CREATE TABLE jobs (
    id UUID PRIMARY KEY,
    dsl TEXT NOT NULL,
    driver TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    rows_extracted BIGINT,
    error TEXT,
    resolved_engine_endpoint TEXT,
    output_sink_kind TEXT NOT NULL CHECK (output_sink_kind IN ('stdout', 'kafka', 's3', 'webhook'))
);

CREATE TABLE job_rows (
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    row_index BIGINT NOT NULL,
    json_value JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, row_index)
);

CREATE INDEX idx_jobs_status_created ON jobs(status, created_at);
CREATE INDEX idx_jobs_output_sink_kind ON jobs(output_sink_kind);
CREATE INDEX idx_job_rows_job_id ON job_rows(job_id);
```

The CHECK constraints on `status` and `output_sink_kind` are
database-level invariants — the engine cannot insert a row with a
status string the schema does not recognise, and the same
phase enum that lives in `ScrapeJobPhase` (v1alpha2 §
`scrapejob_types.go`) is enforced at the storage layer. The
ON DELETE CASCADE on `job_rows.job_id` simplifies job retention:
deleting a `jobs` row removes its audit rows in one statement.

`job_rows` is populated only when a job's `output_sink_kind` is
`'stdout'`. Kafka, S3, and Webhook sinks bypass this table — their
data lives at their respective destinations (the topic, the
bucket, the HTTP endpoint), and duplicating into `job_rows` would
pay double-write costs for no recoverability gain. The
`output_sink_kind` column itself is recorded for every job
regardless of variant, so a reader of the `jobs` table can answer
"where did this job's output go?" without joining.

### Library and dialect

The engine uses `sqlx` (see §8 for the full library matrix).
Compile-time-checked queries — `query!()` macros that read the
schema at build time and reject misshapen SQL — give the engine's
Rust code the same compiler-enforced contract the gRPC bindings
already provide for the wire protocol. The control plane uses
`pgx/v5`; the choice of driver per side is documented in §8 and
the rationale for not using `lib/pq` (in maintenance mode) lives
there.

The dialect is plain PostgreSQL. No vendor extensions. The schema
runs unchanged on PostgreSQL 14 through 16; v1alpha1 of the
refactor pins the Compose stack and the Helm chart's bundled
subchart to PostgreSQL 16 (see §9 / §10).

### Alternatives considered

- **MySQL.** Mature, widely deployed, but the JSON column type
  is less ergonomic than Postgres' JSONB (no GIN indices on
  arbitrary JSONB paths in MySQL until 8.0+, and the operator
  ecosystem in Kubernetes is thinner). The marginal benefit
  from familiarity does not offset the storage-layer
  ergonomics gap.
- **SQLite.** A single-file embedded database is appealing for
  the simplest demonstrator, but the engine and control plane
  are separate Pods that would each need their own SQLite file
  — exactly the multi-writer scenario SQLite cannot serve. A
  network-mounted SQLite file is technically possible and
  uniformly discouraged by SQLite's own documentation.
- **CockroachDB.** PostgreSQL-wire-compatible with global
  replication. The geo-distribution story is the wrong shape
  for a v1alpha1 single-cluster deployment; CockroachDB's
  operational complexity (Raft consensus tuning, range
  splitting) is real cost for value v1alpha1 cannot use.
- **DynamoDB and other managed cloud databases.** Excludes
  self-hosted deployments — the operator who runs Spectre on
  bare metal or in a private cluster cannot use them. The
  Helm chart commits to a subchart-based Postgres in §10
  precisely so self-hosting stays a first-class deployment
  shape.

PostgreSQL is the answer. R4.2 implements the schema above and
the engine's write path; R7.1's Helm chart packages the Bitnami
Postgres subchart as the default (with an off-switch for
operators bringing their own).

## §3 — Kafka

Kafka is the streaming surface for `OutputSink.Kafka`. The
v1alpha2 schema already defines the variant —
`KafkaSink.Brokers []string` and `KafkaSink.Topic string`
(R3.2; `core/control-plane/api/v1alpha2/scrapejob_types.go`,
lines 132–142) — and the reconciler currently rejects any
ScrapeJob that selects it with "kafka output sink not yet
implemented (R4.4)". This ADR records the producer-side
contract that R4.4 implements, so the schema and the runtime
behaviour converge.

### Topic naming

Topics follow the pattern `spectre.rows.<workspace>`. v1alpha1
of the refactor has one workspace, named `default`, so the
canonical topic is `spectre.rows.default`. The workspace
component is reserved for v1alpha2 multi-tenancy; until then,
every job writes to the same topic regardless of namespace or
ScrapeJob name.

Per-job topics were considered and rejected. Spectre's job
cardinality is unbounded — operators can create thousands of
ScrapeJob resources over a deployment's lifetime — and Kafka
topics are not free per-topic (each topic carries metadata
overhead in the broker, and a topic-per-job pattern would
require runtime topic creation that complicates the producer's
authorisation model). One topic per workspace bounds the topic
count to the workspace count and lets downstream consumers
subscribe once per workspace rather than once per job.

### Partitioning

Each topic uses 8 partitions by default. The partition key is
the job's UUID; the producer hashes it into the partition index.
Two consequences follow.

First, all rows for a single job land on the same partition, so
a downstream consumer that orders by `(partition, offset)` sees
that job's rows in production order. The engine's row emission is
sequential per job (one task per running job, draining a
streaming RPC into the sink), so partition order matches
extraction order.

Second, work spreads across partitions roughly evenly across
many concurrent jobs. The 8-partition default is tunable per
environment via a future Helm value (R7.1 territory); the ADR
fixes the default but does not pin it.

### Message shape

One Kafka message per row. The body is the JSONL row exactly as
the engine would have written it to stdout — same schema, same
field order, same line terminator (no terminator inside the
Kafka body — Kafka frames the message). Headers carry the
metadata downstream consumers need to route or filter without
parsing the body:

- `job_id` — the UUID, matching `jobs.id` in the Postgres
  schema (§2)
- `row_index` — monotonic counter per job, matching
  `job_rows.row_index` for stdout-sinked jobs (§2)
- `driver` — `playwright`, `seleniumbase`, or
  `curl-impersonate`
- `timestamp` — ISO-8601 row-emission time

Consumers that need to back-fill a Postgres `job_rows` view (for
example, an analytics service that wants the full audit log
across both Stdout and Kafka jobs) can join on `(job_id,
row_index)`; the headers carry the join keys explicitly so the
consumer never has to parse the body to route.

### Library and broker compatibility

The engine uses `rdkafka` (see §8). The library wraps the
upstream `librdkafka` C library through Rust FFI; the choice
buys access to the most production-tested Kafka producer
implementation in any language and a maintained Rust API on
top.

Brokers compatible with the Apache Kafka 0.11+ wire protocol
all work. R7.1's production Helm path defaults to a Strimzi or
Bitnami Kafka deployment; R6.2's local Compose stack uses
Redpanda single-node — wire-protocol-compatible with Kafka,
single binary, ~150 MB resident, no ZooKeeper dependency. The
producer code does not branch on broker implementation; the
broker list is a configuration value and the producer protocol
is the abstraction.

### Alternatives considered

- **RabbitMQ.** AMQP semantics are queue-shaped (delete on
  consume) rather than log-shaped (immutable, replayable). A
  consumer that loses position cannot rewind without
  application-level retention; Kafka's offset model is the
  exact shape downstream consumers want for row-streaming.
- **NATS JetStream.** Capable, but the cross-language client
  ecosystem is thinner than Kafka's, and the partition
  semantics are weaker. Spectre's downstream consumers are
  intentionally polyglot (the same ADR-0014 polyglot
  argument that picked Node + Python + Go for adapters
  extends to consumers); Kafka's library breadth matters.
- **Apache Pulsar.** Comparable feature set to Kafka with
  additional complexity (BookKeeper, ZooKeeper). Kafka's
  mindshare wins for a project where the streaming surface
  is one consumer-facing decision among many.

Kafka is the answer. R4.4 wires the producer and removes the
admission-time rejection from the v1alpha2 reconciler so
`OutputSink.Kafka` becomes a runnable variant; R7.1 packages
the broker subchart. R5.1 (S3 + Webhook output sinks) consumes
neither this topic nor this producer — those sinks have their
own destinations — but does share the v1alpha2 schema's
discriminated-union shape that R3.2 committed.
