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

## §4 — Redis

Redis stores adapter session metadata. Each adapter — Playwright,
SeleniumBase, curl-impersonate — already maintains a session
table internally: a session_id (UUID) maps to a browser context
or HTTP client object that holds cookies, the navigation
history, and per-session generation counters used for stable-
node tracking under ADR-0010 §3. R4.3 externalises the
*metadata* layer of that table to Redis. The runtime objects
(the browser process, the HTTP client) remain in the adapter
process; the index that lets the adapter find them, and the
metadata a future replacement adapter would need to know about
their existence, lives in Redis.

### Keyspace

Two keys per session, namespaced by adapter:

- `session:<adapter>:<session_id>` — JSON document holding
  the session metadata: creation timestamp, current URL, cookie-
  jar path on the adapter's local volume, generation counter,
  and a stable-node map summary.
- `session:<adapter>:<session_id>:ref` — last-access
  timestamp. Updated on every session-bound RPC; used as the
  signal for LRU-style eviction when an adapter approaches its
  configured session-count ceiling.

The `<adapter>` component is one of the three driver names —
`playwright`, `seleniumbase`, `curl-impersonate` — matching
the values in §3's Kafka headers and the Postgres `jobs.driver`
column. The session_id is the same UUID the adapter returned to
the client on `Initialize`, the same UUID the client carries on
every subsequent RPC, and the same UUID the engine logs and the
control plane records on `ResolvedEngineEndpoint` traces.

### TTL

The metadata key carries a 1-hour idle TTL. Each session-bound
RPC refreshes the TTL via `EXPIRE` on both keys. A session
unused for an hour falls out of Redis and the adapter
invalidates the in-memory runtime object on its next eviction
sweep. The TTL is configurable per environment via Helm values
(R7.1) but the default value bounds the project's documented
contract: clients must not assume a session_id is live after
an hour of inactivity.

### Atomicity

Writes are PUT-style overwrites. The full session-metadata
document is rewritten atomically on each update via `SET`; no
field-level updates, no `HSET` partial mutation, no Redis
transactions. The atomic write boundary is one document. The
choice keeps the failure model simple — either the document is
written or it is not, and a reader on the other side never sees
a half-mutated session.

For the `:ref` timestamp key, a separate `SET` follows the
metadata write. The two keys can momentarily disagree (the
metadata advances before its `:ref`), but the disagreement is
not load-bearing — `:ref` is an eviction hint, not a correctness
input — and the cost of a Redis transaction (MULTI/EXEC, with
its block-on-server-response cost) outweighs the benefit.

### Library matrix

Three adapters in three languages need three Redis clients.
Each language picks the most production-tested client in its
ecosystem:

- Playwright (Node/TypeScript): `ioredis`
- SeleniumBase (Python): `redis-py` (the official driver)
- curl-impersonate (Go): `go-redis/v9`

Engine and control plane do not connect to Redis at all; only
the adapters do. The full library matrix lives in §8.

### Alternatives considered

- **PostgreSQL session table.** Putting session metadata into
  the same Postgres database that holds job state sounds
  appealing for operational simplicity (one stateful service
  fewer). It fails on access pattern. Adapters update session
  metadata on every RPC — every navigation, every screenshot,
  every `Find`/`Click` cycle. Postgres's per-statement parser
  / planner overhead makes it the wrong tool for sub-millisecond
  metadata writes; Redis is purpose-built for this exact load.
  The architectural cost of running both services is real, and
  ADR §6's required-vs-optional matrix records it.
- **Adapter-local file storage.** Writing session metadata to
  a per-Pod volume technically survives Pod restart on the same
  node, but loses the session on Pod reschedule (the standard
  Kubernetes failure mode), loses the session on Compose
  `down`/`up`, and does not solve the problem ADR §5 articulates.
  File storage is a half-solution that hides the choice.
