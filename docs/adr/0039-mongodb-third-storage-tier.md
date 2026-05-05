---
status: accepted
date: 2026-05-05
deciders: [Fabio Caffarello]
---

# MongoDB as third storage tier

## §1 — Context and Problem Statement

[ADR-0023](0023-stateful-services-architecture.md) committed
the v1alpha1 stateful tier set: PostgreSQL (job state) + Kafka
(output streaming, optional) + Redis (session metadata). §1's
backend evaluation surveyed alternatives — MySQL (rejected for
SQL feature parity vs Postgres), SQLite (rejected for
concurrency / multi-tenant deployment), CockroachDB (rejected
for operational complexity at v1alpha1 scale), DynamoDB
(rejected for vendor lock-in + per-row cost economics) — and
selected Postgres as the primary relational tier with Redis
covering hot key-value workloads and Kafka covering streaming.

ADR-0023 §1 did not consider **MongoDB**. The omission is a
real gap: web scraping's data shape has substantial natural
alignment with the document data model that ADR-0023's
evaluation framework — built around relational + key-value +
streaming primitives — could not surface.

[ADR-0036](0036-microservices-catalog-expansion.md)'s catalog
of fifteen services makes the gap operational. Walking the
seven-layer platform-vs-driver responsibility split through
the gate-based decision rule produces seven services whose
primary data shape is **document** (richly nested,
heterogeneous, schema-evolving) rather than relational or
key-value:

- `schema-registry` (slot 9) — versioned schemas; JSON Schema
  literally
- `input-broker` (slot 12) — URL queues with per-source
  metadata heterogeneity
- `session-store` (slot 5) — per-target sessions varying from
  simple cookies to multi-step OAuth + CSRF + refresh
- `audit-log` (slot 8) — append-heavy heterogeneous events
  with time-range queries
- `enricher` (slot 10) — geocoded outputs, classifications,
  embeddings; geospatial + vector workloads
- `fingerprint-broker` (slot 3, corpus) — TLS / browser
  fingerprints with deeply nested attribute hierarchies
- `template-service` (slot 15, v1beta1) — parameterised job
  definitions

Plus two services where Mongo is one of two viable shapes:

- `driver-router` (slot 14, if persisted) — per-target
  capability + cost models
- `fingerprint-broker` ban-counter side — Redis (hot path) +
  Mongo (corpus) hybrid

Forcing all seven primary-document services onto Postgres +
JSONB or Postgres + serialized blobs is **possible** but loses
the architectural advantages document storage provides
(schema flexibility without migrations, atomic
single-document writes that don't require transactions,
specialized indexes — geospatial 2dsphere, vector search,
time-series collections, change streams). At a 7-service
scale across the catalog, the loss compounds.

This ADR fills the gap. It rigorously evaluates each of
ADR-0036's fifteen services against backend selection
criteria; commits seven services to MongoDB primary +
two hybrid; preserves the four Postgres services + three
Redis services + two Postgres-or-Redis-hybrid services
unchanged from ADR-0023's tier set; and codifies the
operational consequences via the in-place [ADR-0023
§14 amendment](0023-stateful-services-architecture.md)
that lands in the same R9.2 PR as this ADR.

The ADR is **deliberately conservative**: it commits a
**Level 2 (Moderate)** Mongo adoption (§5) — Mongo earns its
operational addition by serving 7 services genuinely fitting
the document model, not by replacing Postgres or Redis where
they win. ADR-0023's tier framework expands to four storage
tiers; the discipline of "right tool per workload" (§4) is
the architectural commitment that prevents Mongo from
sprawling beyond justified.

### §1.1 — What this ADR does not yet land

No service code lands. ADR-0039 is contract-only. The first
Mongo-backed services materialise in **Wave 6** (schema-registry
+ input-broker per ADR-0036 §3.4 and §3.5); the chart's
`mongodb` Bitnami subchart and the Compose `mongodb` service
block land in the same Wave 6 build PR sequence. Wave 8 brings
session-store; Wave 9 brings audit-log; Wave 7 brings the
fingerprint-broker hybrid; Wave 10 brings enricher and the
driver-router decision; v1beta1 brings template-service.

This ADR + ADR-0023 §14 must merge **before any Wave 5+ build
PR opens** for a Mongo-backed service. Without them, the chart
has no Mongo subchart, the Compose stack has no Mongo block,
the per-language SDK matrix has no Mongo client choices, and
the deployment-shape contract has no entry for Mongo.

## §2 — Backend selection criteria

Before committing per-service backends, this section codifies
the **decision rule**. ADR-0023 §1 used an ad-hoc evaluation
narrative; ADR-0036 §2's six-gate rule generalises the
service-vs-library decision; ADR-0039 §2 generalises the
**backend-tier decision** for each service whose state lives
outside the engine.

