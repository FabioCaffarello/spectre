# Service catalog (v1alpha2)

> **Operational reference** to the 15-service catalog
> [ADR-0036 §3](../adr/0036-microservices-catalog-expansion.md)
> reserves. This document tracks **status per service** —
> planned (catalog entry; not yet built); authoring (build
> PR open or in flight); shipped (build PR merged; service in
> production smoke). Maintained as a forward-tracker through
> Waves 5 – 10 + v1beta1.

## §1 — The catalog

| # | Slot | Status | Wave | Language | Backend | Layer | First consumer |
|---|------|--------|------|----------|---------|-------|----------------|
| 1 | `proxy-broker` | planned | 5 | Go | Redis | 1.1 (proxy mgmt) | engine |
| 2 | `captcha-solver` | planned | 5 | Go | Postgres | 1.2 (CAPTCHA) | engine |
| 3 | `fingerprint-broker` | planned | 7 | Rust | Mongo + Redis | 1.3 (fingerprints) | engine |
| 4 | `rate-limit-broker` | planned | 7 | Go | Redis | 1.4 (rate limit) | engine |
| 5 | `session-store` | planned | 8 | Go | Mongo | 1.5 (sessions) | engine |
| 6 | `scheduler` | planned | 8 | Go | Postgres | 2.10 (scheduling) | operator |
| 7 | `cost-tracker` | planned | 9 | Go | Postgres | 3.4 (cost) | engine |
| 8 | `audit-log` | planned | 9 | Go | Mongo (time-series) | 3.6 (audit) | engine |
| 9 | `schema-registry` | planned | 6 | Go | Mongo | 4.1 / 2.5 (schemas) | engine + operator |
| 10 | `enricher` | planned | 10 | TBD (Python or Rust) | Mongo + Redis | 4.5 (enrichment) | engine |
| 11 | `dedup-service` | planned | 10 | Go | Redis | 4.6 (dedup) | engine |
| 12 | `input-broker` | planned | 6 | Go | Mongo | 5 (input mgmt) | operator + engine |
| 13 | `secret-broker` | planned | 8 | Go | Postgres or Vault | 7.9 (secrets) | every Go service |
| 14 | `driver-router` | **decision pending Wave 10** | 10 | TBD | Mongo (if persisted) | 6.3 – 6.5 (routing) | engine |
| 15 | `template-service` | deferred to v1beta1 | 11+ | Go | Mongo | 7.1 (templates) | user / operator |

**Status totals as of v1alpha2 architectural foundation
(R9 close)**: 14 planned (Waves 5 – 10) + 1 deferred to
v1beta1.

This catalog is **maintained as services materialise** —
each Wave 5+ build PR updates the affected service's row
to `authoring` (PR open) then `shipped` (PR merged) and
adds a "First materialised" date column.

## §2 — Per-service entries

Each service has a brief operational summary below. The
canonical *decisions* live in
[ADR-0036 §3](../adr/0036-microservices-catalog-expansion.md);
this section is the operational lookup.

### §2.1 — `proxy-broker` (slot 1)

- **Purpose**: centralise proxy IP acquisition, rotation,
  cooldown tracking, and budget accounting across adapters
  and engines
- **Providers**: BrightData, Oxylabs, Smartproxy, IPRoyal,
  NetNut, ScraperAPI, datacenter pools, self-hosted, Tor
  (research)
- **Backend rationale**: Redis canonical for
  cooldown counters + TTL caches per ADR-0039 §3.1
- **Cost emission**: per-acquire / per-release (provider-
  reported or estimated); per ADR-0038 §4.1

### §2.2 — `captcha-solver` (slot 2)

- **Purpose**: route CAPTCHA challenges (image, hCaptcha,
  reCAPTCHA, Turnstile) to providers; return solution tokens
- **Providers**: 2Captcha, Anti-Captcha, CapMonster,
  DeathByCaptcha, NopeCHA
- **Backend rationale**: Postgres for financial-record per-
  job costs per ADR-0039 §3.2 (anti-pattern §4.1: Mongo as
  financial store forbidden)
- **Cost emission**: per successful solve; failed solves
  typically not billed; per ADR-0038 §4.2

### §2.3 — `fingerprint-broker` (slot 3)

- **Purpose**: generate, store, rotate browser fingerprints
  (TLS JA3/JA4, header sequences, browser profiles)
- **Providers**: internal generator, BrowserScan API,
  FingerprintSwitcher, proxy-bundled fingerprints
