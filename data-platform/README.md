# `data-platform/`

Data lake processing — file parsing, transformation between lake
layers, and aggregation of business-ready views.

This directory is intentionally empty at v1alpha1 (Phase R6.6
reservation; refactor closed at R8.1). The category is
defined by **[ADR-0026 §3.7](../docs/adr/0026-platform-taxonomy.md)**;
the lake-layer model, stage taxonomy, and DSL governance are defined
by **[ADR-0029](../docs/adr/0029-data-platform-and-lake-dsls.md)**.

Lake-layer model (medallion):

- **L0 / raw** — engine output via sinks per [ADR-0024](../docs/adr/0024-output-sinks.md).
  Populated by [ADR-0012](../docs/adr/0012-engine-dsl-and-execution-pipeline.md)'s engine
  job DSL (the L0 entry DSL).
- **L1 / bronze** — conformed records (produced by `parse/`).
- **L2 / silver** — cleaned, deduplicated, joined (produced by `transform/`).
- **L3 / gold** — business-aggregated views (produced by `aggregate/`).

Stage subdirectories exist as placeholders (`parse/`, `transform/`,
`aggregate/`); their internal layout is decided per build PR.

Up to three layer-transition DSLs (parse-DSL, transform-DSL,
aggregate-DSL) may emerge, each gated by ADR-0029 §6 governance
criteria (abstraction level, runtime context, audience, evolution
cadence). Default is to adopt existing tooling (dbt SQL, Spark SQL,
DuckDB, parser libraries) before introducing a Spectre-specific DSL.