A service's primary backend is chosen by applying criteria
A – D together, with E as tiebreaker. **If multiple criteria
align on Mongo**, pick Mongo. **If any criterion strongly
favours Postgres or Redis, lean to that.** The default is
*don't change* — services already specified with non-Mongo
backends keep them unless the criteria show clear advantage.

### §2.1 — Criterion A: Data shape

The dominant data shape in the service's persistent state.

| Shape | Backend |
|---|---|
| Highly heterogeneous + nested (varying field shapes per record; deep nesting) | **Mongo** |
| Strictly relational + normalized (predictable schema; cross-table integrity) | **Postgres** |
| Simple key → value or key → counter | **Redis** |

### §2.2 — Criterion B: Access patterns

The dominant access patterns the service serves.

| Pattern | Backend |
|---|---|
| Frequent atomic counters / TTL caches | **Redis** |
| Aggregations + reporting + cross-table joins | **Postgres** |
| Document point lookups + range scans + nested-path queries | **Mongo** |
| Geospatial queries (2dsphere, near, within) | **Mongo** |
| Full-text search | **Mongo** (Atlas Search) or external (Elasticsearch) |
| Time-series with TTL / archival | **Mongo** (time-series collections) or **Postgres** (TimescaleDB) |
| Vector search (embeddings) | **Mongo** (Atlas Vector) or external |

### §2.3 — Criterion C: Consistency requirements

| Requirement | Backend |
|---|---|
| ACID transactions across multiple records | **Postgres** |
| Single-document atomic ops | **Mongo** or **Postgres** |
| Eventually consistent / cache-shaped | **Redis** or **Mongo** |

### §2.4 — Criterion D: Ecosystem maturity for the use case

Some workloads have **canonical ecosystem implementations** in
specific backends. Picking against the canonical loses real
production-tested tooling.

| Workload | Canonical backend | Why |
|---|---|---|
| Financial / billing / invoicing | **Postgres** | Mature SQL ecosystem for billing; ACID; cross-record integrity |
| Audit logs / compliance | **Mongo** or specialized log stores | Append-heavy + heterogeneous; time-range queries |
| Schema registries | **Mongo** or Postgres + JSONB | JSON Schema is a document; Confluent's pattern uses Kafka itself |
| Queue brokers (high-throughput) | **Postgres** SKIP LOCKED or Redis Streams | SKIP LOCKED is gold standard; Mongo `findAndModify` adequate but not equally battle-tested |
| Job schedulers | **Postgres** | `pg_cron` ecosystem; mature scheduler libraries |
| Hot atomic counters / TTL caches | **Redis** | Sub-ms latency; canonical primitive |

### §2.5 — Criterion E: Operational concerns

The tiebreaker. All three production-ready storage tiers
(Postgres / Redis / Mongo) have mature operational tooling,
but the maturity profile differs:

| Concern | Postgres | Redis | Mongo |
|---|---|---|---|
| Backup / DR maturity | mature | weak (in-memory primary) | mature |
| OTel + Prometheus exporters | mature | mature | mature |
| Multi-tenant isolation | schemas mature | weaker | databases work; per-collection ACLs |
| Memory footprint (idle) | mid | low | high |
| Library matrix maturity (4 languages) | strong | strong | strong (official drivers per §14.3) |
| K8s subchart (Bitnami pinned) | yes | yes | yes |

Operational concerns are **largely a wash** at v1alpha2 scale.
The criterion serves as tiebreaker when criteria A – D split.

### §2.6 — The decision rule (formalised)

For each service `s` in ADR-0036's catalog with persistent
state:

1. Apply A (data shape). If shape is unambiguous (strictly
   nested-document or strictly relational or strictly KV),
   that's the backend.
2. If A is mixed, apply B (access patterns). The dominant
   pattern picks.
3. If B is mixed, apply C (consistency). Strong ACID
   requirement → Postgres; eventual consistency acceptable
   → Mongo or Redis per A.
4. If C doesn't break the tie, apply D (ecosystem). The
   canonical-backend match wins.
5. If D doesn't break the tie, apply E (operational). The
   tier with lowest marginal operational cost wins.

The default is **don't change**. ADR-0023's existing tier
choices (Postgres for job state, Redis for session metadata,
Kafka for streaming) are preserved unless §3's per-service
evaluation identifies a clear gain from moving.

## §3 — Service-by-service evaluation

This section evaluates each of ADR-0036's fifteen services
against §2's criteria. Format per service:

- **Data**: what state lives there
- **Access**: dominant patterns
- **Verdict**: 🟢 strong-Mongo / 🟡 mixed / ❌ keep-non-Mongo
- **Decision**: backend pick + rationale

### §3.1 — Slot 1: `proxy-broker` → Redis (unchanged)

- **Data**: cooldown table (proxy ID → cooldown timestamp);
  per-IP ban tracking; per-provider health scores