- **In-memory only (status quo).** What every adapter does
  today. No Redis dependency, no operational service to run.
  And no recovery semantics — every Pod restart loses every
  session. R4.3 changes this; the architectural commitment in
  §5 makes the change explicit.

Redis is the answer. R4.3 wires the adapter clients per the §8
library matrix; R7.1 packages the Bitnami Redis subchart.

## §5 — The session externalization problem

Adapter sessions cannot be Redis-cached. A "session", at the
runtime level, is a Playwright `BrowserContext`, a SeleniumBase
`SB` instance, or a curl-impersonate `*http.Client` — a live
process-bound object holding sockets, file descriptors, and
language-runtime memory that does not survive serialisation.
Move that object to Redis and the deserialised side has bytes,
not behaviour. The metadata that *describes* the session — the
session_id, the cookie-jar path, the navigation history's
last URL, the generation counter — is serialisable. The session
itself is not. R4.3 externalises the metadata; the runtime stays
process-local. That asymmetry is the architectural problem this
ADR records, and the consequence — what happens on adapter Pod
restart — has three reasonable answers and one chosen direction.

### Option A — restart invalidation (chosen)

When an adapter Pod restarts, every session it held becomes
invalid. The Redis metadata persists; the adapter, on startup,
sees session keys whose corresponding runtime objects do not
exist in its memory, and it does not attempt to reconstitute
them. Clients that hold session_ids from before the restart see
`UNAVAILABLE` (or `FAILED_PRECONDITION` on the first session-
bound RPC after restart) and re-call `Initialize` to allocate
fresh session_ids backed by fresh runtime objects.

The cost is borne by the client. A client mid-job sees a session
fail and must restart its work from the beginning of its session
boundary. For Spectre's primary use case — engine driving an
adapter for a single ScrapeJob's duration — the cost is
"restart the job", and v1alpha2's reconciler already retries
failed jobs on the next reconcile cycle. For consumers running
longer-lived sessions (the conformance suite, an interactive
debugging tool), the cost is one round of `Initialize` and the
loss of in-session state (visited URLs, accumulated cookies).
The cost is real and bounded.

The benefit is a contract a reader can hold in their head:
*session_ids are valid until the adapter Pod restarts*. Nothing
more. No subtle "sometimes it survives, sometimes it doesn't"
matrix. No promises about partial recovery the implementation
cannot keep. The contract matches what the runtime actually
delivers.

### Option B — sticky sessions (rejected)

Route every RPC for a given session_id to the specific adapter
Pod that allocated it. Realised via client-side affinity (the
client remembers `(session_id, pod_address)` and dials directly)
or via Service-level routing (a session-aware proxy or a Service
with `sessionAffinity: ClientIP`).

This preserves session liveness across most operational events
— routine reschedules, rolling deployments, scale-up — at the
cost of two architectural compromises. First, the client must
know about Pod-level addressing, not just Service-level
addressing; the v1alpha1 transport contract (ADR-0022) is
plaintext gRPC over a single Service endpoint, and sticky
sessions would push Pod identity into the client's discovery
contract. Second, horizontal scaling of the adapter Pool gets
constrained — a client that pinned to Pod A cannot fail over to
Pod B if Pod A crashes, so the Service's redundancy guarantee
is reduced from "any Pod can serve any RPC" to "any Pod can
serve any *new* RPC". The architectural surface a sticky-session
contract adds — proxy logic, client-side address caching,
re-pinning on Pod death — is non-trivial, and the recovery
semantics it preserves are partial: the cost is paid every day
for a benefit that the failure case (Pod crash) still does not
fully cover.

### Option C — warm recovery (rejected)

On adapter Pod startup, the new Pod reads Redis, sees session
keys for the adapter, and attempts to reconstitute the runtime
state — replay the cookie-jar, restore the navigation history,
re-allocate browser contexts pointed at the last-visited URLs.
Clients that held session_ids from before the restart see their
sessions "still working", with the URL they were on and the
cookies they had accumulated.

