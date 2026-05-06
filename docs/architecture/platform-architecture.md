# Platform architecture (v1alpha2)

> **Entry point** for the post-refactor v1alpha2 architectural
> surface. Six companion documents expand each section's
> operational detail; the canonical *decisions* live in the
> ADRs at [`docs/adr/`](../adr/). This document records *how*
> the platform is shaped; the ADRs record *why* each shape was
> chosen.

## §1 — Platform-vs-driver responsibility split

Spectre's central architectural commitment: the **driver** is
responsible only for *what the page returns* (navigate, query
DOM, extract elements, return raw bytes); the **platform** is
responsible for *everything else around the driver call*
(proxy acquisition, CAPTCHA solving, fingerprint rotation,
rate limiting, session persistence, schema validation, output
enrichment, dedup, sink dispatch, cost tracking, audit).

```
┌──────────────────────────────────────────────────────────────────┐
│                     PLATFORM (15 services)                       │
│                                                                  │
│  acquisition │ workflow │ quality │ output │ input │ ops          │
│  ──────────  │ ──────── │ ─────── │ ────── │ ───── │ ────         │
│  proxy       │ schedule │ cost    │ schema │ input │ secret       │
│  captcha     │          │ audit   │ enrich │       │ template     │
│  fingerprint │          │         │ dedup  │       │              │
│  rate-limit  │          │         │        │       │              │
│  session     │          │         │        │       │              │
│                                                                  │
│  Plus: driver-router (routing intelligence — slot 14)            │
│        Decision: separate service vs engine module — Wave 10.    │
└──────────────────────────────────────────────────────────────────┘
                                │
                                ▼ orchestrated per step by
                       ┌─────────────────┐
                       │   ENGINE        │  ← v1alpha2 = conductor
                       │   (Rust)        │     not god object
                       └────────┬────────┘
                                │ Driver Protocol (frozen,
                                │ ADR-0001) over gRPC / TCP
                                ▼
                       ┌─────────────────┐
                       │   DRIVER        │  ← navigate / query /
                       │  (3 adapters)   │     extract only
                       │                 │
                       │  Playwright 13  │  ← capability divergence
                       │  SeleniumBase 12│     byte-for-byte preserved
                       │  curl-imp.    6 │     (ADR-0017 §1)
                       └─────────────────┘
```

The driver protocol is **frozen** per [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md);
the byte-for-byte capability divergence chain
(Playwright 13 ⊃ SeleniumBase 12 ⊃ curl-impersonate 6) is
preserved through every refactor PR per
[CONTRIBUTING.md](../../CONTRIBUTING.md)'s "Architectural
commitments" #3.

The platform owns the rest. v1alpha2 catalogues 15 services
across all seven layers of platform responsibility; see
[`service-catalog.md`](service-catalog.md) for the full list
with status per service.

## §2 — Service catalog overview

15 services across 7 layers
([ADR-0036](../adr/0036-microservices-catalog-expansion.md)):

| Layer | Services |
|---|---|
| 1. Acquisition | proxy-broker · captcha-solver · fingerprint-broker · rate-limit-broker · session-store |
| 2. Workflow | scheduler |
| 3. Quality / observability | cost-tracker · audit-log |
| 4. Output management | schema-registry · enricher · dedup-service |
| 5. Input management | input-broker |
| 6. Driver abstraction | driver-router *(decision pending Wave 10)* |
| 7. Operational | secret-broker · template-service *(v1beta1)* |

Service shape is **uniform** across the catalog — directory
layout, Helm chart fragment, observability surface, mTLS,
per-service CHANGELOG, per-service ADR tree all follow the
same template per
[ADR-0036 §5](../adr/0036-microservices-catalog-expansion.md).
See [`service-shape.md`](service-shape.md) for the
operational walkthrough; see
[`service-catalog.md`](service-catalog.md) for per-service
status.

## §3 — Three-tier storage architecture

The v1alpha2 stateful tier set is **four backends**: Postgres
+ Kafka (streaming) + Redis + MongoDB. The non-Kafka three
form the **three persistent storage tiers**, each used where
its data shape + access patterns + ecosystem maturity align:

