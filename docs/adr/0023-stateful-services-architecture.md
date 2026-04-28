---
status: accepted
date: 2026-04-28
deciders: [Fabio Caffarello]
---

# Stateful services architecture

## §1 — Context and Problem Statement

Six PRs into the refactor, the architecture Spectre commits to
in ADR-0020 is operationally complete at the service-mesh layer.
ADR-0021 / ADR-0022 retired the subprocess-in-pod transport in
favour of TCP gRPC with env-var discovery (R2.1–R2.3); ADR-0019
+ ADR-0020 §5 reshaped the control plane into a thin gRPC client
of the engine (R3.1); ADR-0019's R3.2 addendum evolved the
ScrapeJob CRD to v1alpha2 with EngineRef and a four-variant
OutputSink discriminated union (R3.2). Every component now talks
to every other component over the network. What remains is what
the network has not yet been asked to carry: anything durable.
Job state lives in the engine's Tokio task and on the operator's
`Status` subresource — both volatile across Pod restart. Output
rows leave the engine via stdout with no streaming surface for
downstream consumers. Adapter session metadata — the UUIDs from
ADR-0010, cookie-jar paths, generation counters — lives in the
adapter's process memory. One Pod restart on any layer loses
every job in flight and every session a client thought it held.

R4 closes that gap. Three stateful services land together:
PostgreSQL for job state and audit, Kafka for output streaming,
Redis for adapter session metadata. The PRs that wire them —
R4.2 (Postgres), R4.3 (Redis), R4.4 (Kafka) — land in series,
but the architectural commitment is one decision because the
three services interlock. Postgres alone cannot recover a job
whose adapter session was lost on Redis-less restart. Kafka
alone cannot publish rows the engine never persisted. Redis
alone externalises metadata no other service indexes.
Piecemeal introduction would leave the system half-stateful,
with the recoverability matrix depending on which PR landed
when. ADR-0023 records the full commitment up front so R4.2 /
R4.3 / R4.4 ship coherent slices of one architecture, not three
negotiations of an emerging one.

## §2 — PostgreSQL

The engine writes one row to a `jobs` table on every admission,
mutates the status column on each transition, and (for
`OutputSink.Stdout` jobs only) appends each extracted row to
`job_rows` as a JSONB document. The control plane reads the
same tables to populate `ScrapeJob` `Status` and to serve
historical queries without round-tripping through the engine.
Both services share one database; the schema is normalised,
owned by the engine, and migrated through sqlx-cli at engine
startup (see §13).

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

The CHECK constraints enforce the same enum values that
v1alpha2's `ScrapeJobPhase` and `OutputSink` discriminants
encode at the API layer. ON DELETE CASCADE simplifies retention
— deleting a `jobs` row removes its audit rows in one
statement. `job_rows` is populated only when `output_sink_kind`
is `'stdout'`; Kafka / S3 / Webhook sinks bypass this table to
avoid double-write costs for no recoverability gain. The
`output_sink_kind` column is recorded for every job regardless
of variant, so a reader can answer "where did the output go?"
without joining.

The engine uses `sqlx` (compile-time-checked queries via
`query!()` macros). The control plane uses `pgx/v5`. The full
library matrix lives in §8; the rationale for not using
`lib/pq` (in maintenance mode) is recorded there. The dialect
is plain PostgreSQL, no vendor extensions; v1alpha1 pins the
Compose stack and the Helm subchart to PostgreSQL 16.

Alternatives evaluated: **MySQL** (less-ergonomic JSON than
JSONB; thinner K8s operator ecosystem); **SQLite** (multi-
writer scenario across separate Pods is not its design);
**CockroachDB** (geo-distribution complexity for a single-
cluster deployment); **DynamoDB / managed cloud DBs**
(excludes self-hosted deployments, which §10 keeps first-class).

## §3 — Kafka

Kafka is the streaming surface for `OutputSink.Kafka`. The
v1alpha2 schema's `KafkaSink.Brokers []string` and
`KafkaSink.Topic string` (R3.2,
`core/control-plane/api/v1alpha2/scrapejob_types.go` lines
132–142) already define the variant; the reconciler currently
rejects it at admission with "kafka output sink not yet
implemented (R4.4)". This section records the producer
contract R4.4 implements.

