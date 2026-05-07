---
status: accepted
date: 2026-05-05
deciders: [Fabio Caffarello]
---

# Microservices catalog expansion and canonical service shape

## §1 — Context and Problem Statement

Phase R8.1 closed the microservices refactor (R1 → R8.1) at
`v0.1.0-alpha.0`. The platform v1alpha1 surface is operationally
complete: a frozen Driver Protocol with three reference adapters,
a Rust engine, a Go control-plane operator, four output sinks,
three stateful dependencies (PostgreSQL, Redis, Kafka), a
production-installable Helm chart, and a green production-smoke
gate in CI. The capability invariant 13 / 12 / 6 (Playwright /
SeleniumBase / curl-impersonate) is preserved byte-for-byte
through every refactor PR.

Phase R9 opens the v1alpha2 architectural foundation. R9 is a
documentation-only phase that crystallises the post-refactor
architectural commitments into canonical artefacts before any
implementation PR opens. This ADR is one of two foundational
ADRs in R9.1 (the other is ADR-0037 — engine as orchestrator);
every subsequent v1alpha2 ADR (0033 / 0034 / 0035 / 0038 / 0039)
references the catalog and the canonical service shape this ADR
codifies.

### §1.1 — What ADR-0028 reserved, and why it is not enough

[ADR-0028](0028-ancillary-infra-services-catalog.md) named **five
slots** under `infra-services/` — `proxy-broker`, `captcha-solver`,
`fingerprint-broker`, `session-store`, `rate-limit-broker` — and
gated each slot's materialisation on a single admission criterion:
*at least two provider integrations are designed* (the
two-provider gate at §5.2 of ADR-0028). The five slots cover
Layer 1 of the platform-vs-driver responsibility split; they do
not cover the rest of the platform surface.

The post-refactor strategic frameworks (v1 → v4, reviewed
iteratively with the maintainer through 2026-05) catalogued the
**full** platform surface across seven layers:

- **Layer 1 — Acquisition** (proxies, CAPTCHAs, fingerprints,
  rate limits, sessions)
- **Layer 2 — Workflow** (scheduling, DSL primitives)
- **Layer 3 — Quality / observability** (metrics, traces, logs,
  audits, costs)
- **Layer 4 — Output management** (schemas, enrichment,
  deduplication, formats)
- **Layer 5 — Input management** (URL queues, batches, sources)
- **Layer 6 — Driver abstraction** (capability matching, routing,
  fallbacks)
- **Layer 7 — Operational** (secrets, templates, multi-tenancy)

Walking the seven layers and applying a broader gate (§2 below)
produces a catalog of **15 services** spanning all seven layers.
ADR-0028's five-slot catalog is a strict subset of that 15.
ADR-0028's two-provider gate is a strict subset of the broader
six-gate rule this ADR adopts (§2 below). Both broadenings are
**additive and structural**, not corrective: ADR-0028's reasoning
is unchanged for the five slots it named; this ADR extends the
named-slot set and the admission rule to absorb the additional
ten services the wider platform surface entails.

### §1.2 — What ADR-0028 §6 rejected, and what changes here

ADR-0028 §6 *also* rejected several candidate services
explicitly, on architectural grounds. Three of those rejections
are revisited by this ADR; the remaining rejections stand.

