---
status: accepted
date: 2026-05-05
deciders: [Fabio Caffarello]
---

# Engine as orchestrator of platform services

## §1 — Context and Problem Statement

[ADR-0036](0036-microservices-catalog-expansion.md) (this PR's
Cluster A) catalogues 15 platform services across the seven
layers of the platform-vs-driver responsibility split. The
catalog is **only useful to the platform if the engine
consumes it**. ADR-0036 enumerates *what* the services are;
this ADR commits to *how the engine evolves* to orchestrate
them.

### §1.1 — Today's engine: thick monolithic orchestrator

The v1alpha1 engine ([ADR-0012](0012-engine-dsl-and-execution-pipeline.md))
absorbs the full platform responsibility set in-process. Its
job-execution loop owns:

1. **DSL parsing** — read the ScrapeJob spec, validate, build
   an execution plan
2. **Driver gRPC client** — Initialize → Navigate → Query →
   Extract → Close against one of three reference adapters
3. **Output sink dispatch** — JSONL rows fan out to stdout /
   Kafka / S3 / Webhook per ADR-0024
4. **Per-job state** — job ID, retry counters, deadlines, row
   counts; in-memory for the job's duration
5. **JSONL row emission** — schema-shaped rows synthesised
   from extraction results

Plus everything else the platform might need is **either absent
or in-engine**: there is no proxy management (adapters handle
proxies ad-hoc or not at all); no CAPTCHA solving (the engine
fails when CAPTCHAs are encountered); no rate limit coordination
(adapters can independently breach per-domain budgets); no
session persistence (each job's session lives only in the
adapter's process); no cost tracking (no visibility); no audit
trail beyond stdout logs.

This shape was the right call for the v1alpha1 surface — the
microservices refactor (R1 → R8.1) closed at a working
production-installable single-flow pipeline with three reference
adapters. But it cannot be the v1alpha2 shape. The 15 services
ADR-0036 catalogues each absorb a responsibility the engine
either holds today or fails to hold today; the engine's role
**must shrink** as the services land.

### §1.2 — Tomorrow's engine: orchestrator of platform services

The v1alpha2 engine becomes a **conductor**. Its
job-execution loop calls a sequence of platform services per
job step, composes their results, and emits the typed
extraction output. Each individual responsibility moves to its
service; the engine **orchestrates the sequence**.

Concretely, the v1alpha2 engine's job-execution loop calls:
**input-broker** for the next URL → **proxy-broker** for a
proxy lease → **fingerprint-broker** for a fingerprint →
**rate-limit-broker** for a politeness reservation →
**session-store** to load any prior session → **driver
(gRPC)** to Navigate / Query / Extract → **schema-registry**
to validate the extraction → **enricher** for post-extraction
enrichment → **dedup-service** for pre-emit dedup →
**sinks** to emit per ADR-0024 → **cost-tracker** /
**audit-log** to record the job's cost and decision trail.

Every consumed service is a separate gRPC call with its own
deadline, its own retry policy, its own failure mode. The
engine becomes thinner along every dimension except the
orchestration one.

### §1.3 — Why this is the deepest architectural change in v1alpha2

Adding any individual service to ADR-0036's catalog is
"easy" in the relative sense — a new binary, a new chart
fragment, a new Compose block. But the engine's evolution
**changes the platform's centre of gravity**: every
job-execution code path now crosses N service boundaries; every
job's failure surface now spans N services; every per-step
latency sums across N gRPC calls. The engine is no longer
the place where the work happens; it is the place where the
**sequence** happens.

This ADR makes the commitment explicit — including the
**latency cost** the orchestrator pattern accepts (§4 below),
the **degradation modes** when individual services are
unavailable (§5), and the **migration sequence** that lands the
refactor incrementally (§6). Every Wave 5+ build PR that
introduces a new service consumes engine-side wiring per this
ADR's contract; without that wiring, the new services have no
consumers and the platform stalls.

### §1.4 — Relationship to ADR-0036

ADR-0036 catalogues **what** the services are; ADR-0037
commits the engine to **how** they are consumed. The two ADRs
are co-authoring partners — neither is complete without the
other:

- ADR-0036 names slots, gates them, defines the canonical
  service shape, and commits the SDK matrix.
- ADR-0037 commits the engine's role-shape evolution, the
  per-step orchestration sequence, the latency-cost mitigation
  strategies, and the degradation modes.

Both ADRs must merge in R9.1 before any v1alpha2 implementation
PR opens. Wave 5's first build PRs (proxy-broker + captcha-solver
per ADR-0036's wave assignment) pair with the **first phase of
the engine refactor** that consumes them; ADR-0037 §6's
migration sequence is the contract.

## §2 — The v1alpha2 engine's responsibilities

The shrunk engine retains responsibility for the work that is
**tightly coupled to job execution** and not separable into a
service per [ADR-0036 §2.7](0036-microservices-catalog-expansion.md)'s
"none of A–F holds" rule.

### §2.1 — Stays in the engine

| Responsibility | Why it stays |
|---|---|
| **DSL parsing** | Happens once at job start; tightly coupled to plan generation; no independent state. ADR-0036 §2.7 counter-example — stays as engine module. |
| **Plan generation** | The execution plan is the engine's internal representation; no consumer outside the engine reads it. The engine **calls** `driver-router` (slot 14) for routing decisions; the plan itself is engine-private. |
| **Per-job in-memory state** | Job ID, retry counters, deadlines, row counts. Lives only for the job's duration; ADR-0036 §2.7 counter-example. |
| **Driver gRPC client** | The engine talks to adapters through the Driver Protocol unchanged ([ADR-0001](0001-driver-protocol-as-architectural-primitive.md) is frozen). The transport, the deadlines, the retry posture — unchanged from v1alpha1. |
| **Output sink dispatch** | Sinks are external systems consumed via ADR-0024's contract — not services in ADR-0036's catalog. Sink dispatch logic lives in the engine. |
| **Service orchestration** | The new central role. Every per-step service call originates here. |

### §2.2 — Moves to a service

| Responsibility | Service (ADR-0036 slot) | Wave |
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
| Driver routing (decision pending) | `driver-router` (14) | 10 |

The migration is **incremental** (§6). The Wave column governs
when each service materialises and the engine consumes it; the
engine refactor is not a single big-bang PR but a sequence of
per-service consumption additions across Waves 5 – 10.

### §2.3 — What the operator does instead

The engine is not the only consumer reshaped by ADR-0036's
catalog. The operator's responsibilities shift slightly:

- The operator continues to reconcile ScrapeJob CRDs to
  terminal state per [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md);
  reconciliation logic is unchanged.
- The operator gains **schedule-driven CRD creation** —
  consuming `scheduler` (slot 6) to instantiate ScrapeJobs at
  cron-trigger time. Per ADR-0036 §3.2 the scheduler service
  *creates* CRDs; reconciliation *drives* them. Two distinct
  responsibilities; the operator is still the reconciler.
- The operator gains **batch-driven CRD orchestration** —
  consuming `input-broker` (slot 12) to spawn child ScrapeJobs
  from ScrapeBatch CRDs. ADR-0033 (R9.4) defines the
  ScrapeBatch CRD; ADR-0019's reconciler extends to handle it.

This ADR commits the *engine's* shape evolution; the operator's
parallel shifts are recorded for completeness and detailed in
ADR-0019's future evolution.

## §3 — The v1alpha2 execution flow

A diagram is worth a hundred prose paragraphs. The execution
flow below is the v1alpha2 engine's per-job loop at full
materialisation (Wave 10 closed; all 14 v1alpha2 services
landed).

### §3.1 — End-to-end flow

```
                                  ┌──────────────┐
                                  │   user       │
                                  └──────┬───────┘
                                         │ submits ScrapeJob / ScrapeBatch
                                         │ (or registers schedule via scheduler)
                                         ▼
                                  ┌──────────────┐         ┌──────────────┐
                                  │  scheduler   │ ──cron──▶  operator    │
                                  │  (slot 6)    │ trigger │  (control-   │
                                  └──────────────┘         │   plane)     │
                                                           └──────┬───────┘
                                                                  │ reconciles
                                                                  │ ScrapeJob /
                                                                  │ ScrapeBatch
                                                                  ▼
                                  ┌────────────────────────────────────────┐
                                  │              engine                    │
                                  │  parses DSL → orchestrates per step    │
                                  └────────────────────────────────────────┘
                                                                  │
        ┌───────────────────────────────┬──────────────┬──────────┼──────────┬──────────────┬───────────────┐
        ▼                               ▼              ▼          ▼          ▼              ▼               ▼
┌──────────────┐              ┌──────────────┐ ┌─────────────┐ ┌──────────┐ ┌─────────────┐ ┌──────────┐ ┌──────────────┐
│ input-broker │              │ proxy-broker │ │ fingerprint-│ │rate-limit│ │ session-    │ │ secret-  │ │schema-       │
│  (slot 12)   │              │  (slot 1)    │ │ broker (3)  │ │ broker(4)│ │ store (5)   │ │ broker(13)│ │registry (9) │
│  next URL    │              │  acquire     │ │  select     │ │ reserve  │ │  load       │ │  fetch    │ │  fetch       │
│              │              │  proxy       │ │  fingerprint│ │  budget  │ │  session    │ │  creds    │ │  schema      │
└──────────────┘              └──────────────┘ └─────────────┘ └──────────┘ └─────────────┘ └──────────┘ └──────────────┘
                                                                  │
                                                                  │ orchestrated payload
                                                                  ▼
                                                          ┌──────────────┐
                                                          │driver-router │ ──▶  pick driver (or in-engine module per ADR-0035)
                                                          │  (slot 14)   │
                                                          └──────┬───────┘
                                                                 │ Navigate / Query / Extract
                                                                 ▼
                                                          ┌──────────────┐
                                                          │ driver gRPC  │
                                                          │ playwright / │
                                                          │ seleniumbase │
                                                          │ curl-imp.    │
                                                          └──────┬───────┘
                                                                 │ extracted rows
                                                                 ▼
                                  ┌──────────────────────────────┴──────────────────────────────┐
                                  │                                                              │
                                  ▼                                                              ▼
                          ┌──────────────┐                                              ┌─────────────┐
                          │  schema-     │ ──▶ valid? ──▶                              │ engine      │
                          │  registry    │                                              │ row         │
                          │  (slot 9)    │                                              │ assembly    │
                          │  validate    │                                              └──────┬──────┘
                          └──────────────┘                                                     │
                                                                                               ▼
                                                                                       ┌──────────────┐
                                                                                       │  enricher    │
                                                                                       │  (slot 10)   │
                                                                                       │  geocode /   │
                                                                                       │  classify    │
                                                                                       └──────┬───────┘
                                                                                              │
                                                                                              ▼
                                                                                       ┌──────────────┐
                                                                                       │ dedup-       │
                                                                                       │ service (11) │
                                                                                       │ membership   │
                                                                                       └──────┬───────┘
                                                                                              │
                                              ┌───────────────────────────────────────────────┼───────────────────────┐
                                              ▼                                               ▼                       ▼
                                      ┌──────────────┐                              ┌──────────────┐         ┌──────────────┐
                                      │   sinks      │                              │ cost-tracker │         │ audit-log    │
                                      │  (Kafka /    │                              │  (slot 7)    │         │  (slot 8)    │
                                      │   S3 / Web / │                              │  emit cost   │         │  emit audit  │
                                      │   stdout)    │                              │              │         │              │
                                      └──────────────┘                              └──────────────┘         └──────────────┘
```

### §3.2 — Per-step orchestration sequence

Translating the diagram into the engine's sequential loop
(simplified pseudocode; real implementation absorbs concurrency
per §4):

```
for each step in plan:
    url = input_broker.NextURL(batch_id)                # Wave 6
    proxy = proxy_broker.Acquire(target=url.domain)     # Wave 5
    fp = fingerprint_broker.Select(profile)             # Wave 7 (cached per session)
    rate_limit_broker.Reserve(scope=url.domain)         # Wave 7
    session = session_store.Load(tenant, target)        # Wave 8
    creds = secret_broker.Fetch(needed_secrets)         # Wave 8

    schema = schema_registry.Get(schema_ref)            # Wave 6 (cached per job)
    driver = driver_router.Pick(target, capabilities)   # Wave 10

    rows = driver.NavigateQueryExtract(url, proxy, fp,
                                       session, creds)  # unchanged shape

    for row in rows:
        validated = schema_registry.Validate(row, schema)
        enriched = enricher.Enrich(validated)           # Wave 10
        if dedup_service.IsNew(dedup_key(enriched)):    # Wave 10
            sinks.Emit(enriched)                        # unchanged

    rate_limit_broker.Release(scope, used=len(rows))
    session_store.Save(tenant, target, session.delta())
    proxy_broker.Release(proxy)

    cost_tracker.Emit(job_id, all_costs)                # Wave 9 (async fire-and-forget)
    audit_log.Emit(job_id, decisions)                   # Wave 9 (async fire-and-forget)
```

The pseudocode is deliberately dense to make the **fan-out**
visible: a single per-step iteration touches **9 + N services**
at full materialisation, where N is the per-row enrichment /
dedup / sink count. This fan-out is the source of the latency
cost §4 mitigates.

### §3.3 — What the diagram hides

Three things the diagram does not show, all material:

- **Caching of stable values.** Fingerprint per session,
  schema per job, capabilities per target — chosen once, not
  per-step. §4.2 details.
- **Async-where-correct.** Cost emission, audit emission, and
  some rate-limit-release cases are fire-and-forget — the
  engine does not block on them. §4.3 details.
- **Service co-location.** In production, engine + most
  services run on the same node when possible (chart-pinned)
  to keep gRPC latency in the sub-millisecond range. §4.5
  details.

## §4 — Latency-cost mitigation strategies

The headline cost of the orchestrator pattern is **latency**.
A naive implementation that synchronously calls 9+ services
per step at 5 ms per call adds 45+ ms per step; a 100-step
job becomes 4.5 seconds slower than v1alpha1's monolithic
engine. For high-volume scraping (millions of rows per day),
this is non-trivial.

This ADR commits to **five mitigation strategies**. Each is
normative — Wave 5+ build PRs that consume services land with
these strategies in place; deviation requires an ADR amendment.

### §4.1 — Strategy 1: Batch operations

Where a service call is naturally batchable, the engine pulls
N items per call rather than 1 item per step.

- **`input-broker.NextURLs(batch_id, n=100)`** — pulls 100
  URLs in one call, processes them in-memory, refills when the
  buffer drops below a threshold. Net effect: ~1 input-broker
  call per 100 steps instead of 1 per step.
- **`proxy-broker.AcquireBatch(target_domain, n=k)`** — when
  the target domain admits N concurrent sessions, pre-acquires
  N leases in one call.
- **`schema-registry.GetBatch([schema_refs])`** — at job start,
  pre-fetches every schema the plan references; per-step
  validation hits an in-memory map.

The batch sizes are configurable per deployment (Helm
`engine.batchSizes.<service>`); defaults err toward larger
batches for high-throughput deployments and smaller batches
for low-latency ones.

### §4.2 — Strategy 2: Per-job / per-session caching

Stable values are chosen once and reused across steps.

| Value | Cache scope | Refresh policy |
|---|---|---|
| Fingerprint | per session (engine in-memory) | session start |
| Schema | per job (engine in-memory) | job start |
| Driver capabilities | per target (engine in-memory, shared across jobs) | TTL (configurable, default 1h) |
| Credentials | per (job, secret-name) | secret-broker rotation event |

The cache is **engine-local** — no separate cache service.
Cache hit ratio at full materialisation is expected to exceed
95% for fingerprints, schemas, and capabilities; the cache
elimination of repeated calls is the dominant latency saving.

### §4.3 — Strategy 3: Async-where-correct

Some service calls are **fire-and-forget** — the engine emits
and proceeds without awaiting acknowledgement. This is correct
where the emission's failure does not invalidate the job.

| Service call | Async? | Failure semantics |
|---|---|---|
| `cost_tracker.Emit` | yes | Lost emission = missing cost row in ledger; engine logs the loss; reconciliation downstream is consumer-side. |
| `audit_log.Emit` | yes | Lost emission = missing audit row; same loss-and-log pattern. |
| `proxy_broker.ReportFailure` | yes | Lost emission = stale cooldown; bounded staleness; eventual consistency acceptable. |
| `proxy_broker.Acquire` / `Release` | **no** | Synchronous — proxy state must be consistent across consumers. |
| `rate_limit_broker.Reserve` | **no** | Synchronous — rate limiting requires accurate counters. |
| `schema_registry.Validate` | **no** | Synchronous — validation failure invalidates the row. |
| `dedup_service.IsNew` | **no** | Synchronous — emit-or-skip decision depends on it. |
| `input_broker.NextURL` | **no** | Synchronous — the URL is the next step's input. |

The async / sync per-call posture is **normative** — Wave 5+
build PRs follow this table. Adding a new async-eligible call
requires updating this table in a future ADR amendment.

### §4.4 — Strategy 4: Tunable per deployment

Lightweight deployments can disable optional services entirely.
The chart's `values.yaml` exposes per-service `enabled` flags;
when a service is disabled, the engine falls back to a
**no-op stub** that returns an empty / pass-through value.

| Service | Default `enabled` | Disabled-mode behaviour |
|---|---|---|
| `enricher` | `true` | Engine skips enrichment step; rows pass through unmodified. |
| `audit-log` | `true` | Engine drops audit emissions silently. |
| `cost-tracker` | `true` | Engine drops cost emissions silently. |
| `dedup-service` | `true` | Engine emits all rows (no dedup). |
| `proxy-broker` | `true` | Engine fails the job (proxies are required at any production scale). |
| `captcha-solver` | `true` | Engine fails the job at the first CAPTCHA encountered. |
| `schema-registry` | `true` | Engine emits without typed validation (rows are JSONL with no schema check). |
| `input-broker` | `true` | Engine reads URLs from the ScrapeJob's inline `urls:` field instead. |
| `session-store` | `false` | (off by default) Sessions live only for the job's duration. |
| `secret-broker` | `false` | (off by default) Engine reads credentials from env vars or mounted files. |
| `fingerprint-broker` | `false` | (off by default) Driver picks its own default fingerprint. |
| `rate-limit-broker` | `true` | Engine fails the job (rate limit coordination is required at any multi-tenant scale). |
| `scheduler` | `false` | (off by default) Schedules are submitted via the operator's own CRDs. |
| `driver-router` | `true` | Engine consults a fallback static routing table. |

The required-vs-optional default is per-service. ADR-0036's
catalog summary (§3.9) records the v1alpha2-default service
set; this table records the engine's behaviour when each
service is off.

### §4.5 — Strategy 5: Service co-location

The chart pins services to **the same node where bandwidth
and latency benefit**. Specifically:

- The engine + the high-frequency services (`proxy-broker`,
  `rate-limit-broker`, `schema-registry` cache) are
  co-scheduled via `nodeAffinity` rules in the chart's
  `_helpers.tpl`.
- The lower-frequency services (`scheduler`, `cost-tracker`,
  `audit-log`) can be scheduled freely.

Co-location keeps the per-call gRPC latency in the
**sub-millisecond range** for the hot-path services; without
it, cross-AZ calls can add 5–10ms each, which compounds
quickly at the per-step fan-out.

This is a chart-level concern that ADR-0036 §5.2 partially
references (chart fragments contribute to `_helpers.tpl`).
The first Wave 5 build PR (proxy-broker) materialises the
co-location pattern; subsequent services follow.

### §4.6 — The latency budget is real and accepted

Even with all five mitigations, the orchestrator pattern adds
**measurable latency vs v1alpha1's in-process engine**. The
expected per-step overhead, post-mitigation:

| Strategy applied | Per-step overhead |
|---|---|
| All hot-path services co-located + cached | ~2 – 5 ms |
| Cross-AZ services (cost, audit) async | 0 ms (fire-and-forget) |
| Disabled services | 0 ms (no-op stubs) |
| Total typical | ~5 ms / step |

For a 100-step job: ~500 ms added. For a 10,000-step job:
~50 s added. For a million-step batch (input-broker fan-out):
1 hour added at peak orchestration cost.

The decoupling benefits — independent scaling, independent
failure surfaces, polyglot SDK consumption, independent
evolvability — are accepted as worth this cost for **most
workloads**. Workloads where they are not (extreme low-latency
real-time scraping; embedded / on-device deployments) are
out of v1alpha2 scope and revisit in v1beta1+.

## §5 — Degradation modes when services are unavailable

The orchestrator pattern's other cost is **failure-mode
expansion** — the engine now has N more dependencies, each of
which can fail independently. This ADR commits to per-service
degradation behaviour: what does the engine do when a service
is down?

### §5.1 — Required services: engine fails the job

For services where the engine cannot proceed without a valid
response, the engine **fails the job with a clear error**:

| Service | Failure mode | Engine behaviour |
|---|---|---|
| `proxy-broker` | unavailable | Fail job: `PROXY_BROKER_UNAVAILABLE` |
| `captcha-solver` | unavailable + CAPTCHA encountered | Fail job step: `CAPTCHA_UNREACHABLE` |
| `rate-limit-broker` | unavailable | Fail job: `RATE_LIMIT_UNAVAILABLE` |
| `schema-registry` | unavailable + schema required | Fail job: `SCHEMA_REGISTRY_UNAVAILABLE` |
| `input-broker` | unavailable + batch source | Fail job: `INPUT_BROKER_UNAVAILABLE` |
| `driver-router` | unavailable | Fall back to static routing table per §4.4 |

Failure includes a structured `DriverError.Code` per
[ADR-0009](0009-navigate-and-session-lifecycle.md)'s error
mapping; the operator surfaces the code in the ScrapeJob's
status; the cost-tracker / audit-log (if up) record the
failure.

### §5.2 — Optional services: engine degrades gracefully

For services where the engine can proceed in a reduced state,
the engine **continues with degraded behaviour and logs the
degradation**:

| Service | Failure mode | Engine behaviour |
|---|---|---|
| `enricher` | unavailable | Skip enrichment step; emit unenriched rows; log `ENRICHER_UNAVAILABLE`. |
| `dedup-service` | unavailable | Emit all rows (no dedup); log `DEDUP_UNAVAILABLE`. |
| `audit-log` | unavailable | Drop audit emissions; log `AUDIT_LOG_UNAVAILABLE` once per job. |
| `cost-tracker` | unavailable | Drop cost emissions; log `COST_TRACKER_UNAVAILABLE` once per job. |
| `session-store` | unavailable | Treat session as fresh; log `SESSION_STORE_UNAVAILABLE`. |
| `fingerprint-broker` | unavailable | Driver picks default; log `FINGERPRINT_BROKER_UNAVAILABLE`. |
| `secret-broker` | unavailable + secret needed | Fall back to env vars / files; log if neither resolves. |

Graceful degradation **preserves the job** at the cost of
reduced functionality. The `DriverError.Code` taxonomy from
ADR-0009 expands to include the per-service unavailability
codes; ADR-0009 evolution is forward-tracked (not in R9.1
scope).

### §5.3 — Circuit-breaker patterns

Each engine-side service client wraps gRPC calls in a
**circuit breaker**:

- After K consecutive failures (default K=5), the breaker
  opens and skips calls for a cooldown window (default 30s).
- During cooldown, the engine treats the service as
  unavailable per §5.1 / §5.2.
- After cooldown, the breaker enters half-open: a single test
  call determines whether the breaker closes (success) or
  re-opens (failure).

Circuit-breaker parameters are configurable per service via
Helm `engine.circuitBreaker.<service>.{threshold,cooldown}`;
defaults are the same shape as the engine's existing retry
policy posture per [ADR-0022 §4](0022-tcp-grpc-transport.md).

The circuit-breaker layer is **engine-side**, not per-service.
A service that returns errors does not know it has tripped a
breaker; the engine maintains the breaker state.

## §6 — Migration sequence

The engine's evolution is **incremental, not big-bang**. The
v1alpha1 engine code is replaced piecewise as each Wave's
services land; at no point is the engine non-functional.

### §6.1 — Per-Wave engine refactor scope

| Wave | Services landing | Engine refactor scope |
|---|---|---|
| 5 | proxy-broker, captcha-solver | First orchestrator pattern lands. Engine consumes proxy + captcha. Caching scaffold introduced (§4.2). Circuit-breaker scaffold introduced (§5.3). |
| 6 | schema-registry, input-broker | Schema fetch + validation moves out of engine. URL queue moves out of engine. ScrapeBatch CRD support lands. |
| 7 | rate-limit-broker, fingerprint-broker | Rate-limit reservation lands. Fingerprint selection moves out of engine. DSL workflow primitives (pagination, conditionals, multi-step nav) are **engine-internal** evolution per ADR-0036 §3.2 — not service consumers. |
| 8 | session-store, secret-broker, scheduler | Session persistence + credential acquisition land. Operator gains schedule-driven CRD creation. |
| 9 | cost-tracker, audit-log | Cost + audit emission paths land (async per §4.3). |
| 10 | dedup-service, enricher, driver-router | Dedup + enrichment lands. Driver routing decision (service vs engine module) materialises per ADR-0035 (R9.4); engine integrates accordingly. |

Each Wave's engine refactor is one or more **transformational
PRs** under the v1alpha2 process rigor matrix
([CONTRIBUTING.md](../../CONTRIBUTING.md), R9.0). Each requires
a master phase prompt, multi-cluster commits, and exhaustive
acceptance criteria. Wave 5 is the heaviest because the
orchestrator scaffolding lands; subsequent waves are
service-by-service additions to the established pattern.

### §6.2 — What the v1alpha1 engine looks like at each stage

The engine's code path coverage shrinks per Wave:

- **Pre-Wave 5** (today): engine contains everything.
- **After Wave 5**: engine no longer manages proxy / CAPTCHA;
  consumes proxy-broker + captcha-solver.
- **After Wave 6**: engine no longer manages schemas or URL
  queues; consumes schema-registry + input-broker.
- **After Wave 7**: engine no longer manages rate limits or
  fingerprints; engine DSL gains pagination / conditional /
  multi-step primitives.
- **After Wave 8**: engine no longer manages sessions or
  credentials.
- **After Wave 9**: engine emits cost + audit events (rather
  than discarding them).
- **After Wave 10**: engine becomes a pure conductor; per-row
  enrichment + dedup move out; driver routing decision lands.

By Wave 10's close, the engine is **structurally minimal** —
DSL parsing, plan generation, per-job state, sink dispatch,
service orchestration. Every other v1alpha1 responsibility
has moved to a service.

### §6.3 — Backward compatibility during migration

ADR-0036's "no legacy paths survive" principle (extending
[CONTRIBUTING.md](../../CONTRIBUTING.md)'s "Architectural
commitments" #2) extends here: when a Wave moves a
responsibility from the engine to a service, the **engine's
old path is deleted in the same PR**. No "temporary in-engine
fallback" survives once the service path lands.

The exception is the §4.4 disabled-service degradation: the
engine retains a **no-op stub or fallback** for each optional
service so light deployments work. The stub is **not** the
old in-engine implementation — it is a degraded behaviour
explicitly designed for the disabled case (see §4.4 table).
Old in-engine code paths are deleted unconditionally.

## §7 — Confirmation (acceptance criteria)

The orchestrator pattern is working when the following hold
**by the close of Wave 10**:

- **The engine binary's source size has shrunk** vs
  v1alpha1's engine — most code paths are in services, not
  in `engines/engine/`.
- **Every service in ADR-0036's catalog has at least one
  engine-side consumer wired** (§3.2's pseudocode is the
  contract; per-service consumption lives in the engine's
  per-Wave refactor).
- **The latency budget (§4.6) is met** in production smoke —
  per-step overhead is in the documented range (~5 ms per
  step typical) for the smoke-cluster topology. Any deviation
  is investigated; if persistent, an ADR amendment records
  the new budget.
- **Degradation modes (§5) are exercised in CI** — the
  production-smoke gate (R7.2) extends with per-service
  unavailability scenarios, asserting graceful degradation
  for optional services and clean failure for required ones.
- **The async-where-correct posture (§4.3) is honoured** —
  no PR introduces a synchronous emission path for a
  fire-and-forget service without amending §4.3's table.
- **Circuit breakers (§5.3) trip and recover** in CI's
  integration tests for at least one service.

A signal that the pattern needs revision: more than one Wave
build PR encounters per-step latency that exceeds the §4.6
budget by more than 50% **after** all five mitigations are
applied. That's evidence the orchestrator pattern is the
wrong shape for the workload; the response is an ADR
amendment that revises the latency budget or moves a
hot-path responsibility back into the engine.

## §8 — What's deferred / out of scope

R9.1 declines these deliberately. Each is a real concern; each
belongs to a later phase or to a sibling ADR.

- **The actual engine refactor implementation.** This ADR is
  contract-only. Wave 5+ build PRs materialise the pattern.
- **Per-service deadline / retry tuning.** Each service's
  build PR sets its deadline / retry policy per ADR-0036 §5.4
  observability + ADR-0027's SDK wrapper conventions; this
  ADR records the orchestration shape, not per-RPC tuning.
- **Driver routing decision (service vs engine module).**
  ADR-0035 (R9.4) settles this; ADR-0037 §2.2 + §6.1 records
  both shapes pending the decision.
- **Engine multi-tenancy enforcement.** Multi-tenant
  isolation across engine + services is a v1beta1 concern;
  the catalog reserves the operational layer (ADR-0036 §3.6)
  for this work but does not commit to it in v1alpha2.
- **Engine HA / replication.** Today's engine is single-replica;
  multi-replica engine + cross-replica state coordination is
  v1beta1 territory.
- **Engine multi-region deployment.** Service co-location
  (§4.5) assumes single-region for v1alpha2; multi-region
  topologies (with cross-region service co-location, regional
  service replicas) defer to v1beta1.
- **DSL evolution v1alpha2 → v1beta1.** ADR-0035 (R9.4)
  governs the DSL trajectory; this ADR commits the engine's
  orchestration shape, not the DSL it parses.
- **Stream-engine variant.** ADR-0026 §3.2 reserved
  `engines/` plural to accommodate future specialised engines
  (continuous-collection vs the current batch-engine). A
  hypothetical stream-engine consuming the same catalog of
  services is reasonable but not in v1alpha2 scope.

## §9 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive. The engine's driver-side
  contract is unchanged from v1alpha1.
- [ADR-0009](0009-navigate-and-session-lifecycle.md) —
  Navigate, session lifecycle, and driver error mapping. The
  §5 degradation modes extend ADR-0009's `DriverError.Code`
  taxonomy.
- [ADR-0012](0012-engine-dsl-and-execution-pipeline.md) —
  Engine DSL and execution pipeline. The v1alpha1 baseline this
  ADR's evolution starts from.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — Control plane. The operator's parallel evolution (§2.3) is
  recorded here for completeness; ADR-0019's own evolution is
  forward-tracked.
- [ADR-0020](0020-microservices-architecture-supersession.md)
  — Microservices architecture. The four refactor drivers
  extend forward.
- [ADR-0022](0022-tcp-grpc-transport.md) — gRPC transport.
  Every per-step service call uses ADR-0022's transport;
  circuit-breaker parameters (§5.3) align with ADR-0022 §4.
- [ADR-0023](0023-stateful-services-architecture.md) —
  Stateful services. Each consumed service's persistent state
  lives in the tier ADR-0023 (+ §11 in R9.2) commits.
- [ADR-0024](0024-output-sinks.md) — Output sinks. Sinks remain
  external consumed via ADR-0024's contract — not services in
  this ADR's orchestration sequence.
- [ADR-0036](0036-microservices-catalog-expansion.md) — The
  15-service catalog. This ADR is the consumption side of
  ADR-0036's contract.
- ADR-0033 / ADR-0034 / ADR-0035 / ADR-0038 / ADR-0039 (R9.2 +
  R9.4) materialise the per-subsystem details the orchestrator
  pattern depends on.
- [`docs/roadmap.md`](../roadmap.md) §4 (rewritten in R9.7) —
  the Wave 1 – 12 detailed plan. §6.1's per-Wave engine refactor
  scope is the implementation side of the roadmap.
- [CONTRIBUTING.md](../../CONTRIBUTING.md) "v1alpha2 process
  rigor matrix" (R9.0) — Wave 5+ engine refactor PRs are
  reviewed under the transformational-change rigor.