- **Backend rationale**: hybrid per ADR-0039 §3.3 — Mongo
  for the deeply nested fingerprint corpus; Redis for hot
  ban-counter tracking
- **Language rationale**: Rust per ADR-0028 §3.2 —
  computation-heavy services fit Rust

### §2.4 — `rate-limit-broker` (slot 4)

- **Purpose**: coordinate per-domain, per-tenant, per-job-
  class request budgets across engines and adapters
- **Backend rationale**: Redis sliding-window counters per
  ADR-0039 §3.4 (canonical Redis pattern)

### §2.5 — `session-store` (slot 5)

- **Purpose**: persist browser session state (cookies, OAuth
  tokens, CSRF state) across adapter restarts and job
  boundaries
- **Backend rationale**: Mongo for richly nested per-target
  session shapes per ADR-0039 §3.5 (revised from ADR-0028
  §4.4's Postgres default — document model fits where
  rigid relational schemas hurt)

### §2.6 — `scheduler` (slot 6)

- **Purpose**: persist cron-style schedule entries; emit
  ScrapeJob CRDs at trigger time
- **Backend rationale**: Postgres mature scheduler
  ecosystem (`pg_cron` pattern) per ADR-0039 §3.6
- **Distinction from operator**: the operator reconciles
  CRDs that already exist; the scheduler creates them.
  ADR-0036 §1.2 supersedes ADR-0028 §6's "operator IS the
  scheduler" rejection with this reframe.

### §2.7 — `cost-tracker` (slot 7)

- **Purpose**: per-job cost ledger + per-tenant period
  rollups; webhook hooks for downstream invoicing
- **Backend rationale**: Postgres for financial-record ACID
  per ADR-0039 §3.7 (anti-pattern §4.1)
- **Out of scope**: invoicing itself (PDF generation,
  payment processing) per ADR-0038 §7

### §2.8 — `audit-log` (slot 8)

- **Purpose**: append-only per-job decision records (proxy
  chosen, selector matched, CAPTCHA triggered, retry
  executed)
- **Backend rationale**: Mongo time-series collections per
  ADR-0039 §3.8 (append-heavy + heterogeneous events +
  time-range queries)
- **Distinction from log aggregation**: audit-log is per-
  job semantic decisions, not stdout / structured-log
  collection. ADR-0036 §1.2 clarifies vs ADR-0028 §6's
  rejected `log-aggregator`.

### §2.9 — `schema-registry` (slot 9)

- **Purpose**: versioned output schemas (JSON Schema Draft
  2020-12); evolution rules; compatibility checks
- **Backend rationale**: Mongo per ADR-0039 §3.9 — JSON
  Schema is literally documents
- **Compatibility default**: `BACKWARD`; breaking changes
  register as new major versions per ADR-0034 §6

### §2.10 — `enricher` (slot 10)

- **Purpose**: post-extraction enrichment — geocoding,
  classification, embeddings; geospatial + vector queries
- **Providers**: geocoding (Mapbox / Google / OSM);
  classification (internal model / vendor LLM)
- **Backend rationale**: Mongo (geospatial 2dsphere; vector
  search via Atlas Vector) + Redis cache by content hash
  per ADR-0039 §3.10
- **Language rationale**: TBD at materialisation — Python
  if NLP/ML-heavy; Rust if CPU-bound transformation. Surfaces
  to maintainer at Wave 10 per ADR-0036 §3.4.

### §2.11 — `dedup-service` (slot 11)

- **Purpose**: deduplicate output rows across job
  boundaries (bloom filter + hash-set membership)
- **Backend rationale**: Redis canonical for hot-path
  membership per ADR-0039 §3.11

### §2.12 — `input-broker` (slot 12)

- **Purpose**: URL queue with per-URL lifecycle (seen →
  queued → in-flight → succeeded / failed → re-queued);
  per-batch progress; ScrapeBatch CRD orchestration
- **Sources**: sitemap, file (ConfigMap), API push, Kafka
  queue, seeded crawl per ADR-0033 §5