- **Access**: very frequent point reads ("is this proxy
  available?"); atomic increments (ban counts); TTL-based
  expiry
- **Verdict**: ❌ canonical Redis use case. Mongo would add
  latency without benefit.
- **Decision**: **Redis** — unchanged from ADR-0023's tier
  set + ADR-0036 §3.1 catalog entry.

### §3.2 — Slot 2: `captcha-solver` → Postgres (unchanged)

- **Data**: per-job cost ledger; per-tenant balance;
  per-provider success rates; transaction history
- **Access**: append-heavy (transactions); aggregations
  (cost per tenant per month); strict consistency for
  financial records
- **Verdict**: ❌ financial data → ACID matters. SQL
  ecosystem for billing/invoicing/reporting wins decisively.
  Criterion D strongly favours Postgres.
- **Decision**: **Postgres** — unchanged.

### §3.3 — Slot 3: `fingerprint-broker` → Mongo (corpus) + Redis (counters)

- **Data**: fingerprint corpus (TLS profiles, JA3 / JA4, header
  sequences, cipher suites); per-target ban counters
- **Access**: corpus reads by criteria (browser / OS / target
  compatibility); deeply nested document reads; atomic counter
  writes
- **Verdict**: 🟢 corpus → Mongo (deeply nested; schema
  flexibility absorbs HTTP/3, post-quantum TLS without
  migrations) + ❌ counters → Redis (hot path)
- **Decision**: **Mongo (corpus) + Redis (counters)** — hybrid;
  matches ADR-0036 §3.1 + ADR-0023 §14.1.

### §3.4 — Slot 4: `rate-limit-broker` → Redis (unchanged)

- **Data**: per-domain counters with sliding windows;
  robots.txt cache (with TTL)
- **Access**: very frequent atomic increments per request;
  TTL expiry; sliding-window operations
- **Verdict**: ❌ canonical Redis pattern (sliding-window
  rate limiting). Mongo writes have higher latency due to
  WiredTiger overhead and replication semantics.
- **Decision**: **Redis** — unchanged.

### §3.5 — Slot 5: `session-store` → Mongo (revised from Postgres)

- **Data**: per-tenant per-target cookies / OAuth tokens / CSRF
  tokens / refresh metadata / OAuth multi-step state; richly
  nested (cookie attributes; token refresh metadata; multi-step
  flows); encrypted at rest
- **Access**: load by `(tenant_id, target_domain)` at session
  start; save at session end; TTL-aware reads
- **Verdict**: 🟢 strong-Mongo. Sessions vary substantially
  across targets (simple cookies vs OAuth vs CSRF + refresh +
  session warming); document model fits where rigid relational
  schema hurts.
- **Decision**: **Mongo** — revised from ADR-0028 §4.4;
  matches ADR-0036 §3.1.

### §3.6 — Slot 6: `scheduler` → Postgres (unchanged)

- **Data**: scheduled job registry (cron expressions,
  next-run timestamps, last-run results, parameters)
- **Access**: query "what's due in next N minutes?"; update
  "this just ran with result X"
- **Verdict**: 🟡 neutral — slight Mongo edge for time-series,
  slight Postgres edge for ecosystem (`pg_cron`, mature
  scheduler libraries). Schedule entries are document-shaped
  (cron + parameters + run history); Postgres-backed
  schedulers are well-established.
- **Decision**: **Postgres** — unchanged. Ecosystem maturity
  (criterion D) wins; Mongo doesn't offer enough advantage
  for the operational addition.

### §3.7 — Slot 7: `cost-tracker` → Postgres (unchanged)

- **Data**: per-job cost ledger; per-tenant aggregations;
  billing periods
- **Access**: append-heavy writes; aggregations; strict
  consistency for financial records
- **Verdict**: ❌ same reasoning as captcha-solver — financial
  data, ACID, billing tooling. Anti-pattern §4.1 explicitly
  rejects Mongo here.
- **Decision**: **Postgres** — unchanged.

### §3.8 — Slot 8: `audit-log` → Mongo time-series (revised from S3 + index)

- **Data**: append-only decision records (proxy chosen,
  selector matched, CAPTCHA triggered, retry executed); highly
  heterogeneous event shapes
- **Access**: high-volume appends; query by `(job_id, timestamp
  range)`; rarely updated
- **Verdict**: 🟢 strong-Mongo time-series. Append-heavy +
  heterogeneous + time-range queries is exactly the workload
  Mongo time-series collections target; change streams enable
  real-time audit consumption.
- **Alternative considered + rejected**: OpenSearch / Elasticsearch
  (purpose-built for log search) — adds a fifth storage tier
  unjustified at v1alpha2 scale; revisit post-v1beta1 if audit
  volumes outgrow Mongo's time-series capacity.
- **Decision**: **Mongo time-series collections**; matches
  ADR-0036 §3.3.

### §3.9 — Slot 9: `schema-registry` → Mongo (revised from Postgres)

- **Data**: versioned schemas; evolution rules; compatibility
  checks
- **Access**: read-mostly (services validate); write-rarely
  (schema authors register)
- **Verdict**: 🟢 strong-Mongo. Schemas are literally documents
  — JSON Schema is the natural representation; atomic
  single-document writes prevent conflicting versions; uniqueness
  indexes enforce one-version-per-name.
- **Alternatives considered**: Kafka (Confluent's pattern),
  Postgres + JSONB — both work but lose the document fit.
- **Decision**: **Mongo**; matches ADR-0036 §3.4.

### §3.10 — Slot 10: `enricher` → Mongo (+ Redis cache)

- **Data**: enrichment outputs (geocoded coordinates, classified
  labels, embeddings); persistent enrichment history
- **Access**: cache reads by content hash; geospatial queries
  (2dsphere); vector search (semantic similarity); document-
  shaped outputs varying by enrichment type
- **Verdict**: 🟢 excellent Mongo fit — geospatial 2dsphere on
  geocoded outputs; Atlas Vector Search (6.0+ GA) on
  embeddings; flexible document shapes. No other backend
  matches Mongo's specialised indexes.
- **Decision**: **Mongo (primary) + Redis (content-hash cache)**
  — hybrid; matches ADR-0036 §3.4.

### §3.11 — Slot 11: `dedup-service` → Redis (unchanged)

- **Data**: dedup keys with TTL (bloom filter or hash
  store)
- **Access**: very frequent membership checks; frequent
  inserts; TTL expiry
- **Verdict**: ❌ Bloom filters in Redis (or in-process)
  are the canonical solution. Mongo provides no advantage
  for this hot path. Anti-pattern §4.2 explicitly rejects
  Mongo here.
- **Decision**: **Redis** — unchanged.

### §3.12 — Slot 12: `input-broker` → Mongo (revised from Postgres + Redis)

- **Data**: URL queue with per-URL lifecycle; per-batch
  progress; heterogeneous per-URL metadata (search-result URLs
  with `query+page`; sitemap URLs with `lastmod+priority`;
  API-pushed URLs with custom payloads)
- **Access**: append; atomic claim; lifecycle update; batch
  progress aggregate
- **Verdict**: 🟢 storage → strong-Mongo (heterogeneous URL
  metadata; document model fits diverse shapes); 🟡 queue
  semantics → mixed (Postgres SKIP LOCKED is gold standard;
  Mongo `findAndModify` adequate at scraping volumes —
  millions, not billions).
- **Decision**: **Mongo end-to-end** — schema flexibility for
  diverse input sources outweighs SKIP LOCKED purity at this
  scale. ADR-0033 (R9.4) re-justifies; **documented exception**
  to anti-pattern §4.3. Matches ADR-0036 §3.5.

### §3.13 — Slot 13: `secret-broker` → Postgres or Vault (unchanged)

- **Data**: encrypted secrets with rotation history;
  audit access
- **Access**: read by key (most common); audit log of access;
  rotation triggers
- **Verdict**: ❌ keep Postgres OR HashiCorp Vault. Secrets
  management benefits from strong audit-log integrity
  (financial-record-like). Postgres's transactional
  semantics + mature audit-log libraries win. Many secrets
  vaults (HashiCorp Vault) are storage-agnostic; the choice
  is per-deployment per ADR-0036 §3.6.
- **Decision**: **Postgres OR Vault** — unchanged. Mongo not
  preferred — audit integrity matters more than schema
  flexibility for secrets.

### §3.14 — Slot 14: `driver-router` → Mongo (if persisted) or in-memory

- **Data**: capability cache; per-target driver-success history;
  cost models
- **Access**: read by target characteristics; update by job
  outcomes; time-series success patterns
- **Verdict**: 🟢 good Mongo fit — heterogeneous per-target
  metadata, time-series success history, flexible documents.
- **Decision**: **Mongo (if persisted) OR in-memory + periodic
  snapshot**. ADR-0035 (R9.4)'s service-vs-engine-module
  decision determines whether persistence applies. Matches
  ADR-0036 §3.7.

### §3.15 — Slot 15: `template-service` → Mongo (v1beta1)

- **Data**: parameterised job definitions
- **Access**: read by template ID; instantiate with
  parameters
- **Verdict**: 🟢 excellent Mongo fit. Job templates are
  literally documents (DSL YAML / JSON with parameter
  placeholders). Schema flexibility for diverse template
  shapes naturally.
- **Decision**: **Mongo** when this lands in v1beta1.

### §3.16 — Backend matrix summary

| # | Slot | Backend | Source |
|---|------|---------|--------|
| 1 | proxy-broker | Redis | ADR-0023 (unchanged) |
| 2 | captcha-solver | Postgres | ADR-0023 (unchanged) |
| 3 | fingerprint-broker | **Mongo (corpus) + Redis (counters)** | ADR-0039 — hybrid |
| 4 | rate-limit-broker | Redis | ADR-0023 (unchanged) |
| 5 | session-store | **Mongo** | ADR-0039 — revised |
| 6 | scheduler | Postgres | ADR-0023 family — unchanged |
| 7 | cost-tracker | Postgres | ADR-0023 family — unchanged |
| 8 | audit-log | **Mongo time-series** | ADR-0039 — revised |
| 9 | schema-registry | **Mongo** | ADR-0039 — revised |
| 10 | enricher | **Mongo + Redis cache** | ADR-0039 — hybrid |
| 11 | dedup-service | Redis | ADR-0023 (unchanged) |
| 12 | input-broker | **Mongo** | ADR-0039 — revised |
| 13 | secret-broker | Postgres or Vault | ADR-0023 family — unchanged |
| 14 | driver-router | **Mongo (if persisted)** | ADR-0039 — new option |
| 15 | template-service | **Mongo** (v1beta1) | ADR-0039 — v1beta1 |

**Mongo adoption**: 7 services primary (5, 8, 9, 10, 12, 15
+ corpus of 3) + 2 hybrid (3 + 14 if persisted). **Postgres**:
4 services (2, 6, 7, 13). **Redis**: 4 services (1, 4, 11) +
hybrid use in 3 + 10. The catalog matches ADR-0036 §3.9
**byte-for-byte** per the master prompt §15.4
catalog-consistency invariant.

This matrix is **rigorously justified** — each service's
backend is defended by §2's criteria with a per-service
evaluation. The seven-Mongo adoption is not a rounded number
or a strategic posture; it is the count of services where
§2's rule lands on Mongo for substantive reasons.

## §4 — Anti-pattern catalog

The "right tool per workload" discipline requires explicit
guardrails preventing Mongo overuse — its schema flexibility
makes it tempting to use everywhere; resisting that
temptation is the architectural commitment.

### §4.1 — Anti-pattern: Mongo as primary financial store

**Forbidden**: Mongo for cost-tracker, captcha-solver costs,
billing data, secret-broker audit log.

**Why**: Postgres's ACID + foreign-key integrity matters for
financial data. Multi-row transactions, joins for invoicing,
mature billing tooling — all favour Postgres decisively.
Criterion D (ecosystem maturity) is decisive here.

### §4.2 — Anti-pattern: Mongo as hot atomic counter store

**Forbidden**: Mongo for proxy-broker cooldowns,
rate-limit-broker counters, dedup-service membership.

**Why**: Redis's atomic primitives + sub-millisecond latency
win unambiguously. Mongo writes have higher latency
(replication semantics, WiredTiger overhead). Criterion B
(access patterns) and E (memory footprint) both favour
Redis here.

### §4.3 — Caution: Mongo as generic queue broker

**Caution**: assuming Mongo is the right queue for any
URL-handling service just because URLs can be documents.

**Why**: Queue claim semantics matter — Postgres SKIP LOCKED
is the gold standard for high-throughput claim patterns.
Mongo `findAndModify` works but is not equally
battle-tested.

**Documented exception**: `input-broker` uses Mongo
end-to-end (§3.12) because schema flexibility for diverse URL
metadata across input sources is more valuable than queue
purity at scraping volumes (millions of URLs per batch, not
billions). The exception is documented; **other queue-shaped
services** that emerge in v1beta1+ revisit the Postgres
SKIP LOCKED option first.

### §4.4 — Anti-pattern: Mongo for "future flexibility I might need"

**Forbidden**: picking Mongo speculatively because "the
schema might evolve later".

**Why**: Pick based on the data shape **today**. If the shape
is currently relational and might-be-document later, pick
Postgres + JSONB. JSONB covers most flexibility needs without
the operational addition of a third storage tier. Speculative
Mongo adoption inflates the catalog past Level 2 (§5)
without earning the addition.

### §4.5 — Anti-pattern: Mongo replacing Postgres

**Forbidden**: framing this ADR as "Mongo replaces Postgres".

**Why**: ADR-0023's Postgres tier is preserved. Mongo is
**added** as a third storage tier where it wins. The
architecture has **three persistent storage tiers** —
Postgres, Redis, Mongo — each used where its criterion-A
data shape, criterion-B access patterns, and criterion-D
ecosystem maturity align. ADR-0036's catalog has 4 Postgres
services; they are **not migrating** to Mongo.

### §4.6 — Anti-pattern: Mongo without indexing strategy

**Forbidden**: shipping a Mongo-backed service without:

- Indexes for every query path used in production
- Index analysis (`explain('executionStats')`) before merge
- Monitoring on slow queries
- Plans for index size growth

**Why**: Mongo's "schema flexibility" is real but indexing
discipline is mandatory. Schema flexibility shifts cost from
schema migrations (Postgres) to indexing vigilance (Mongo).
ADR-0036 §5's canonical service shape mandates indexing
requirements as part of the per-service Mongo build PR's
acceptance criteria.

## §5 — Three adoption levels

Three plausible levels of Mongo adoption considered. Each
is a distinct architectural posture with distinct trade-offs.

### §5.1 — Level 1: Conservative (1 – 2 services on Mongo)

- audit-log only (or audit-log + session-store)

**Pros**: Lowest operational addition. Mongo earns its keep
via specialised time-series collections for audit.

**Cons**: Most catalog services that benefit from Mongo
(input-broker, schema-registry, enricher, fingerprint corpus)
keep their non-ideal backends. The §3 evaluation finds clear
per-service Mongo wins for 7 services; Level 1 leaves 5 of
them on suboptimal backends.

**Total Mongo adoption**: 6 – 13% of catalog services.

### §5.2 — Level 2: Moderate (7 services on Mongo + 2 hybrid) — RECOMMENDED

- session-store, audit-log, schema-registry, input-broker,
  enricher (5 services Mongo-primary)
- fingerprint-broker corpus, driver-router (if persisted)
  (2 hybrid)
- template-service (v1beta1, Mongo when materialised)

**Pros**: Mongo earns its operational addition by serving 7
catalog services that genuinely fit the document model. Each
backend choice is **rigorously justified** by §3's
per-service evaluation. The "right tool per workload"
discipline (§4) prevents sprawl beyond the justified set.

**Cons**: Operational addition is real (§6) — Helm subchart,
Compose entry, backup / DR posture, monitoring, library
matrix maintenance, indexing discipline, cognitive load on
contributors.

**Total Mongo adoption**: 33 – 47% of catalog services
(7 / 15 primary + 2 / 15 hybrid).

### §5.3 — Level 3: Aggressive (Level 2 + L0 / L1 lake on Mongo)

- Level 2 +
- Mongo as L0 sink option (ADR-0024 amendment)
- Mongo as Bronze layer storage (ADR-0029 amendment)
- Bronze + Silver pipelines via change streams

**Pros**: Maximum platform alignment with document data
model. Real-time L0 → L1 pipelines via change streams.

**Cons**: Substantial operational addition. Bronze layer
storage choice has long-term consequences — data migration
across storage tiers is hard.

**Total Mongo adoption**: 53%+ of services + lake.

### §5.4 — v4 framework recommendation: Level 2

Reasoning:

- **Each service's pick is rigorously justified** (§3 has
  per-service criteria evaluation)
- **Operational addition is real but earned** — 7 services
  serve genuinely better with the document model
- **L0 / L1 lake decision deferred** (§7) — ADR-0024 +
  ADR-0029 amendments adding Mongo as a sink option and as
  Bronze storage are **substantial decisions** worth their
  own dedicated reasoning, not bundled into v1alpha2
- **Anti-pattern guardrails (§4)** prevent Mongo overuse

Level 1 underuses Mongo — services that fit the document
model keep ill-suited backends. Level 3 over-commits before
seeing how Level 2 lands operationally. Level 2 is the
threshold where Mongo earns its addition; v1beta1+ revisits
the lake decision with Level 2 evidence.

**This ADR commits Level 2.**

## §6 — Operational implications (7 costs)

Adding Mongo as a third storage tier has concrete
operational costs. ADR-0039 articulates them honestly so
v1alpha2 contributors understand the addition is real.

### §6.1 — Helm chart growth

`build/helm/spectre/Chart.yaml` adds the Bitnami `mongodb`
subchart pinned per ADR-0030's policy; `values.yaml` adds a
`mongodb:` block (replica-set / standalone, auth, persistence,
resources); `values-ci.yaml` adds CI overrides. First Mongo
build PR (Wave 6) lands these.

### §6.2 — Compose stack growth

`docker-compose.yml` adds a `mongodb` service block following
[ADR-0025](0025-compose-stack.md) §3's pattern. Profile: `core`
(always-on; required tier per ADR-0023 §14.2 from Wave 6 onward).

### §6.3 — Backup / DR posture

Mongo's tooling (`mongodump`, oplog tailing) is mature but
distinct from Postgres / Redis. The first Wave 6 build PR
documents backup schedule, restore procedures, replica-set
considerations, point-in-time recovery; ongoing maturity is
Wave 8+ evolution.

### §6.4 — Monitoring / observability

`mongodb-exporter` deploys alongside the existing Postgres /
Redis / Kafka exporters. Per-service metrics: slow queries,
lock percentages, connection pool saturation, index usage.
ADR-0031 (R9.3) governs the cross-cutting framework;
ADR-0036 §5.4 includes Mongo-specific metrics for
Mongo-backed services.

### §6.5 — Library matrix maintenance

Per-language Mongo drivers per ADR-0023 §14.3 (Go
`mongo-go-driver`; Rust `mongodb` crate; Python `pymongo` /
`motor`; TypeScript `mongodb`). Standard pinning + security
+ breaking-change overhead.

### §6.6 — Indexing discipline

Each Mongo-backed service: indexes defined in code (idempotent
at startup); `explain('executionStats')` in PR reviews; slow
query monitoring in production; index size growth plans.
**Continuous work** — Mongo's schema flexibility shifts cost
from schema migrations to indexing vigilance. ADR-0036 §5
mandates indexing in the per-service build PR.

### §6.7 — Cognitive load

Three persistent storage tiers (plus Kafka = four stateful
tiers). Onboarding overhead: contributors internalise §2's
half-page decision rule for backend selection. ADR-0029 §3 +
ADR-0036 §3 + ADR-0039 §3 form the per-service backend
reference; CONTRIBUTING.md gains a "choosing a backend"
subsection in R9.6 / R9.7.

These seven costs are **earned** at Level 2 — 7 services serve
materially better with Mongo. Below 7, the operational addition
is unjustified (Level 1); above 7 (Level 3), the lake decision
is its own ADR sequence in v1beta1+ territory.

## §7 — L0 / L1 lake decision deferred

Beyond the 15 catalog services, there is the **scraped data
itself**. ADR-0029 §4 reserves four lake layers (L0 raw / L1
bronze / L2 silver / L3 gold). Two extensions of the Mongo
tier are **deferred** by this ADR:

### §7.1 — Mongo as L0 sink (ADR-0024 amendment, deferred)

Today's L0 sinks (per [ADR-0024](0024-output-sinks.md) §1 –
§4): stdout, Kafka, S3, HTTP webhook. Adding **Mongo as a
fifth sink** would absorb document-natural sites (e-commerce
listings, social posts, news articles, real estate listings)
where the L0 layer benefits from Mongo's schema flexibility +
change streams + geospatial / vector indexes.

