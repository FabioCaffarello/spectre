# Engine as orchestrator (v1alpha2)

> **Operational companion** to
> [ADR-0037](../adr/0037-engine-as-orchestrator.md). ADR-0037
> commits the engine's evolution from v1alpha1's monolithic
> orchestrator to v1alpha2's conductor of platform services;
> this document walks the operational shape with
> contributor-facing per-Wave detail.

## §1 — The engine's responsibilities

### What stays in the engine

Five responsibilities remain in `engines/engine/` per
[ADR-0037 §2.1](../adr/0037-engine-as-orchestrator.md):

| Responsibility | Why it stays |
|---|---|
| **DSL parsing** | Tightly coupled to plan generation; happens once at job start; no independent state |
| **Plan generation** | Engine-internal representation; no consumer outside the engine reads the plan; calls driver-router for routing decisions but the plan itself is engine-private |
| **Per-job in-memory state** | Job ID, retry counters, deadlines, row counts; lives only for the job's duration |
| **Driver gRPC client** | The Driver Protocol stays frozen ([ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md)); the engine talks to adapters through the unchanged contract |
| **Output sink dispatch** | Sinks are external systems consumed via [ADR-0024](../adr/0024-output-sinks.md)'s contract — not services in the catalog; sink dispatch logic lives in the engine |

Plus the new central role:

| Responsibility | Origin |
|---|---|
| **Service orchestration** | New v1alpha2 role — every per-step service call originates here |

### What moves to a service

13 responsibilities migrate out of the v1alpha1 engine to
catalog services across Waves 5 – 10:

| Responsibility | Service (slot) | Wave |
|---|---|---|
| Proxy acquisition / cooldown / failure tracking | `proxy-broker` (1) | 5 |
| CAPTCHA solving | `captcha-solver` (2) | 5 |
| Browser fingerprint generation / rotation | `fingerprint-broker` (3) | 7 |
| Rate-limit reservation | `rate-limit-broker` (4) | 7 |
| Session persistence across runs | `session-store` (5) | 8 |
| URL queue / batch progress | `input-broker` (12) | 6 |
| Output schema validation | `schema-registry` (9) | 6 |
| Post-extraction enrichment | `enricher` (10) | 10 |
| Pre-emit deduplication | `dedup-service` (11) | 10 |
| Cost emission | `cost-tracker` (7) | 9 |
| Audit emission | `audit-log` (8) | 9 |
| Credential acquisition | `secret-broker` (13) | 8 |
| Driver routing intelligence | `driver-router` (14) — **decision pending Wave 10** | 10 |

By Wave 10's close, the engine is **structurally minimal** —
DSL parsing, plan generation, per-job state, sink dispatch,
service orchestration. Every other v1alpha1 responsibility
has moved to a service.

## §2 — Per-step service orchestration

The engine's per-step loop fans out to 9+ services per
iteration at full materialisation. The execution flow:

```
                     scheduler                operator
                        │                        │
                        │ cron trigger          │ reconciles ScrapeJob/Batch
                        ▼                        ▼
┌─────────────────────────────────────────────────────────────────┐
│                          ENGINE                                  │
│  parses DSL → generates plan → orchestrates per step            │
└─────────────────────────────────────────────────────────────────┘
                               │
       ┌───────────────────────┼───────────────────────┐
       ▼                       ▼                       ▼
input-broker             proxy-broker          fingerprint-broker
(next URL)               (acquire)             (select)

       │                       │                       │
       └───────────────────────┼───────────────────────┘
                               ▼
                       rate-limit-broker (reserve)

                               │
       ┌───────────────────────┼───────────────────────┐
       ▼                       ▼                       ▼
session-store              secret-broker          schema-registry
(load)                     (fetch creds)          (fetch schema, cached)

                               │
                               ▼
                       driver-router (pick driver)

                               │
                               ▼
                       driver gRPC (Driver Protocol)
                       Navigate / Query / Extract → rows

                               │
       ┌───────────────────────┼───────────────────────┐
       ▼                       ▼                       ▼
schema-registry            enricher               dedup-service
(validate)                 (geocode/classify)     (membership)

                               │
       ┌───────────────────────┼───────────────────────┐
       ▼                       ▼                       ▼
sinks                     cost-tracker          audit-log
(Kafka/S3/Webhook)        (async emit)          (async emit)
```

Pseudocode per
[ADR-0037 §3.2](../adr/0037-engine-as-orchestrator.md):

