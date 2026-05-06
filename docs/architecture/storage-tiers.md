# Storage tiers (v1alpha2)

> **Operational companion** to
> [ADR-0023 §14](../adr/0023-stateful-services-architecture.md)
> + [ADR-0039](../adr/0039-mongodb-third-storage-tier.md).
> Those ADRs commit the three-tier model + per-service backend
> matrix; this document is the operational reference for
> contributors picking backends and operators running them.

## §1 — The three-tier model

v1alpha2 stateful state lives in **three persistent storage
tiers** (Postgres + Redis + MongoDB) plus **Kafka** for
streaming. Each tier is used where its data shape + access
patterns + ecosystem maturity align — the discipline is
**right tool per workload**.

```
┌──────────────────────────────────────────────────────────────┐
│                    PERSISTENT STORAGE                        │
│                                                              │
│  ┌─────────────┐    ┌─────────────┐    ┌──────────────┐      │
│  │  POSTGRES   │    │    REDIS    │    │   MONGODB    │      │
│  │             │    │             │    │              │      │
│  │ relational  │    │ key→value   │    │  documents   │      │
│  │ ACID        │    │ TTL caches  │    │  geospatial  │      │
│  │ aggregations│    │ counters    │    │  vector      │      │
│  │ joins       │    │ sub-ms      │    │  time-series │      │
│  │ financial   │    │ bloom       │    │  flexible    │      │
│  │ data        │    │ filters     │    │  schemas     │      │
│  └─────────────┘    └─────────────┘    └──────────────┘      │
│                                                              │
│  Used by 4 services Used by 3 + 2 hyb. Used by 7 + 2 hyb.    │
└──────────────────────────────────────────────────────────────┘
                            +
┌──────────────────────────────────────────────────────────────┐
│                       STREAMING                              │
│                                                              │
│                       ┌──────────┐                           │
│                       │  KAFKA   │ output streaming;         │
│                       │ optional │ admission-gated per       │
│                       │ tier     │ ADR-0024 §5               │
│                       └──────────┘                           │
└──────────────────────────────────────────────────────────────┘
```

Required vs optional per
[ADR-0023 §14.2](../adr/0023-stateful-services-architecture.md):

| Tier | Production | Dev (Compose) |
|---|---|---|
| Postgres | REQUIRED | REQUIRED |
| MongoDB | REQUIRED (Wave 6+) | REQUIRED (Wave 6+) |
| Redis | REQUIRED | REQUIRED |
| Kafka | OPTIONAL | INCLUDED |

Once Wave 6 lands schema-registry + input-broker, MongoDB
joins Postgres + Redis as a required tier. There is no
"spectre without Mongo" mode beyond Wave 6.

## §2 — Per-service backend matrix

All 15 catalog services × 3 persistent tiers. Source:
[ADR-0036 §3.9](../adr/0036-microservices-catalog-expansion.md)
+ [ADR-0039 §3.16](../adr/0039-mongodb-third-storage-tier.md).

| # | Slot | Backend | Layer |
|---|------|---------|-------|
| 1 | proxy-broker | **Redis** (cooldowns, ban tracking) | 1.1 |
| 2 | captcha-solver | **Postgres** (financial; per-job costs) | 1.2 |
| 3 | fingerprint-broker | **Mongo (corpus) + Redis (counters)** | 1.3 |
| 4 | rate-limit-broker | **Redis** (sliding-window counters) | 1.4 |
| 5 | session-store | **Mongo** (richly nested per-target sessions) | 1.5 |
| 6 | scheduler | **Postgres** (cron-style registry; pg_cron pattern) | 2.10 |
| 7 | cost-tracker | **Postgres** (financial-record; ACID) | 3.4 |
| 8 | audit-log | **Mongo time-series** (append-heavy; heterogeneous events) | 3.6 |
| 9 | schema-registry | **Mongo** (JSON Schema is documents) | 4.1, 2.5 |
| 10 | enricher | **Mongo (primary) + Redis (cache)** (geospatial; vector) | 4.5 |
| 11 | dedup-service | **Redis** (bloom filters; hot-path membership) | 4.6 |
| 12 | input-broker | **Mongo** (heterogeneous URL metadata; documented exception per ADR-0039 §4.3) | 5.1 – 5.6 |
| 13 | secret-broker | **Postgres or Vault** (audit integrity) | 7.9 |
| 14 | driver-router | **Mongo (if persisted)** or in-memory (decision Wave 10) | 6.3 – 6.5 |
| 15 | template-service | **Mongo** (parameterised job definitions) | 7.1 (v1beta1) |

