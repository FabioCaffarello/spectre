---
status: accepted
date: 2026-04-30
deciders: [Fabio Caffarello]
---

# Data platform and lake DSLs

## §1 — Context and Problem Statement

ADR-0026 §3.7 reserves `data-platform/` as the home for
parsing, transformation, and aggregation modules that consume
the engine's output and produce downstream data products.
ADR-0026 deliberately defers the *internals* of that category
to this ADR: the lake-layer model, the stage taxonomy under
`data-platform/`, the relationship to the engine job DSL
(ADR-0012), and the governance criteria for when a new DSL is
warranted versus extending an existing one.

Today's engine landscape stops at the sink boundary. A
`ScrapeJob` reconciles into an engine `RunJob`; the engine
plans the job, executes it through an adapter, and writes
extracted records to one of four sinks (Stdout, Kafka, S3,
Webhook) per ADR-0024. The records the engine produces are
already structured (per-job schemas via the engine DSL's
`extract: { fields: ... }` block, ADR-0012 §1) but not yet
*conformed*: schemas vary per-job, timestamps are whatever the
source page provides, currencies are textual, deduplication
across runs has not happened, and aggregations across jobs
don't exist.

The platform vision extends past this stopping point. A
production deployment of Spectre will need:

- **Bytes-level parsing** for sources where the engine's
  in-browser extraction is insufficient: PDFs, XLSX exports,
  large JSON dumps, multi-page HTML archives where in-session
  extraction doesn't fit. The engine cannot parse a PDF; that's
  not what browser automation is for.
- **Schema conformance** across jobs: the same logical entity
  (a product, a listing, a user) extracted by different jobs
  with different per-job schemas needs to land in one
  conformed schema before downstream consumers can join across
  sources.
- **Cleaning, deduplication, joining**: the operations a data
  engineer recognises as "silver-layer work" — normalising
  timestamps to UTC, currencies to a canonical denomination,
  free-text fields into enums, deduplicating by content hash,
  joining facts to dimensions.
- **Aggregation and serving**: business-aggregate views
  (counts by category, time-windowed metrics, top-N reports)
  optimised for query engines (DuckDB, Trino, ClickHouse) or
  BI tools.

Without a designated home and a layer model, each of these
concerns ends up wherever the first PR places it. The engine
absorbs parsing logic that doesn't belong inside an
orchestrator. A one-off Python script under `tools/` becomes
the de facto silver-layer pipeline. The first aggregation lives
in a notebook attached to nothing in version control.

The user's prior framing matched this analysis: *"a DSL que
temos seria usada para construir a primeira camada de entrada
do dado do lake e podemos ter DSLs diferentes para as demais
camadas do lake."* This ADR commits to that framing concretely:
the engine job DSL (ADR-0012) is the L0 entry DSL; subsequent
layers may have their own DSLs, gated by the governance
criteria in §6.

ADR-0029 does not land any data-platform code. R6.6's
restructure PR (ADR-0026 §4) materialises `data-platform/` as
a placeholder directory with stage subdirectories
(`parse/`, `transform/`, `aggregate/`) and per-stage
`README.md` files pointing at this ADR. First inhabitants land
in R7.x or later phases under the §7 admission criteria.

## §2 — Decision summary

R6.6 commits to:

- **A four-layer lake model** following the medallion idiom:
  L0 / **raw**, L1 / **bronze**, L2 / **silver**, L3 /
  **gold**. An optional L4 / **platinum** is mentioned for
  ML-feature use cases but not normatively reserved by this
  ADR.
- **Three stage transitions** between layers:
  - L0 → L1: **parse** (bytes → conformed records)
  - L1 → L2: **transform** (conformed records → cleaned tables)
  - L2 → L3: **aggregate** (cleaned tables → business views)
  Each stage has a dedicated subdirectory under
  `data-platform/`.
- **Up to three layer-transition DSLs**, one per stage:
  parse-DSL, transform-DSL, aggregate-DSL. Each is gated by
  the §6 governance criteria; no DSL is materialised in R6.6.
  The engine job DSL (ADR-0012) is preserved as the L0 entry
  DSL — the transition into the lake from the world — and is
  not amended by this ADR.