| ADR-0028 §6 rejection | Status under ADR-0036 | Reframe |
|-----------------------|-----------------------|---------|
| `secrets-broker` ("Secrets are deployment configuration") | **Superseded** — admitted as `secret-broker` (slot 13) | The framework's gate-based analysis identifies secret-broker as a uniform-interface service over heterogeneous backends (HashiCorp Vault, AWS Secrets Manager, Kubernetes Secrets, Postgres-backed), satisfying gate A (provider abstraction) plus gates B + D + F. Secret-broker does **not replace** the deployment system — it provides cross-service uniform access semantics, audit trails, and rotation primitives that are otherwise reinvented per-consumer. |
| `scheduler` / `job-queue` ("the operator IS the scheduler") | **Superseded** — admitted as `scheduler` (slot 6) | The framework distinguishes two distinct responsibilities: **reconciliation** (the operator's job today — watch CRDs, drive them to terminal state) and **cron-trigger persistence + ScrapeJob emission over time** (a separate responsibility not absorbed by reconciliation). The scheduler service owns the second responsibility; the operator's reconciliation loop is unchanged. The two are complementary, not duplicative. |
| `telemetry-collector` / `log-aggregator` ("OTel covers it") | **Stands** — telemetry stays cross-cutting | ADR-0031 (R9.3) commits to OpenTelemetry as the cross-cutting framework; telemetry collection is **not** a service we build. The new `audit-log` service (slot 8) is a **distinct concern** — per-job decision audit (which proxy was acquired, which selector matched, which CAPTCHA was triggered), not generic log aggregation. ADR-0028 §6's rejection of log-aggregation does not apply to audit-log. |
| `account-pool` | **Stands** — deferred | Per-tenant account-management with TOS / legal nuance remains out of scope; revisit if a concrete consumer surfaces. |
| `vendor-shim` | **Stands** — anti-pattern | Vendor integrations remain private to their host service's `internal/providers/<vendor>/` tree per ADR-0028 §3.3. |
| `config-broker` / `feature-flag-service` | **Stands** — deployment-config concern | Feature flags belong to the protocol surface (per ADR-0004) or to the deployment system (env vars, ConfigMaps); a bespoke service remains rejected. |

The supersession is **explicit and recorded**: this ADR cites
ADR-0028 §6 by name, identifies the three superseded rejections,
and provides the gate-based reframe for each. ADR-0028's text is
not modified — per Phase R9's documentation-only posture and the
project's "ADRs 0001–0030 are immutable" commitment
(CONTRIBUTING.md, "Architectural commitments"), in-place
amendments to accepted ADRs in the 0001–0030 range are limited
to the documented R6.5.4 / R6.6 / R6.3 evolution-note precedent
and to the planned ADR-0023 §11 amendment in R9.2. Future readers
of ADR-0028 §6 follow the breadcrumb: ADR-0028's rejections are
authoritative for the v1alpha1 context in which they were
written; ADR-0036 supersedes the three reframed rejections for
the v1alpha2 context.

### §1.3 — Why a normative service shape, beyond the catalog

ADR-0028 §3 (canonical service shape) committed each infra
service to: one protocol per service, one binary, N pluggable
providers behind the protocol, per-language SDK clients per
ADR-0027, one Compose service entry per ADR-0025, observability
surface (gRPC reflection, Health, structured logs). That shape
covered five services. With 15 services in scope, the canonical
shape needs to absorb additional operational patterns — Helm
chart fragment conventions, CI surface auto-extension, mTLS
certificate templates, per-service CHANGELOG, per-service ADR
trees — without each service's build PR re-deciding the same
patterns.

The pattern set this ADR codifies (§5 below) extends ADR-0028 §3
with the operational surface that emerges only at multi-service
scale: chart-fragment templates, CI matrix auto-extension, mTLS
wiring, per-service changelog discipline, per-service ADR trees.
Each pattern is **normative** — future infra-service build PRs
follow the pattern exactly, deviations require an ADR amendment
to this one.

## §2 — Service-vs-library decision gate

ADR-0028 §5.2 set one admission gate: **two-provider design**.
The gate works for provider-abstraction services (proxy,
captcha) but does not cover the other reasons a responsibility
becomes a service. This ADR generalises the gate to **six rules
A–F**. A responsibility becomes a microservice if **any** of
A–F holds.

### §2.1 — Gate A: Provider abstraction

The responsibility has **two or more external provider
implementations** behind a uniform API. A consumer calling the
abstraction does not care which provider implements it.

- **Examples**: `proxy-broker` (BrightData / Oxylabs / Smartproxy
  / self-hosted); `captcha-solver` (2Captcha / Anti-Captcha /
  CapMonster / DeathByCaptcha); `fingerprint-broker` (noble-tls
  / curl-impersonate / custom JA3-JA4 generators); `enricher`
  (geocoding via Mapbox / Google / OSM; classification via
  internal model / vendor LLM); `secret-broker` (Vault / AWS
  Secrets Manager / Kubernetes Secrets / Postgres).
- **Counter-example**: the engine's job-planning algorithm is
  not a provider abstraction — there is no second "provider" of
  job planning behind a uniform API. Stays as an engine module.

This is ADR-0028's original gate, preserved verbatim.

### §2.2 — Gate B: Persistent state ownership

The responsibility owns **persistent state that outlives any
single job execution**. Multiple consumers read or write that
state and need durability + concurrency guarantees beyond what
in-process memory provides.

- **Examples**: `session-store` (cookies / OAuth tokens / CSRF
  state across runs); `rate-limit-broker` (per-domain counters
  with sliding windows); `schema-registry` (versioned schemas);
  `input-broker` (URL queues with per-URL lifecycle); `audit-log`
  (append-only decision records); `cost-tracker` (per-job cost
  ledger).
- **Counter-example**: the engine's per-job in-memory state
  (current row, retry count, deadline) is not persistent — it
  lives only for the job's duration. Stays in-process.

### §2.3 — Gate C: Independent scaling characteristics

The responsibility's load profile **differs structurally** from
the engine's, and separating allows independent horizontal
scaling without scaling the engine in lockstep.

- **Examples**: `enricher` (CPU-bound geocoding or
  classification — scales with enrichment throughput, not job
  throughput); `dedup-service` (memory-bound bloom filters —
  scales with key-space size); `captcha-solver` (network-I/O
  bound to external APIs — scales with CAPTCHA arrival rate).
- **Counter-example**: the engine's DSL parser is CPU-bound but
  scales 1:1 with job submission — there is no scaling
  divergence. Stays as an engine module.

### §2.4 — Gate D: Cross-cutting consumption

The responsibility is consumed by **three or more different
consumer categories** (engine + adapters + operator + future
SDKs / data-platform), and embedding it in any one would break
the others' decoupling.

- **Examples**: `schema-registry` (engine validates extraction;
  operator validates ScrapeJob spec; downstream consumers
  validate output); `secret-broker` (every service needing
  credentials); `audit-log` (every service emits decisions);
  `rate-limit-broker` (engine checks; scheduler defers;
  input-broker defers queueing).
- **Counter-example**: the operator's CRD-watching loop is
  consumed only by the operator — embedding it in the operator
  is correct.

### §2.5 — Gate E: Independent evolvability

The responsibility evolves at a **cadence different from its
consumers**. Tightly coupling forces unnecessary redeploys when
the responsibility changes.

- **Examples**: `input-broker` (input source ingesters evolve
  independently of engine job execution); `cost-tracker` (cost
  models evolve independently of job logic); `schema-registry`
  (schemas evolve independently of consumers); `scheduler`
  (scheduling policies evolve independently of execution).
- **Counter-example**: the engine's gRPC transport stays in
  lockstep with the Driver Protocol — they are tightly coupled
  by design (ADR-0001).

### §2.6 — Gate F: Operational independence

The responsibility benefits from **separate failure / health /
oncall surfaces**. Failure of this thing should be observable
and recoverable independently from the rest of the platform.

- **Examples**: `captcha-solver` (network-fragile to vendor
  APIs; clear circuit-breaker boundary); `enricher`
  (3rd-party-API-dependent; should fail-soft);
  `proxy-broker` (cooldown logic separable from job execution);
  `audit-log` (compliance retention independent of job
  retention).
- **Counter-example**: the engine's row emission to sinks is
  not a separate failure surface — emission failure invalidates
  the job by design (ADR-0024).

### §2.7 — When none of A–F holds: stays as library or module

If **none** of A–F applies, the responsibility stays in-process
as a library (under `shared-libs/<lang>/`) or as a module of
the consuming service. The default is *not* a microservice; the
default is *colocate*.

- **Field selectors** (CSS / XPath helpers) — driver-internal;
  tightly coupled to driver execution; no independent state;
  stays as a library inside each adapter's source tree.
- **Retry primitives** — every SDK uses them; cross-cutting with
  no state; lives at `sdks/<lang>/common/` per ADR-0027 §5.3.
- **DSL parsing** — happens once at job start; no independent
  state; tightly coupled to engine execution; stays as an
  engine module.
- **Job lifecycle FSM** — operator's reconciliation logic;
  inseparable from CRD watching; stays as an operator module.

The gate is **conservative enough to avoid sprawl** — applying
A–F to the platform surface generates 15 services, not 25 — but
**permissive enough to capture all 15 legitimate splits** that
the seven-layer framework identifies. Future candidates for
service status are evaluated against A–F; rejection requires
applying A–F and finding none satisfied.

## §3 — The 15-service catalog

This section enumerates the catalog. Every entry is structured
identically:

- **Slot** — directory under `infra-services/<slot>/`
- **Language** — implementation language with rationale
- **Gate(s)** — which of A–F justify the split
- **Layer(s)** — which platform layer(s) the service serves
- **State / backend** — what state it owns and which storage tier
- **Consumers (v1alpha2 likely)** — who calls the service
- **Wave** — when the service materialises in the
  [roadmap](../roadmap.md) sequence

The state column references the three storage tiers
[ADR-0023](0023-stateful-services-architecture.md) commits to
(Postgres + Kafka + Redis), plus **MongoDB as a fourth tier**
that ADR-0039 adds in R9.2 (forward reference). Per-service
backend rationale lives in ADR-0039 §3 once that ADR lands; this
catalog records the decision but the rigorous justification is
ADR-0039's. Until R9.2 merges, the Mongo-backed entries below
are **proposed** in this ADR and **ratified** in ADR-0039.

### §3.1 — Layer 1: Acquisition services

#### Slot 1 — `proxy-broker`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | A (BrightData / Oxylabs / Smartproxy / self-hosted) + F (network-fragile, circuit-breaker boundary) |
| Layer | 1.1 (proxy management) |
| State / backend | Cooldown table, per-IP ban tracking, per-provider health — **Redis** |
| Consumers (v1alpha2) | engine; adapters (apply per-driver) |
| Wave | 5 |

Centralises proxy acquisition, rotation, cooldown tracking, and
budget accounting. Go for concurrent network I/O at high fan-out
+ mature gRPC / Redis ecosystems. Slot named by ADR-0028 §4.1;
canonical shape unchanged.

#### Slot 2 — `captcha-solver`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | A (2Captcha / Anti-Captcha / CapMonster) + C (network-I/O bound differently from engine) + F (failure isolation) |
| Layer | 1.2 (CAPTCHA solving) |
| State / backend | Per-provider success rates, per-tenant balance, transaction history — **Postgres** (financial-record-shaped per ADR-0039 anti-pattern §1) |
| Consumers (v1alpha2) | engine (when DSL declares CAPTCHA route); adapters (when driver detects CAPTCHA mid-execution) |
| Wave | 5 |

Routes CAPTCHA challenges (image / hCaptcha / reCAPTCHA /
Turnstile) to a provider, returns a solution token. Postgres
deliberately — financial-record shape (ACID, mature
billing / invoicing ecosystem) wins per ADR-0039 anti-pattern §1.
Slot named by ADR-0028 §4.2; canonical shape unchanged.

#### Slot 3 — `fingerprint-broker`

| Field | Value |
|-------|-------|
| Language | Rust |
| Gate(s) | A (noble-tls / curl-impersonate / custom JA3-JA4) + C (CPU-intensive cryptographic randomness for fingerprint generation) |
| Layer | 1.3 (browser fingerprinting) |
| State / backend | Fingerprint corpus — **Mongo** (richly nested documents per ADR-0039); per-target ban counters — **Redis** (hot atomic counters) |
| Consumers (v1alpha2) | engine (selects at session start); adapters (apply via driver-specific config) |
| Wave | 7 |

Generates, stores, and rotates browser fingerprints (UA,
Accept-Language, viewport, canvas / WebGL, TLS JA3 / JA4, HTTP/2
fingerprint). Rust per ADR-0028 §3.2 — computation-heavy
services fit Rust. Hybrid backend per ADR-0039 §3: fingerprints
are deeply nested documents (TLS extensions, header sequences,
browser-version overrides) where Mongo's schema flexibility
absorbs new dimensions; ban counters are hot atomic increments
where Redis wins unambiguously.

#### Slot 4 — `rate-limit-broker`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (per-domain counter persistence) + D (engine + scheduler + input-broker consume) + E (rate policies evolve independently) |
| Layer | 1.4 (rate limiting / politeness) |
| State / backend | Per-domain sliding-window counters, robots.txt cache — **Redis** (canonical TTL-counter pattern) |
| Consumers (v1alpha2) | engine (checks before request); input-broker (defers queueing if rate-limited); scheduler (defers scheduling) |
| Wave | 7 |

Coordinates per-domain, per-tenant, per-job-class request
budgets across engines and adapters. Go for stateful
coordination with Redis at low latency. Redis backend per
ADR-0039 anti-pattern §2 — sliding-window rate limits are
canonical Redis territory. Slot named by ADR-0028 §4.5.

#### Slot 5 — `session-store`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (cookies / tokens persist across runs) + D (multiple drivers / sessions read same store) + E (session policies evolve independently) |
| Layer | 1.5 (session management) |
| State / backend | Per-tenant per-target cookie jars, OAuth tokens, CSRF state — **Mongo** (richly nested per-target session shapes per ADR-0039) |
| Consumers (v1alpha2) | engine (loads at session start, saves at end); adapters (consume via session config injected by engine) |
| Wave | 8 |

Persists browser session state across adapter restarts and job
boundaries. Go per ADR-0028 §4.4. Mongo backend (rather than
ADR-0028 §4.4's Redis / Postgres list) per ADR-0039 §3 —
sessions vary substantially across targets (simple cookies vs
OAuth vs multi-step CSRF + refresh + session warming); document
model fits where rigid relational schemas hurt. Encryption-at-rest
key-management vs deployer-concern decision deferred to the
build PR.

### §3.2 — Layer 2: Workflow services

#### Slot 6 — `scheduler`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (scheduled job registry) + E (scheduling policies evolve independently of execution) |
| Layer | 2.10 (job scheduling) |
| State / backend | Cron expressions, next-run timestamps, last-run results — **Postgres** (mature `pg_cron`-shaped scheduler libraries; ecosystem maturity wins per ADR-0039 §3) |
| Consumers (v1alpha2) | operator (creates ScrapeJobs from triggered schedules); user (registers schedules) |
| Wave | 8 |

Persists cron-style schedule entries, emits ScrapeJob CRDs at
trigger time. **Supersedes ADR-0028 §6's `scheduler` rejection**
("the operator IS the scheduler") per §1.2: reconciliation
(operator) and cron-trigger persistence + CRD emission
(scheduler) are strictly distinct — reconciliation drives CRDs
that already exist; the scheduler creates them. Embedding
scheduling in the operator would entangle two unrelated
lifecycles (cron-trigger evolution vs CRD reconciliation) and
break gate E. DSL workflow primitives (pagination, conditionals,
multi-step) stay as engine modules per §2.7 counter-example.

### §3.3 — Layer 3: Quality / observability services

Layer 3 is mostly **cross-cutting** — metrics, traces, logs are
covered by ADR-0031's OpenTelemetry framework (R9.3), not by
services. Two Layer 3 responsibilities **are** services:

#### Slot 7 — `cost-tracker`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (per-job cost ledger) + D (proxy-broker emits proxy cost; captcha-solver emits captcha cost; engine emits compute time) + E (cost models evolve independently) |
| Layer | 3.4 (cost tracking) |
| State / backend | Per-job cost ledger, per-tenant aggregations, billing periods — **Postgres** (financial-record shape; ACID matters) |
| Consumers (v1alpha2) | proxy-broker / captcha-solver / engine (emit cost events); operator (surfaces in ScrapeJob status); user (queries via API) |
| Wave | 9 |

Aggregates cost emissions into per-job and per-tenant rollups.
Billing-integration hooks are consumer-side; this service
exposes the ledger API. ADR-0038 (R9.4) defines the contract.

#### Slot 8 — `audit-log`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (append-only audit records) + D (every service emits audits) + F (independent retention / compliance lifecycle) |
| Layer | 3.6 (audit trail) |
| State / backend | Append-only decision log — **Mongo time-series collections** (per ADR-0039 §3 — append-heavy + heterogeneous events + time-range queries fits Mongo time-series) |
| Consumers (v1alpha2) | every service (emit); operator (surfaces in status); user (queries) |
| Wave | 9 |

Records per-job decisions (proxy acquired, selector matched,
CAPTCHA triggered, retry executed). Mongo time-series per
ADR-0039 §3 — append-heavy + heterogeneous event shapes +
time-range queries fit time-series collections; change streams
enable real-time audit consumption. **Clarifies ADR-0028 §6's
`log-aggregator` rejection per §1.2**: that rejection covered
generic stdout / structured-log collection (OTel territory);
audit-log is a distinct concern — per-job decision records with
semantic structure, retention independent of operational logs.

### §3.4 — Layer 4: Output management services

Layer 4 has three services plus engine-internal output evolution
(formats, partitioning) that stays in the engine.

#### Slot 9 — `schema-registry`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (versioned schemas) + D (engine validates extraction; operator validates ScrapeJob spec; downstream consumers validate output) + E (schemas evolve independently of consumers) |
| Layer | 4.1, 2.5 (output schemas with validation) |
| State / backend | Versioned schemas, evolution rules, compatibility checks — **Mongo** (per ADR-0039 §3 — schemas are literally documents; JSON Schema is the natural representation) |
| Consumers (v1alpha2) | engine, operator, all SDKs, downstream data consumers |
| Wave | 6 |

Authoritative store for output schemas (ADR-0034, R9.4) —
versioning, compatibility checks, evolution rules. Go for
CRUD-heavy API; Confluent's schema-registry is the precedent.
Mongo per ADR-0039 §3 — JSON Schema is literally documents;
atomic single-document writes prevent conflicting versions.

#### Slot 10 — `enricher`

| Field | Value |
|-------|-------|
| Language | **TBD** at materialisation (Python if NLP/ML-heavy; Rust if CPU-bound transformation) |
| Gate(s) | A (geocoding via Mapbox / Google / OSM; classification via internal model / vendor LLM) + C (compute-bound or IO-bound differently from engine) + F (vendor dependency, fail-soft boundary) |
| Layer | 4.5 (output enrichment) |
| State / backend | Enrichment outputs (geocoded addresses, classifications, embeddings), enrichment cache — **Mongo** (geospatial 2dsphere queries, vector search via Atlas Vector, document shapes per ADR-0039) + **Redis** (hot cache by content hash) |
| Consumers (v1alpha2) | engine (post-extraction enrichment); data-platform transform stage |
| Wave | 10 |

Enriches extracted rows with geocoded coordinates, classified
labels, embeddings. Language TBD — Python if NLP / ML-heavy
(mature ML libs); Rust if CPU-bound transformation (best
per-row perf). **Decision surfaces to the maintainer** at Wave
10; catalog reserves the slot under both shapes. Mongo per
ADR-0039 §3 — geospatial 2dsphere on geocoded outputs, vector
search via Atlas Vector on embeddings, document-shaped outputs
that vary by enrichment type; no other backend matches.

#### Slot 11 — `dedup-service`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (bloom filter or hash store across runs) + C (memory-bound; scales differently from engine) |
| Layer | 4.6 (output deduplication) |
| State / backend | Dedup keys with TTL — **Redis** (bloom filters + hash sets at sub-millisecond latency) |
| Consumers (v1alpha2) | engine (pre-emit dedup check); data-platform aggregate stage |
| Wave | 10 |

Deduplicates output rows across job boundaries. Go for
memory-efficient bloom-filter + Redis client maturity. Redis
per ADR-0039 anti-pattern §2 — hot-path membership checks are
canonical Redis territory. Output **sinks** (Kafka / S3 /
Webhook / stdout) remain external systems per ADR-0024 — not
services.

### §3.5 — Layer 5: Input management services

#### Slot 12 — `input-broker`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (URL queue with lifecycle) + D (operator + engine + user submit) + E (input sources evolve independently) |
| Layer | 5.1–5.6 (entire input layer) |
| State / backend | URL queue with per-URL lifecycle (seen → queued → in-flight → succeeded / failed → re-queue), per-batch progress, per-URL metadata — **Mongo** (per ADR-0039 §3 — heterogeneous URL metadata across input sources; document model fits diverse schemas; `findAndModify` handles claim semantics at scraping volumes) |
| Consumers (v1alpha2) | operator (creates ScrapeBatch CRDs); engine (pulls next URL); user (submits batches) |
| Wave | 6 |

The runtime for the entire input-management layer: batched URL
ingestion, per-URL lifecycle, per-batch progress, queue-claim
semantics. ADR-0033 (R9.4) defines the full subsystem contract.
Mongo (rather than Postgres SKIP LOCKED + Redis queue) is the
most consequential backend decision — schema flexibility for
diverse input sources (search-result URLs with `query+page`;
sitemap URLs with `lastmod+priority`; API-pushed URLs with
custom payloads) is more valuable than SKIP LOCKED purity at
scraping volumes (millions of URLs, not billions). ADR-0039
anti-pattern §3 explicitly notes the trade-off; ADR-0033
re-justifies.

### §3.6 — Layer 7 (partial): Operational services

Layer 7's full surface (multi-tenancy enforcement, compliance,
anti-detection learning) is v1beta1 territory. One Layer 7
service is in v1alpha2 scope:

#### Slot 13 — `secret-broker`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | A (Vault / AWS SM / K8s Secrets / Postgres-backed) + B (encrypted secrets with rotation history) + D (every service that needs credentials) + F (security isolation) |
| Layer | 7.9 (secrets management) |
| State / backend | Encrypted secrets with rotation history, audit access — **Postgres** (or Vault, depending on deployment) — financial-record-like audit-integrity wins per ADR-0039 anti-pattern §1 |
| Consumers (v1alpha2) | every service that needs credentials (proxy-broker, captcha-solver, sinks, ...) |
| Wave | 8 |

Uniform-interface gRPC API for credential acquisition, rotation,
audit. Backend pluggable across Vault / AWS Secrets Manager /
K8s Secrets / Postgres; consumers see one contract.
**Supersedes ADR-0028 §6's `secrets-broker` rejection** per
§1.2: this service does **not replace** the deployment system's
secret storage — it provides cross-service uniform access
semantics (consistent retry / deadline / audit policy), audit
trails, and rotation primitives that are otherwise reinvented
per consumer. Gate A applies (multiple backends behind one API);
B + D + F apply (rotation state, cross-cutting consumption,
security-isolation surface).

### §3.7 — Layer 6: Driver abstraction (decision pending)

#### Slot 14 — `driver-router`

| Field | Value |
|-------|-------|
| Language | **TBD** — Go (separate service) or Rust (engine module) |
| Gate(s) | D (cross-cutting if separate) + E (routing policies evolve independently) |
| Layer | 6.3 – 6.5 (driver routing intelligence) |
| State / backend | Capability cache, per-target driver-success history, cost models — **Mongo** if persisted; in-memory + periodic snapshot otherwise |
| Consumers (v1alpha2) | engine (asks router which driver to use) |
| Wave | 10 |

Centralises driver routing intelligence: capability matching,
cost-aware selection, fallback chains. Service-vs-engine-module
is a **substantial architectural decision** — separate service
gives cleaner decoupling and A/B testable routing strategies;
engine module gives lower latency and tighter coupling to job
execution. ADR-0035 (R9.4) surfaces full trade-off analysis;
**decision surfaces to the maintainer at Wave 10**. Catalog
reserves the slot under both shapes.

### §3.8 — v1beta1 territory

#### Slot 15 — `template-service`

| Field | Value |
|-------|-------|
| Language | Go |
| Gate(s) | B (job templates registry) + E (templates evolve independently) |
| Layer | 7.1 (job templating) |
| State / backend | Parameterised job definitions — **Mongo** (templates are documents per ADR-0039 §3) |
| Consumers (v1beta1) | user (registers templates); operator (instantiates) |
| Wave | 11+ (deferred to v1beta1) |

Persists parameterised ScrapeJob templates, instantiates them
with parameters at submission time. Deferred to v1beta1 — slot
named to prevent ad-hoc placement.

### §3.9 — Catalog summary

| # | Slot | Language | Wave | Layer(s) | Backend |
|---|------|----------|------|----------|---------|
| 1 | proxy-broker | Go | 5 | 1.1 | Redis |
| 2 | captcha-solver | Go | 5 | 1.2 | Postgres |
| 3 | fingerprint-broker | Rust | 7 | 1.3 | Mongo + Redis |
| 4 | rate-limit-broker | Go | 7 | 1.4 | Redis |
| 5 | session-store | Go | 8 | 1.5 | Mongo |
| 6 | scheduler | Go | 8 | 2.10 | Postgres |
| 7 | cost-tracker | Go | 9 | 3.4 | Postgres |
| 8 | audit-log | Go | 9 | 3.6 | Mongo (time-series) |
| 9 | schema-registry | Go | 6 | 4.1, 2.5 | Mongo |
| 10 | enricher | TBD | 10 | 4.5 | Mongo + Redis |
| 11 | dedup-service | Go | 10 | 4.6 | Redis |
| 12 | input-broker | Go | 6 | 5.1–5.6 | Mongo |
| 13 | secret-broker | Go | 8 | 7.9 | Postgres or Vault |
| 14 | driver-router | TBD | 10 | 6.3–6.5 | Mongo (if persisted) |
| 15 | template-service | Go | 11+ (v1beta1) | 7.1 | Mongo |

**Language distribution**: Go 11 / Rust 1 / TBD 2 / Go-deferred
1.

**Backend distribution**: Mongo 7 + 2 hybrid; Postgres 4; Redis
3 + 2 hybrid (proposed; ratified in ADR-0039 R9.2).

**Wave distribution**: Wave 5 = 2 services; Wave 6 = 2; Wave 7
= 2; Wave 8 = 3; Wave 9 = 2; Wave 10 = 3; v1beta1 = 1.

**14 of 15 services** are in v1alpha2 scope; `template-service`
defers to v1beta1.

### §3.10 — What is *not* in the catalog

By the gate in §2.7 — and consistent with ADR-0028 §6's
preserved rejections — the following are not services:

- **Field selectors** (CSS / XPath helpers) — driver-internal
  libraries within each adapter.
- **Retry primitives** — `sdks/<lang>/common/` per ADR-0027 §5.3.
- **DSL parsing** — engine module.
- **Job lifecycle FSM** — operator module.
- **Metrics / tracing / logging collection** — cross-cutting via
  OpenTelemetry per ADR-0031, **not services**.
- **Output sinks** (Kafka / S3 / Webhook / stdout) — external
  systems consumed via ADR-0024's sink contract, **not services
  we build**.
- **Multi-tenancy enforcement** — operator + RBAC, not a
  separate service until very large scale (revisit in
  v1beta1+).
- **`account-pool`, `vendor-shim`, `config-broker` /
  `feature-flag-service`** — preserved rejections from ADR-0028
  §6.

## §4 — Polyglot SDK matrix

15 services × 4 v1alpha2 languages (Rust / Go / Python /
TypeScript) = up to 60 SDK packages. Maintaining 60 packages is
real engineering burden. ADR-0027 §3.1 already commits SDK
admission to "the first non-trivial consumer in that language
landing"; with 15 services that gate becomes critical.

### §4.1 — The realistic v1alpha2 SDK matrix

For each service, who is likely to consume in each language at
v1alpha2:

| Service | Rust (engine) | Go (operator + Go-adapters + Go-services) | Python (Python-adapters) | TypeScript (TS-adapters) |
|---------|---------------|------------------------------------------|---------------------------|---------------------------|
| proxy-broker | ✓ engine acquires | ✓ Go adapters apply; other Go services check budget | ✓ seleniumbase applies | ✓ playwright applies |
| captcha-solver | ✓ engine routes | maybe | ✓ if seleniumbase detects mid-job | ✓ if playwright detects |
| fingerprint-broker | ✓ engine selects | ✓ if Go adapter applies | ✓ seleniumbase applies | ✓ playwright applies |
| rate-limit-broker | ✓ engine checks | ✓ scheduler defers; input-broker defers | maybe | maybe |
| session-store | ✓ engine load / save | ✓ if Go adapter | ✓ seleniumbase | ✓ playwright |
| scheduler | — | ✓ operator integrates | — | — |
| cost-tracker | ✓ engine emits | — (passive consumer; queries via API) | — | — |
| audit-log | ✓ engine emits | ✓ all Go services emit | maybe Python emits | maybe TS emits |
| schema-registry | ✓ engine validates | ✓ operator validates | ✓ Python adapters validate input | ✓ TS adapters validate |
| enricher | ✓ engine post-extract | — | — | — |
| dedup-service | ✓ engine pre-emit | — | — | — |
| input-broker | ✓ engine pulls | ✓ operator orchestrates | — | — |
| secret-broker | ✓ engine pulls | ✓ all Go services pull | ✓ Python adapters pull | ✓ TS adapters pull |
| driver-router | ✓ engine routes | — | — | — |

Approximately **35 – 40 SDK packages** at v1alpha2 maturity (the
TypeScript and Python columns are sparser than the Rust and Go
columns; many services have engine-only consumers). This
reflects **actual consumers**; speculative SDKs are excluded by
the admission gate (§4.3 below).

### §4.2 — Codegen tooling and the 80:20 ratio

To make 35–40 SDKs maintainable, this ADR formalises ADR-0027's
commitment to **codegen-heavy SDKs**:

- One `proto/spectre/<service>/<version>/<service>.proto` per
  service (canonical shape per ADR-0028 §3.1)
- Automatic SDK generation per language workspace via the
  per-SDK build hook (ADR-0027 §4.2 — `tonic-build` for Rust,
  `buf generate` for Go / Python / TS)
- Per-language SDK package wrapping generated bindings with
  ADR-0027 §5.1's wrapper surface (deadlines, retries, error
  mapping, telemetry hooks)