Topics follow the pattern `spectre.rows.<workspace>`. v1alpha1
has one workspace named `default`, so the canonical topic is
`spectre.rows.default`. Per-job topics were rejected: job
cardinality is unbounded, Kafka topics carry per-topic broker
metadata overhead, and runtime topic creation complicates the
producer's authorisation model. The workspace component is
reserved for v1alpha2 multi-tenancy.

Each topic uses 8 partitions by default (tunable via Helm
values). The partition key is the job's UUID. All rows for one
job land on one partition, so a downstream consumer that
orders by `(partition, offset)` sees the job's rows in
extraction order; work spreads roughly evenly across partitions
for many concurrent jobs.

One Kafka message per row. The body is the JSONL row exactly
as the engine would have written to stdout. Headers carry
metadata for routing and filtering without parsing the body:
`job_id` (UUID matching `jobs.id`), `row_index` (monotonic per
job), `driver` (`playwright` / `seleniumbase` /
`curl-impersonate`), `timestamp` (ISO-8601).

The engine uses `rdkafka` (see §8). Brokers compatible with
the Apache Kafka 0.11+ wire protocol all work. R7.1's
production path defaults to a Strimzi or Bitnami Kafka
deployment; R6.2's Compose stack uses Redpanda single-node —
wire-compatible, single binary, no ZooKeeper. The producer
code does not branch on broker implementation.

Alternatives evaluated: **RabbitMQ** (queue-shaped semantics
delete on consume; Kafka's offset model is the right shape for
replayable streaming); **NATS JetStream** (thinner cross-
language ecosystem; weaker partition semantics); **Apache
Pulsar** (BookKeeper / ZooKeeper add complexity).

R4.4 wires the producer and removes the admission-time
rejection from the v1alpha2 reconciler. R5.1 (S3 + Webhook)
shares the discriminated-union shape from R3.2 but routes to
its own destinations.

## §4 — Redis

Each adapter maintains a session table internally: a
session_id (UUID) maps to a browser context or HTTP client
object holding cookies, navigation history, and a generation
counter used for stable-node tracking under ADR-0010 §3. R4.3
externalises the *metadata* layer to Redis. Runtime objects
stay process-local; the index that lets the adapter find them
lives in Redis.

Two keys per session, namespaced by adapter:

- `session:<adapter>:<session_id>` — JSON document holding
  creation timestamp, current URL, cookie-jar path, generation
  counter, stable-node map summary.
- `session:<adapter>:<session_id>:ref` — last-access
  timestamp; LRU-eviction signal.

`<adapter>` matches the `driver` value in §3 Kafka headers and
the `jobs.driver` column in §2. `<session_id>` is the same UUID
returned on `Initialize` and carried on every subsequent RPC.

The metadata key carries a 1-hour idle TTL, refreshed on each
session-bound RPC via `EXPIRE` on both keys. A session unused
for an hour falls out of Redis; the adapter invalidates the
in-memory runtime on its next eviction sweep. The default is
configurable per environment via Helm values.

Writes are PUT-style overwrites — the full document is
rewritten atomically on each update via `SET`. No field-level
updates, no Redis transactions. The `:ref` key is updated
separately; the two keys can momentarily disagree but `:ref`
is an eviction hint, not a correctness input, and the cost of
`MULTI/EXEC` outweighs the benefit.

Three adapters in three languages each pick their ecosystem's
production-tested client (full matrix in §8): `ioredis`
(Playwright), `redis-py` (SeleniumBase), `go-redis/v9`
(curl-impersonate). Engine and control plane do not connect to
Redis.

Alternatives evaluated: **Postgres session table** (per-RPC
sub-millisecond metadata writes; Postgres's parser/planner is
the wrong tool); **adapter-local file storage** (survives in-
place restart, not reschedule; hides the §5 choice);
**in-memory only** (today's behaviour — every restart loses
every session).

## §5 — The session externalization problem

