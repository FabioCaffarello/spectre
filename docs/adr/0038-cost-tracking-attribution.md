---
status: accepted
date: 2026-05-06
deciders: [Fabio Caffarello]
---

# Cost tracking and per-job attribution

## §1 — Context and Problem Statement

The v1alpha1 platform has **no cost visibility**. Every
scrape consumes resources — proxy IP allocations, CAPTCHA
solver credits, compute time, network egress, storage —
but no aggregate surface tells a tenant "this batch cost
$N" or tells a platform operator "tenant X consumed $M
across all batches this month".

The shape was defensible during the refactor — establishing
the working pipeline before adding cost scaffolding. The
shape is **not defensible** at v1alpha2 production scale:

- **Multi-tenant deployments** require per-tenant cost
  attribution for chargeback / showback / quota
  enforcement. Without per-tenant ledgers, fair-share
  resource allocation across tenants is impossible.
- **Per-job cost transparency** is a real user need —
  tenants want to know which scrape jobs cost the most,
  which targets have unfavourable cost economics, which
  drivers offer the best cost-efficiency.
- **Cost-aware routing** (per [ADR-0035](0035-dsl-evolution-driver-abstraction.md)
  §5.2) requires actual cost data — the driver-router (if
  it materialises per ADR-0035 §6) selects drivers based
  on per-target cost; without per-target cost data, the
  routing logic has no signal.
- **Provider cost-management** is operationally critical —
  proxy providers (BrightData, Oxylabs, Smartproxy) and
  CAPTCHA providers (2Captcha, Anti-Captcha, CapMonster)
  charge per-call; without aggregation, the platform
  cannot detect cost anomalies (a misconfigured job
  burning through proxy budget; a CAPTCHA-storm target
  consuming solver credits at 10× normal rate).

This ADR commits the v1alpha2 platform to:

- A **`cost-tracker` service** (slot 7 per
  [ADR-0036 §3.3](0036-microservices-catalog-expansion.md))
  that owns the per-job cost ledger and per-tenant
  aggregations
- **Cost emission points** at gate-A services that incur
  per-call cost (proxy-broker per-acquire; captcha-solver
  per-solve; engine per-job-compute-time)
- **Per-job attribution** keyed by `job_id` so all costs
  for a job aggregate cleanly
- **Tenant aggregation** with per-tenant per-period
  rollups for billing / showback hooks
- **Billing integration shape** — hooks for downstream
  invoicing, **not** invoicing itself (deferred per
  v1beta1 financial-tooling work)

ADR-0038 is one of four subsystem ADRs in R9.4; together
with ADR-0033 (input management), ADR-0034 (output
schemas), and ADR-0035 (DSL evolution) they materialise the
catalog services that ADR-0036 reserves.

### §1.1 — What this ADR does not yet land

No service code, no proto file, no chart fragment, no
cost-emission paths land in R9.4. This ADR is contract-only.
The first build PR is **Wave 9** (per ADR-0036's wave
assignment) — `cost-tracker` service materialises alongside
`audit-log`, with engine-side and provider-service-side cost
emissions wired in the same PR sequence. ADR-0038 §9 records
the dependency on
[ADR-0031](0031-observability-framework.md) — cost-tracker
extends the observability framework's metric taxonomy, so
ADR-0031's Wave 3 first observability PR is a soft
prerequisite.

## §2 — Decision summary

R9.4 commits the cost-tracking subsystem to:

- **`cost-tracker` service** at `infra-services/cost-tracker/`
  per ADR-0036's canonical service shape (Go; **Postgres**
  backend per [ADR-0039 §3.7](0039-mongodb-third-storage-tier.md)
  — financial-record shape, ACID matters, anti-pattern §4.1
  rejects Mongo here; gates B + D + E per ADR-0036 §3.3).
- **Cost emission contract** — gate-A services that incur
  per-call cost emit `Cost.Record` events asynchronously
  (per [ADR-0037 §4.3](0037-engine-as-orchestrator.md)
  fire-and-forget pattern); the engine emits per-job
  compute time at job completion.