Distribution: 7 Mongo-primary + 2 hybrid; 4 Postgres; 3
Redis + 2 hybrid.

## §3 — Decision criteria summary (when to pick each tier)

For contributors materialising new services or evaluating
backend changes, apply
[ADR-0039 §2](../adr/0039-mongodb-third-storage-tier.md)'s
five criteria together with E as tiebreaker:

| Criterion | Postgres | Redis | Mongo |
|---|---|---|---|
| **A. Data shape** | strictly relational + normalized | simple key → value or counter | heterogeneous + nested documents |
| **B. Access patterns** | aggregations + joins + reporting | atomic counters + TTL caches | document point lookups + range scans + geospatial + vector + time-series |
| **C. Consistency** | ACID transactions across multiple records | eventually consistent / cache-shaped | single-document atomic ops |
| **D. Ecosystem maturity** | financial / billing / invoicing; queue brokers (SKIP LOCKED); schedulers (pg_cron) | hot atomic counters; rate limiting; bloom filters | audit logs / compliance; schema registries; document storage |
| **E. Operational concerns** (tiebreaker) | mature backup / DR; mid memory | weak DR (in-memory primary); lowest memory | mature backup / DR; highest memory |

Default rule: **don't change** — services already specified
keep their backend unless the criteria show clear gain. New
services apply A–D together; if multiple align on Mongo,
pick Mongo; if any strongly favours Postgres or Redis, lean
to that.

## §4 — Anti-patterns (six rules preventing Mongo overuse)

Mongo's flexibility makes it tempting to use everywhere.
Resist. Per
[ADR-0039 §4](../adr/0039-mongodb-third-storage-tier.md):

| # | Anti-pattern | Use this instead |
|---|---|---|
| 1 | Mongo as primary financial store | **Postgres** — ACID + foreign-key integrity matters |
| 2 | Mongo as hot atomic counter | **Redis** — atomic primitives + sub-ms latency win |
| 3 | Mongo as generic queue broker | **Postgres SKIP LOCKED** is gold standard. *Documented exception*: input-broker (per §3.12 of ADR-0039 — schema flexibility for diverse URL metadata wins at scraping volumes) |
| 4 | Mongo for "future flexibility I might need" | **Postgres + JSONB** — covers most flexibility without operational addition |
| 5 | Mongo replacing Postgres | **Three-tier architecture, not replacement** — Postgres remains primary for relational data; Mongo is added where it wins |
| 6 | Mongo without indexing strategy | **Index discipline mandatory** — code-defined indexes; `explain('executionStats')` analysis in PR review; slow query monitoring |

Reviewers reject build PRs that violate any of these
without explicit `<!-- SURFACE: -->` HTML comments
justifying the deviation. Anti-pattern violations are
review-rejected, not silently accepted.

## §5 — Operational shape per tier

### §5.1 — Postgres operations

- **Helm subchart**: Bitnami `postgresql` pinned per
  [ADR-0030](../adr/0030-helm-chart-structure.md)
- **Compose service**: `postgres:16-alpine`
  ([ADR-0023 §9](../adr/0023-stateful-services-architecture.md))
- **Backup**: `pg_dump` + `pg_basebackup` patterns; mature
  ecosystem
- **Library**: `pgx/v5` (Go) per ADR-0023 §8;
  `sqlx` (Rust) for engine
- **Auth**: SCRAM-SHA-256; password rotation via secret-broker
  when it materialises (Wave 8)