```python
for each step in plan:
    url = input_broker.NextURL(batch_id)
    proxy = proxy_broker.Acquire(target=url.domain)
    fp = fingerprint_broker.Select(profile)              # cached per session
    rate_limit_broker.Reserve(scope=url.domain)
    session = session_store.Load(tenant, target)
    creds = secret_broker.Fetch(needed_secrets)
    schema = schema_registry.Get(schema_ref)             # cached per job
    driver = driver_router.Pick(target, capabilities)

    rows = driver.NavigateQueryExtract(url, proxy, fp, session, creds)

    for row in rows:
        validated = schema_registry.Validate(row, schema) # in-engine cache
        enriched = enricher.Enrich(validated)
        if dedup_service.IsNew(dedup_key(enriched)):
            sinks.Emit(enriched)

    rate_limit_broker.Release(scope, used=len(rows))
    session_store.Save(tenant, target, session.delta())
    proxy_broker.Release(proxy)
    cost_tracker.Emit(job_id, all_costs)                # async fire-and-forget
    audit_log.Emit(job_id, decisions)                   # async fire-and-forget
```

Per-step fan-out is **9 + N services** where N is the
per-row enrichment / dedup / sink count. Latency cost is
real and accepted; mitigation strategies §3 below.

## §3 — Latency mitigation strategies

A naive implementation calling 9+ services synchronously per
step at 5 ms each adds 45+ ms per step; a 100-step job
becomes 4.5 seconds slower than v1alpha1. Five strategies
hold the typical per-step overhead in the **~5 ms range**
post-mitigation per ADR-0037 §4.

### §3.1 — Batch operations

Where naturally batchable, pull N items per call rather than
1 per step:

- `input_broker.NextURLs(batch_id, n=100)` — 1 broker call
  per 100 steps instead of 1 per step
- `proxy_broker.AcquireBatch(target_domain, n=k)` — pre-
  acquire N concurrent leases
- `schema_registry.GetBatch([schema_refs])` — pre-fetch
  every schema the plan references at job start

Batch sizes are deployment-configurable
(`engine.batchSizes.<service>` in Helm values).

### §3.2 — Per-job / per-session caching

| Cached value | Scope | Refresh |
|---|---|---|
| Fingerprint | per session (engine in-memory) | session start |
| Schema | per job (engine in-memory) | job start |
| Driver capabilities | per target (engine in-memory, cross-job) | TTL (default 1h) |
| Credentials | per (job, secret-name) | secret-broker rotation event |

Cache hit ratio at full materialisation: >95% for
fingerprints, schemas, capabilities. The cache eliminates
repeated calls for the dominant latency saving.

### §3.3 — Async-where-correct

Some calls are **fire-and-forget**:

| Service call | Async? | Failure semantics |
|---|---|---|
| `cost_tracker.Emit` | ✓ | Lost emission = missing cost row; logged loss |
| `audit_log.Emit` | ✓ | Lost emission = missing audit row; logged loss |
| `proxy_broker.ReportFailure` | ✓ | Lost emission = stale cooldown; bounded staleness OK |
| `proxy_broker.Acquire` / `Release` | ✗ | Synchronous — proxy state must be consistent |
| `rate_limit_broker.Reserve` | ✗ | Synchronous — accurate counters required |
| `schema_registry.Validate` | ✗ | Synchronous — failure invalidates the row |
| `dedup_service.IsNew` | ✗ | Synchronous — emit-or-skip decision depends |
| `input_broker.NextURL` | ✗ | Synchronous — URL is the next step's input |

The async/sync per-call posture is **normative** — Wave 5+
build PRs follow this table per ADR-0037 §4.3.

### §3.4 — Tunable per deployment

Lightweight deployments disable optional services entirely
via Helm `values.yaml` flags. The engine falls back to a
no-op stub returning empty / pass-through values.

| Service | Default `enabled` | Disabled-mode behaviour |
|---|---|---|
| enricher | true | Skip enrichment; rows pass through |
| audit-log | true | Drop emissions silently |
| cost-tracker | true | Drop emissions silently |
| dedup-service | true | Emit all rows (no dedup) |
| schema-registry | true | Emit without typed validation |
| input-broker | true | Read from inline `urls:` field |
| session-store | false | Sessions live only for job duration |
| secret-broker | false | Read credentials from env vars |
| fingerprint-broker | false | Driver picks default |
| scheduler | false | Schedules submit via operator's own CRDs |
| driver-router | true | Fall back to static routing table |
| **proxy-broker** | true | **FAILS** the job (required at any production scale) |
| **captcha-solver** | true | **FAILS** the job at first CAPTCHA |
| **rate-limit-broker** | true | **FAILS** the job (required at multi-tenant scale) |

### §3.5 — Service co-location

The chart pins services to **the same node where bandwidth
and latency benefit** via `nodeAffinity` rules in
`_helpers.tpl`:

- Engine + high-frequency services (proxy-broker,
  rate-limit-broker, schema-registry cache) co-scheduled
- Lower-frequency services (scheduler, cost-tracker,
  audit-log) scheduled freely

Co-location keeps per-call gRPC latency in the
**sub-millisecond range** for hot-path services; without it,
cross-AZ calls add 5–10ms each, compounding quickly at
per-step fan-out.