- **Per-job ledger** — every job's complete cost trail is
  retrievable by `job_id`; the audit trail is append-only
  per ADR-0023's job-state-immutability invariant.
- **Per-tenant rollups** — per-tenant per-period
  aggregations (hourly, daily, monthly) computed
  incrementally; queryable for chargeback / quota /
  alerting.
- **Billing integration hooks** — webhooks fire on rollup
  computation; downstream invoicing systems consume
  rollups via the cost-tracker's read API. The invoicing
  itself (PDF generation, payment processing, accounts-
  receivable integration) is **out of scope** — the
  service publishes ledger data; financial systems do
  what financial systems do.

The split honours ADR-0036's gate B (cost-tracker owns
financial-record state outliving any single job execution),
gate D (cross-cutting consumption — proxy-broker emits;
captcha-solver emits; engine emits; operator surfaces in
ScrapeJob status; user queries via API), and gate E (cost
models evolve independently of job logic).

## §3 — cost-tracker service contract

### §3.1 — Data model

The cost-tracker persists three primary entity types:

- **`CostEvent`** — atomic cost emission. Per
  `(job_id, emitter, timestamp)`; carries the cost amount,
  cost unit, provider (where applicable), and arbitrary
  context metadata.
- **`JobCostLedger`** — derived view per `job_id`;
  aggregates all `CostEvent` entries for the job into a
  total + per-emitter breakdown.
- **`TenantPeriodRollup`** — derived rollup per
  `(tenant_id, period_start, period_end)`; aggregates
  `JobCostLedger` entries for jobs landing in the period
  into a total + per-emitter breakdown + per-target
  breakdown.

The schema is **deliberately simple**:

```sql
CREATE TABLE cost_events (
    id              BIGSERIAL PRIMARY KEY,
    job_id          TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    emitter         TEXT NOT NULL,        -- service slot or "engine"
    timestamp       TIMESTAMPTZ NOT NULL,
    amount          NUMERIC(20, 8) NOT NULL,
    unit            TEXT NOT NULL,        -- "USD", "credit", "request", "second"
    provider        TEXT,                 -- e.g., "brightdata", "2captcha"; null for engine
    context         JSONB,                -- per-emitter metadata
    INDEX (job_id),
    INDEX (tenant_id, timestamp)
);

CREATE TABLE tenant_period_rollups (
    tenant_id       TEXT NOT NULL,
    period_start    TIMESTAMPTZ NOT NULL,
    period_end      TIMESTAMPTZ NOT NULL,
    total_cost      NUMERIC(20, 8) NOT NULL,
    breakdown       JSONB NOT NULL,       -- per-emitter / per-provider / per-target
    computed_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, period_start, period_end)
);
```

`JobCostLedger` is **a view** over `cost_events`, not a
materialised table — per-job aggregation is a small
SELECT on the indexed `job_id` column; materialising would
trade write efficiency for read efficiency in the wrong
direction (cost emission is high-volume; per-job reads are
low-volume).

`tenant_period_rollups` **is** materialised because
cross-period rollup queries can span millions of cost
events; the rollup table provides O(1) lookup once
computed. The compute job runs incrementally per §6.

### §3.2 — RPC surface (indicative; build PR settles)

```proto
service CostTracker {
  // Emission (called by gate-A services + engine)
  rpc Record(RecordRequest) returns (Empty);
  // ^ async fire-and-forget per ADR-0037 §4.3

  // Per-job ledger queries
  rpc GetJobLedger(GetJobLedgerRequest) returns (JobCostLedger);

  // Per-tenant rollup queries
  rpc GetTenantRollup(GetTenantRollupRequest) returns (TenantPeriodRollup);
  rpc ListTenantRollups(ListTenantRollupsRequest) returns (ListResponse);

  // Billing integration hooks
  rpc RegisterRollupWebhook(RegisterRollupWebhookRequest)
      returns (Webhook);
  rpc ListRollupWebhooks(ListRollupWebhooksRequest)
      returns (ListResponse);
}
```

