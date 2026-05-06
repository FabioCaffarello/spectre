---
status: accepted
date: 2026-05-06
deciders: [Fabio Caffarello]
---

# Input management subsystem

## §1 — Context and Problem Statement

The v1alpha1 `ScrapeJob` CRD ([ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md))
encodes a **single DSL targeting a single execution**.
Every job carries its own DSL, its own list of URLs, its own
output sink configuration. The shape works for the v1alpha1
production-installable surface — a sample ScrapeJob targeting
one or two URLs reaches `Completed` against the production-smoke
Helm chart per R7.2's CI gate.

The shape **fails at scraping volumes** real deployments
need. Web scraping at platform scale is not "submit one
ScrapeJob per URL"; it is:

- **Bulk URL ingestion** — a marketing analytics tenant
  scrapes 100,000 e-commerce listings nightly; submitting
  100,000 ScrapeJob CRs is operationally untenable
  (etcd / kube-apiserver pressure; per-job reconciliation
  overhead; status surface bloat).
- **Per-URL lifecycle tracking** — the same URL may need to
  be re-attempted on failure, re-scraped on a recurring
  cadence, deduplicated against prior fetches, prioritised
  by source, throttled per-domain. The ScrapeJob CRD has
  no per-URL state surface beyond "submit URL list, get rows
  back".
- **Heterogeneous input sources** — URL queues do not
  originate from one source. Real workflows ingest from
  sitemap discovery, search-result harvest, API push, file
  upload, queue consumption (Kafka topic of new
  product IDs), seeded crawl (BFS from a starting point).
  Each source has different metadata shape and different
  arrival cadence.
- **Batch progress tracking** — a tenant submitting 100,000
  URLs needs visibility into "how many succeeded / failed /
  pending / re-queued"; a per-job status only describes one
  job.

The v1alpha1 platform leaves all of these concerns to **the
caller** — the user submitting work to the platform — or
fails to address them at all. This ADR commits the v1alpha2
platform to:

- A new `ScrapeBatch` CRD that encodes a **batch of work**
  the operator orchestrates into N child ScrapeJob CRs over
  time
- An `input-broker` service (slot 12 per
  [ADR-0036 §3.5](0036-microservices-catalog-expansion.md))
  that owns the URL queue lifecycle, the per-URL metadata,
  and the per-batch progress aggregation
- A clear separation: the **operator orchestrates**
  (ScrapeBatch reconciliation, ScrapeJob spawning, batch
  progress aggregation); the **input-broker persists**
  (URL queue, lifecycle, metadata)

ADR-0033 is one of four subsystem ADRs in R9.4 (the others
are ADR-0034 — output schemas; ADR-0035 — DSL evolution;
ADR-0038 — cost tracking); together they materialise the
catalog services that ADR-0036's catalog reserves.

### §1.1 — What this ADR does not yet land

No service code, no CRD schema, no proto file, no operator
reconciler, no chart fragment land in R9.4. This ADR is
contract-only. The first build PR is **Wave 6** (per
ADR-0036's wave assignment) — `input-broker` service +
ScrapeBatch CRD + operator reconciler extension, alongside
the schema-registry materialisation per ADR-0034.

## §2 — Decision summary

R9.4 commits the input-management subsystem to two
artefacts:

- **`input-broker` service** at `infra-services/input-broker/`
  per ADR-0036's canonical service shape (Go; Mongo backend
  per ADR-0039 §3.12; gates B + D + E per ADR-0036 §3.5).
  Owns URL queue persistence, per-URL lifecycle, per-URL
  metadata, per-batch progress.
- **`ScrapeBatch` CRD** in the existing `spectre.io/v1alpha2`
  API group per ADR-0019's CRD-scoping pattern. Encodes a
  batch's input source, scrape DSL template, output sink
  template, batch-level scheduling parameters. The operator
  reconciles ScrapeBatch CRs by **enqueueing URLs into
  input-broker** and **spawning child ScrapeJob CRs over
  time** as URLs become available.

The two artefacts are **complementary, not duplicative**:

- ScrapeBatch CR holds the **declarative batch shape**
  (what to scrape, how to scrape it, where to send output).
  It is small (a few KB) and ergonomic for users.