**Deferred to v1beta1**. The decision involves:

- Amending ADR-0024 §3 / §4's sink contract
- Engine support for the Mongo sink (a Wave 8+ build PR
  scope)
- Per-tenant Mongo sink configuration
- Trade-off documentation: Parquet/Iceberg (analytics
  columnar aggregation, infrequent updates) vs Mongo
  (operational queries, frequent updates, geospatial / vector
  / change-stream patterns)

ADR-0029 §4's existing "Bronze tables as Parquet on S3,
Iceberg / Delta, Postgres schema" enumeration **does not
mention Mongo**; an ADR-0029 amendment would add Mongo as a
first-class Bronze / Silver storage option. Both amendments
defer.

### §7.2 — Mongo as Bronze layer storage (ADR-0029 amendment, deferred)

L1 Bronze (per ADR-0029 §4): conformed records — parsed PDFs
/ XLSXes / HTML extractions land here. Heterogeneous within
domains, conformed across them. **Excellent Mongo fit**:

- Geospatial queries on bronze data (real-estate price by
  location; businesses near a point)
- Time-series patterns (price history per SKU; sentiment
  over time)
- Vector search on bronze embeddings (semantic search)
- Change streams enable L1 → L2 stream pipelines

**Deferred to v1beta1**. The decision is its own ADR
sequence; bundling into v1alpha2 would inflate scope past
the Level 2 commitment.