The RPC surface follows ADR-0028 §3.1's canonical
proto-package convention (`spectre.costtracker.v1alpha1`)
and ADR-0027's per-language SDK admission gate. The
`Record` RPC is async-emission per ADR-0037 §4.3; the
read RPCs are synchronous.

### §3.3 — Idempotency

`Record` is **idempotent** — duplicate emissions for the
same `(job_id, emitter, timestamp, sequence)` are
deduplicated at the cost-tracker. The `sequence` field is
emitter-supplied; emitters increment per emission.

The idempotency exists for the async fire-and-forget
pattern — emitters retry on transient failure (per
ADR-0027's SDK retry policy), and retries must not
double-count costs.

## §4 — Cost emission points

Three categories of emitters in v1alpha2: provider
services (gate A), engine, and **non-emitters** (services
that do not incur per-call cost).

### §4.1 — proxy-broker emissions

`proxy-broker` (slot 1 per ADR-0036 §3.1) emits a
`CostEvent` on every successful proxy lease release:

- **`emitter`**: `"proxy-broker"`
- **`amount`**: provider-reported per-call cost
  (BrightData / Oxylabs / Smartproxy / ... charge per
  request or per byte; the broker tracks this from the
  provider API)
- **`unit`**: `"USD"` or the provider's reporting currency
- **`provider`**: the specific provider (`"brightdata"`,
  `"oxylabs"`, etc.)
- **`context`**: `{ region, sticky, lease_duration_seconds,
  bytes_transferred }`

Cost emission happens **post-release** — the broker
reconciles the lease's cost from provider APIs (rate-limited
to avoid hitting provider API quotas; some providers offer
batch cost-reporting endpoints), then emits the event with
the actual cost.

For providers that don't expose per-call cost APIs,
proxy-broker emits **estimated cost** based on
configuration (per-region rate cards; per-tenant
contracted rates); the `context.estimated: true` flag
distinguishes these from provider-reported events. v1beta1
work refines estimation accuracy.

### §4.2 — captcha-solver emissions

`captcha-solver` (slot 2 per ADR-0036 §3.1) emits per
successful solve:

- **`emitter`**: `"captcha-solver"`
- **`amount`**: provider per-solve cost
- **`unit`**: `"USD"` or `"credit"` (some providers
  meter in pre-purchased credits)
- **`provider`**: `"2captcha"`, `"anticaptcha"`,
  `"capmonster"`, etc.
- **`context`**: `{ challenge_type, solve_duration_ms,
  credits_remaining }`

Failed solves do NOT emit cost — providers typically don't
charge for failures, and emitting zero-cost events would
inflate the ledger noise.

### §4.3 — Engine compute-time emissions

The engine emits a `CostEvent` at job completion
representing compute time consumed:

- **`emitter`**: `"engine"`
- **`amount`**: `compute_seconds × per_second_rate`
- **`unit`**: `"USD"`
- **`provider`**: null (compute is platform-internal)
- **`context`**: `{ steps_executed, rows_emitted,
  compute_seconds, driver_kind }`

