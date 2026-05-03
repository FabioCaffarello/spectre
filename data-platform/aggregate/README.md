# `data-platform/aggregate/`

Stage L2 → L3 (silver → gold): pre-compute business-ready aggregates
from silver tables.

Defined by **[ADR-0029 §4.3](../../docs/adr/0029-data-platform-and-lake-dsls.md)**.
Empty at v1alpha1 (Phase R6.6 reservation; refactor closed at
R8.1).

Scope:
- Aggregations (count, sum, avg, percentile) by dimensions
- Time-windowed metrics (rolling 7-day, week-over-week)
- Star-schema fact-and-dimension tables
- Top-N tables; denormalised serving-layer views

The transform-DSL (§4.2) and aggregate-DSL may merge into one unified
record-DSL or stay separate; the choice is made by the first within-lake
DSL build PR per ADR-0029 §6.3.