### §7.3 — Why deferred

Three reasons:

1. **L2 (Moderate) commits first**. The 7-service catalog
   adoption is the threshold where Mongo earns operational
   addition. Until Level 2 ships and operationally proves
   itself across Waves 6 – 10, extending to lake layers is
   speculative.
2. **Lake decisions are hard to reverse**. Bronze storage
   choice has long-term consequences — data migration is
   hard. v1beta1's evidence base from Level 2 production
   operation is the right point to commit.
3. **ADR-0024 + ADR-0029 amendments would be substantial**.
   Bundling them into ADR-0039 expands the PR to ~1500 lines
   beyond the master prompt's ~700 estimate. Defer for
   focus.

ADR-0029's R9.4 amendment (when it lands per ADR-0036 §10's
forward-reference) may add a §4 footnote surfacing Mongo as a
future Bronze option without commit. The architectural
commitment defers.

## §8 — Confirmation (acceptance criteria)

The Mongo tier is working when the following hold **by the
close of Wave 9**:

- **The seven primary-Mongo services and two hybrids ship**
  per ADR-0036 §3 catalog and §3.16 backend matrix.
  schema-registry + input-broker land Wave 6;
  fingerprint-broker (corpus) lands Wave 7; session-store +
  secret-broker land Wave 8; audit-log lands Wave 9; enricher
  + driver-router (if persisted) land Wave 10.