The `per_second_rate` is **deployment-side
configuration** — operators set their own
infrastructure cost (e.g., $0.0002 per CPU-second based on
their cluster's per-pod cost). Default for development /
testing is `0.0` (no compute cost emitted; only
provider-emitted events accumulate).

### §4.4 — Future emitters

Wave 9+ build PRs add emitters as services materialise:

- **enricher** (slot 10) — per geocode call (third-party
  geocoder API costs); per classification call (LLM costs
  if vendor models are used)
- **fingerprint-broker** (slot 3) — if external fingerprint
  providers are integrated (per provider API costs)
- **secret-broker** (slot 13) — typically no per-call cost,
  but Vault-backed deployments may incur API costs

The emitter shape is **uniform** — every emitter calls
`cost_tracker.Record` with the same `CostEvent` shape per
§3.1; new emitters add no new RPCs.

### §4.5 — What does NOT emit cost

Not every service emits cost events. Services that **do
not emit**:

- **rate-limit-broker, dedup-service, schema-registry,
  input-broker, session-store, audit-log** — operational
  services with no per-call provider cost; their cost is
  amortised in engine compute-time emissions.
- **Sinks** — storage costs (Kafka retention, S3 storage,
  Webhook bandwidth) are **deployment-side
  infrastructure cost**, not per-job platform cost.
  Tenants pay for their own sink infrastructure separately;
  the platform does not aggregate it.
- **Adapters (Playwright, SeleniumBase, curl-impersonate)**
  — adapter compute time rolls up into engine compute-time
  emissions (the engine tracks per-job total compute,
  including adapter calls).

## §5 — Per-job attribution

### §5.1 — `job_id` as the join key

Every `CostEvent` carries the `job_id` of the originating
ScrapeJob. The job_id is **propagated** through every
service call via the gRPC metadata `job-id` header (per
ADR-0031 §2.5's correlation ID set). Services receiving
the call extract the job_id from metadata and include it
in their cost emission.

The propagation chain:
- Operator creates ScrapeJob with `job_id = <CR name>`
- Operator dials engine with `job-id` metadata header
- Engine receives, tracks per-job, propagates to every
  per-step service call
- Provider services emit cost with the propagated `job_id`
- cost-tracker indexes events by `job_id` for ledger
  retrieval

### §5.2 — Job ledger query

`GetJobLedger(job_id)` returns:

```proto
message JobCostLedger {
  string job_id = 1;
  string tenant_id = 2;
  google.protobuf.Timestamp job_started_at = 3;
  google.protobuf.Timestamp job_completed_at = 4;

  // Total cost in canonical currency (USD)
  double total_usd = 5;

  // Per-emitter breakdown
  repeated EmitterCost per_emitter = 6;
  // Per-provider breakdown (collapses across emitters)
  repeated ProviderCost per_provider = 7;

  // Raw events (for auditing / drill-down)
  repeated CostEvent events = 8;
}
```

The ledger is **idempotent and reproducible** — the same
job's ledger query always returns the same total (events
are append-only; no in-place updates). This is the
financial-record-integrity invariant per ADR-0039 §4.1
anti-pattern (Mongo as financial store: forbidden);
Postgres ACID semantics are essential.

### §5.3 — Surfacing in ScrapeJob status

The operator (per ADR-0019) extends ScrapeJob's
`status` with a cost summary:

```yaml
status:
  phase: Succeeded
  rowsExtracted: 42
  totalCost:                       # added in Wave 9
    amount: 0.0234
    currency: USD
    breakdown:
      proxy-broker: 0.0142
      captcha-solver: 0.0085
      engine: 0.0007
```

The operator polls `cost_tracker.GetJobLedger` post-job-
completion (one call per job; not per reconciliation).
Failures to retrieve the ledger surface
`COST_TRACKER_UNAVAILABLE` per ADR-0031 §6.2 but do **not**
fail the job — cost data is informational, not load-bearing
for job execution.

### §5.4 — Surfacing in batch progress

ScrapeBatch (per ADR-0033) status extends with per-batch
cost rollup:

```yaml
status:
  inputSourceStatus:
    succeeded: 132890
    # ... other fields ...
  totalCost:                       # added in Wave 9
    amount: 31.4523
    currency: USD
    breakdown:
      proxy-broker: 19.8234
      captcha-solver: 11.2891
      engine: 0.3398
```

The operator aggregates per-child-job cost ledgers into the
batch-level rollup at reconciliation time. For batches with
millions of child jobs, the cost-tracker's
`GetTenantRollup` (per §6) provides the aggregate without
walking child jobs individually.

## §6 — Tenant aggregation

Per-tenant per-period rollups support chargeback /
showback / quota use cases. The rollup window is
configurable; defaults are hourly, daily, monthly.

### §6.1 — Rollup compute strategy

Rollups compute **incrementally** — at each window
boundary, the cost-tracker:

1. Queries `cost_events` for the period
   `(tenant_id, [period_start, period_end))`
2. Aggregates by emitter, provider, target_domain
3. Inserts (or upserts) into `tenant_period_rollups`
4. Fires registered webhooks (§7) with the rollup payload

The compute is **scheduled inside the cost-tracker** — a
periodic worker runs the rollup at window boundaries
(every hour for hourly rollups; midnight UTC for daily;
first-of-month for monthly). The schedule is configurable
per deployment.

### §6.2 — Rollup query

`GetTenantRollup(tenant_id, period_start, period_end)`
returns:

```proto
message TenantPeriodRollup {
  string tenant_id = 1;
  google.protobuf.Timestamp period_start = 2;
  google.protobuf.Timestamp period_end = 3;
  double total_usd = 4;

  // Per-emitter breakdown
  repeated EmitterCost per_emitter = 5;
  // Per-provider breakdown
  repeated ProviderCost per_provider = 6;
  // Per-target-domain breakdown
  repeated TargetCost per_target = 7;

  // Job count contributing to the rollup
  int64 job_count = 8;

  google.protobuf.Timestamp computed_at = 9;
}
```

### §6.3 — Custom periods + cross-tenant aggregation

Custom-period queries (non-standard windows) compute
on-the-fly via `GetTenantRollup`; expensive for long spans;
the cost-tracker logs slow queries. v1beta1 may add weekly
/ quarterly pre-computed periods based on tenant demand.

Cross-tenant aggregation (platform-operator queries) uses
the `tenant_period_rollups` table directly via SQL —
operator-tooling concern, not a cost-tracker RPC.

## §7 — Billing integration shape

Cost-tracker provides **hooks for downstream invoicing**;
it is **not an invoicing system itself**. The hooks:

### §7.1 — Rollup webhooks

Tenants (or platform operators) register webhooks via
`RegisterRollupWebhook`:

```yaml
webhook:
  url: "https://billing.tenant-a.com/spectre-rollup"
  authentication:
    bearerToken: "<secret-broker-managed>"
  filter:
    period: monthly                # only fires on monthly rollups
    minTotal: 100.00               # only fires if total >= $100
  retryPolicy:
    maxAttempts: 5
    backoffSeconds: [60, 300, 1800, 7200, 21600]
```

When a rollup completes, the cost-tracker POSTs the
TenantPeriodRollup payload (JSON) to the webhook URL with
the configured authentication. Failed deliveries retry per
the policy; persistent failures surface
`WEBHOOK_DELIVERY_FAILED` audit events per ADR-0031 §6.

The webhook authentication piggybacks on
[ADR-0024 §4](0024-output-sinks.md)'s webhook authentication
deferral — when ADR-0032 §7's webhook auth ADR lands, the
cost-tracker rollup webhooks adopt the same auth mechanism.

### §7.2 — What invoicing systems do

Downstream invoicing consumes rollup webhooks (or polls
`ListTenantRollups`) and handles invoice generation,
payment processing, accounts-receivable, dunning, tenant
portals — all out of scope for cost-tracker. The
cost-tracker's responsibility ends at "publish the rollup
to consumers"; downstream financial systems handle
invoicing primitives the cost-tracker would duplicate
poorly.

### §7.3 — On-demand cost report API

For tenants without a billing system integration, the
cost-tracker exposes a **read API** the platform's
end-user surface (a future tenant portal; CLI tooling) can
consume:

- `GetJobLedger(job_id)` per §5.2
- `GetTenantRollup(tenant_id, period)` per §6.2
- `ListTenantRollups(tenant_id, since)` for trend display

These APIs are **read-only** — they expose the same data
the webhook publishes; the difference is poll-based
(read API) vs push-based (webhook).

### §7.4 — Quota enforcement

Cost-aware **quota enforcement** (e.g., "tenant X has
exceeded $1000 this month; reject new ScrapeJobs") is
**not** in scope for cost-tracker. The cost-tracker
provides the data; quota enforcement lives in a separate
component — likely the operator's webhook validation per
ADR-0019 — that consumes the cost-tracker's read API.
v1beta1 work materialises the quota integration when
real demand surfaces.

## §8 — Backend choice

The cost-tracker uses **PostgreSQL as the primary
backend** per [ADR-0039 §3.7](0039-mongodb-third-storage-tier.md).
The choice is rigorously justified there; the summary:

- **Financial-record shape** — multi-row aggregations
  (job ledgers, tenant rollups), strict consistency for
  billing data, foreign-key integrity for tenant /
  emitter references. Anti-pattern §4.1 explicitly rejects
  Mongo for financial stores.
- **SQL ecosystem maturity for billing/invoicing** — the
  downstream integrations (§7) typically expect SQL
  semantics for accounts-receivable; Postgres aligns.
- **ACID transactions** — rollup compute (§6.1) requires
  atomic reads of cost_events + writes to
  tenant_period_rollups; Postgres handles this trivially.
  Mongo's per-document atomicity falls short for
  cross-collection rollups.

The pinning policy follows ADR-0023 §8 (Postgres library:
`pgx/v5` for Go services); the cost-tracker reuses the
existing pinning. No new database needed — the
cost-tracker uses the same Postgres instance the engine
and operator use, with its own database / schema for
isolation.

## §9 — Migration sequence

R9.4's ADR-0038 is documentation-only. The materialisation:

| Wave | Scope |
|---|---|
| Wave 3 (soft prerequisite) | ADR-0031's first observability PR lands the metrics taxonomy that includes `spectre_<slot>_cost_units`. The metric shape is the v1alpha1 emission path — observability metrics. The ADR-0038 cost-tracker consumes these as one input alongside service-side emissions. |
| Wave 5 – 8 | gate-A services (proxy-broker, captcha-solver) emit cost via the metric path per ADR-0031 §7. The data accumulates without a tracker; engineer-side tooling scrapes Prometheus. |
| Wave 9 (build PR) | cost-tracker service materialises + engine emits compute-time cost + provider services emit per-call cost via the cost-tracker's `Record` RPC + operator surfaces `status.totalCost` in ScrapeJob + ScrapeBatch. The Wave 9 PR sequence pairs with audit-log per ADR-0036. |
| Wave 9 (post-build) | Tenant rollup webhooks register; downstream invoicing systems integrate (per-tenant; outside platform scope). |
| Wave 10 | Driver-router (per ADR-0035 §6) consumes per-target cost data for cost-aware selection (per ADR-0035 §5.2). |
| v1beta1 | Quota enforcement (§7.4) materialises if real demand surfaces. |

The Wave 9 build PR is **transformational scope** — the
cost-tracker service materialises with engine emission
paths, provider service emission paths, operator status
extension, and chart fragment in the same PR sequence.
ADR-0031's Wave 3 first observability PR is a soft
prerequisite (the metric taxonomy is the v1alpha1
emission path Wave 5 – 8 services use before the tracker
exists).

## §10 — Confirmation (acceptance criteria)

The framework is working when the following hold **by the
close of Wave 9**:

- **A ScrapeJob's `status.totalCost`** reflects the actual
  cost of the job within 30 seconds of completion (one
  reconciliation cycle).
- **A ScrapeBatch's `status.totalCost`** aggregates child
  ScrapeJob costs correctly.
- **Per-tenant rollup compute** runs incrementally at
  hourly / daily / monthly boundaries; the
  `tenant_period_rollups` table populates without manual
  intervention.
- **Rollup webhooks fire** for registered tenants when the
  rollup completes; failed deliveries retry per the
  configured policy.
- **Idempotent emission** — duplicate
  `Record(job_id, emitter, sequence)` calls do not
  double-count costs; verified via integration tests.
- **`COST_TRACKER_UNAVAILABLE` graceful degradation** —
  when the cost-tracker is offline, jobs continue to
  execute; cost-emission events buffer locally
  (engineer-side tooling) or are dropped (provider
  services, per ADR-0037 §4.3 fire-and-forget); the audit
  trail records the loss per ADR-0031 §6.

A signal that the framework needs revision: more than one
Wave 9+ tenant pilot reports a real cost-tracking workflow
not covered by per-emitter / per-provider / per-target
breakdowns. That's evidence the breakdown taxonomy is
incomplete; the response is an ADR amendment that adds the
missing dimension to the rollup shape, not per-tenant
deviation.

## §11 — What's deferred / out of scope

R9.4 declines these deliberately. Each is a real concern;
each belongs to a later phase or to a sibling ADR.

- **Invoicing itself** (PDF generation, payment processing,
  accounts-receivable). Per §7.2 — out of platform scope.
- **Quota enforcement.** Per §7.4 — separate component
  consuming cost-tracker data; v1beta1 when demand
  surfaces.
- **Cost forecasting / prediction.** ML-based forecasts
  ("at current rate, this batch will cost $X") are out
  of v1alpha2 scope; potential v1beta1 ML work.
- **Cost anomaly detection.** Alerting on per-tenant cost
  spikes ("tenant X spent 10× normal yesterday") is
  v1beta1 — the cost-tracker provides the data; alerting
  rules belong in observability tooling.
- **Multi-currency support.** All amounts in USD at
  v1alpha2 (canonical). Tenant-side currency conversion
  is downstream invoicing concern. v1beta1 may add
  per-tenant base currency configuration.
- **Cost attribution to upstream consumers.** When
  `tenant-a` triggers a workflow that feeds
  `tenant-b`'s downstream pipeline, who pays? V1beta1
  multi-tenant orchestration concern.
- **Provider-side cost reconciliation.** Some providers
  invoice monthly with reconciled cost (different from
  per-call cost reports); reconciliation between
  cost-tracker estimates and provider invoices is
  v1beta1.
- **Compute-time-cost calibration.** v1alpha2's per-second
  rate is deployment-configured; calibrating rates to
  actual cluster cost (CPU + memory + network) requires
  cluster-cost integration outside platform scope.
- **Per-row cost attribution.** Allocating a job's cost
  across emitted rows (some rows expensive, some cheap)
  is v1beta1.
- **Cost SLAs / budgets per ScrapeBatch.**
  `ScrapeBatch.spec.budget` field that aborts the batch
  when cumulative cost exceeds threshold — v1beta1
  feature; cost-tracker provides the data, scheduling
  enforcement is operator concern.

## §12 — Reference materials

- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane; the operator's `status.totalCost`
  extension per §5.3.
- [ADR-0023](0023-stateful-services-architecture.md) —
  stateful services; Postgres tier carries forward; no
  new database.
- [ADR-0024](0024-output-sinks.md) — output sinks; rollup
  webhooks (§7.1) reuse ADR-0024's webhook contract;
  webhook authentication shares the deferral with ADR-0032
  §7.
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy;
  per-language cost-tracker SDKs follow the admission
  gate.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) —
  infra-services catalog precedent.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart;
  cost-tracker chart fragment lands per the canonical
  pattern.
- [ADR-0031](0031-observability-framework.md) —
  observability framework; §7 of that ADR commits the
  metric emission shape this ADR consumes.
- [ADR-0036](0036-microservices-catalog-expansion.md) —
  the 15-service catalog; `cost-tracker` is slot 7.
- [ADR-0037](0037-engine-as-orchestrator.md) — engine as
  orchestrator; §4.3 commits the async emission pattern;
  §5 degradation modes treat cost-tracker as optional
  (graceful degradation).
- [ADR-0039](0039-mongodb-third-storage-tier.md) —
  MongoDB tier; §3.7 evaluates cost-tracker's backend
  (Postgres; anti-pattern §4.1 rejects Mongo).
- ADR-0033 (this PR's Cluster A) — input management;
  ScrapeBatch `status.totalCost` aggregates child job
  ledgers.
- ADR-0034 (this PR's Cluster B) — output schema; per-row
  cost attribution (deferred per §11) interfaces with
  schema-aware row counts.
- ADR-0035 (this PR's Cluster C) — DSL evolution;
  cost-aware driver routing (§5.2 of that ADR) consumes
  this ADR's per-target cost data.
- ADR-0032 (R9.3) — service-to-service mTLS; rollup
  webhook authentication per §7.1 shares ADR-0032 §7's
  webhook auth deferral.
- Webhook delivery patterns:
  <https://www.rfc-editor.org/rfc/rfc6585> (HTTP
  retry semantics)
