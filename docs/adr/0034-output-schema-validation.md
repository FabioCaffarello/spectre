---
status: accepted
date: 2026-05-06
deciders: [Fabio Caffarello]
---

# Output schema and validation framework

## §1 — Context and Problem Statement

The v1alpha1 platform's output is **untyped JSONL** — the
engine emits rows as JSON objects with whatever fields the
DSL's extraction selectors happen to produce; sinks
(stdout / Kafka / S3 / Webhook per
[ADR-0024](0024-output-sinks.md)) ship the rows downstream
verbatim. Downstream consumers receive bytes; they parse
heuristically; they break when extraction selectors evolve.

The shape was defensible during the refactor — establishing
the wire-level extraction primitive (Driver Protocol +
DSL → engine → sink chain) without conflating it with
type-level validation. The shape is **not defensible** in
v1alpha2:

- **Heterogeneous extraction outputs** without schemas force
  every downstream consumer to invent its own validation.
  The marketing-analytics tenant's products differ from the
  real-estate-pricing tenant's listings differ from the
  social-media tenant's posts; without typed declarations,
  cross-tenant tooling cannot share validation logic.
- **Schema evolution is implicit** today — when an
  extraction selector changes (a new field added; a field's
  type changes from string to decimal), downstream consumers
  silently break. There is no contract, no compatibility
  check, no version surface.
- **Operator validation is impossible** — the operator
  cannot reject a malformed `ScrapeJob.spec.dsl` at
  admission because there is no schema to validate against.
- **The audit-log service** (slot 8 per
  [ADR-0036 §3.3](0036-microservices-catalog-expansion.md))
  records per-job decisions; without typed schemas,
  audit records cannot prove "this row matched the
  declared output shape" — only "this row was emitted".

This ADR commits the v1alpha2 platform to:

- A **`schema-registry` service** (slot 9 per
  [ADR-0036 §3.4](0036-microservices-catalog-expansion.md))
  that owns versioned output schemas, evolution rules, and
  compatibility checks
- A **schema declaration in DSL** so each ScrapeJob
  references the schema it produces; the engine validates
  every emitted row against the referenced schema before
  sink dispatch
- A **schema evolution model** that distinguishes additive
  changes (compatible) from breaking changes
  (require a new version + migration)

ADR-0034 is one of four subsystem ADRs in R9.4; together
with ADR-0033 (input management), ADR-0035 (DSL evolution),
and ADR-0038 (cost tracking) they materialise the catalog
services that ADR-0036 reserves.

### §1.1 — What this ADR does not yet land

No service code, no proto file, no DSL parser change, no
chart fragment land in R9.4. This ADR is contract-only.
The first build PR is **Wave 6** (per ADR-0036's wave
assignment) — `schema-registry` service materialises
alongside `input-broker` (per ADR-0033) in the same PR
sequence.

## §2 — Decision summary

R9.4 commits the output-schema subsystem to two artefacts:

- **`schema-registry` service** at
  `infra-services/schema-registry/` per ADR-0036's canonical
  service shape (Go; Mongo backend per ADR-0039 §3.9; gates
  B + D + E per ADR-0036 §3.4). Owns schema persistence,
  versioning, evolution rules, compatibility checks.
- **Schema declaration in DSL** as a `schema:` block in the
  v1alpha2 ScrapeJob DSL (per ADR-0035 §4). The block
  references a versioned schema by name + version; the
  engine fetches the schema once at job start, validates
  every emitted row against it, and rejects rows that fail
  validation per a configurable failure policy.

The split honours ADR-0036's gate B (schema-registry owns
versioned-schema state outliving any single job execution),
gate D (cross-cutting consumption — engine validates;
operator validates ScrapeJob spec; downstream consumers
validate output), and gate E (schemas evolve independently
of consumers).

## §3 — Schema declaration in DSL

### §3.1 — DSL `schema:` block

ScrapeJobs declare their output schema in the DSL:

```yaml
spec:
  dsl:
    navigate: { url: "https://example.com/products/123" }
    extract:
      schema:
        ref: "spectre.io/products/v2"   # registry name + version
        validation:
          mode: STRICT                  # STRICT | LENIENT | OFF
          onFailure: FAIL_ROW           # FAIL_ROW | FAIL_JOB | LOG_AND_EMIT
      selectors:
        name: { css: "h1.product-title" }
        price: { css: ".price", transform: parsePrice }
        sku: { attr: "data-sku" }
```

The `schema.ref` points to a versioned schema in the
schema-registry. The version is **explicit and required**
— no `latest` tag at v1alpha2 (avoids the implicit-coupling
problem; deferred to v1beta1 if real demand surfaces).

### §3.2 — Schema reference syntax

Schema refs follow `<namespace>/<name>/v<version>`:

- **`namespace`** — schema namespace; conventionally the
  tenant's domain or organisation (`spectre.io`,
  `tenant-a.com`)
- **`name`** — schema name within the namespace
  (`products`, `listings`, `social-posts`)
- **`version`** — explicit semver-like version
  (`v1`, `v2`, `v2.1.0`)

The full ref is **immutable once registered** — a schema
at `spectre.io/products/v2` is the same content forever;
schema changes produce a new ref (`v3` for breaking,
`v2.1.0` for additive).

### §3.3 — Schema body shape (JSON Schema)

The schema body follows **JSON Schema Draft 2020-12** as
the validation primitive — required fields, type
constraints, format constraints (uri, date-time, regex),
nested objects, arrays, oneOf / anyOf for unions. Example
fragment for `spectre.io/products/v2`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "spectre.io/products/v2",
  "type": "object",
  "required": ["url", "name", "price"],
  "properties": {
    "url":   { "type": "string", "format": "uri" },
    "price": {
      "type": "object",
      "required": ["amount", "currency"],
      "properties": {
        "amount":   { "type": "number" },
        "currency": { "type": "string", "pattern": "^[A-Z]{3}$" }
      }
    }
  }
}
```

The choice over alternatives: JSON Schema's broad
ecosystem (per-language validators: Go `gojsonschema`,
Rust `jsonschema`, Python `jsonschema`, TypeScript `ajv`)
+ schema-on-read flexibility fits document-shaped scraping
output. Avro / Protobuf trade flexibility for stricter
typing; OpenAPI is a superset overkill for per-row
validation.

### §3.4 — Validation modes

Per `validation.mode` in the DSL:

- **`STRICT`** (default) — every emitted row must validate;
  validation failures invoke `onFailure`.
- **`LENIENT`** — required fields must be present and
  type-correct; optional-field constraints
  (`minLength`, `pattern`, etc.) are warnings logged but
  do not invoke `onFailure`.
- **`OFF`** — schema is fetched (proves the ref exists) but
  no validation runs. Useful for development scaffolding;
  not recommended for production.

### §3.5 — Failure policies

Per `validation.onFailure`:

- **`FAIL_ROW`** (default) — drop the row; emit a
  `SCHEMA_VALIDATION_FAILED` audit event per ADR-0031 §6;
  proceed with the next row. The job overall succeeds if
  enough rows pass per the engine's per-job thresholds.
- **`FAIL_JOB`** — fail the entire job at the first
  validation failure; surface
  `SCHEMA_VALIDATION_FAILED` as the job's terminal failure
  code per ADR-0009.
- **`LOG_AND_EMIT`** — log a warning, emit the row anyway.
  Useful during schema migration windows when downstream
  consumers tolerate slight drift; explicit opt-in per job.

The default `FAIL_ROW` matches the operational principle
that one bad row should not invalidate a 100K-URL batch
while preserving the schema contract for the rows that do
emit.

## §4 — schema-registry service contract

### §4.1 — RPC surface (indicative; build PR settles)

```proto
service SchemaRegistry {
  // Schema lifecycle
  rpc Register(RegisterRequest)        returns (Schema);
  rpc Get(GetRequest)                  returns (Schema);
  rpc List(ListRequest)                returns (ListResponse);

  // Compatibility checks
  rpc CheckCompatibility(CheckCompatibilityRequest)
      returns (CompatibilityResult);

  // Validation (synchronous; called by engine per row)
  rpc Validate(ValidateRequest)        returns (ValidateResult);
}
```

The RPC surface follows ADR-0028 §3.1's canonical
proto-package convention (`spectre.schemaregistry.v1alpha1`)
and ADR-0027's per-language SDK admission gate. SDK
packages land per ADR-0027 §3.1 when their first consumer
materialises; the engine (Rust) is the first consumer in
Wave 6.

### §4.2 — Validate semantics

The engine calls `Validate(ValidateRequest)` per emitted
row. The request carries:

- **`schema_ref`** — the schema ref from the DSL
- **`row`** — the row to validate (JSON-encoded bytes)
- **`mode`** — STRICT / LENIENT / OFF

The response carries:

- **`valid`** (bool)
- **`failures`** (repeated) — per-field failure messages
  (path; expected; actual)

Validation is **synchronous and engine-blocking** per
ADR-0037 §4.3 — schema validation must complete before
sink emission decides. The latency cost is in the engine's
per-step budget; per-job schema caching (per ADR-0037 §4.2)
amortises the registry round-trip.

### §4.3 — Schema caching

To meet the latency budget, the engine **caches schemas
per job**:

- At job start, the engine fetches `Get(schema_ref)`
  once; the schema body lives in engine memory for the
  job's duration.
- Per-row validation runs **in-engine** against the cached
  schema; no per-row registry round-trip.
- The engine's local validator (per-language JSON Schema
  library) handles the validation call.

The `Validate` RPC exists for **non-engine consumers**
(operator validating ScrapeJob spec; SDK consumers
validating client-side). The engine's hot path is local.

### §4.4 — Schema registration flow

A schema is registered via:

```bash
spectre schema register \
  --ref spectre.io/products/v2 \
  --file ./products-v2.json \
  --previous spectre.io/products/v1