- **Monitoring**: `postgres_exporter` + Prometheus per
  ADR-0031 §6.4
- **Indexing discipline**: standard SQL indexes; `EXPLAIN
  ANALYZE` in PR review

### §5.2 — Redis operations

- **Helm subchart**: Bitnami `redis` pinned per ADR-0030
- **Compose service**: `redis:7-alpine` (ADR-0023 §9)
- **Backup**: RDB snapshots + AOF; weaker DR than
  Postgres / Mongo (in-memory primary; persistent state
  reload required on restart)
- **Library**: `ioredis` (TypeScript), `redis-py` (Python),
  `go-redis/v9` (Go) per ADR-0023 §8
- **Auth**: ACL-based; password rotation via secret-broker
- **Monitoring**: `redis_exporter` + Prometheus
- **Sizing discipline**: memory-aware key TTLs; eviction
  policy per use case (`allkeys-lru` default; `volatile-lru`
  for cache-heavy workloads)

### §5.3 — MongoDB operations

- **Helm subchart**: Bitnami `mongodb` pinned per ADR-0023
  §14.1 + ADR-0030
- **Compose service**: `mongo:7` standalone for dev;
  replica-set in production
  ([ADR-0023 §14.4](../adr/0023-stateful-services-architecture.md))
- **Backup**: `mongodump` + oplog tailing; replica-set
  considerations; point-in-time recovery via oplog
- **Library**: `mongo-go-driver` (Go), `mongodb` crate
  (Rust), `pymongo` / `motor` (Python), `mongodb` (TS) per
  ADR-0023 §14.3
- **Auth**: SCRAM-SHA-256 minimum; X.509 cert-based via
  cert-manager when ADR-0032 lands (Wave 3+)
- **Monitoring**: `mongodb-exporter` + Prometheus;
  per-service metrics (slow queries, lock %, connection
  pool saturation, index usage)
- **Indexing discipline**: code-defined indexes idempotent
  at startup; `explain('executionStats')` in PR review;
  index size growth plans

### §5.4 — Kafka operations (separate from persistent tiers)

Kafka is **streaming**, not persistent storage in the
backend-matrix sense. See [`kafka.md`](kafka.md) for the
v1alpha1 operational guide. v1alpha2 changes for Kafka:

- The `queue` input source variant in ScrapeBatch
  (per ADR-0033 §5.4) consumes Kafka topics for URL
  ingestion
- The `kafka` output sink (per
  [ADR-0024 §3](../adr/0024-output-sinks.md)) ships rows
  to Kafka topics — unchanged from v1alpha1

## §6 — Reference materials

### ADRs

- [ADR-0023](../adr/0023-stateful-services-architecture.md)
  — original Postgres + Kafka + Redis tier set; §14
  amendment adds MongoDB
- [ADR-0024](../adr/0024-output-sinks.md) — output sinks;
  Kafka admission-gating
- [ADR-0030](../adr/0030-helm-chart-structure.md) — Helm
  chart; subchart pinning policy
- [ADR-0036 §3](../adr/0036-microservices-catalog-expansion.md)
  — 15-service catalog with per-service backend column
- [ADR-0039](../adr/0039-mongodb-third-storage-tier.md) —
  Mongo as third storage tier; backend selection criteria
  + service-by-service evaluation + anti-pattern catalog +
  operational implications

### Companion architecture docs

- [`platform-architecture.md`](platform-architecture.md) §3
  — three-tier overview
- [`service-shape.md`](service-shape.md) §8 step 12 —
  stateful dependency declaration in build PRs
- [`postgres.md`](postgres.md), [`redis.md`](redis.md),
  [`kafka.md`](kafka.md) — v1alpha1 operational guides
  (carry forward)

### External

- Bitnami charts: <https://github.com/bitnami/charts>
- MongoDB Atlas Vector Search: <https://www.mongodb.com/docs/atlas/atlas-vector-search/>
- pg_cron: <https://github.com/citusdata/pg_cron>