- A `just new-service-sdk <slot> <lang>` recipe that scaffolds
  the wrapper from a template

The **codegen-to-handwritten ratio** target is **~80:20 per
package** — the typed client struct, construction helpers,
error mapping, and re-exports are templated; the per-protocol
wrapper customisation (retry policies tuned per RPC class,
telemetry hooks, deadline budgets) is the 20% of hand-written
material per package.

This ratio enables **language addition** at low marginal cost.
Adding a fifth language (Java if a JVM consumer emerges, Kotlin
for mobile) is mostly: add a `sdks/<lang>/` workspace, run
codegen across protocols, customise wrappers per ADR-0027 §5.1
— not 15 from-scratch implementations.

### §4.3 — SDK admission gate (formalised)

ADR-0027 §3.1's "first non-trivial consumer" rule is preserved
verbatim. This ADR formalises the gate as **two strict
sub-criteria**:

A new SDK package `sdks/<lang>/<service>/<version>/` lands only
if **at least one** holds:

1. A consumer in `<lang>` that calls the service exists in the
   monorepo, landing in the **same PR or in a directly
   preceding PR** of the same merge sequence.
2. An external consumer has committed to migration with
   maintained adapter status (a community contributor cited in
   the PR description; an in-flight integration whose author is
   on-record).