- **The Helm chart's `mongodb` subchart ships** in the same
  Wave 6 build PR sequence as schema-registry + input-broker
  per §6.1.
- **The Compose `mongodb` service block ships** in the same
  Wave 6 sequence per §6.2.
- **No anti-pattern §4 violation** lands. Reviewers reject
  build PRs that:
  - Move financial data to Mongo (anti-pattern §4.1)
  - Use Mongo for hot atomic counters (anti-pattern §4.2)
  - Pick Mongo speculatively without §3-style criteria
    justification (anti-pattern §4.4)
  - Ship a Mongo-backed service without indexing strategy
    (anti-pattern §4.6)
- **Backend matrix consistency** — ADR-0036 §3.9 and
  ADR-0039 §3.16 list the same 15 services with the same
  backends. Drift between the two is a review-rejection
  trigger.
- **Library matrix discipline** — Mongo drivers are pinned
  per ADR-0023 §14.3 in each consumer's manifest at first
  use; bumps are routine; replacements require an ADR
  amendment.
- **Indexing strategy is enforced** — per-service Mongo
  build PRs include `explain('executionStats')` analysis in
  the PR description for each query path.

A signal that the §2 decision rule needs revision: more than
one Wave build PR encounters a real per-service backend pick
that doesn't fit §2's criteria + §3's evaluation rationale.
That's evidence the criteria are incomplete; the response is
an ADR amendment that refines the criteria, not an ad-hoc
backend choice.

