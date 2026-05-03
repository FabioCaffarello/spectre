# `data-platform/parse/`

Stage L0 → L1 (raw → bronze): extract structured records from the
raw bytes the engine produced.

Defined by **[ADR-0029 §4.1](../../docs/adr/0029-data-platform-and-lake-dsls.md)**.
Empty at v1alpha1 (Phase R6.6 reservation; refactor closed at
R8.1).

Scope:
- File-format parsers (HTML archive, JSON, JSON-Lines, CSV, XLSX, PDF, XML, RSS)
- Schema mapping (per-job-schema → bronze-schema)
- Deduplication (content hash; per-source ID)
- Lineage stamping (source job ID, source URL, ingestion timestamp, parser version)
- Validation against the bronze schema; quarantine on rejection

A library-shaped parser does not require an ADR. A service-shaped or
DSL-shaped module follows ADR-0029 §7.3 admission criteria.