Adapter sessions cannot be Redis-cached. A "session" at the
runtime level is a Playwright `BrowserContext`, a SeleniumBase
`SB` instance, or a curl-impersonate `*http.Client` — a live
process-bound object holding sockets, file descriptors, and
language-runtime memory that does not survive serialisation.
The metadata that *describes* the session (session_id, cookie-
jar path, last URL, generation counter) is serialisable. The
session itself is not. R4.3 externalises the metadata; the
runtime stays process-local. The consequence — what happens on
adapter Pod restart — has three reasonable answers and one
chosen direction.

### Option A — restart invalidation (chosen)

When an adapter Pod restarts, every session it held becomes
invalid. Redis metadata persists; the adapter, on startup, sees
session keys whose runtime objects no longer exist and does not
attempt to reconstitute them. Clients that hold session_ids
from before the restart see `UNAVAILABLE` (or
`FAILED_PRECONDITION` on the first session-bound RPC after
restart) and re-call `Initialize` to allocate fresh
session_ids backed by fresh runtime objects.

The cost is borne by the client. A client mid-job sees a
session fail and must restart its work from the session
boundary. For Spectre's primary use case — engine driving an
adapter for a single ScrapeJob's duration — the cost is
"restart the job", and v1alpha2's reconciler already retries
failed jobs on the next reconcile. For longer-lived sessions
(conformance suite, interactive debugging tools), the cost is
one round of `Initialize` and the loss of in-session state.
The cost is real and bounded.

The benefit is a contract a reader can hold in their head:
*session_ids are valid until the adapter Pod restarts.* No
"sometimes it survives" matrix. No promises about partial
recovery the implementation cannot keep. The contract matches
what the runtime actually delivers.

### Option B — sticky sessions (rejected)

Route every RPC for a session_id to the specific Pod that
allocated it — via client-side affinity (the client remembers
`(session_id, pod_address)` and dials directly) or Service-
level routing (a session-aware proxy or a Service with
`sessionAffinity: ClientIP`).

This preserves session liveness across most operational events
at the cost of two compromises. First, the client must know
about Pod-level addressing rather than Service-level; the
v1alpha1 transport contract (ADR-0022) is plaintext gRPC over
a single Service endpoint, and sticky sessions push Pod
identity into the discovery contract. Second, horizontal
scaling gets constrained — a client pinned to Pod A cannot
fail over to Pod B if Pod A crashes, so the Service's
redundancy guarantee weakens from "any Pod can serve any RPC"
to "any Pod can serve any *new* RPC". The architectural
surface (proxy logic, client-side address caching, re-pinning
on Pod death) is non-trivial, and the recovery semantics it
preserves are partial: the cost is paid every day for a
benefit the failure case still does not fully cover.

### Option C — warm recovery (rejected)

On adapter Pod startup, the new Pod reads Redis, sees session
keys for its adapter, and reconstitutes runtime state — replay
the cookie-jar, restore navigation history, re-allocate browser
contexts pointed at last-visited URLs. Clients see their
sessions "still working".

The architectural problem is what "still working" hides.
Browser state is not the cookies and URL alone — it is the
JavaScript heap, WebSocket connections, per-tab event-listener
graph, rendering engine's per-page caches. Replaying cookies
and URL gives the client *some* of what they had — enough to
feel like the session survived — and silently misses the rest.
A client mid-form-fill, mid-CAPTCHA, mid-SPA-navigation finds
the recovered session plausibly different in ways the contract
does not specify and the implementation cannot fully enumerate.
Worse, partial recovery creates a false sense of resilience:
operators see "sessions survive Pod restart" in the docs and
design retry budgets accordingly, then run into the cases warm
recovery does not cover with no honest contract to fall back
on.