## §9 — What's deferred / out of scope

R9.2 declines these deliberately. Each is a real concern;
each belongs to a later phase or to a sibling ADR.

- **Mongo as L0 sink + Mongo as Bronze storage** — deferred
  per §7.
- **Per-service Mongo schema details** — `$jsonSchema`
  validation rules, collection structure, index definitions
  are per-service build PR concerns. ADR-0036 §5's canonical
  service shape mandates indexing strategy in code; the
  specifics belong in the build PR.
- **Replica-set vs sharded-cluster topology** — v1alpha2
  default is replica-set in production; sharded-cluster
  topology is a v1beta1+ scaling concern.
- **Mongo Atlas (cloud-managed) vs self-hosted** — the chart
  + Compose default is self-hosted via Bitnami subchart;
  Atlas-managed deployments are supported via standard
  connection-string overrides but not the documented
  default.
- **Per-tenant Mongo database isolation** — multi-tenant
  isolation across Mongo databases is a v1beta1 concern;
  v1alpha2 default is per-service database with tenant-id
  embedded in document fields.
- **Mongo-side access-control granularity** — per-collection
  ACLs, field-level encryption, audit-log Mongo features —
  v1beta1+ as compliance work matures.
- **Time-series collection capacity tuning** — audit-log's
  retention policy, archival to S3 / cold storage, time-series
  bucket sizing — Wave 9 build PR concerns.