- **Stage taxonomy under `data-platform/<stage>/`.** Each
  stage subdirectory hosts (a) at most one DSL package per
  language, (b) zero-or-more implementation modules
  (libraries or services), (c) a per-stage README. The
  internal layout within a stage is decided per build PR;
  this ADR fixes only the stage boundary.
- **DSL governance criteria** for when a new DSL is warranted
  vs. extending an existing one. The criteria are normative
  and apply equally to the existing engine DSL (ADR-0012) and
  to future stage DSLs.
- **Strict layering.** A stage may consume layers above it
  (a transform reads bronze) but may not produce layers below
  it. Stage skipping (raw → silver, bronze → gold) is allowed
  when a stage's cost is unjustified for the use case;
  modelling that explicitly is preferable to silent
  hop-skipping.

## §3 — The lake-layer model

Four canonical layers; one optional. Each layer is defined by
*data quality and conformance*, not by storage technology or by
producer identity. The model is normative — a future PR adding
a data-platform module declares which layer it produces.

### §3.1 — L0 / raw

**Definition.** Engine output, byte-faithful. The records (or
files) the engine emitted via its sinks, with no processing
beyond what the engine itself did. Schema is per-job and
heterogeneous; lineage points back to a `RunJob` ID.

**Producer.** The engine (per ADR-0012 + ADR-0024). The L0
layer is *populated* by the engine writing to sinks — S3 for
object-shaped output, Kafka for stream-shaped output, Postgres
for relational output, Webhook for push integrations.

**Consumer surface.** Whatever sink the engine wrote to. L0
consumers read sink-native (e.g., S3 prefix scans, Kafka topic
consumption); they do not read through a data-platform module.

**Governance.** L0 is the boundary of the lake. The engine job
DSL (ADR-0012) is the *entry DSL* for L0 — it controls what
records land. The lake model does not amend ADR-0012's
contract.

### §3.2 — L1 / bronze