## §4 — Degradation modes

The orchestrator pattern's other cost is failure-mode
expansion. ADR-0037 §5 commits per-service degradation
behaviour:

### §4.1 — Required services (engine fails the job)

| Service | Failure code |
|---|---|
| proxy-broker | `PROXY_BROKER_UNAVAILABLE` |
| captcha-solver (when CAPTCHA encountered) | `CAPTCHA_UNREACHABLE` |
| rate-limit-broker | `RATE_LIMIT_UNAVAILABLE` |
| schema-registry (when schema required) | `SCHEMA_REGISTRY_UNAVAILABLE` |
| input-broker (when batch source) | `INPUT_BROKER_UNAVAILABLE` |
| driver-router | Fall back to static routing table |

Failure surfaces a structured `DriverError.Code` per
[ADR-0009](../adr/0009-navigate-and-session-lifecycle.md);
the operator surfaces the code in the ScrapeJob's status;
the cost-tracker / audit-log (if up) record the failure.

### §4.2 — Optional services (engine degrades gracefully)

| Service | Degradation |
|---|---|
| enricher | Skip enrichment; emit unenriched rows; log `ENRICHER_UNAVAILABLE` |
| dedup-service | Emit all rows (no dedup); log `DEDUP_UNAVAILABLE` |
| audit-log | Drop emissions; log once per job |
| cost-tracker | Drop emissions; log once per job |
| session-store | Treat session as fresh; log |
| fingerprint-broker | Driver picks default; log |
| secret-broker | Fall back to env vars / files |

Graceful degradation **preserves the job** at the cost of
reduced functionality.

### §4.3 — Circuit breakers

Each engine-side service client wraps gRPC calls in a
circuit breaker:

- After K consecutive failures (default 5), the breaker
  opens and skips calls for a cooldown window (default 30s)
- During cooldown, the engine treats the service as
  unavailable per §4.1 / §4.2
- After cooldown, the breaker enters half-open: a single
  test call determines whether the breaker closes (success)
  or re-opens (failure)

Parameters configurable per service via Helm
`engine.circuitBreaker.<service>.{threshold,cooldown}`.

## §5 — Migration from v1alpha1 engine

The refactor is **incremental**, not big-bang. The v1alpha1
engine code is replaced piecewise as each Wave's services
land; at no point is the engine non-functional.

| Wave | Engine refactor scope | Engine shape after |
|---|---|---|
| Pre-Wave 5 (today) | (no changes) | v1alpha1 monolith |
| 5 | Orchestrator scaffolding lands; engine consumes proxy + captcha | Proxy / CAPTCHA delegated; orchestration pattern established |
| 6 | Schema + URL queue delegated | + schema validation + URL queue out of engine |
| 7 | Rate-limit + fingerprint delegated; DSL primitives engine-internal | + rate / fingerprint out of engine; DSL gains pagination / conditional / multi-step |
| 8 | Session + credentials delegated | + session / credentials out of engine |
| 9 | Cost + audit emission paths land | + cost / audit emission active |
| 10 | Dedup + enrichment delegated; driver-router decided | Engine = pure conductor; driver-router decision materialises |

Per
[ADR-0037 §6.3](../adr/0037-engine-as-orchestrator.md): the
engine's old code paths are **deleted in the same PR** as
their replacement. No "temporary in-engine fallback"
survives once the service path lands. The exception is
§3.4's disabled-service no-op stubs — those are explicitly
designed for the disabled case, not preserved old code.

## §6 — Reference materials

### ADRs

- [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive; the freeze that allows the
  engine refactor to be platform-side
- [ADR-0009](../adr/0009-navigate-and-session-lifecycle.md)
  — driver error mapping; per-service unavailability codes
  extend this enum
- [ADR-0012](../adr/0012-engine-dsl-and-execution-pipeline.md)
  — v1alpha1 engine baseline
- [ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane; the operator's parallel evolution
  (schedule-driven CRD creation; ScrapeBatch reconciliation)
- [ADR-0022](../adr/0022-tcp-grpc-transport.md) — gRPC
  transport; circuit-breaker parameters align with §4 posture
- [ADR-0036](../adr/0036-microservices-catalog-expansion.md)
  — 15-service catalog (consumption side of ADR-0037)
- [ADR-0037](../adr/0037-engine-as-orchestrator.md) — engine
  as orchestrator (the source)

### Companion docs

- [`platform-architecture.md`](platform-architecture.md) §4
  — execution flow overview
- [`service-catalog.md`](service-catalog.md) — per-service
  status
- [`storage-tiers.md`](storage-tiers.md) — backend matrix
- [`observability.md`](observability.md) — failure-code
  surfacing + per-service metrics

### Process

- [CONTRIBUTING.md](../../CONTRIBUTING.md) — v1alpha2 process
  rigor matrix; Wave 5+ engine refactor PRs are
  transformational scope