```

The `--previous` flag triggers a **compatibility check**
(§5 below). If compatible, the schema is registered;
otherwise, the registration fails with the violations.

Schema registration is **deployment-side concern** — the
schema-registry's RPC surface is in-cluster; the
`spectre schema` CLI is a thin wrapper that the operator
or build tooling invokes. v1alpha2 ships the CLI as a
build target alongside the existing engine binary; v1beta1
may add a kubectl plugin or a webhook integration.

## §5 — Validation at engine extraction

### §5.1 — Per-row validation flow

The engine's per-row validation flow (per ADR-0037 §3.2's
pseudocode + §4.2's caching):

```
on row extraction:
    schema = engine_local_cache.get(schema_ref)
    result = local_validator.validate(row, schema, mode)
    if !result.valid:
        emit_audit(SCHEMA_VALIDATION_FAILED, schema_ref, failures)
        emit_metric(spectre_schema_validation_failures_total, schema_ref)
        switch on_failure:
            FAIL_ROW:    skip row; continue
            FAIL_JOB:    fail job with SCHEMA_VALIDATION_FAILED
            LOG_AND_EMIT: log warning; emit row anyway
    else:
        emit row to sinks per ADR-0024
        emit_metric(spectre_schema_validation_pass_total, schema_ref)
```

The metrics emit per ADR-0031 §5 + §8 — the
`spectre_schema_validation_pass_ratio` quality metric (per
ADR-0031 §8) derives from
`pass_total / (pass_total + failures_total)`.

### §5.2 — Validation at operator admission

The operator (per ADR-0019) validates the ScrapeJob's
`spec.dsl.extract.schema.ref` at admission:

- The webhook validation calls
  `schema-registry.Get(schema_ref)` to confirm the schema
  exists.
- Failure (`SCHEMA_REGISTRY_UNAVAILABLE` or `NOT_FOUND`)
  rejects the ScrapeJob with a clear error.
- Success accepts the ScrapeJob; the engine subsequently
  caches the same schema at job start.

This admission-time check **catches typos early** — a user
submitting a ScrapeJob with `spectre.io/products/v3`
(misspelled or unregistered) gets immediate feedback rather
than an opaque per-row failure mid-execution.

### §5.3 — Validation at SDK consumer side

Per-language SDKs (per ADR-0027) ship a thin wrapper
around the schema-registry's RPC + the language's JSON
Schema validator. Downstream consumers parse rows from
sinks (Kafka topic; S3 object; webhook payload) and call
the SDK's `validate(row, schema_ref)` to assert shape
conformance. This is **opt-in** — consumers that trust the
engine's validation skip it; consumers requiring
defence-in-depth opt in.

## §6 — Schema evolution rules

Schema evolution is the central trade-off of any
schema-registry: too restrictive and consumers cannot
extend; too permissive and downstream consumers break.
ADR-0034 commits the v1alpha2 platform to a **conservative
default** with explicit opt-in for breaking changes.

### §6.1 — Compatibility modes

Schema-registry supports two compatibility modes per
schema namespace:

- **`BACKWARD`** (default) — new versions can be **read by
  consumers expecting older versions**. Required fields can
  be added only if they have defaults; optional fields can
  be added freely; type widening is allowed (string → enum
  with broader values); type narrowing is forbidden.
- **`NONE`** — any schema change registers; no
  compatibility check. Use for schema-on-read workflows
  where downstream consumers tolerate arbitrary drift
  (audit-log raw events, for example).

The default `BACKWARD` matches Confluent's schema-registry
default and aligns with the JSON-Schema-on-read pattern of
web scraping.

### §6.2 — Allowed changes (BACKWARD)

| Change | Allowed? |
|---|---|
| Add optional field | ✓ |
| Add required field with default | ✓ |
| Add required field without default | ✗ |
| Remove optional field | ✓ |
| Remove required field | ✗ |
| Widen type (`string` → `string \| null`) | ✓ |
| Narrow type (`string \| null` → `string`) | ✗ |
| Add enum value | ✓ |
| Remove enum value | ✗ |
| Tighten format constraint | ✗ |
| Loosen format constraint | ✓ |
| Rename field | ✗ (= remove + add) |

The compatibility checker walks the JSON Schema AST and
applies the rules mechanically.

### §6.3 — Breaking changes require new major version

Breaking changes are **not blocked** — they are
**explicit**. Registering `spectre.io/products/v3` after
`v2` is allowed without compatibility check (the registry
treats v2 → v3 as a new major version, not a v2 update).
The registration includes a **migration document** that
describes:

- What changed (field renames, type changes, removed
  fields)
- Migration approach (consumer-side adaptation, parallel
  running, dual-write window)
- Deprecation timeline for the old version

Migration documents are **markdown stored alongside the
schema** in the registry; they surface in the registry's
UI / CLI when a consumer queries for compatibility between
versions.

### §6.4 — Versioning conventions

Schema versions follow a semver-aligned pattern:

- **`v1`, `v2`, `v3`** — major versions; backward-incompatible
- **`v2.1`, `v2.2`** — minor versions within a major; only
  additive changes allowed
- **`v2.1.0`, `v2.1.1`** — patch versions; cosmetic /
  documentation changes only; no semantic differences

The compatibility checker enforces the conventions —
attempting to register `v2.1` with a breaking change is
rejected; attempting to register `v3` always passes (it is
a new major).

### §6.5 — Schema deprecation

Old major versions can be **deprecated** without removal
— the registry marks them
`deprecated: true; deprecated_at: <timestamp>; replacement:
<ref>` and emits warnings when the deprecated version is
fetched. Removal happens only after consumers have
migrated; the registry does not enforce removal automatically.

## §7 — Backend choice

The schema-registry uses **MongoDB as the primary backend**
per ADR-0039 §3.9. The choice is rigorously justified there;
the summary:

- **Schemas are literally documents** — JSON Schema is the
  natural representation; serialising to relational rows
  loses fidelity.
- **Atomic single-document writes** prevent conflicting
  versions during concurrent registrations; the
  `findAndModify` operation handles the
  check-then-register flow atomically.
- **Uniqueness indexes** on `(namespace, name, version)`
  enforce one-version-per-name without application-level
  locking.

Per-collection structure:

- `schemas` — one document per (namespace, name, version)
  triple; indexed on `(namespace, name, version)` (unique)
  and on `(namespace, name)` (for listing all versions of
  a name)
- `compatibility_modes` — one document per `(namespace,
  name)` recording the active mode
- `migrations` — one document per breaking-version pair
  (e.g., `(spectre.io/products, v2, v3)`) with the
  migration document body

ADR-0039 §4.6's anti-pattern ("Mongo without indexing
strategy") applies — the build PR includes
`explain('executionStats')` analysis for the version-lookup
hot path.

## §8 — Migration sequence

R9.4's ADR-0034 is documentation-only. The materialisation:

| Wave | Scope |
|---|---|
| Wave 6 (build PR) | schema-registry service materialised + DSL `schema:` block parsed by engine + operator admission validates schema refs + chart fragment for schema-registry per ADR-0036 §5.2. The Wave 6 PR sequence pairs with input-broker per ADR-0033 (the two services land together). |
| Wave 6 (post-build) | Initial schema set bootstrapped — the `spectre.io/products/v1`, `spectre.io/listings/v1`, `spectre.io/social-posts/v1` schemas register as starting examples; tenants register their own schemas thereafter. |
| Wave 9 | Quality metrics from ADR-0031 §8 surface — `spectre_schema_validation_pass_ratio` becomes a dashboard primitive. |
| Wave 10 | Schema-aware enrichment — the enricher service (slot 10 per ADR-0036 §3.4) consumes schema definitions to apply schema-specific enrichment rules (e.g., `geocoded_location` field triggers geocoding). |
| v1beta1 | Schema-on-read for output sinks — sinks may transform output to a target schema (via the schema-registry's `Transform` RPC, not in v1alpha2). |

The Wave 6 build PR is **transformational scope** under
the v1alpha2 process rigor matrix — the schema-registry
service materialises with the DSL parser change, the
operator admission webhook, and the engine validation
hot-path together.

## §9 — Confirmation (acceptance criteria)

The framework is working when the following hold **by the
close of Wave 6**:

- **A ScrapeJob with `extract.schema.ref` lands `Succeeded`**
  in production smoke (R7.2 extended for Wave 6) — every
  emitted row passes validation against the registered
  schema.
- **A ScrapeJob with a typo'd schema ref is rejected at
  admission** by the operator's webhook validation —
  `SCHEMA_NOT_FOUND` surfaces in the rejection message.
- **A row that violates the schema** (deliberately injected
  in a smoke fixture) is dropped per `FAIL_ROW` default; an
  audit event records the violation; the job overall
  succeeds.
- **Schema evolution under `BACKWARD` compatibility works**
  — registering `spectre.io/products/v1.1.0` (additive
  change) succeeds; registering `v1.2.0` with a removed
  required field fails the compatibility check.
- **Schema migration to a new major version works** —
  registering `spectre.io/products/v2` after `v1`
  succeeds (no compatibility check between major versions);
  the migration document body is stored.
- **Quality metric `spectre_schema_validation_pass_ratio`**
  is queryable in production smoke.

A signal that the framework needs revision: more than one
Wave 6+ tenant pilot reports that JSON Schema's
expressiveness is insufficient for their output shape (e.g.,
graph-shaped data; recursive structures Mongo handles but
JSON Schema describes awkwardly). That's evidence the
schema language needs extension; the response is an ADR
amendment that adds an alternative schema language
(probably Avro or Protobuf via cross-format adapters), not
a per-tenant deviation.

## §10 — What's deferred / out of scope

R9.4 declines these deliberately. Each is a real concern;
each belongs to a later phase.

- **Schema-on-read transformation.** The schema-registry's
  `Transform` RPC (mapping rows from one schema to another)
  is a v1beta1 concern — useful for output-format
  conversion (Parquet, Avro) but not in v1alpha2 scope.
- **Schema-driven extraction.** Generating extraction
  selectors from schemas (the inverse of the v1alpha2
  selectors-produce-rows model) is a v1beta1 concern.
- **Latest-version aliases.** The `latest` tag (e.g.,
  `spectre.io/products/latest`) creates implicit coupling
  that breaks consumers when "latest" advances. Deferred to
  v1beta1; v1alpha2 requires explicit versions.
- **Multi-tenant schema isolation.** Per-tenant schema
  namespaces (each tenant sees only their own schemas) is
  a v1beta1 multi-tenancy concern.
- **Schema fuzzing.** Property-based testing of schemas
  (generating valid + invalid rows automatically) is a
  testing-tooling concern outside ADR scope.
- **Cross-format schema export.** Exporting a JSON Schema
  as Avro / Protobuf / OpenAPI via the registry is
  v1beta1.
- **Field-level access control.** PII / GDPR-shaped
  per-field redaction at the registry level is a
  compliance concern handled by v1beta1's broader
  compliance work, not ADR-0034.
- **Schema GraphQL gateway.** Exposing schemas as a
  GraphQL surface for downstream tooling is v1beta1.
- **Schema-driven UI generation.** Auto-generating
  ScrapeJob editing UIs from schemas is a v1beta1
  developer-experience concern.
- **Schema retention / archival.** Old schema versions
  remain in the registry indefinitely at v1alpha2;
  archival to cold storage after deprecation is a
  v1beta1 operational concern.

## §11 — Reference materials

- [ADR-0009](0009-navigate-and-session-lifecycle.md) —
  driver error mapping; `SCHEMA_VALIDATION_FAILED` and
  `SCHEMA_NOT_FOUND` extend ADR-0009's
  `DriverError.Code` enum at Wave 6.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane; the operator's admission webhook
  validates schema refs per §5.2.
- [ADR-0024](0024-output-sinks.md) — output sinks; sinks
  emit validated rows; sinks themselves do not validate
  (the engine validates pre-emission per §5.1).
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy;
  per-language schema-registry SDKs follow the admission
  gate. SDK consumers may opt into client-side validation
  per §5.3.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) —
  catalog precedent for canonical service shape.
- [ADR-0029](0029-data-platform-and-lake-dsls.md) — data
  platform and lake DSLs; output schemas align with the
  bronze layer's typed-record contract per ADR-0029 §3.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart;
  the schema-registry chart fragment lands per the
  canonical pattern.
- [ADR-0031](0031-observability-framework.md) —
  observability framework; §5 metrics taxonomy includes
  `spectre_schema_validation_*`; §6 makes
  `SCHEMA_VALIDATION_FAILED` a first-class failure code.
- [ADR-0036](0036-microservices-catalog-expansion.md) —
  the 15-service catalog; `schema-registry` is slot 9.
- [ADR-0037](0037-engine-as-orchestrator.md) — engine as
  orchestrator; §3.2 pseudocode shows
  `schema_registry.Get` + per-row validation in the
  per-row processing flow.
- [ADR-0039](0039-mongodb-third-storage-tier.md) —
  MongoDB tier; §3.9 evaluates schema-registry's backend.
- ADR-0033 (this PR's Cluster A) — input management;
  ScrapeBatch's `scrapeTemplate.dsl` uses the schema-block
  syntax this ADR commits.
- ADR-0035 (this PR's Cluster C) — DSL evolution; the
  `schema:` block is part of the v1alpha2 DSL surface.
- JSON Schema Draft 2020-12:
  <https://json-schema.org/draft/2020-12/schema>
- Confluent schema-registry compatibility modes:
  <https://docs.confluent.io/platform/current/schema-registry/avro.html#compatibility-types>