**Definition.** Conformed records. Per-job schemas have been
mapped onto a canonical entity schema; each record carries
typed fields, lineage metadata (source job ID, source URL,
ingestion timestamp), and a content hash for deduplication.
File-format parsing (PDFs, XLSXes, archived HTML pages
extracted from the L0 layer's blob storage) lands here.

**Producer.** `data-platform/parse/` modules.

**Consumer surface.** Stable, queryable. Parquet on S3, an
Iceberg / Delta table, a Postgres schema — the build PR for
each inhabitant chooses. Bronze tables are the first
queryable surface after L0; analysts can read bronze for raw
inspection.

**Governance.** Bronze schemas are *conformed within a domain*
but not necessarily across all domains. Two parse modules for
"product listings from different vendor sites" produce bronze
records that share a common product schema; a parse module for
"news articles" produces a different bronze schema. ADR-0029
does not commit to a single canonical bronze schema across all
domains — that's per-domain modelling work.

### §3.3 — L2 / silver

**Definition.** Cleaned, deduplicated, joined. Currencies
normalised to a canonical denomination (often USD or per-tenant
preference); timestamps in UTC ISO-8601; free-text fields
mapped to enums or canonical entity references; duplicates
collapsed (by content hash, by business key); facts joined to
dimensions where the dimensions exist in silver. The data
engineer's "ready for analysis" layer.

**Producer.** `data-platform/transform/` modules.

**Consumer surface.** Same storage substrate options as bronze
(Parquet / Iceberg / Postgres). Silver is the canonical layer
for cross-source analytical work.

**Governance.** Silver schemas SHOULD be canonical across the
platform — the same logical entity (Product, User, Listing) has
one silver schema regardless of source. Bridge tables between
sources land here. Slowly-changing dimensions (Type 2 history,
effective-from/effective-to) live here.

### §3.4 — L3 / gold

**Definition.** Business-aggregate views, query-optimised.
Pre-computed aggregations (counts, sums, time-windowed
metrics), star-schema fact-and-dimension tables, denormalised
views for BI tools, top-N tables for serving layers. The data
engineer's "ready for the dashboard" layer.

**Producer.** `data-platform/aggregate/` modules.

**Consumer surface.** Query engine endpoints (DuckDB, Trino,
ClickHouse), BI tool data sources, API services that expose
gold views to product surfaces.

**Governance.** Gold schemas are *use-case-specific* — designed
for a particular dashboard, report, or product feature. Two
gold tables answering different questions don't have to
reconcile their dimensions; they each derive from silver.

### §3.5 — L4 / platinum (optional, deferred)

Some teams add a fifth layer for ML feature stores or
human-curated executive summaries. ADR-0029 mentions L4 to
record the convention but does not normatively reserve a
stage subdirectory for it. If platinum becomes a real need, a
successor ADR adds `data-platform/feature/` (or whatever name
fits) and amends §2.

## §4 — Stage taxonomy under `data-platform/`

Three stage subdirectories. Each stage owns the L_n → L_{n+1}
transition.

### §4.1 — `data-platform/parse/` (L0 → L1)

**Purpose.** Extract structured records from raw bytes the
engine produced. Run as batch jobs (against S3 prefixes), as
streaming consumers (against Kafka topics), or as on-demand
RPCs invoked by the engine itself when a single-page parse is
needed mid-job.

**Scope of work.**
- File-format parsers (HTML archive, JSON, JSON-Lines, CSV,
  XLSX, PDF, XML, RSS, plain text).
- Schema mapping (per-job-schema → bronze-schema).
- Deduplication (content hash; per-source ID).
- Lineage stamping (source job ID, source URL,
  ingestion timestamp, parser version).
- Validation against the bronze schema (rejected records routed
  to a quarantine table).

**Internal layout.** Per build PR; the stage boundary is
fixed but the per-stage internals are not. Plausible shapes:

```
data-platform/parse/
├── README.md
├── dsl/                    (parse-DSL, when admitted per §6)
│   └── <lang>/v1alpha1/
├── lib/                    (per-format parsers as libraries)
│   ├── html/
│   ├── pdf/
│   ├── xlsx/
│   └── json/
└── service/                (parse service binary, when admitted)
    └── <name>/
```

**Languages.** Mixed. Performance-critical parsers (PDF, XLSX)
are good fits for Rust. Schema-mapping logic is a good fit for
Python (rich library ecosystem) or Rust (type discipline).

### §4.2 — `data-platform/transform/` (L1 → L2)

**Purpose.** Clean, conform, deduplicate, and join bronze
records into silver tables. Most data-engineering practice for
this layer falls into the dbt / Spark SQL / Trino mould; the
DSL question (§6) is whether Spectre adopts an existing
DSL/runtime or builds its own.

**Scope of work.**
- Type coercions (string → typed timestamp, string → typed
  decimal currency).
- Enum normalisation (free-text categories → canonical enum).
- Deduplication across sources (same logical entity from
  different parsers).
- Joins facts to dimensions; resolves entity references.
- Slowly-changing dimensions (Type 2 history).
- Quality assertions (silver-level data contracts).

**Internal layout.** Per build PR. Plausible shape mirrors
parse/.

**Languages.** Transform work is typically a SQL-like discipline.
The `dsl/` subdirectory may host a SQL-shaped transform DSL or
a more imperative Python/Rust transform layer; the choice is
per-build-PR-decision under §6.

### §4.3 — `data-platform/aggregate/` (L2 → L3)

**Purpose.** Pre-compute aggregations, window functions, top-N
tables, denormalised views from silver tables. Optimised for
query latency; refresh cadence (batch nightly, streaming,
on-demand) per use case.

**Scope of work.**
- Aggregations (count, sum, avg, percentile) by dimensions.
- Time-windowed metrics (rolling 7-day, week-over-week).
- Star-schema views from silver fact tables.
- Top-N tables.
- API-layer-shaped views (denormalised, single-table queries).

**Internal layout.** Per build PR. Plausible shape mirrors
parse/ and transform/.

**Languages.** Aggregations are typically SQL or query-engine
DSLs. The aggregate DSL (§6) may overlap heavily with the
transform DSL; the question of whether they merge into one DSL
is itself a §6 governance call.

## §5 — Relationship to the engine job DSL (ADR-0012)

The engine job DSL (ADR-0012) is preserved unchanged. This ADR
recognises it explicitly as the **L0 entry DSL** — the DSL
through which records enter the lake. ADR-0012's design
decisions (high-level over protocol, manual validation, JSONL
streaming output) carry forward without modification.

The engine job DSL is *not* a stage DSL. It does not transition
between lake layers; it transitions from "the world" to L0.
The asymmetry is honest:

| DSL                       | Boundary             | Layer transition  |
|---------------------------|----------------------|-------------------|
| Engine job DSL (ADR-0012) | World → lake         | (entry to L0)     |
| parse-DSL (ADR-0029, §6)  | L0 → L1              | within-lake       |
| transform-DSL (ADR-0029)  | L1 → L2              | within-lake       |
| aggregate-DSL (ADR-0029)  | L2 → L3              | within-lake       |

The engine DSL's RPC-level granularity (compile YAML to a
sequence of `Initialize → Navigate → Query → Extract → Close`)
is appropriate for "drive a browser to a page". The within-lake
DSLs are at a different abstraction level (operate on records
or tables, not on browser sessions) and the choice between
extending the engine DSL vs. introducing a new DSL is governed
by §6.