| Tier | Used for | Catalog services |
|---|---|---|
| **Postgres** | Financial / audit-integrity data | captcha-solver · scheduler · cost-tracker · secret-broker |
| **Redis** | Hot atomic counters · TTL caches · bloom filters | proxy-broker · rate-limit-broker · dedup-service · *(+ hybrid use in fingerprint-broker, enricher)* |
| **MongoDB** | Document-shaped state · time-series · geospatial · vector | session-store · audit-log (time-series) · schema-registry · input-broker · enricher · fingerprint-broker (corpus) · driver-router (if persisted) · template-service |

The discipline is **right tool per workload** — Mongo
where documents win, Postgres where relations win, Redis
where speed wins. See [`storage-tiers.md`](storage-tiers.md)
for per-service rationale and the six anti-patterns
preventing Mongo overuse
([ADR-0039 §4](../adr/0039-mongodb-third-storage-tier.md)).

## §4 — Execution flow

```
user / scheduler
      │
      │ submits ScrapeJob / ScrapeBatch CR
      ▼
operator (control-plane)
      │
      │ reconciles → instructs engine (gRPC, mTLS-authenticated)
      ▼
engine
      │
      │ orchestrates per step:
      │  → input-broker (next URL)
      │  → proxy-broker (acquire)
      │  → fingerprint-broker (select)
      │  → rate-limit-broker (reserve)
      │  → session-store (load)
      │  → secret-broker (fetch)
      │  → schema-registry (fetch schema, cached)
      │  → driver-router (pick driver)
      ▼
driver (gRPC; Driver Protocol frozen)
      │
      │ navigate / query / extract → rows
      ▼
engine row pipeline:
      │  → schema-registry (validate)
      │  → enricher (geocode / classify / embed)
      │  → dedup-service (membership check)
      │  → sinks (Kafka / S3 / Webhook / stdout)
      │  → cost-tracker / audit-log (async fire-and-forget)
```

The engine's role evolves from *thick monolithic
orchestrator* (v1alpha1) to *conductor of platform services*
(v1alpha2). Per-step latency is mitigated by batching,
per-job caching, async-where-correct emissions, tunable
per-deployment service-disable flags, and chart-pinned
service co-location. See
[`engine-orchestrator.md`](engine-orchestrator.md) for the
operational walkthrough; the architectural decisions live in
[ADR-0037](../adr/0037-engine-as-orchestrator.md).

## §5 — DSL evolution at a glance

The ScrapeJob DSL evolves across four versions:

| Version | Surface | Status |
|---|---|---|
| **v1alpha1** | Driver-RPC-mirrored verbs (`navigate` / `query` / `extract` / `output`); flat; no control flow | Frozen per ADR-0001 |
| **v1alpha2** | v1alpha1 + 5 primitives (pagination, conditional, multi-step nav, schema declaration, transforms); driver-explicit | Wave 6 – 7 build PRs |
| **v1beta1** | Intent-declarative (capability hints replace `driver.kind`); driver-router-driven routing; `driverHint` opt-in | v1beta1 work; sketched in ADR-0035 §3.3 |
| **v1** | Fully abstract intent (target schema + site + SLA → platform decides execution plan) | Far-future; illustrative only |

v1alpha2 primitives are **engine-internal evolution** — the
DSL parser expands them into v1alpha1-shaped Driver Protocol
calls. The protocol freeze is preserved
([ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md)).
See [`dsl-evolution.md`](dsl-evolution.md) for the operational
walkthrough with full per-version examples and migration
paths.

## §6 — v1alpha1 vs v1alpha2 vs v1beta1 surface comparison