Speculative SDKs (no consumer; "we might need this in
TypeScript later") **wait**. Adding the SDK later is a small PR;
shipping speculative SDKs accumulates dead weight at 15-services
scale. The two-sub-criterion gate is normative — reviewers reject
speculative SDK PRs and direct contributors to amend this ADR
first if circumstances change.

## §5 — Canonical service shape

Every service in §3's catalog has the same structural shape.
The shape extends ADR-0028 §3 with the operational patterns
that emerge only at multi-service scale. Per-service specifics
fill in the cells; deviations require an ADR amendment to this
one.

### §5.1 — Directory structure

```
infra-services/<slot>/
  proto -> ../../proto/spectre/<slot>/v1alpha1/   # symlink, not copy
  cmd/<slot>/main.<ext>                            # binary entrypoint
  internal/                                        # service-specific logic
    server/                                        # gRPC server impl
    providers/                                     # vendor implementations (private)
    state/                                         # state-backend client
  config/                                          # default config + env loader
  Dockerfile                                       # builds the service image
  Makefile                                         # build / test / lint
  README.md                                        # service-specific docs
  CHANGELOG.md                                     # per-service changelog (§5.6)
  adr/                                             # per-service ADRs (§5.7)
    0000-template.md
```

The `proto` symlink (rather than copy) is a contract: there is
**one source of truth** for the protocol contract — the file
under `proto/`. The service tree references it; the SDK tree
references it; the engine references it. ADR-0026 §5's
dependency rules govern.

`just new-service <slot> <language>` scaffolds the directory
structure from a template; the template is part of `tools/build/`
and lives there once Wave 5 opens.

### §5.2 — Helm chart fragment

Every service contributes the **same chart fragment** structure
under `build/helm/spectre/templates/`:

```
templates/
  <slot>.yaml                   # Deployment + Service + ServiceMonitor
  <slot>-rbac.yaml              # ServiceAccount + Role + RoleBinding (if needed)
  <slot>-config.yaml            # ConfigMap with defaults
  <slot>-cert.yaml              # cert-manager Certificate (per ADR-0032; §5.5)
```

`values.yaml` gets a top-level `<slotCamelCase>:` block matching
the existing `engine:` / `controlPlane:` shape established by
[ADR-0030](0030-helm-chart-structure.md):

```yaml
proxyBroker:
  enabled: true
  replicas: 2
  image:
    registry: docker.io
    repository: fabiocaffarello/spectre-proxy-broker
    tag: ""                 # defaults to chart appVersion when empty
    pullPolicy: IfNotPresent
  service:
    port: 8094
  probes:
    readiness:
      grpc:
        port: 8094
    liveness:
      grpc:
        port: 8094
  resources: {}
  nodeSelector: {}
  extraEnv: []
```

ADR-0030 §3 commits to **single chart with named-template
helpers** for project services (no subcharts for first-party
services; subcharts only for stateful dependencies). That
posture extends here: each new infra-service is a chart
**fragment** within `build/helm/spectre/templates/`, not a
subchart. The `_helpers.tpl` named-template helpers are reused
across fragments.

The chart-fragment shape is **normative**. A future build PR
that ships a new infra-service with a different chart shape is
review-rejected; the fragment template is the contract.

### §5.3 — Compose service block

Every service contributes a **uniform Compose block** to
`docker-compose.yml`, following the pattern ADR-0025 §3
established for engine + adapters + control-plane:

```yaml
services:
  proxy-broker:
    image: spectre-proxy-broker:dev
    build:
      context: .
      dockerfile: infra-services/proxy-broker/Dockerfile
    profiles: ["core"]               # always-on for full-stack dev
    ports:
      - "8094:8094"
    healthcheck:
      test: ["CMD", "/bin/grpc_health_probe", "-addr=:8094"]
      interval: 5s
      timeout: 3s
      retries: 5
    depends_on:
      redis:
        condition: service_healthy
    environment:
      SPECTRE_PROXY_BROKER_LISTEN_ADDR: ":8094"
      SPECTRE_PROXY_BROKER_REDIS_URL: "redis://redis:6379"
```

Profile assignment per service:
- **`core`** — always-on services in the default development
  graph (proxy-broker, captcha-solver, schema-registry,
  input-broker, rate-limit-broker, session-store, scheduler,
  cost-tracker, audit-log, secret-broker)
- **`adapters`** — included when adapter-only experimentation is
  the workflow (fingerprint-broker, dedup-service, enricher,
  driver-router, template-service)

Profile decisions per service are recorded in the build PR's
description.

### §5.4 — Observability surface

Every service exposes the canonical surface (extending
ADR-0028 §3.6):

- **gRPC reflection** (per ADR-0021 §6 pattern)
- **gRPC `Health` service** following ADR-0030's
  Kubernetes-native `grpc:` probe convention (no
  `grpc_health_probe` binary in the image; cluster's
  Kubernetes 1.27+ probes dial directly)
- **Prometheus `/metrics`** on a sidecar port (default `:9090`
  + service port, deployment-level config flips it)
- **OpenTelemetry trace context propagation** via gRPC
  metadata, per ADR-0031's framework (R9.3)
- **Structured logging** (JSON to stdout) with mandated fields:
  `service`, `level`, `timestamp`, `request_id`, `job_id`,
  `tenant_id`, `latency_ms`, `caller`. Fields beyond the
  mandated set are per-service.

ADR-0031 governs the precise wire format (OTLP vs Prometheus
push vs scrape; OTel SDK choices per language); this catalog
references rather than restates.

### §5.5 — Service-to-service mTLS

Every service receives a **cert-manager Certificate** via the
chart's `_helpers.tpl` template, gated by the chart's
`cert-manager.enabled` flag (default `false`; users with
cert-manager already installed flip it on). Service-to-service
gRPC traffic uses mTLS by default when the flag is on.

ADR-0032 (R9.3) governs the certificate authority shape, the
chart's Certificate template, and the engine ↔ adapter ↔
operator wiring; this catalog references rather than restates.

### §5.6 — Per-service CHANGELOG

Each service has its own `CHANGELOG.md` for **independent
release cadence**. The repo-level `CHANGELOG.md` tracks
platform-level changes (ADR landings, cross-service refactors,
chart bumps); per-service changes (provider additions, retry
policy tweaks, error-mapping fixes) are recorded in the
service's local file.

This enables independent semver per service if the platform
ever splits release trains (e.g., proxy-broker hits 2.0.0 while
engine stays at 1.5.0). For v1alpha2, the platform's release
train is unified — every service's `CHANGELOG.md` mirrors the
platform's `[Unreleased]` window — but the structural separation
is in place from the first inhabitant.

### §5.7 — Per-service ADRs

Each service has its own `infra-services/<slot>/adr/` for
**service-internal decisions**: provider integration choices
(why BrightData first vs Oxylabs first), state-backend
trade-offs (Redis pipeline vs Lua scripts), retry policy
decisions (exponential vs fixed). Repo-level ADRs (`docs/adr/`)
remain for platform-wide decisions.

This prevents the platform-wide ADR set from bloating to
hundreds of entries when each of 15 services has 5+ internal
decisions. The platform-wide ADR set stays focused on the
architectural surface the **whole platform** shares; per-service
ADRs hold service-internal nuance.

### §5.8 — CI surface auto-extension

When a new service lands under `infra-services/<slot>/`, the
following CI surfaces auto-extend:

- **`.github/workflows/build.yml`** — bake matrix entry for the
  service's image (one new line per service)
- **Per-language lint job** — auto-discovers the service's
  source tree
- **Per-language test job** — auto-discovers
- **`.github/workflows/scan.yml`** (Wave 1) — Trivy scan on the
  built image
- **`.github/workflows/publish.yml`** (Wave 1) — cosign keyless
  signing integrated as a post-bake step (see W1.4 update below)
- **`.github/workflows/helm-lint.yml`** — chart fragment lints
  via the existing chart-lint gate
- **`.github/workflows/production-smoke.yml`** — included in
  smoke when the service is part of the smoke-cluster topology

The auto-extension is **lightweight glob-based discovery** in
the workflow files; the build PR for a new service does not
need to amend the workflows themselves. ADR-0036 + ADR-0037 land
the contract; Wave 1's CI work materialises the per-workflow
glob patterns.

#### §5.8 W1.4 update (2026-05-07) — cosign integrated into publish.yml

This ADR shipped (R9.1, 2026-05-04) reserving
`.github/workflows/sign.yml` as the canonical filename for
cosign signing, on the assumption that signing would run as a
separate workflow triggered post-publish. W1.4 (cosign keyless
via GitHub OIDC) materialised the signing step and the
implementation took a different path: cosign signing is **a
post-bake step inside `publish.yml`**, not a standalone
workflow.

The trade evaluated:

- **Standalone `sign.yml`** — matches the §5.8 bullet as
  originally written; would trigger on `workflow_run` after a
  successful publish. Cost: handoff brittleness (image
  references must flow through artifacts or be re-resolved
  from the registry); two workflows must complete for a
  release to be considered "done"; an interrupted handoff
  leaves unsigned images on Docker Hub.

- **Integrated step in `publish.yml`** (chosen) — cosign runs
  in the same job after `Verify pushed manifests`, signing by
  manifest-list digest resolved from the verify loop. Wins:
  atomicity (signing failure fails the same workflow that
  pushed; no unsigned images survive), digest reuse (no need
  to re-resolve from the registry), narrower trust boundary
  (`id-token: write` only fires when push-to-registry runs).
  Trade: `publish.yml`'s permissions widen to include
  `id-token: write`, scoped at the job level.

The §5.8 bullet list above is amended in-place: the
`sign.yml` row is replaced by a `publish.yml` row noting the
integrated post-bake step. Future Wave 5+ build PRs that add
new services do not need to wire a separate signing workflow —
adding the new image's name to `publish.yml`'s sign loop is
the auto-extension surface.

The `sign.yml` filename remains **reserved** (not used) — if
a future evolution splits signing into its own workflow (e.g.,
SBOM attestation alongside signing, or signing artifacts
beyond container images), `sign.yml` is the canonical landing
spot.

Verification recipe for downstream consumers lives in
[`docs/architecture/releases.md`](../architecture/releases.md)
"Image signing" section.

## §6 — Deployment posture

### §6.1 — Compose: every service joins the default graph

Every materialised service joins the Compose stack as a
peer of the existing engine / adapters / control-plane services.
Profile assignment per §5.3 governs whether the service is in
the default `core` profile or a more selective one.

ADR-0025 §3's port allocation pattern is the convention: the
engine occupies 8090; the three adapters occupy 8091 / 8092 /
8093; the operator's HTTP probe occupies 8090. New services pick
the next available port in the 80xx range, recorded in the
service's chart-fragment values block (§5.2) and the Compose
service block (§5.3). Port allocation is per-PR; collisions are
review-caught.

### §6.2 — Helm: chart fragments, not subcharts (for first-party services)

ADR-0030 §3 commits the chart to **single-chart-with-named-
templates** for first-party services; subcharts are reserved for
stateful dependencies (Postgres, Redis, Kafka, MinIO, and
Mongo per ADR-0023 §11 in R9.2).

Each new infra-service is a **chart fragment** within
`build/helm/spectre/templates/` per §5.2 — not a separate
subchart. Rationale:

- **Single helm install** for the platform, regardless of how
  many services ship.
- **Shared `_helpers.tpl`** across fragments (label conventions,
  image-registry computation, cert-manager template wiring) —
  avoids drift across services.
- **Single values surface** (`values.yaml`) per platform
  deployment — operators tune one file, not one per service.

The first infra-service build PR that ships in Wave 5
(`proxy-broker`) lands the per-fragment template-naming
convention, the `_helpers.tpl` extensions, and the values-file
block; subsequent services follow the same shape.

### §6.3 — Stateful dependencies: declared per service

Every Mongo / Postgres / Redis-backed service declares its
stateful dependency in the chart-fragment `values.yaml` block
and in the Compose `depends_on` clause. The dependency surfaces
as:

- A `condition.<service>` flag in the chart's deployment
  template (services skip startup if their dependency is not
  enabled).
- A `depends_on` entry in the Compose block (services wait for
  their dependency's healthcheck before starting).

ADR-0023 + ADR-0023 §11 (R9.2) + ADR-0030 govern the stateful
tier deployment posture; this catalog references rather than
restates.

## §7 — Migration sequence

R9.1's ADR-0036 + ADR-0037 are documentation-only; no service
code lands. Per-service build PRs land one service at a time
across Waves 5 onwards, each gated by the canonical shape (§5)
and the ADR's applicable to that service:

| Wave | Services | ADR(s) materialising |
|------|----------|----------------------|
| Wave 5 | proxy-broker, captcha-solver | ADR-0036 first inhabitants; engine refactor per ADR-0037 |
| Wave 6 | schema-registry, input-broker | ADR-0033 (input mgmt), ADR-0034 (output schemas), Mongo subchart per ADR-0023 §11 / ADR-0039 |
| Wave 7 | rate-limit-broker, fingerprint-broker | (DSL primitives in engine — not services) |
| Wave 8 | session-store, scheduler, secret-broker | (mTLS rollout per ADR-0032; first per-service auth boundary) |
| Wave 9 | cost-tracker, audit-log | ADR-0038 (cost), ADR-0031 (observability concretes) |
| Wave 10 | dedup-service, enricher, driver-router | ADR-0035 (driver routing decision); enricher language decision |
| v1beta1 | template-service | (deferred) |

Per-service build PRs are **transformational scope** under the
v1alpha2 process rigor matrix
([CONTRIBUTING.md](../../CONTRIBUTING.md), R9.0). Each requires
a master phase prompt, multi-cluster commits, exhaustive
acceptance criteria. The Wave plan is the long-form schedule;
[`docs/roadmap.md`](../roadmap.md) §4 (rewritten in R9.7)
carries the per-Wave detail.

The order **is not arbitrary**. Wave 5's proxy + captcha are
high-conviction (any production deployment beyond research-grade
needs them). Wave 6's schema-registry + input-broker are
**foundation services** — every subsequent service consumes
schemas; every input-driven scrape uses input-broker. Wave 7's
rate-limit + fingerprint absorb the acquisition layer. Wave 8's
session + scheduler + secret round out operational. Wave 9's
cost + audit close the quality / observability layer. Wave 10's
dedup + enricher + driver-router are the v1alpha2 culmination.
Reordering is permissible **only with maintainer approval** —
the dependency relationships (e.g., enricher depends on
schema-registry; cost-tracker depends on observability
infrastructure) are encoded in the order.

## §8 — Confirmation (acceptance criteria)

The catalog and canonical shape are working when the following
hold **across at least three Wave 5+ build PRs**:

- **A new infra-service PR** explicitly cites its slot's catalog
  entry in §3, demonstrates which gates A–F apply, and follows
  the canonical shape in §5 without deviation.
- **No ad-hoc service** lands in `infra-services/` outside the
  catalog. Reviewers reject PRs introducing un-catalogued
  services and direct contributors to amend this ADR first.
- **The chart fragment shape (§5.2) is reused exactly** —
  fragment template names, values-file block shape, helper
  conventions are identical across services.
- **The CI surface auto-extension (§5.8) holds** — adding a
  service requires lightweight workflow file edits (one line
  per service in the bake matrix) rather than full workflow
  rewrites.
- **The polyglot SDK matrix (§4.1) is honoured** — no SDK
  package lands without its first consumer; speculative SDKs
  do not accumulate.
- **Per-service CHANGELOGs (§5.6) and per-service ADR trees
  (§5.7) are populated** — service-internal decisions live in
  the per-service tree, not in `docs/adr/`.
- **Cross-references between services and platform-wide ADRs
  resolve** — ADR-0033 / 0034 / 0035 / 0038 / 0039 (R9.2 –
  R9.4) and ADR-0031 / 0032 (R9.3) all cite this catalog.

A signal that the catalog needs revision: more than one Wave
build PR in a row encounters a real consumer need that doesn't
fit any catalogued slot and doesn't fit the §3.10 not-in-catalog
list. That's evidence the catalog is incomplete; the response
is a successor ADR (an ADR-0036 amendment in v1beta1+ era)
adding the missing slot, not an ad-hoc placement under
`infra-services/`.

## §9 — What's deferred / out of scope

R9.1 declines these deliberately. Each is a real concern; each
belongs to a later phase or to a sibling ADR.

- **Service code, protocol files, chart fragments, Compose
  entries.** This ADR is contract-only. Per-service build PRs
  (Wave 5 onwards) materialise the catalog one service at a
  time, each gated by §3's per-service criteria and §5's
  canonical shape. R9.1's ADR-0036 + ADR-0037 pair must merge
  before any v1alpha2 implementation PR opens — every
  subsequent ADR (R9.2 – R9.4) and every Wave 5+ build PR
  references the catalog and the canonical shape this ADR
  establishes.
- **Per-service RPC surface details.** §3 names slots and
  states the canonical shape; the per-service `proto/spectre/<slot>/`
  RPC surface is each Wave build PR's design decision.
- **Per-service provider rosters.** §3 is non-exhaustive on
  providers; provider additions / removals are routine PRs, not
  catalog amendments.
- **Multi-version protocol coexistence within a service.** When
  a service's protocol moves from `v1alpha1` to `v1alpha2`,
  ADR-0004's versioning scheme and ADR-0027's SDK strategy
  govern. This catalog only names `v1alpha1` slots.
- **Inter-service composition.** "Engine acquires a proxy from
  proxy-broker, then a fingerprint from fingerprint-broker,
  then a CAPTCHA solution from captcha-solver" — composition
  lives in the engine per ADR-0026 §3.2 and per ADR-0037; this
  catalog does not prescribe orchestration.
- **Tenant-isolation policies.** Per-tenant budget isolation,
  per-tenant audit trails, per-tenant routing rules are
  per-service-build decisions, not catalog constraints.
- **The chart's subchart-vs-fragment decision for Mongo /
  Postgres / Redis stateful tiers.** ADR-0023 + ADR-0030 govern;
  this catalog references.
- **The CI workflow templates (§5.8 glob patterns).** Wave 1's
  CI work materialises the templates; this catalog records the
  intent.
- **Per-service language decisions for `enricher` and
  `driver-router`.** Both surface to the maintainer at
  materialisation (Wave 10) per §3.4 and §3.7.
- **L0 / L1 lake Mongo decision.** ADR-0024 / ADR-0029
  amendments adding Mongo as a sink option and as a Bronze
  storage option are deferred to v1beta1 territory per the
  framework v4 §4 and §7. ADR-0029 §4 footnote (R9.4) surfaces
  Mongo as a future option.
- **External SDK publishing.** Publishing the SDK packages to
  crates.io / PyPI / npm / Go module proxy is post-v1alpha2;
  internal monorepo consumption is the v1alpha2 contract.
- **`apps/` category.** End-user CLI tools that consume SDKs
  the way the engine and operator do. Reserved by ADR-0026 §9
  for a future ADR; not in scope here.

## §10 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive. The engine still consumes Driver
  Protocol; the catalog services are siblings on the protocol
  surface.
- [ADR-0004](0004-protocol-versioning-strategy.md) — Protocol
  versioning. Each catalogued slot follows path-based versioning
  for its own protocol.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — Control plane and ScrapeJob CRD. The scheduler's reframe
  (§3.2) preserves ADR-0019's reconciliation responsibility
  unchanged.
- [ADR-0020](0020-microservices-architecture-supersession.md)
  — Microservices architecture. The four refactor drivers
  (microservices over subprocess; TCP / gRPC over UDS; stateful
  services included; Compose-only development) extend forward
  to every service in this catalog.
- [ADR-0021](0021-service-discovery.md) — Service discovery.
  Each catalogued service registers under its slot's DNS short
  name (`proxy-broker`, `captcha-solver`, etc.) per ADR-0021's
  pattern.
- [ADR-0022](0022-tcp-grpc-transport.md) — TCP / gRPC transport.
  Every service speaks gRPC over TCP; mTLS overlay per
  ADR-0032.
- [ADR-0023](0023-stateful-services-architecture.md) —
  Stateful services. The Postgres / Redis / Kafka tiers carry
  forward; ADR-0023 §11 (R9.2) adds Mongo as the third storage
  tier per ADR-0039.
- [ADR-0024](0024-output-sinks.md) — Output sinks. Sinks are
  external systems consumed via ADR-0024's contract; not
  services in this catalog.
- [ADR-0025](0025-compose-stack.md) — Compose stack. Every
  catalogued service contributes a Compose block per §5.3.
- [ADR-0026](0026-platform-taxonomy.md) — Platform taxonomy.
  This ADR fills the `infra-services/` cell (§3.5 of ADR-0026).
- [ADR-0027](0027-sdk-strategy.md) — Multi-language SDK
  strategy. §4's polyglot SDK matrix is ADR-0027's admission
  gate applied to 15 services.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) — The
  five-slot catalog this ADR extends. §1.2 supersedes ADR-0028
  §6 selectively for `secret-broker`, `scheduler`, and clarifies
  `audit-log` vs log-aggregation.
- [ADR-0029](0029-data-platform-and-lake-dsls.md) — Data
  platform and lake DSLs. Cross-cutting consumption rules from
  ADR-0026 §5 extend to data-platform consumers of catalog
  services.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart
  structure (R7.1). §5.2 chart-fragment shape extends ADR-0030's
  single-chart-with-named-templates posture.
- ADR-0037 (this PR's Cluster B) — Engine as orchestrator of
  platform services. The engine evolution that consumes every
  service in this catalog.
- ADR-0031 / ADR-0032 (R9.3) and ADR-0033 / ADR-0034 / ADR-0035 /
  ADR-0038 / ADR-0039 (R9.2 + R9.4) materialise the cross-cutting
  frameworks (observability, mTLS, MongoDB tier) and per-subsystem
  contracts referenced inline above.
- [`docs/roadmap.md`](../roadmap.md) §4 (rewritten in R9.7) —
  the Wave 1 – 12 detailed plan.
- [CONTRIBUTING.md](../../CONTRIBUTING.md) "v1alpha2 process
  rigor matrix" (R9.0) — the cadence under which Wave 5+ build
  PRs are reviewed.
