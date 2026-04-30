// SPDX-License-Identifier: Apache-2.0

//! Typed query functions for the `jobs` and `job_rows` tables.
//!
//! Each function corresponds to one transition in the `ScrapeJob`
//! lifecycle the engine drives end-to-end. The status column is
//! authoritative (CHECK-constrained at the schema level to
//! `{pending,running,completed,failed}` per ADR-0023 §2); the engine
//! does not write `pending` because admission validation runs
//! synchronously in [`crate::server`] before any row is inserted —
//! the first persisted state is `running`.
//!
//! Compile-time-checked via `sqlx::query!` macros (§8). The offline
//! cache at `engines/engine/.sqlx/` is the input the Dockerfile (and
//! CI) consume when `SQLX_OFFLINE=1`; regenerate via
//! `cargo sqlx prepare` against a Postgres carrying the migrations
//! from `engines/engine/migrations/`.

use sqlx::PgPool;
use uuid::Uuid;

/// Insert a fresh `jobs` row in the `running` state. Called once per
/// admitted `RunJob`, right after parse + plan succeed and before
/// the streaming task is spawned.
///
/// `started_at` is set by Postgres `NOW()` so the engine never has to
/// reach for a wall-clock crate; `created_at` defaults the same way
/// at the schema level. `resolved_engine_endpoint` is left NULL in
/// v1alpha1 — the engine does not know the host:port the operator
/// dialed; the column exists for v1alpha2's audit needs and may be
/// populated by the control plane via UPDATE.
///
/// # Errors
///
/// Returns the underlying [`sqlx::Error`] when the INSERT fails (FK
/// / CHECK violation, connection lost, duplicate primary key).
pub async fn insert_job(
    pool: &PgPool,
    id: Uuid,
    dsl: &str,
    driver: &str,
    output_sink_kind: &str,
) -> Result<(), sqlx::Error> {
    sqlx::query!(
        r#"
        INSERT INTO jobs (id, dsl, driver, status, started_at, output_sink_kind)
        VALUES ($1, $2, $3, 'running', NOW(), $4)
        "#,
        id,
        dsl,
        driver,
        output_sink_kind,
    )
    .execute(pool)
    .await?;
    Ok(())
}

/// Append one extracted row to `job_rows`. Called only when
/// `output_sink_kind = 'stdout'`; per ADR-0023 §2 Kafka / S3 /
/// Webhook sinks bypass `job_rows` to avoid double-write costs.
///
/// `row_index` is the engine's monotonic counter for the job and
/// pairs with `job_id` as the composite primary key.
///
/// # Errors
///
/// Returns the underlying [`sqlx::Error`] when the INSERT fails (FK
/// to a missing `jobs.id`, duplicate `(job_id, row_index)`, etc).
pub async fn record_job_row(
    pool: &PgPool,
    job_id: Uuid,
    row_index: i64,
    json_value: &serde_json::Value,
) -> Result<(), sqlx::Error> {
    sqlx::query!(
        r#"
        INSERT INTO job_rows (job_id, row_index, json_value)
        VALUES ($1, $2, $3)
        "#,
        job_id,
        row_index,
        json_value,
    )
    .execute(pool)
    .await?;
    Ok(())
}

/// Mark a running job as `completed`, recording the total row count.
/// `completed_at` is set by Postgres `NOW()`.
///
/// # Errors
///
/// Returns the underlying [`sqlx::Error`] when the UPDATE fails. A
/// missing `id` is not an error — `UPDATE` returns zero affected
/// rows; the caller can inspect the result if it cares.
pub async fn mark_completed(
    pool: &PgPool,
    id: Uuid,
    rows_extracted: i64,
) -> Result<(), sqlx::Error> {
    sqlx::query!(
        r#"
        UPDATE jobs
        SET status = 'completed', rows_extracted = $2, completed_at = NOW()
        WHERE id = $1
        "#,
        id,
        rows_extracted,
    )
    .execute(pool)
    .await?;
    Ok(())
}

/// Mark a running job as `failed`, recording the error message.
/// `completed_at` is set by Postgres `NOW()`.
///
/// # Errors
///
/// Returns the underlying [`sqlx::Error`] when the UPDATE fails.
pub async fn mark_failed(pool: &PgPool, id: Uuid, error: &str) -> Result<(), sqlx::Error> {
    sqlx::query!(
        r#"
        UPDATE jobs
        SET status = 'failed', error = $2, completed_at = NOW()
        WHERE id = $1
        "#,
        id,
        error,
    )
    .execute(pool)
    .await?;
    Ok(())
}