- **Backend rationale**: Mongo for heterogeneous URL
  metadata across input sources per ADR-0039 §3.12 —
  **documented exception** to anti-pattern §4.3 ("Mongo as
  generic queue broker")

### §2.13 — `secret-broker` (slot 13)

- **Purpose**: uniform-interface gRPC API for credential
  acquisition, rotation, audit; pluggable backends (Vault,
  AWS SM, K8s Secrets, Postgres)
- **Backend rationale**: Postgres or Vault per ADR-0039
  §3.13 — audit-integrity matters (anti-pattern §4.1
  applies in spirit)
- **Reframes ADR-0028 §6 rejection**: not a *replacement*
  for the deployment system's secret storage — a uniform-
  interface wrapper for cross-service access semantics,
  audit trails, and rotation primitives. ADR-0036 §1.2
  supersedes the rejection.

### §2.14 — `driver-router` (slot 14) — decision pending Wave 10

- **Purpose**: capability matching per target; cost-aware
  driver selection; fallback chain orchestration
- **Decision**: separate service vs engine module —
  surfaces to maintainer at Wave 10 per ADR-0035 §6 with
  full trade-off analysis. Catalog reserves the slot under
  both shapes.
- **Backend (if persisted)**: Mongo per ADR-0039 §3.14
- **Language**: TBD with the service-vs-module decision

### §2.15 — `template-service` (slot 15) — v1beta1

- **Purpose**: parameterised job definitions; instantiate
  with parameters at submission time
- **Status**: deferred to v1beta1 per ADR-0036 §3.8

## §3 — How to read this catalog

- **Status** progresses `planned → authoring → shipped`
  per Wave assignments. A service in `planned` has its slot
  reserved; the build PR has not opened. `authoring` means
  a build PR is open or in flight. `shipped` means the
  build PR has merged and the service runs in production
  smoke.
- **Wave** indicates the planned materialisation phase per
  [`docs/roadmap.md`](../roadmap.md) §4. Reordering is
  permissible only with maintainer approval — the
  dependency relationships (e.g., enricher depends on
  schema-registry) are encoded in the order.
- **Backend** matches
  [ADR-0036 §3.9](../adr/0036-microservices-catalog-expansion.md)
  byte-for-byte per the master prompt §15.4 catalog-
  consistency invariant. Drift between this catalog,
  ADR-0036 §3.9, and ADR-0039 §3.16 is a review-rejection
  trigger.
- **First consumer** identifies the service that motivates
  the build per ADR-0028 §5.1's admission gate (no
  speculative services). Multiple consumers may exist; the
  first column lists the materialising consumer.

## §4 — Updating this catalog

This catalog is **maintained as services land**:

1. **At PR open**: change status from `planned` → `authoring`;
   reference the build PR.
2. **At PR merge**: change status from `authoring` →
   `shipped`; add merge date; reference per-service
   `infra-services/<slot>/CHANGELOG.md` for ongoing
   per-service changes.
3. **At deferred / cancelled**: change status to
   `deferred` or `superseded` with an ADR amendment to
   ADR-0036 explaining the change.

Catalog updates are **PR-side** — each Wave 5+ build PR
includes a one-line update to this file as part of the
acceptance criteria.

## §5 — Reference materials

### ADRs

- [ADR-0026](../adr/0026-platform-taxonomy.md) — platform
  taxonomy; `infra-services/` category
- [ADR-0028](../adr/0028-ancillary-infra-services-catalog.md)
  — original five-slot catalog
- [ADR-0036](../adr/0036-microservices-catalog-expansion.md)
  — 15-service catalog expansion (the source for §1's
  table)
- [ADR-0037](../adr/0037-engine-as-orchestrator.md) — engine
  orchestrator pattern
- [ADR-0039](../adr/0039-mongodb-third-storage-tier.md) —
  per-service backend matrix; ratifies the §1 backend
  column
- [ADR-0033](../adr/0033-input-management-subsystem.md) —
  input-broker (slot 12) contract
- [ADR-0034](../adr/0034-output-schema-validation.md) —
  schema-registry (slot 9) contract
- [ADR-0035](../adr/0035-dsl-evolution-driver-abstraction.md)
  — driver-router (slot 14) decision deferral
- [ADR-0038](../adr/0038-cost-tracking-attribution.md) —
  cost-tracker (slot 7) contract

### Companion docs

- [`platform-architecture.md`](platform-architecture.md) §2
  — service catalog overview
- [`service-shape.md`](service-shape.md) — canonical service
  shape every catalog inhabitant follows
- [`storage-tiers.md`](storage-tiers.md) §2 — backend matrix
  reference
- [`engine-orchestrator.md`](engine-orchestrator.md) §1 —
  engine consumption per service

### Process

- [`docs/roadmap.md`](../roadmap.md) §4 — Wave 1 – 12
  detailed plan (R9.7)
- [`docs/v1alpha2-audit.md`](../v1alpha2-audit.md) — per-PR
  v1alpha2 phase history (R9.8)