- **Vector index sizing for enricher** — embedding dimension,
  index parameters, Atlas Vector vs self-hosted — Wave 10
  build PR concerns.

## §10 — Reference materials

- [ADR-0023](0023-stateful-services-architecture.md) — the
  v1alpha1 stateful tier; ADR-0023 §14 (same R9.2 PR) extends
  it with Mongo and references this ADR for rigorous reasoning.
- [ADR-0024](0024-output-sinks.md) — output sinks; Mongo-as-L0
  amendment deferred per §7.1.
- [ADR-0026](0026-platform-taxonomy.md) — platform taxonomy;
  Mongo-backed services live under `infra-services/`.
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy; Mongo client
  libraries (§14.3) follow ADR-0027's admission gate.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) — the
  original five-slot catalog ADR-0036 extends.
- [ADR-0029](0029-data-platform-and-lake-dsls.md) — data
  platform; Mongo-as-Bronze amendment deferred per §7.2.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart;
  `mongodb` subchart pinning follows ADR-0030's existing pattern.
- [ADR-0036](0036-microservices-catalog-expansion.md) — the
  15-service catalog; §3.16 here matches ADR-0036 §3.9
  byte-for-byte (master prompt §15.4 invariant).
- [ADR-0037](0037-engine-as-orchestrator.md) — engine as
  orchestrator; per-service backend choice is invisible to
  engine orchestration.
- ADR-0032 (R9.3, forthcoming) — mTLS; Mongo X.509 auth (§14.4)
  follows the same per-service certificate provisioning.
- ADR-0033 / ADR-0034 / ADR-0038 (R9.4, forthcoming) — per-subsystem
  ADRs that materialise Mongo-backed services this ADR provisions.
- MongoDB official drivers: <https://www.mongodb.com/docs/drivers/>
- Bitnami `mongodb` Helm chart:
  <https://github.com/bitnami/charts/tree/main/bitnami/mongodb>
- Mongo time-series collections:
  <https://www.mongodb.com/docs/manual/core/timeseries-collections/>
- Mongo Atlas Vector Search:
  <https://www.mongodb.com/docs/atlas/atlas-vector-search/>
