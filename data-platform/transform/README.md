# `data-platform/transform/`

Stage L1 → L2 (bronze → silver): clean, conform, deduplicate, and
join bronze records into silver tables.

Defined by **[ADR-0029 §4.2](../../docs/adr/0029-data-platform-and-lake-dsls.md)**.
Empty in Phase R6.6.

Scope:
- Type coercions (string → typed timestamp, string → typed decimal)
- Enum normalisation, currency conversion, locale handling
- Cross-source deduplication
- Joins facts to dimensions; entity resolution
- Slowly-changing dimensions (Type 2 history)
- Quality assertions (silver-level data contracts)

The default for transform DSLs is to adopt an existing SQL dialect
(dbt, Spark SQL, DuckDB, Trino) before designing a Spectre-specific
DSL — see ADR-0029 §6.4.