The complexity cost is real too. Each adapter has a different
runtime model (Playwright's CDP-driven Chromium tree,
SeleniumBase's Python-driven WebDriver session, curl-
impersonate's Go-driven HTTP client) and warm recovery needs a
per-adapter implementation of "given this Redis blob,
materialise an equivalent runtime". Three implementations,
three failure surfaces, and the union of "sessions sometimes
survive, sometimes silently degrade" replaces the simple
restart-invalidation contract.

### The choice and its cost

ADR-0023 commits to restart invalidation. Clients re-call
`Initialize` on `UNAVAILABLE`. The Redis metadata persists
across restart not as a recovery surface but as an indexable
record of which sessions existed — useful for debugging,
useful for v1alpha2 contracts that may build on it (multi-
tenant accounting, session-level audit), and useful as the
data structure sticky-session and warm-recovery options would
build on if v1alpha2 deliberately revisits the choice.

The cost — clients restarting jobs on adapter Pod restart — is
the cost the runtime imposes regardless of how the metadata
layer is organised. Restart invalidation makes that cost
visible and contractual. Sticky sessions hide it (until they
don't); warm recovery hides it (and creates harder-to-debug
failures when the recovery is incomplete). v1alpha2 may
revisit if real users run longer-lived sessions whose restart
cost is operationally significant; v1alpha1 commits to the
honest contract.

## §6 — Required vs optional

| Service   | Production | Dev (Compose) | Rationale |
|-----------|------------|---------------|-----------|
| Postgres  | REQUIRED   | REQUIRED      | Job state always persisted; no in-memory mode. |
| Kafka     | OPTIONAL   | INCLUDED      | Required only when a ScrapeJob selects `OutputSink.Kafka`. Admission rejects new Kafka sinks if unavailable. |
| Redis     | REQUIRED   | REQUIRED      | Session metadata always written. Defines the §5 restart-invalidation contract. |

**Postgres always.** Engine startup validates the dial, runs
migrations (§13), and only then registers the gRPC service. A
startup-time outage is a startup-time engine failure — visible
to `kubectl get pod` as a crash loop, to `docker compose up` as
non-zero exit. There is no "engine without Postgres" mode.

**Kafka admission-gated.** Engine startup validates the
producer dial with softer semantics. If the broker is
reachable, the engine logs "kafka producer ready" and accepts
admission of new Kafka-sinked ScrapeJobs. If unreachable, the
engine logs a warning, marks the Kafka admission gate disabled,
and continues to start. New Kafka-sinked ScrapeJobs are
rejected at admission with the same "kafka not available"
message R3.2 returns for "kafka not yet implemented" —
semantically distinct, surfaceably similar to the operator.
Jobs with other sinks continue unaffected. The architectural
distinction is that Kafka is a *consumer-chosen* destination
selected per-job by v1alpha2; an operator who never runs
Kafka-sinked jobs genuinely does not need Kafka at all.

**Redis always.** Adapter startup validates the dial — same
model as Postgres for the engine. The §5 contract requires
Redis as the session index; an adapter without Redis would have
to invent a different index or break the contract. Both are
worse than the simple "adapter requires Redis" rule.

Spectre runs the full stack or does not run. There is no
minimal-dependency variant. The trade-off — deployment-shape
constraint for operational-clarity gain — is recorded honestly
in R8.1's documentation refresh.

## §7 — Network topology

The post-R4 topology has eight long-lived services: control-
plane, engine, three adapters, Postgres, Redis, and (when an
operator runs it) Kafka.

```
control-plane ──gRPC──▶ engine ──gRPC──▶ adapter (3×)
      │                  │  │                │
      │ pgx/v5     sqlx   │  │ rdkafka        │ ioredis / redis-py / go-redis
      ▼                   │  ▼                ▼
  PostgreSQL  ◀───────────┘  Kafka          Redis
  (jobs,                     (spectre.       (session:*)
   job_rows)                  rows.*,
                              optional)
```

Six connection patterns: **engine → Postgres** (read/write,
one row per admission + status mutations + optional `job_rows`
appends); **engine → Kafka** (write only, shared producer);
**engine → adapters** (pre-R4 path, gRPC+TCP via ADR-0022,
discovery via ADR-0021 §5, unchanged); **adapters → Redis**
(read/write per RPC); **control plane → Postgres** (read only);
**control plane → engine** (pre-R4 path; ADR-0019 §5's
`EngineClientRunner` preserved).

Two non-connections: **adapters do not connect to Postgres**
(job state is the engine's concern; adapters do not know which
job they serve) and **adapters do not connect to Kafka**
(output streaming is the engine's concern). The adapter surface
to the rest of the system is exactly what the Driver Protocol
carries — the §1 frame stays intact.

The control plane gains the Postgres dial as a *new*
dependency, not a replacement: the engine dial drives
execution, the Postgres dial serves status reads.

## §8 — Library choices and pinning

Each library is the most production-tested, maintained option
in its ecosystem at the time of writing. Library *replacements*
require a follow-up ADR; library *version bumps* are normal-
course maintenance.

- **Engine (Rust).** `sqlx` for Postgres (compile-time-checked
  queries via `query!()` macros; Tokio-async; rejected:
  `tokio-postgres` (no compile-time check), `diesel` / `sea-
  orm` (ORM machinery the engine does not need)). `rdkafka`
  for Kafka (Rust FFI over `librdkafka`, the ecosystem's
  canonical Kafka implementation; rejected: `rskafka`,
  `kafka-rust` — pure-Rust but less production-exercised). No
  Redis dependency.
- **Control plane (Go).** `pgx/v5` for Postgres (modern Go
  driver with native protocol, connection pooling, prepared-
  statement caching, JSONB-aware). The historical alternative
  `lib/pq` is in maintenance mode — security fixes only, no
  new features — and ADR-0023 explicitly commits to `pgx/v5`
  so R4.2 does not reach for `lib/pq` by reflex. No Kafka or
  Redis dependency.
- **Playwright adapter (Node / TypeScript).** `ioredis` for
  Redis (mature, full command surface, includes Cluster /
  Sentinel for v1alpha2 evolution; the alternative
  `node-redis` is also viable but `ioredis` is selected for
  community familiarity).
- **SeleniumBase adapter (Python).** `redis-py` (the official
  driver, package name `redis`; maintained by the Redis team).
- **curl-impersonate adapter (Go).** `go-redis/v9` (idiomatic
  context-aware API; the older `redigo` is less actively
  developed).

Each library is pinned in the respective dependency manifest
at R4.2 / R4.3 / R4.4 time (`Cargo.toml`, `go.mod`,
`package.json`, `pyproject.toml`) using the ecosystem's
idiomatic compatibility-range operator. ADR-0023 commits the
library, not the version number.

## §9 — Compose stack composition

R6.2 lands the full Compose stack; ADR-0025 will record the
design. The stateful slice committed here so R4.2 / R4.3 / R4.4
integration tests have a stable target:

- **Postgres**: `postgres:16-alpine`, ~80 MB compressed,
  ~250 MB resident. Version 16 matches the schema's tested
  surface and the Helm subchart's default (§10).
- **Kafka**: `redpandadata/redpanda:latest` single-node, ~150
  MB compressed, ~250 MB resident. Wire-compatible with Kafka
  0.11+, single binary, no ZooKeeper. Production uses real
  Kafka via §10; the Compose convenience does not leak
  upward.
- **Redis**: `redis:7-alpine`, ~30 MB compressed, ~10 MB
  resident at startup.

Total: ~260 MB compressed image weight, under 600 MB resident
at idle. R6.3's devcontainer is sized accordingly. Service
names follow ADR-0021 §5: `postgres`, `kafka` (the Redpanda
container exposes itself as `kafka`), `redis`. Application
services connect via `postgres:5432`, `kafka:9092`,
`redis:6379` — no port-forward dance.

## §10 — Production deployment

R7.1's Helm chart packages stateful services as managed
subcharts; ADR-0026 records the chart design. The slice
ADR-0023 binds:

- **Postgres**: Bitnami's `postgresql` chart, pinned by major
  version. StatefulSet, PVC, Service, Secret. `postgresql.
  enabled: false` accepts an external Postgres.
- **Kafka**: two viable options, decision deferred to R7.1.
  Bitnami's `kafka` chart materialises Kafka directly
  (simpler for operators already on Bitnami subcharts);
  Strimzi's operator pattern composes better with Spectre's
  own operator-based architecture. The Helm-values shape
  (`kafka.enabled`, external-broker fallback) is independent
  of which is chosen.
- **Redis**: Bitnami's `redis` chart. StatefulSet (or master-
  replica under HA values), Service, Secret. `redis.enabled:
  false` accepts an external Redis.

Every subchart carries an `enabled: false` toggle. When
disabled, the operator supplies the connection URL via Helm
values, the chart references a Kubernetes Secret via
`valueFrom.secretKeyRef`, and the rendered Deployment's
`SPECTRE_*_URL` env vars resolve from the Secret. This pattern
lets self-hosted operators bring cloud-managed services
without forking the chart.

Connection URLs embed credentials. Production deployments must
not hard-code in `values.yaml`; the chart references Secrets
for every URL. R7.1's chart README documents the Secret shapes
the operator pre-creates. Storage sizing, the Postgres backup
story, and Kafka retention belong in ADR-0026 / R7.1.

## §11 — Migration order across phases

R4 lands in three PRs. The order reflects blast radius and
dependency direction.

**R4.2 first — Postgres.** Smallest blast radius. The engine's
write path and the control plane's read path are both new code
paths reviewable in isolation; existing stdout-sinked
ScrapeJobs continue to reach `kubectl logs` unchanged because
Postgres writes are *additions* to the engine's task graph, not
replacements. A reviewer can read the diff and verify the
stdout flow is intact.

**R4.3 second — Redis.** The highest-risk PR of the entire
refactor. The §5 restart-invalidation contract changes the
relationship clients have with adapter sessions. Three adapters
in three languages each get a Redis client, a write path, and
the `Initialize`-rejection-on-startup model. The conformance
suite is the canonical verification surface; R4.3 must keep the
13 / 12 / 6 capability assertions byte-for-byte identical
through the switch (ADR-0017's invariant). Lands after R4.2 so
the highest-risk PR rides on a known-good Postgres path; if
R4.3 introduces a regression, the bisect against R4.2 is clean.
R4.3 carries its own staged rollout — Playwright first as the
reference, SeleniumBase and curl-impersonate following.

**R4.4 last — Kafka.** Strictly depends on R4.2. The Kafka
producer runs alongside Postgres writes (the engine writes one
row to `jobs` regardless of sink; `output_sink_kind = 'kafka'`
for these jobs). Without R4.2, R4.4 would either skip `jobs`
writes for Kafka jobs (coverage gap) or duplicate R4.2's work.
Lowest existing-user impact: no production user exercises
`OutputSink.Kafka` today (R3.2's reconciler rejects it).

Cross-ADR seams: **ADR-0010** preserved — metadata moves to
Redis, `Initialize`-to-`Close` contract byte-for-byte
identical. **ADR-0017** preserved — 13 / 12 / 6 invariant
holds because Kafka / S3 / Webhook are *engine* capabilities,
not *driver-protocol* capabilities. **ADR-0019 §6** evolves —
control plane reads stdout when sink is `Stdout`; for the
other variants output flows directly to the sink. **ADR-0021
§5** extended with the three URL env vars (§12). No existing
ADR is superseded; R4 is purely additive.

## §12 — Configuration via env vars

ADR-0021 §5 established the env-var-per-dependency convention
for the service-mesh layer. ADR-0023 extends it to the
stateful services. Each service reads its own connection
configuration at startup; no central configuration store, no
ConfigMap holding the cross-service shape.

```
SPECTRE_POSTGRES_URL=postgres://user:pass@host:5432/dbname
SPECTRE_KAFKA_BROKERS=broker1:9092,broker2:9092
SPECTRE_REDIS_URL=redis://host:6379/0
```

The forms match each ecosystem's idiomatic convention:
`postgres://` per libpq (accepted by `sqlx` and `pgx/v5`);
comma-separated `host:port` for Kafka (accepted by `rdkafka`
directly); `redis://` per RFC (accepted by `ioredis`,
`redis-py`, `go-redis/v9`).

Per-service reading: engine reads Postgres + Kafka; control
plane reads Postgres only; each adapter reads Redis only. A
service that does not need a stateful-service dial does not
read its env var.

Connection URLs embed credentials. Production deployments
populate the env vars from Kubernetes Secrets via
`valueFrom.secretKeyRef`; R7.1's chart defaults to this. The
Compose stack carries credentials in `environment:` directly —
acceptable for local-dev throwaway values, not for production.

## §13 — Migrations and schema evolution

Migrations live in `core/engine/migrations/` as versioned SQL
files (`<timestamp>_<name>.sql`). R4.2 lands the first
(`<timestamp>_initial_schema.sql` containing §2's schema);
every subsequent schema change adds a new file. Timestamps are
immutable once committed; reordering or renaming after merge
is forbidden. sqlx records applied migrations in a
`_sqlx_migrations` table keyed on filename, so a migration the
engine has already applied does not run again.

The engine runs migrations at startup, before serving traffic:
connect using `SPECTRE_POSTGRES_URL`, apply any new files in
timestamp order, register the gRPC service, start serving. If
migrations fail (broken SQL, conflicting state, permission
error) the engine exits non-zero — under Helm a Pod crash
loop, under Compose a non-zero exit. The operator rolls back
the deployment or fixes the migration in a follow-up PR.

The embedded model was chosen over "separate Kubernetes Job
runs migrations, engine waits": one artifact, one deployment
topology, one log stream. Slow migrations delay engine
readiness, which for v1alpha1's small schema is not a concern.
A future migration large enough to make the embedded model
painful is itself an architectural signal worth a dedicated
ADR; the choice is reversible without retroactive ADR-0023
changes.

sqlx migrations are forward-only — no "down" scripts. Rolling
back a bad migration is a new forward migration that reverses
the change. The discipline matches production reality (where
"down" against live data is rarely the right action). SQL-level
idempotency (`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT
EXISTS`, equivalent guards) is the migration author's
responsibility, so a migration is safe to re-apply on a
partially-migrated database.

The schema's wire-level version is the latest applied
migration's timestamp. There is no semver on the schema —
v1alpha1 / v1alpha2 versioning lives at the CRD layer
(ADR-0019), not the storage layer. A reader needing "what
schema version is this database running" reads
`_sqlx_migrations`.

## More Information

- [ADR-0010 — Element lifecycle and capability gating](0010-element-lifecycle-and-capability-gating.md)
  (preserved; metadata layer moves to Redis, lifecycle
  contract byte-for-byte identical)
- [ADR-0017 — curl-impersonate extraction and final capability divergence](0017-curl-impersonate-extraction-and-final-capability-divergence.md)
  (preserved; 13 / 12 / 6 invariant carries through R4
  unchanged — sinks are engine capabilities, not driver-
  protocol capabilities)
- [ADR-0019 — Control plane architecture and ScrapeJob CRD](0019-control-plane-architecture-and-scrapejob-crd.md)
  (§6 evolves: control plane reads stdout when sink is Stdout;
  Kafka / S3 / Webhook output flows to its sink)
- [ADR-0020 — Microservices architecture supersession](0020-microservices-architecture-supersession.md)
  (the architectural anchor; this ADR is the §5 phase R4 work)
- [ADR-0021 — Service discovery](0021-service-discovery.md)
  (§5 env-var convention extended in §12)
- [ADR-0022 — TCP / gRPC transport](0022-tcp-grpc-transport.md)
  (v1alpha1 plaintext-gRPC stance; stateful services follow
  the same trusted-network assumption)
- sqlx documentation: <https://github.com/launchbadge/sqlx>
- rdkafka documentation: <https://github.com/fede1024/rust-rdkafka>
- pgx documentation: <https://github.com/jackc/pgx>
- Redpanda quick-start:
  <https://docs.redpanda.com/current/get-started/quick-start/>
- Strimzi (Kafka on Kubernetes): <https://strimzi.io/>
- Bitnami charts: <https://github.com/bitnami/charts>
- [`docs/refactor-audit.md`](../refactor-audit.md) — per-PR
  work plan for R4.2 / R4.3 / R4.4
- [`docs/refactoring-status.md`](../refactoring-status.md) —
  live phase tracker