The architectural problem is what "still working" hides. Browser
state is not the cookies and URL alone. It is the JavaScript
heap, the WebSocket connections, the per-tab event-listener
graph, the rendering engine's per-page caches. Replaying
cookies and the URL gives a client *some* of what they had —
enough to feel like the session survived — and silently misses
the rest. A client that was mid-form-fill, mid-CAPTCHA, mid-
single-page-app navigation finds the recovered session is
plausibly different in ways the contract does not specify and
the implementation cannot fully enumerate. Worse, the partial
recovery creates a false sense of resilience: operators see
"sessions survive Pod restart" in the docs and design retry
budgets accordingly, then run into the cases warm recovery does
not cover and have no honest contract to fall back on.

The complexity cost is real too. Each of the three adapters has
a different runtime model — Playwright's CDP-driven Chromium
process tree, SeleniumBase's Python-driven WebDriver session,
curl-impersonate's Go-driven HTTP client — and warm recovery
needs a per-adapter implementation of "given this Redis blob,
materialise an equivalent runtime". Three implementations,
three failure surfaces, and the union of "sessions sometimes
survive, sometimes silently degrade" replaces the simple
restart-invalidation contract.

### The choice and its cost

ADR-0023 commits to restart invalidation (Option A). The
contract is "session_ids are valid for the lifetime of the
adapter Pod that allocated them". Clients re-call `Initialize`
on `UNAVAILABLE`. The Redis metadata persists across restart
not as a recovery surface but as an indexable record of what
sessions existed — useful for debugging, useful for v1alpha2
contracts that may build on it (multi-tenant accounting,
session-level audit), and useful as the data structure
sticky-session and warm-recovery options would build on if
v1alpha2 deliberately revisits the choice.