In practice: the engine DSL stays at ADR-0012's abstraction
level. The within-lake DSLs are governed below.

## §6 — DSL governance: when new vs. when to extend

A new DSL is *expensive* — a grammar to design, a parser to
build, a runtime to maintain, an idiom for users to learn. The
default answer is "extend an existing DSL". This section
specifies when the default flips.

### §6.1 — When to extend an existing DSL

Default. Extend when **all** of the following hold:

- **Same abstraction level.** The new feature is at the same
  conceptual altitude as the DSL's existing constructs. Adding
  a `take_screenshot:` step to the engine DSL is at the same
  level as `navigate:` — extension. Adding a join clause to the
  engine DSL is not — that's at table-level, not page-level.
- **Same runtime context.** The new feature runs in the same
  execution model. The engine DSL runs in-process per job;
  adding a "schedule cron" feature would push it into a
  different runtime — that's not extension.
- **Same consumer audience.** The DSL's users (developers
  writing scrape configs vs. analysts writing aggregations) are
  the same. Forcing analysts to learn the engine DSL's
  RPC-shaped semantics for an aggregation DSL would mismatch
  the audience.
- **Additive change.** The extension does not break existing
  DSL programs. Extensions that require migrating existing
  jobs are protocol-version-equivalent changes (per ADR-0004's
  versioning posture for the engine DSL); they may be the
  right call but cross the bar from "extend" to "new version".

### §6.2 — When to introduce a new DSL

A new DSL is warranted when **any** of the following holds:

- **Different abstraction level.** Records-and-tables level
  vs. browser-and-RPCs level vs. query-and-aggregations level.
  These are different problem domains; trying to express all in
  one DSL produces a baroque grammar.
- **Different runtime context.** Per-page execution
  (engine DSL) vs. batch over an S3 prefix (parse DSL) vs.
  scheduled SQL execution (transform/aggregate DSL).
- **Different consumer audience.** Developers writing scrape
  configs (engine DSL) vs. data engineers writing transforms
  (transform DSL) vs. analysts writing aggregations
  (aggregate DSL). Audiences diverge by abstraction expectations
  and by tooling fit.
- **Different evolution cadence.** A DSL that changes weekly
  (an analyst-facing aggregation DSL) shouldn't share a
  versioning lifecycle with one that changes quarterly (the
  engine DSL).

### §6.3 — When to merge two stage DSLs

The transform-DSL and aggregate-DSL may be the same DSL in
practice — both operate on relational records, both are SQL-
shaped, both serve the same data-engineering audience. ADR-0029
does *not* prescribe two distinct DSLs at the L1 → L2 → L3
boundaries; the build PR for the first within-lake DSL chooses:

- **One unified record-DSL** for L1 → L2 → L3 (one grammar,
  one runtime, stages distinguished by configuration). Lower
  cost; risk that aggregation-specific features (windowing,
  approximate aggregates) compromise the transform-side
  ergonomics.
- **Two separate DSLs** (transform-DSL distinct from
  aggregate-DSL). Higher cost; better fit per use case.

