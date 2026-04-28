# PostgreSQL — engine writes, control-plane reads

R4.2 introduced PostgreSQL as the durable store for ScrapeJob
state and audit rows. The architectural commitment lives in
[ADR-0023 §2 / §13](../adr/0023-stateful-services-architecture.md);
this document is the operator-facing companion: how the schema is
shaped, how migrations evolve, how the engine and control-plane
each connect to it, and what failure modes look like in practice.

## Schema

Two tables, both owned by the engine. The schema is plain
PostgreSQL with no vendor extensions.

```sql
jobs (
    id UUID PRIMARY KEY,                   -- = ScrapeJob.UID
    dsl TEXT NOT NULL,
    driver TEXT NOT NULL,                  -- "playwright" | "seleniumbase" | "curl-impersonate"
    status TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    rows_extracted BIGINT,
    error TEXT,
    resolved_engine_endpoint TEXT,
    output_sink_kind TEXT NOT NULL CHECK (output_sink_kind IN ('stdout','kafka','s3','webhook'))
)

job_rows (
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    row_index BIGINT NOT NULL,
    json_value JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, row_index)
)
```

The CHECK constraints mirror v1alpha2's `ScrapeJobPhase` and
`OutputSink` discriminants — the schema rejects writes the
reconciler should already have rejected at admission. `job_rows`
is populated only when `output_sink_kind = 'stdout'` per
ADR-0023 §2; Kafka / S3 / Webhook sinks (R4.4 / R5.1) skip the
audit table. `ON DELETE CASCADE` makes retention single-statement.

## Migration discipline (ADR-0023 §13)

Migrations live in `core/engine/migrations/` as
`<YYYYMMDDHHMMSS>_<name>.sql` files. R4.2 lands the first
(`20260428150332_initial_schema.sql`); each subsequent schema
change adds a new file. **Once committed and merged, a migration
file is immutable.** Renaming, reordering, or editing a merged
file breaks deployments — sqlx records the file's checksum in
`_sqlx_migrations` and refuses to start an engine whose embedded
migrations diverge from the database's history.

The engine runs migrations at startup, before registering the
gRPC service: connect via `SPECTRE_POSTGRES_URL`, apply any new
files in timestamp order, register the service, start serving.
A migration failure is engine startup failure (exit code 1).

To regenerate the offline-mode `.sqlx/` query cache after
adding or changing a `query!` invocation:

```bash
just compose-up
DATABASE_URL=postgres://spectre:spectre_dev_password@localhost:5432/spectre \
  cargo sqlx prepare --workspace
git add core/engine/.sqlx/
```

The cache is what lets the Dockerfile build under
`SQLX_OFFLINE=1` without a running Postgres.

## Connection lifecycle

**Engine.** A single `sqlx::PgPool` constructed at startup via
`Database::from_env()`. Default size 5; tunable via
`SPECTRE_POSTGRES_MAX_CONNS`. The pool dials eagerly so a
misconfigured deployment surfaces as a startup-time error, not
a first-RPC error. Per-`RunJob` writes (`insert_job`,
`record_job_row`, `mark_completed` / `mark_failed`) acquire a
connection from the pool and release it on completion.

**Control plane.** A single `*pgxpool.Pool` constructed at
startup via `db.FromEnv()`. Uses pgxpool's built-in default
size (≈25). The reconciler holds the pool pointer; each
`Reconcile` call passes it through `db.GetJob` /
`db.CountJobRows` for restart-recovery reads.

Both pools recover transparently from transient disconnects by
re-dialling; the engine and operator binaries do not need to
restart on Postgres restart.

## Unavailability semantics

ADR-0023 §6 commits Postgres as **REQUIRED**. The behavioural
contracts:

- **Engine startup, Postgres unreachable → engine exits 1.**
  Visible to `kubectl get pod` as a crash loop, to
  `docker compose up` as non-zero exit. Same for missing
  `SPECTRE_POSTGRES_URL` or a malformed URL. There is no
  degraded "engine without Postgres" mode.
- **Engine mid-`RunJob`, transient Postgres error on a row
  write → log + continue.** An audit gap is preferred to
  aborting a running scrape over a failed `record_job_row`.
- **Engine mid-`RunJob`, transient Postgres error on
  `mark_completed`/`mark_failed` → log + still emit the gRPC
  terminal event.** The client always sees a definitive end.
- **Operator startup, Postgres unreachable → operator exits 1.**
  Same crash-loop semantics as the engine.
- **Operator mid-Reconcile, restart-recovery query errors →
  ScrapeJob marked Failed** with the underlying error wrapped as
  `"postgres: restart recovery failed: ..."`. v1alpha1 does not
  retry; v1alpha2 may add transient-error backoff.

## Local development

```bash
cp .env.example .env
just compose-up            # postgres:16-alpine, healthchecked
just engine-run            # connects to localhost:5432
just pw-run 9091           # Playwright adapter
just op-run                # operator (also dials Postgres)
```

`just compose-reset` drops the volume and re-applies migrations
from a clean slate — useful when iterating on schema changes.
The dev credentials in `docker-compose.yml` are visible in
source; production deployments populate `SPECTRE_POSTGRES_URL`
from Kubernetes Secrets per ADR-0023 §10 / §12.

## Tests

Engine: `just engine-db-test` runs `#[ignore]`-gated integration
tests against `SPECTRE_POSTGRES_URL` covering insert / row append
/ mark-completed / mark-failed / FK cascade. The default
`just engine-test` stays DB-free so contributors without
Postgres get green unit-suite results.

Control plane: `make test` covers the reconciler's
restart-recovery branches via pgxmock; the `internal/db` package
has its own pgxmock unit tests for `GetJob` and `CountJobRows`.

## References

- [ADR-0023](../adr/0023-stateful-services-architecture.md) §2
  (schema), §6 (REQUIRED), §8 (sqlx + pgx/v5), §12 (env vars),
  §13 (migration discipline)
- sqlx: <https://github.com/launchbadge/sqlx>
- pgx/v5: <https://github.com/jackc/pgx>
- PostgreSQL 16 docs: <https://www.postgresql.org/docs/16/>