- input-broker holds the **expanding URL state**
  (potentially millions of URL records with per-URL
  lifecycle). It scales independently of the operator's
  CRD-watch loop.

The split honours ADR-0036's gate B (input-broker owns
persistent state outliving any single job execution),
gate D (cross-cutting consumption — operator + engine + user
all interact), and gate E (input sources evolve
independently of the operator's reconciliation logic).

## §3 — The ScrapeBatch CRD

### §3.1 — CRD shape

The `ScrapeBatch` CRD lives at `spectre.io/v1alpha2`
alongside the existing `ScrapeJob` CRD (per ADR-0019). The
spec captures input source (oneof: sitemap, file,
apiPush, queue, seededCrawl per §5), scrape DSL template
(v1alpha2 DSL per ADR-0035 with `${URL}` / `${URL_METADATA.<field>}`
placeholders the operator substitutes per child job),
batch-level scheduling (maxConcurrentJobs, perDomainRateLimit
delegating to rate-limit-broker per ADR-0036 §3.1,
retryPolicy with backoff schedule, completionPolicy:
OnAllProcessed | OnSuccessThreshold | OnDeadline), and
optional metadata propagated to per-URL records:

```yaml
apiVersion: spectre.io/v1alpha2
kind: ScrapeBatch
metadata:
  name: nightly-product-catalog
  namespace: tenant-a
spec:
  inputSource:
    sitemap:
      url: "https://example.com/sitemap.xml"
      includeFilters: ["/products/*"]
  scrapeTemplate:
    engineRef: { service: { name: spectre-engine } }
    dsl:
      navigate: { url: "${URL}" }
      extract:
        schema: { ref: spectre.io/products/v2 }
        selectors:
          name:  { css: "h1.product-title" }
          price: { css: ".price", transform: parsePrice }
    outputSink: { kafka: { topic: "tenant-a.products.raw" } }
  scheduling:
    maxConcurrentJobs: 50
    perDomainRateLimit: { maxRequestsPerMinute: 60 }
    retryPolicy: { maxAttempts: 3, backoffSeconds: [60, 300, 1800] }
    completionPolicy: OnAllProcessed
  metadata:
    tenant: tenant-a
    priority: normal
```

The full per-source variants of `inputSource` are detailed
in §5; the example above shows `sitemap` for brevity.

### §3.2 — Status surface

```yaml
status:
  phase: Running   # Pending | Running | Succeeded | Failed | Paused
  inputSourceStatus:
    discovered: 142347         # total URLs ingested by input-broker
    inflight: 47               # currently in a ScrapeJob
    succeeded: 132890
    failed: 1284
    requeued: 8126
    pending: 0
  observedGeneration: 1
  conditions:
    - type: InputSourceReady
      status: "True"
    - type: BatchProgressing
      status: "True"
      lastTransitionTime: "2026-05-05T03:14:00Z"
    - type: AllProcessed
      status: "False"
  lastJobCreatedAt: "2026-05-05T04:22:11Z"
  lastJobCreatedName: "nightly-product-catalog-job-7842"
```

The `inputSourceStatus` aggregate is **derived from
input-broker** — the operator queries the broker's
`Batch.GetProgress(batch_id)` RPC at reconciliation time.

### §3.3 — DSL template substitution

Per-URL DSL substitution uses **explicit placeholder
syntax** rather than free-form templating:

- **`${URL}`** — substituted with the URL the input-broker
  produces
- **`${URL_METADATA.<field>}`** — substituted with the
  per-URL metadata field; e.g.,
  `${URL_METADATA.priority}` substitutes with the URL's
  `priority` metadata field

The operator performs substitution at child-job-spawn time
before submitting the resulting ScrapeJob. Substitution is
**string-level** — no expression evaluation, no
templating-language semantics; this is deliberate to keep
the substitution surface narrow and reviewable.

ADR-0035 (this PR's Cluster C) defines the v1alpha2 DSL
that the `dsl:` block follows. The placeholder set is part
of the ScrapeBatch CRD contract, not the DSL.

### §3.4 — CRD versioning

`ScrapeBatch` lands at `v1alpha2` matching the existing
`ScrapeJob`'s API version per ADR-0019. The two CRDs share
the same API group and the same upgrade lifecycle; a
hypothetical `v1` API version evolution applies to both
CRDs simultaneously per the existing ADR-0019 +
[ADR-0030 §8](0030-helm-chart-structure.md) CRD upgrade
shape.

## §4 — input-broker service contract

### §4.1 — Per-URL lifecycle states

Each URL in a batch traverses a state machine:

```
        ┌────────────┐
        │   seen     │  ← ingested by input source; deduped against prior batches
        └─────┬──────┘
              │ enqueueable (rate-limit + concurrency permit)
              ▼
        ┌────────────┐
        │  queued    │  ← awaiting claim by operator-spawned job
        └─────┬──────┘
              │ claimed
              ▼
        ┌────────────┐
        │ in-flight  │  ← child ScrapeJob is processing
        └─────┬──────┘
              │
       ┌──────┴──────┐
       ▼             ▼
┌────────────┐  ┌────────────┐
│ succeeded  │  │   failed   │  ← retry per ScrapeBatch.spec.scheduling.retryPolicy
└────────────┘  └─────┬──────┘
                      │ retry budget remaining
                      ▼
                ┌────────────┐
                │  re-queued │  ← back to "queued" with attempt counter incremented
                └────────────┘
```

The state machine is **per-URL**; failures of one URL do
not affect siblings. The state transitions are recorded as
events at the input-broker for audit observability per
[ADR-0031](0031-observability-framework.md).

### §4.2 — RPC surface (indicative; build PR settles)

```proto
service Input {
  // Batch lifecycle
  rpc CreateBatch(CreateBatchRequest) returns (Batch);
  rpc GetBatch(GetBatchRequest) returns (Batch);
  rpc GetProgress(GetProgressRequest) returns (BatchProgress);
  rpc PauseBatch(PauseBatchRequest) returns (Empty);
  rpc ResumeBatch(ResumeBatchRequest) returns (Empty);
  rpc CancelBatch(CancelBatchRequest) returns (Empty);

  // URL lifecycle
  rpc AppendUrls(AppendUrlsRequest) returns (AppendUrlsResponse);
  rpc ClaimUrl(ClaimUrlRequest) returns (Url);
  rpc CompleteUrl(CompleteUrlRequest) returns (Empty);
  rpc FailUrl(FailUrlRequest) returns (Empty);

  // Source ingester registration (Wave 8+ — not in Wave 6 build PR)
  rpc RegisterSource(RegisterSourceRequest) returns (Source);
}
```

The RPC surface follows
[ADR-0028 §3.1](0028-ancillary-infra-services-catalog.md)'s
canonical proto-package convention (`spectre.input.v1alpha1`)
and ADR-0027's per-language SDK admission gate. SDK packages
land per ADR-0027 §3.1 when their first consumer materialises;
the operator (Go) is the first consumer in Wave 6.

### §4.3 — Per-URL metadata

The `Url` message carries:

- **`url`** (string) — the URL itself
- **`batch_id`** (string) — owning batch
- **`source`** (enum) — which input source produced it
  (`SITEMAP`, `FILE`, `API_PUSH`, `QUEUE`, `SEEDED_CRAWL`)
- **`metadata`** (map<string, string>) — per-source variable
  metadata (`query+page` for search-result URLs;
  `lastmod+priority` for sitemap URLs; arbitrary payloads
  for API-pushed URLs)
- **`priority`** (enum) — `HIGH | NORMAL | LOW`
- **`attempt_count`** (int32) — how many ScrapeJobs have
  attempted this URL
- **`last_error`** (string, optional) — the most recent
  failure code if attempt_count > 0
- **`enqueued_at`** / **`first_seen_at`** / **`last_attempt_at`**
  (timestamps) — observability

The metadata map is **flexible by design** — different
input sources produce different metadata shapes; the document
model from Mongo (per §7) accommodates the diversity
without a schema migration.

### §4.4 — Claim semantics

Claim is the hot path: each spawned child ScrapeJob calls
`ClaimUrl(batch_id)` to receive the next URL to process.
Claim semantics:

- **Exactly-once semantics within a batch** — a URL is
  claimed by at most one ScrapeJob at a time; concurrent
  claims for the same URL fail with `ALREADY_CLAIMED`.
- **Lease-based** — claims have a TTL (default 30 minutes,
  configurable); if the claiming ScrapeJob doesn't
  `CompleteUrl` or `FailUrl` within the TTL, the URL
  returns to `queued` automatically.
- **Priority-ordered** — `HIGH` priority URLs are claimed
  first; ties broken by `enqueued_at` ascending (FIFO).
- **Rate-limit-aware** — the input-broker calls
  `rate-limit-broker.Reserve` (per ADR-0036 §3.4) before
  serving a claim; if the per-domain budget is exhausted,
  claim fails with `RATE_LIMITED` and the operator backs
  off.

The Mongo `findAndModify` operation (per ADR-0039 §3.12)
implements the claim atomically. ADR-0039 anti-pattern §4.3
explicitly notes input-broker as the documented exception
to "Mongo as queue broker" caution — schema flexibility
across input sources is more valuable than Postgres
SKIP LOCKED purity at scraping volumes (millions of URLs
per batch, not billions).

## §5 — Input source variety

Five input source types are supported in v1alpha2.

### §5.1 — Sitemap

The input-broker's sitemap ingester fetches the URL the
ScrapeBatch's `inputSource.sitemap.url` references, parses
the XML (sitemap protocol; sitemap-index protocol),
applies include / exclude filter regexes, ingests the
resulting URLs as `seen → queued`. Recursive sitemap
indexes resolve to a maximum depth of 3 to prevent runaway
expansion.

### §5.2 — File

The input-broker's file ingester reads URLs from a
ConfigMap the ScrapeBatch references. Supported formats:
JSONL (one JSON object per line; each must have a `url`
field), TXT (one URL per line), CSV (URL in a configurable
column).

The ConfigMap-mount path is **deliberate** — it integrates
with Kubernetes-native secret / config patterns and makes
the URL list reproducible (the ConfigMap's resourceVersion
is the source-of-truth identifier for a given batch
ingestion).

### §5.3 — API push

The input-broker's API-push ingester polls a user-provided
HTTP endpoint at the configured interval; each poll
returns a list of URLs to enqueue. The endpoint contract is
small (POST returns JSONL of URLs); the user implements it
per their own data-source integration.

The polling cadence is per-batch (`pollIntervalSeconds`);
backoff applies on poll failure with the same backoff
schedule as ScrapeJob retries.

### §5.4 — Queue

The input-broker's queue ingester subscribes to a Kafka
topic (per [ADR-0023 §3](0023-stateful-services-architecture.md))
and ingests URLs from each message. The consumer group is
per-batch (the ScrapeBatch's name plus a fixed prefix), so
parallel ScrapeBatches over the same topic each consume
independently with at-least-once semantics.

The Kafka tier is the existing ADR-0023 §3 Kafka tier; no
new Kafka cluster is provisioned. v1beta1 may extend to
non-Kafka queues (RabbitMQ, NATS, AWS SQS); the
abstraction layer in §6 below accommodates the extension.

### §5.5 — Seeded crawl

The input-broker's seeded-crawl ingester accepts a list of
seed URLs and a follow-link pattern; as ScrapeJobs complete
and emit links matching the pattern, those links are
enqueued back into the broker as new URLs (depth-tracked,
respecting `maxDepth`).

Seeded crawl is the **most computationally complex** input
source — it requires per-URL link extraction in the engine,
deduplication against prior URLs in the batch, and depth
tracking. v1alpha2 ships seeded crawl with a deliberately
simple regex-based follow pattern; advanced shapes (link
classification, semantic crawling, extraction-driven
crawling) defer to v1beta1.

### §5.6 — Source extension model

The five sources above are the **v1alpha2-built set**.
Extending to a new source type (e.g., RSS, Atom, GraphQL
introspection) is:

- A new variant in the `ScrapeBatch.spec.inputSource` oneof
- A new ingester implementation under
  `infra-services/input-broker/internal/ingesters/<source>/`
- A new variant in the `Url.source` enum
- A new entry in the canonical `RegisterSource` RPC

No ADR amendment required for adding sources within the
existing extension model; reviewers approve based on the
ingester following the established shape.

## §6 — Batch progress tracking

The input-broker's `GetProgress(batch_id)` RPC returns:

```proto
message BatchProgress {
  string batch_id = 1;
  int64 discovered_total = 2;
  int64 in_seen = 3;
  int64 in_queued = 4;
  int64 in_flight = 5;
  int64 succeeded = 6;
  int64 failed_terminal = 7;     // exhausted retry budget
  int64 requeued = 8;
  int64 pending = 9;             // discovered but not yet eligible
  google.protobuf.Timestamp last_state_change = 10;
  // Per-domain breakdown for observability
  repeated PerDomainCounters per_domain = 11;
  // Per-status-code breakdown for failure analysis
  repeated PerErrorCodeCounters per_error_code = 12;
}
```

The progress aggregate is **computed at the broker** —
the operator does not aggregate by walking child ScrapeJob
statuses. The per-error-code breakdown surfaces ADR-0009's
`DriverError.Code` taxonomy per ADR-0031 §6.

The operator reconciles ScrapeBatch periodically (default
30 seconds; configurable per `ScrapeBatch.spec.scheduling`)
and updates `status.inputSourceStatus` from the broker's
progress response. Reconciliation **does not block on
broker availability** — when the broker is unavailable,
the operator surfaces `BatchProgressing=Unknown` and
continues to attempt updates with backoff per ADR-0037 §5.3.

## §7 — Backend choice

The input-broker uses **MongoDB as the primary backend**
per [ADR-0039 §3.12](0039-mongodb-third-storage-tier.md).
The choice is rigorously justified there; the summary:

- **Heterogeneous URL metadata** across input sources is
  document-natural — search-result harvest URLs carry
  `query+page`; sitemap URLs carry `lastmod+priority`;
  API-pushed URLs carry arbitrary payloads. Document model
  fits diverse shapes naturally.
- **`findAndModify` claim semantics** at scraping volumes
  (millions, not billions) work reliably. Postgres
  SKIP LOCKED is the gold standard at higher volumes;
  v1beta1+ revisits if scraping volume crosses that
  threshold.
- **Change streams** enable real-time `BatchProgress`
  updates without polling.

ADR-0039 §4.3 explicitly notes input-broker as the
documented exception to anti-pattern §4.3 ("Mongo as
generic queue broker" caution). The exception is recorded;
other queue-shaped services emerging in v1beta1+ revisit
Postgres SKIP LOCKED first.

Per-collection structure:

- `batches` — one document per ScrapeBatch with
  configuration + lifecycle metadata
- `urls` — one document per URL across all batches with
  per-URL state + metadata; indexed by `(batch_id, state,
  priority, enqueued_at)` for the claim hot path

Index strategy follows ADR-0039 §4.6 (anti-pattern: "Mongo
without indexing strategy") — the build PR includes
`explain('executionStats')` analysis for the claim and
progress queries.

## §8 — Operator integration

The control-plane operator (per ADR-0019) reconciles both
ScrapeJob and ScrapeBatch CRDs. The reconcilers share the
operator binary but run independently per kubebuilder's
multi-controller pattern.

### §8.1 — ScrapeBatch reconciler responsibilities

1. **Spec validation** — validate the ScrapeBatch spec at
   admission (per the operator's existing webhook validation
   from ADR-0019 §4); reject malformed input source
   configurations.
2. **Input source registration** — on first reconciliation
   of a new ScrapeBatch, register the input source with
   the input-broker via `CreateBatch` + `RegisterSource`
   (when applicable).
3. **Child job spawning** — at each reconcile, claim up to
   `(maxConcurrentJobs - inFlightCount)` URLs from the
   broker and spawn a child ScrapeJob for each, with
   substituted DSL per §3.3.
4. **Status update** — query
   `input-broker.GetProgress(batch_id)` and update
   `status.inputSourceStatus`.
5. **Completion detection** — when the batch's
   `completionPolicy` is satisfied, set `status.phase` to
   `Succeeded` / `Failed`; stop spawning child jobs.

### §8.2 — Child ScrapeJob ownership

Child ScrapeJobs are owned by their parent ScrapeBatch via
Kubernetes `ownerReferences`; deletion of the parent
cascades. Child ScrapeJob status feeds back into the
broker via the engine-side `CompleteUrl` / `FailUrl` calls
at job completion.

The child ScrapeJob shape is **unchanged** from
ADR-0019 — the same CRD schema, the same reconciler. The
only difference is `metadata.ownerReferences` linking back
to the parent ScrapeBatch.

### §8.3 — Concurrency control

The reconciler enforces `spec.scheduling.maxConcurrentJobs`
by counting child ScrapeJobs in non-terminal phases
(Pending or Running). When the limit is reached, the
reconciler stops spawning new jobs until existing ones
complete. The next reconciliation cycle (default 30s)
re-evaluates.

`spec.scheduling.perDomainRateLimit` delegates to the
`rate-limit-broker` service (slot 4 per
[ADR-0036 §3.1](0036-microservices-catalog-expansion.md));
the input-broker's claim semantics already incorporate this
(§4.4) — the operator does not re-implement.

## §9 — Migration sequence

R9.4's ADR-0033 is documentation-only. The materialisation:

| Wave | Scope |
|---|---|
| Wave 6 (build PR) | input-broker service materialised + ScrapeBatch CRD added at `spectre.io/v1alpha2` + operator gains ScrapeBatch reconciler + chart fragment for input-broker per ADR-0036 §5.2 + Mongo subchart per ADR-0023 §14.1. The Wave 6 PR sequence pairs with schema-registry per ADR-0034 (the two services land together). |
| Wave 8+ | Source extension model (§5.6) materialises further sources (RSS / Atom / GraphQL) per consumer demand. |
| Wave 9 | Per-batch cost attribution surfaces in the cost-tracker (per ADR-0038) — `BatchProgress` includes cost rollups when ADR-0038 lands. |
| Wave 10 | Driver-router integration — when ADR-0035 settles the driver-router decision, ScrapeBatch's per-URL claim path may consult the router for capability-aware claim ordering. |
| v1beta1 | Advanced source shapes (link classification; semantic crawling; extraction-driven crawling) per §5.5's deferral. |

The Wave 6 build PR is **transformational scope** under the
v1alpha2 process rigor matrix
([CONTRIBUTING.md](../../CONTRIBUTING.md), R9.0). The PR
bundles input-broker materialisation + CRD addition +
operator extension + chart fragment + Mongo subchart — but
each cluster within the PR follows the canonical service
shape per ADR-0036.

## §10 — Confirmation (acceptance criteria)

The subsystem is working when the following hold **by the
close of Wave 6**:

- **A `ScrapeBatch` CR with 100 URLs** lands `succeeded` in
  the production-smoke gate (R7.2 extended for Wave 6) —
  100 child ScrapeJobs spawn over time, each completes,
  the parent's `status.inputSourceStatus.succeeded`
  reaches 100.
- **Per-URL retry semantics work** — a transient failure
  (deliberately injected in the smoke fixture) re-queues
  the URL; the next attempt succeeds; the parent batch
  reaches `Succeeded` overall.
- **Per-domain rate limiting honours the broker's
  delegation** — a smoke fixture targeting a single domain
  with `perDomainRateLimit.maxRequestsPerMinute: 60`
  produces no more than 60 child ScrapeJobs per minute.
- **The five v1alpha2 input source types each have a
  passing smoke fixture** — sitemap; file; API push;
  queue; seeded crawl. Each fixture is a separate test
  case in the production-smoke gate.
- **`BatchProgress` updates surface in the operator's CRD
  status** within 30 seconds (one reconciliation cycle) of
  state changes at the broker.
- **Cross-references resolve** — ADR-0034 / ADR-0036 /
  ADR-0037 / ADR-0039 cite ADR-0033 where relevant; this
  ADR cites them where relevant.

A signal that the subsystem needs revision: more than one
Wave 6+ tenant pilot (Wave 4 user pilot) reports a real
input-management workflow not covered by the five sources
+ extension model. That's evidence the source set is
incomplete; the response is an ADR amendment that adds the
missing source per §5.6's extension model, or a successor
ADR if the missing capability is structural rather than
just a new variant.

## §11 — What's deferred / out of scope

R9.4 declines these deliberately. Each is a real concern;
each belongs to a later phase or to a sibling ADR.

- **Bulk URL deduplication across batches.** v1alpha2's
  `dedup-service` (slot 11 per ADR-0036 §3.4) deduplicates
  **output rows**, not input URLs. Cross-batch URL dedup
  (the same URL appearing in multiple batches; deduping
  to scrape it once) is a v1beta1 concern.
- **Distributed input-broker.** v1alpha2's input-broker is
  a single deployment per cluster; multi-region
  deployments with broker replication are v1beta1.
- **Per-source ingester scheduling.** Source ingesters
  (sitemap, file, API push, queue, seeded crawl) all share
  the input-broker process at v1alpha2; per-source scaling
  via separate ingester deployments is a v1beta1
  optimisation.
- **Custom source ingesters.** The five built-in source
  types are the v1alpha2 set. User-defined ingesters
  (plugin model, source SDK) are v1beta1.
- **Streaming ScrapeBatch results.** Per-URL completions
  surface in `status.inputSourceStatus` aggregates;
  per-URL streaming via watch on a separate CRD or via
  webhooks is v1beta1.
- **Webhook-driven batch creation.** External systems
  triggering batches via the operator's API webhook (vs
  applying CRs via kubectl) are v1beta1 — depends on the
  operator's external API surface.
- **CRD evolution v1alpha2 → v1.** ScrapeBatch + ScrapeJob
  evolve together at API version transitions per ADR-0019;
  the v1alpha2 → v1 path is its own future ADR.
- **Per-tenant input-broker isolation.** Multi-tenant
  deployments may need per-tenant collection isolation
  in Mongo; v1beta1 concern.
- **Backfill orchestration.** Re-scraping existing batches
  after schema evolution (per ADR-0034) — the operator
  may grow a `BackfillBatch` CR that references a prior
  batch and re-scrapes a subset; v1beta1 concern.

## §12 — Reference materials

- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane and ScrapeJob CRD; ScrapeBatch lands at
  the same API version per ADR-0019's CRD-scoping pattern;
  the operator gains a parallel reconciler.
- [ADR-0023](0023-stateful-services-architecture.md) —
  stateful services; the Kafka tier (§3) carries forward
  for the queue input source. ADR-0023 §14 (R9.2) added
  Mongo as the third storage tier — input-broker's
  primary backend.
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy;
  per-language input-broker SDKs follow the admission
  gate.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) —
  the original five-slot catalog; input-broker materialises
  outside ADR-0028's named set, in the ADR-0036 expansion.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart;
  CRD upgrade lifecycle for ScrapeBatch follows ADR-0030
  §8's existing pattern (alongside ScrapeJob).
- [ADR-0036](0036-microservices-catalog-expansion.md) —
  the 15-service catalog; `input-broker` is slot 12.
- [ADR-0037](0037-engine-as-orchestrator.md) — engine as
  orchestrator; the engine consumes input-broker via
  `NextURL` per §3.2's orchestration sequence.
- [ADR-0039](0039-mongodb-third-storage-tier.md) —
  MongoDB tier; §3.12 evaluates input-broker's backend
  with the documented anti-pattern §4.3 exception.
- ADR-0034 (this PR's Cluster B) — output schema and
  validation framework. Schema substitution in
  `scrapeTemplate.dsl` follows ADR-0034's schema-declaration
  syntax.
- ADR-0035 (this PR's Cluster C) — DSL evolution. The
  v1alpha2 DSL ScrapeBatch's `scrapeTemplate.dsl` follows
  is ADR-0035's commitment.
- ADR-0038 (this PR's Cluster D) — cost tracking. Per-batch
  cost rollups at Wave 9 surface in the broker's
  `BatchProgress`.
- Kubernetes CRD versioning:
  <https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/>
- Sitemap protocol: <https://www.sitemaps.org/protocol.html>