The choice is a build-PR decision under §7 admission criteria,
not a §6 governance answer per se.

### §6.4 — When to adopt an existing DSL vs. build one

Building a DSL from scratch is expensive. For each stage's
DSL, the build PR should consider whether an existing DSL fits
before designing a Spectre-specific one:

- **Transform / aggregate.** dbt SQL, Spark SQL, Trino SQL,
  DuckDB SQL, ClickHouse SQL are all candidates. A Spectre-
  specific DSL must justify why an existing SQL dialect
  doesn't fit (tight integration with the engine's job
  metadata, custom UDFs the engine produces, ...).
- **Parse.** Off-the-shelf parser combinator libraries
  (nom in Rust, pyparsing in Python) and per-format libraries
  (BeautifulSoup, lxml, pdfplumber, openpyxl) typically
  obviate a parse-DSL entirely; the parse stage may be
  library-shaped, not DSL-shaped. The build PR for a parse
  module declares which.

The default for L1+ stages is: **adopt existing tooling first;
introduce a Spectre-specific DSL only when adoption would
compromise integration or audience fit**.

## §7 — Admission criteria

Two layers: admitting a new *stage* under `data-platform/`
(amends this ADR's §4); admitting a new *module* within an
existing stage.

### §7.1 — Admitting a new stage

A new stage subdirectory under `data-platform/` requires:

1. **Distinct layer transition** not covered by `parse/`,
   `transform/`, or `aggregate/`. Adding `serve/` for L3 → API
   would be a candidate; adding `enrich/` between bronze and
   silver might be (or might fit transform).
2. **ADR amending §4** of this document, justifying the new
   stage and locating it in the lake-layer model (§3).
3. **At least one intended inhabitant** — speculative stages
   are rejected.

### §7.2 — Admitting a new module within an existing stage

| Stage          | Admission gate                                                                                                                                              |
|----------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `parse/`       | Format-specific parser: a known consumer producing the format that needs parsing. Library-shaped: no ADR. Service-shaped or DSL-shaped: per §7.3 below.     |
| `transform/`   | Domain-specific transform: at least one bronze schema in scope, at least one silver schema designed. Library-shaped: no ADR. DSL or service: per §7.3.       |
| `aggregate/`   | Use-case-specific aggregation: at least one consumer (dashboard, API, BI tool) needing the gold view. Library / SQL: no ADR. DSL or service: per §7.3.      |

### §7.3 — Admitting a stage DSL or stage service

A new stage DSL or stage service binary requires a build PR
that demonstrates:

1. **Concrete consumer.** At least one inhabitant of the same
   stage (or directly above) needs it. "We may want a DSL" is
   not sufficient.
2. **§6 governance check.** The build PR explicitly answers
   the §6 questions and shows why an existing DSL or library
   doesn't fit (or, for service-shaped modules, why a library
   doesn't fit).
3. **Versioning posture.** DSLs follow path-based versioning
   (per ADR-0004's analogue for protocols): `data-platform/<stage>/dsl/<lang>/v1alpha1/`.
   Services follow infra-services posture per ADR-0028 §3.
4. **Deployment posture.** Compose service entry per ADR-0025
   (for service-shaped modules); justfile recipe per existing
   conventions; CI integration per existing patterns.

### §7.4 — Cross-stage modules

A module that spans two stages (e.g., a parser that immediately
applies a transform) is rejected as a cross-stage module. The
correct shape is two modules — one per stage — with the
boundary materialised at the L_n schema. Cross-stage shortcuts
hide layer responsibilities and break the strict-layering
invariant (§2).

A module that *skips* a stage (e.g., raw → silver, bronze →
gold) is allowed but flagged. The build PR records the
rationale in the module's README. Skipping is sometimes
appropriate (the silver layer would be trivially equal to
bronze for a single-source domain; collapsing them is honest)
but should not be the default.

## §8 — Confirmation

The data-platform structure is working when:

- **A new data-platform PR** declares which layer it produces
  and which stage hosts it; reviewers verify the placement
  matches the §3 layer definitions and the §4 stage
  responsibilities.
- **DSL proposals** explicitly walk through the §6 governance
  questions before introducing a new DSL.
- **Cross-layer queries are coherent**: silver-layer consumers
  see one canonical entity schema regardless of source; gold
  views derive from silver, not from bronze (or document the
  exception in the build PR's README).
- **Library-shaped first**: parse/transform/aggregate modules
  default to libraries; service-shaped and DSL-shaped modules
  earn their existence by §7.3.

A signal that the model needs revision: more than one PR in a
phase ad-hoc skips the stage taxonomy (an "engine writes
directly to silver" PR, a "parse module produces gold" PR).
That's evidence either the layer model is wrong for the
domain, or the stage definitions need refinement; the response
is an ADR amendment, not a one-off bypass.

## §9 — What's deferred / out of scope

R6.6 declines these deliberately. Each is a real concern; each
belongs to a later phase or to a sibling concern.

- **Implementation of any stage.** This ADR is the model and
  the taxonomy. Per-stage build PRs are R7.x and later phase
  work, individually gated by §7.
- **Storage substrate per layer.** Whether bronze is Parquet
  on S3, Iceberg on S3, Delta on S3, Postgres tables, or a
  mix — per build PR. The model defines layer semantics, not
  storage.
- **Orchestration and scheduling.** Who triggers a parse job,
  on what cadence, with what dependency on engine jobs — out
  of scope for this ADR. A future operator (sibling to the
  control-plane operator per ADR-0026 §3.3) may host a
  `ParseJob` CRD or equivalent; the operator is not amended
  by this ADR.
- **Catalog of bronze/silver/gold schemas.** Schema design is
  per-domain modelling work, not platform-architecture work.
  This ADR does not commit to specific entity schemas.
- **Data quality framework.** Great Expectations / Soda /
  per-stage assertions — out of scope. The §3 governance
  notes that quality assertions live in silver; the framework
  choice is per build PR.
- **Lineage tracking system.** OpenLineage / Atlas / per-job
  lineage metadata — out of scope. Bronze lineage stamps
  (source job ID, source URL, ingestion timestamp) are the
  L1 conformance baseline; broader lineage tooling is later.
- **Per-tenant isolation in the data platform.** Multi-tenant
  data isolation (per-tenant prefixes, per-tenant tables,
  cross-tenant join controls) is per-deployment work; this
  ADR doesn't constrain tenant model.
- **Streaming vs. batch execution model.** A parse module may
  be batch (S3 prefix scan), streaming (Kafka consumer), or
  on-demand (RPC invoked by engine). The model is per build
  PR; ADR-0029 doesn't pick one execution shape.
- **The L4 / platinum layer.** Mentioned in §3.5 for
  completeness; not normatively reserved. A successor ADR
  adds the stage when needed.
- **Adoption of an external catalog/governance tool**
  (DataHub, Atlas, Marquez). Out of scope.
- **Engine DSL extensions.** The engine DSL is governed by
  ADR-0012. Extensions to it are governed by §6.1's "extend"
  criteria but are out of scope for this ADR — they belong in
  ADR-0012 evolution.

## §10 — Reference materials

- [ADR-0012](0012-engine-dsl-and-execution-pipeline.md) —
  Engine DSL and execution pipeline. The L0 entry DSL.
  Preserved unchanged by this ADR.
- [ADR-0024](0024-output-sinks.md) — Output sinks. The L0
  layer is populated by sinks per this ADR.
- [ADR-0026](0026-platform-taxonomy.md) — Platform taxonomy.
  This ADR fills the `data-platform/` cell (§3.7 of ADR-0026).
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy. Future
  data-platform service modules (per §4 service shapes) follow
  the same SDK conventions for protocol consumption.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) —
  Ancillary infra services. Data-platform modules may consume
  infra-services (a parser calling captcha-solver to decode a
  CAPTCHA-protected page); the cross-category dependency is
  allowed by ADR-0026 §5.
- Medallion architecture overview:
  <https://www.databricks.com/glossary/medallion-architecture>
- dbt: <https://docs.getdbt.com/>
- Apache Iceberg: <https://iceberg.apache.org/>
- DuckDB: <https://duckdb.org/>
- Trino: <https://trino.io/>