The cost — clients restarting jobs on adapter Pod restart — is
the cost the runtime imposes regardless of how the metadata
layer is organised. Restart invalidation makes that cost visible
and contractual. Sticky sessions hide it (until they don't);
warm recovery hides it (and creates harder-to-debug failures
when the recovery is incomplete). v1alpha2 may revisit the
choice if real users run longer-lived sessions whose restart
cost is operationally significant; v1alpha1 commits to the
honest contract.

## §6 — Required vs optional

The three stateful services do not all carry the same
deployment commitment. Two are required everywhere; one is
required only when a workload exercises it. The commitment is
the same in development (Compose) and in production (Helm).

| Service   | Production | Dev (Compose) | Rationale |
|-----------|------------|---------------|-----------|
| Postgres  | REQUIRED   | REQUIRED      | Job state is always persisted. There is no in-memory mode. |
| Kafka     | OPTIONAL   | INCLUDED      | Required only when a ScrapeJob selects `OutputSink.Kafka`. Admission rejects new Kafka sinks if the broker is unavailable. |
| Redis     | REQUIRED   | REQUIRED      | Session metadata is always written. Defines the restart-invalidation contract from §5. |

### Postgres always

Engine startup validates the Postgres dial: it opens the
connection pool, runs migrations (§13), and only then registers
the gRPC service. A startup-time Postgres outage is a startup-
time engine failure — visible to `kubectl get pod` as a crash
loop and to `docker compose up` as the container exiting non-
zero. There is no "engine without Postgres" mode. A reader
debugging a deployment sees one binary cause for "engine not
serving"; an operator does not have to weigh "is this a
Postgres-side issue or a config-side issue" against any toggle.

### Kafka admission-gated

Engine startup validates the Kafka producer dial, but the
failure semantics are softer than Postgres'. If the broker is
reachable, the engine logs "kafka producer ready" and accepts
admission of new ScrapeJobs with `OutputSink.Kafka`. If the
broker is unreachable, the engine logs a warning, marks the
Kafka admission gate disabled, and *continues to start*. New
ScrapeJobs with `OutputSink.Kafka` are rejected at admission
with the same "kafka not available" message R3.2's reconciler
returned for "kafka not yet implemented" — semantically
distinct, surfaceably similar to the operator. ScrapeJobs in
flight with current sinks (Stdout, S3, Webhook) continue
unaffected.

The architectural distinction is that Kafka is a *consumer-
chosen* dependency. Postgres and Redis are the architecture's
own state store; an operator does not opt out of them by
configuration. Kafka is a destination, selected per-job by the
v1alpha2 schema; an operator who never runs Kafka-sinked jobs
genuinely does not need Kafka at all. The deployment matrix
respects that distinction.

### Redis always

Adapter startup validates the Redis dial. Same model as
Postgres for the engine — a startup-time Redis outage is a
startup-time adapter failure. There is no "adapter without
Redis" mode, and the rationale is the §5 contract. The restart-
invalidation contract requires that Redis be the index of which
sessions exist; an adapter running without Redis would have to
either invent a different index (file storage, in-memory only)
or break the contract. Both options are worse than the simple
"adapter requires Redis" rule, and ADR-0023 commits the simple
rule.

### No "lite mode"

The combined effect is that Spectre runs the full stack or
does not run. There is no minimal-dependency variant for
testing or for resource-constrained deployments. The conformance
suite (R6.2 / Compose) and the production deployment (R7.1 /
Helm) both pull Postgres + Redis (and optionally Kafka) into
the topology. The R8.1 documentation refresh records this
explicitly so a reader who arrives at the docs without reading
this ADR understands the deployment shape.

The single-mode commitment trades a deployment-shape constraint
for an operational-clarity gain. An operator who has the stack
running has the same stack every other operator has. A
contributor reading the codebase does not have to thread "what
if Postgres is unavailable" through every code path. The
trade-off is recorded honestly.

## §7 — Network topology

The post-R4 topology has eight long-lived services on the
network: control-plane, engine, three adapters, Postgres,
Redis, and (when an operator runs it) Kafka. The connection
graph stays sparse — each service knows only about the
dependencies it actually needs, and no service holds a channel
it does not use.

```
┌──────────────────┐       ┌──────────────────┐
│  control-plane   │──gRPC─▶│       engine      │
│  (operator Pod)  │       │  (Rust service)  │
└──────────────────┘       └──────────────────┘
         │                          │   │
         │ pgx/v5                   │   │ rdkafka
         │                          │   │
         ▼                          │   ▼
┌──────────────────┐                │ ┌──────────────────┐
│   PostgreSQL     │◀─sqlx──────────┘ │      Kafka       │
│  (jobs, rows)    │                  │ (spectre.rows.*) │
└──────────────────┘                  └──────────────────┘
                                       (when operator runs it)

┌──────────────────┐       ┌──────────────────┐
│      engine      │──gRPC─▶│   adapter Pod    │
│  (Rust service)  │       │ (3× per topology)│
└──────────────────┘       └──────────────────┘
                                    │
                                    │ ioredis / redis-py / go-redis
                                    ▼
                           ┌──────────────────┐
                           │      Redis       │
                           │  (session:*)     │
                           └──────────────────┘
```

Five connection patterns make up the graph:

- **Engine → Postgres.** Read/write. The engine writes one row
  to `jobs` per admission, mutates the status column on each
  transition, and (for stdout-sinked jobs) appends to
  `job_rows`. The pool is one connection at idle, scaling under
  load to a configurable per-engine cap.
- **Engine → Kafka.** Write only. One producer per engine,
  publishing to `spectre.rows.<workspace>` for jobs whose
  `output_sink_kind` is `'kafka'`. The producer is shared
  across all jobs that need it; partition selection (§3) gives
  per-job ordering without per-job producers.
- **Engine → Adapters (3).** The pre-R4 transport, unchanged.
  gRPC over TCP per ADR-0022; service discovery via env vars
  per ADR-0021 §5; each adapter exposes the same Driver
  Protocol surface ADR-0001 froze and ADR-0008 / ADR-0014 /
  ADR-0016 instantiated.
- **Adapters → Redis.** Read/write. Each adapter holds one
  Redis client and writes session metadata on every state-
  changing RPC. The `:ref` timestamp updates are read-mostly
  for the eviction sweep.
- **Control plane → Postgres.** Read only. The control plane
  populates `Status` subresources from Postgres queries; no
  writes flow this direction. Engine is the sole writer; the
  control plane is one of two readers (the other is whatever
  ad-hoc query an operator runs via `psql`).
- **Control plane → Engine.** The pre-R4 path, unchanged. The
  reconciler dials the resolved engine endpoint per ScrapeJob
  and consumes the `RunJob` stream. ADR-0019 §5's
  `EngineClientRunner` (R3.1) is preserved verbatim.

Two non-connections are worth recording. **Adapters do not
connect to Postgres.** Job state is the engine's concern;
adapters do not know which job they are serving (the engine
addresses them per-RPC, not per-job). **Adapters do not
connect to Kafka.** Output streaming is the engine's concern;
the engine consumes the adapter's RPC stream and writes onward
to whichever sink the v1alpha2 schema selected. The adapter
surface to the rest of the system is exactly what the Driver
Protocol carries — nothing more — and the §1 frame "the
protocol does not change" stays intact through R4.

The control-plane → engine path is the seam ADR-0019 §5
preserved through three runner implementations
(`StubRunner`, `SubprocessRunner`, `EngineClientRunner`). R4
does not touch it. The control plane gains a Postgres dial as
a *new* dependency, not a replacement for the engine dial; the
two paths coexist, with the engine dial driving execution and
the Postgres dial serving status reads.

## §8 — Library choices and pinning

ADR-0023 commits to specific client libraries per language.
The commitments close out the per-PR debate that R4.2 / R4.3 /
R4.4 would otherwise reopen. Each library is the most
production-tested, maintained option in its ecosystem at the
time of writing; if new information surfaces during
implementation, the response is a follow-up ADR, not a per-PR
re-litigation.

### Engine (Rust)

- **Postgres**: `sqlx`. Compile-time-checked queries (`query!`
  macros that read the database schema at build time), async-
  first API on Tokio, no ORM machinery to learn around. The
  alternative — `tokio-postgres` directly — is lower-level
  with no compile-time check, and an ORM (`diesel`,
  `sea-orm`) introduces an abstraction the engine does not
  need.
- **Kafka**: `rdkafka`. Wraps `librdkafka` via Rust FFI, which
  in turn is the canonical Kafka client across the ecosystem
  (used by the C / Python / Node clients downstream). The pure-
  Rust alternatives (`rskafka`, `kafka-rust`) are less
  exercised at production scale; the FFI cost of `rdkafka` is
  paid once per process and the implementation maturity is
  worth it.
- **Redis**: not used engine-side (per §7). Listed here for
  symmetry; the engine has no Redis dependency.

### Control plane (Go)

- **Postgres**: `pgx/v5`. The modern Go Postgres driver. Native
  protocol support (no `database/sql` indirection unless
  desired), connection pooling built in, prepared-statement
  caching, JSONB-aware. The historical alternative `lib/pq` is
  in maintenance mode — no new features, security fixes only —
  and ADR-0023 §8 explicitly commits to `pgx/v5` so R4.2's
  implementation does not reach for `lib/pq` by reflex.

The control plane has no Kafka or Redis dependency; the only
new dependency R4 introduces control-plane-side is `pgx/v5`.

### Playwright adapter (Node / TypeScript)

- **Redis**: `ioredis`. Mature, maintained, supports the
  full Redis command surface, includes Cluster / Sentinel
  support for v1alpha2 if Spectre's deployment shape evolves.
  The alternative `node-redis` is also viable; `ioredis` is
  selected for its slightly larger feature surface and
  community familiarity.

The Playwright adapter has no Postgres or Kafka dependency.

### SeleniumBase adapter (Python)

- **Redis**: `redis-py` (the official driver, package name
  `redis`). Maintained by the Redis team itself; the de-facto
  standard Python client. Async support via `redis.asyncio`
  if SeleniumBase's adapter wrapper goes async; v1alpha1 of
  the adapter is sync, so the sync API is the path R4.3 takes
  initially.

The SeleniumBase adapter has no Postgres or Kafka dependency.

### curl-impersonate adapter (Go)

- **Redis**: `go-redis/v9`. Mature Go Redis client; idiomatic
  context-aware API. The alternative `redigo` is older and
  less actively developed; `go-redis/v9` is selected.

The curl-impersonate adapter has no Postgres or Kafka
dependency.

### Pinning discipline

Each library lands at a specific minor version pinned in the
respective dependency manifest at R4.2 / R4.3 / R4.4 time:

- Engine: `Cargo.toml` `[dependencies]` block, pinned via
  semver `~` operators on minor (e.g. `sqlx = "~0.8"`).
- Control plane: `core/control-plane/go.mod`, pinned via the
  Go module system.
- Playwright: `adapters/playwright/package.json`, pinned via
  `npm`'s `^` (caret-major).
- SeleniumBase: `adapters/seleniumbase/pyproject.toml`,
  pinned via PEP 440 `~=` (compatible-release).
- curl-impersonate: `adapters/curl-impersonate/go.mod`,
  pinned via the Go module system.

The implementation PRs (R4.2 / R4.3 / R4.4) commit specific
versions; this ADR commits the *libraries* and the *pinning
discipline*, not the version numbers themselves. Library
version bumps over the project's life are normal-course
maintenance; library *replacements* require revisiting this
section.

## §9 — Compose stack composition

R6.2 lands the local Compose stack. ADR-0025 will record the
full stack design; this section commits the stateful-service
slice of it so R4.2 / R4.3 / R4.4 can rely on a known shape
when their integration tests run against it.

Three image choices, picked for footprint and operational
parity with what the Helm chart will run in production:

- **Postgres**: `postgres:16-alpine`. Roughly 80 MB
  compressed, ~250 MB resident. PostgreSQL 16 is the version
  the schema (§2) is tested against and the version the
  Helm chart's Bitnami subchart targets by default (§10).
  Alpine base keeps the image small; the Postgres binary is
  upstream-built so behaviour matches the Bitnami / vanilla
  Postgres images operators run elsewhere.
- **Kafka**: `redpandadata/redpanda:latest` (single-node
  configuration). Roughly 150 MB compressed, ~250 MB
  resident under load. Wire-protocol compatible with Apache
  Kafka 0.11+ — the producer code does not branch on broker
  identity — and ships as a single Go binary with no
  ZooKeeper dependency. The choice trades the dev-loop
  startup cost (a full Kafka + ZooKeeper pair would take
  20-30 seconds to be admission-ready) for a few seconds of
  Redpanda startup. Production deployments use real Kafka
  (or Strimzi-managed Kafka, or Bitnami-managed Kafka — see
  §10); the Compose-stack convenience does not leak into the
  production model.
- **Redis**: `redis:7-alpine`. Roughly 30 MB compressed,
  ~10 MB resident at startup. The official Redis image on
  Alpine; nothing exotic.

Total stateful overhead in the dev stack: roughly 260 MB
compressed image weight, under 600 MB resident at idle. R6.3's
devcontainer (Docker-in-Docker) is sized accordingly.

Service names in the Compose `services:` block follow the
discovery convention from ADR-0021 §5: `postgres`, `kafka`
(the Redpanda container exposes itself as `kafka` so consumer
code reading `SPECTRE_KAFKA_BROKERS=kafka:9092` resolves the
right service), `redis`. Application services connect via
`postgres:5432`, `kafka:9092`, `redis:6379` — no extra
configuration, no port-forward dance.

The stack composition is recorded here so R4.2 / R4.3 / R4.4's
integration tests have a stable target. R6.2 implements the
`docker-compose.yml`; R8.1's documentation refresh will narrate
the stack from the operator's perspective.