| Dimension | v1alpha1 (today) | v1alpha2 (this phase) | v1beta1 (next phase) |
|---|---|---|---|
| Service count | 4 (engine + 3 adapters) + operator | 14 + operator (template-service deferred) | All 15 + future Layer 7 |
| Stateful tiers | Postgres + Kafka + Redis | + MongoDB (4 tiers) | unchanged |
| DSL | Driver-RPC-mirrored | + 5 workflow primitives | Intent-declarative |
| Driver selection | Explicit `driver.kind` | Explicit (preserved) | `driverHint` opt-in; router default |
| Output | Untyped JSONL | Typed via schema-registry | Schema-on-read transforms |
| Input | Per-ScrapeJob URL list | + ScrapeBatch CRD + input-broker | + advanced source shapes |
| Observability | stdout logs only | OpenTelemetry (metrics + traces + structured logs) | + log aggregation + SLOs |
| Auth | None within cluster | Optional mTLS (chart flag default off) | mTLS default + per-service authorisation |
| Cost visibility | None | Per-job ledger + per-tenant rollups | + quota enforcement + forecasting |
| Multi-tenancy | Single-tenant assumed | Multi-tenant ready (per-tenant ledger / rollups / namespaces) | Full multi-tenant isolation |

The **structural commitments** (frozen Driver Protocol;
strict-subset capability chain; no legacy paths; Compose as
the dev environment; explicit ADR supersession; unweakened
tests) carry forward unchanged from v1alpha1 through v1beta1
per CONTRIBUTING.md "Architectural commitments".

## §7 — Reference materials

### ADRs that define the v1alpha2 architectural surface

- [ADR-0036](../adr/0036-microservices-catalog-expansion.md)
  — 15-service catalog + canonical shape
- [ADR-0037](../adr/0037-engine-as-orchestrator.md) — engine
  as orchestrator
- [ADR-0023 §14](../adr/0023-stateful-services-architecture.md)
  + [ADR-0039](../adr/0039-mongodb-third-storage-tier.md) —
  MongoDB tier
- [ADR-0031](../adr/0031-observability-framework.md) —
  observability framework
- [ADR-0032](../adr/0032-service-to-service-mtls.md) —
  service-to-service mTLS
- [ADR-0033](../adr/0033-input-management-subsystem.md) —
  input management subsystem
- [ADR-0034](../adr/0034-output-schema-validation.md) —
  output schema and validation
- [ADR-0035](../adr/0035-dsl-evolution-driver-abstraction.md)
  — DSL evolution and driver abstraction
- [ADR-0038](../adr/0038-cost-tracking-attribution.md) —
  cost tracking

### Operational companions (this directory)

- [`service-shape.md`](service-shape.md) — canonical service
  shape walkthrough
- [`service-catalog.md`](service-catalog.md) — 15-service
  status reference
- [`storage-tiers.md`](storage-tiers.md) — three-tier backend
  matrix
- [`engine-orchestrator.md`](engine-orchestrator.md) — engine
  v1alpha2 shape
- [`observability.md`](observability.md) — observability
  operational guide
- [`dsl-evolution.md`](dsl-evolution.md) — DSL trajectory

### v1alpha1 architectural baseline (preserved)

- [`overview.md`](overview.md) — v1alpha1 platform overview
  (canonical; this v1alpha2 doc adds forward-looking detail)
- [`engine.md`](engine.md), [`control-plane.md`](control-plane.md),
  [`driver-protocol.md`](driver-protocol.md) — per-component
  v1alpha1 detail
- [`postgres.md`](postgres.md), [`redis.md`](redis.md),
  [`kafka.md`](kafka.md) — stateful tier operational detail
- [`output-sinks.md`](output-sinks.md),
  [`container-images.md`](container-images.md),
  [`helm-chart.md`](helm-chart.md),
  [`production-smoke.md`](production-smoke.md),
  [`releases.md`](releases.md),
  [`development-environment.md`](development-environment.md)

### Process and roadmap

- [CONTRIBUTING.md](../../CONTRIBUTING.md) — v1alpha2 process
  rigor matrix (R9.0)
- [`docs/roadmap.md`](../roadmap.md) — Wave 1 – 12 detailed
  plan (R9.7)
- [`docs/v1alpha2-audit.md`](../v1alpha2-audit.md) — per-PR
  v1alpha2 phase history (R9.8)
