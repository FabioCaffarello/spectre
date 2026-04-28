// SPDX-License-Identifier: Apache-2.0

//! Integration tests for `spectre_engine::db::jobs`. Each test runs
//! against a real Postgres dialled via `SPECTRE_POSTGRES_URL` (the
//! same env var the engine binary reads at startup, ADR-0023 §12).
//!
//! Marked `#[ignore]` so the standard `cargo test` /
//! `just engine-test` run never depends on a database: contributors
//! without Postgres get green results from the unit-level suite, and
//! CI / local-dev workflows opt in via
//! `cargo test --test db_integration -- --ignored` (or the
//! `engine-db-test` justfile recipe added alongside).
//!
//! The migrator runs at the start of every test against a freshly
//! truncated schema — `sqlx::migrate` is idempotent so the same
//! `_sqlx_migrations` row is reused across runs. The TRUNCATE
//! cascade resets `jobs` (and `job_rows` via FK) so each test starts
//! from a clean slate.

#![cfg(test)]

use spectre_engine::db::{jobs, run_migrations};
use sqlx::postgres::{PgPool, PgPoolOptions};
use std::env;
use uuid::Uuid;

const URL_ENV: &str = "SPECTRE_POSTGRES_URL";

async fn pool() -> PgPool {
    let url = env::var(URL_ENV)
        .unwrap_or_else(|_| panic!("{URL_ENV} must be set to run db_integration tests"));
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&url)
        .await
        .expect("connect to Postgres");
    run_migrations(&pool).await.expect("apply migrations");
    sqlx::query("TRUNCATE jobs RESTART IDENTITY CASCADE")
        .execute(&pool)
        .await
        .expect("truncate");
    pool
}

#[tokio::test]
#[ignore = "requires a running Postgres at SPECTRE_POSTGRES_URL"]
async fn insert_job_creates_running_row() {
    let pool = pool().await;
    let id = Uuid::new_v4();

    jobs::insert_job(
        &pool,
        id,
        "spectre: v1alpha1",
        "playwright",
        "stdout",
    )
    .await
    .expect("insert_job");

    let row: (String, String, String) = sqlx::query_as(
        "SELECT status, driver, output_sink_kind FROM jobs WHERE id = $1",
    )
    .bind(id)
    .fetch_one(&pool)
    .await
    .expect("select");
    assert_eq!(row.0, "running");
    assert_eq!(row.1, "playwright");
    assert_eq!(row.2, "stdout");
}

#[tokio::test]
#[ignore = "requires a running Postgres at SPECTRE_POSTGRES_URL"]
async fn record_job_row_appends_audit_rows_in_order() {
    let pool = pool().await;
    let job_id = Uuid::new_v4();
    jobs::insert_job(&pool, job_id, "dsl", "playwright", "stdout")
        .await
        .unwrap();

    for index in 0..3i64 {
        jobs::record_job_row(&pool, job_id, index, &serde_json::json!({"i": index}))
            .await
            .expect("record_job_row");
    }

    let rows: Vec<(i64, serde_json::Value)> = sqlx::query_as(
        "SELECT row_index, json_value FROM job_rows WHERE job_id = $1 ORDER BY row_index",
    )
    .bind(job_id)
    .fetch_all(&pool)
    .await
    .expect("select rows");
    assert_eq!(rows.len(), 3);
    for (i, (index, value)) in rows.iter().enumerate() {
        let i64_i = i64::try_from(i).expect("index in i64 range");
        assert_eq!(*index, i64_i);
        assert_eq!(value, &serde_json::json!({"i": i64_i}));
    }
}

#[tokio::test]
#[ignore = "requires a running Postgres at SPECTRE_POSTGRES_URL"]
async fn mark_completed_finalises_running_row() {
    let pool = pool().await;
    let id = Uuid::new_v4();
    jobs::insert_job(&pool, id, "dsl", "playwright", "stdout")
        .await
        .unwrap();

    jobs::mark_completed(&pool, id, 42).await.expect("mark");

    let row: (String, Option<i64>, Option<String>) = sqlx::query_as(
        "SELECT status, rows_extracted, error FROM jobs WHERE id = $1",
    )
    .bind(id)
    .fetch_one(&pool)
    .await
    .expect("select");
    assert_eq!(row.0, "completed");
    assert_eq!(row.1, Some(42));
    assert_eq!(row.2, None);
}

#[tokio::test]
#[ignore = "requires a running Postgres at SPECTRE_POSTGRES_URL"]
async fn mark_failed_records_error_message() {
    let pool = pool().await;
    let id = Uuid::new_v4();
    jobs::insert_job(&pool, id, "dsl", "playwright", "stdout")
        .await
        .unwrap();

    jobs::mark_failed(&pool, id, "adapter dial: connection refused")
        .await
        .expect("mark");

    let row: (String, Option<String>) =
        sqlx::query_as("SELECT status, error FROM jobs WHERE id = $1")
            .bind(id)
            .fetch_one(&pool)
            .await
            .expect("select");
    assert_eq!(row.0, "failed");
    assert_eq!(row.1.as_deref(), Some("adapter dial: connection refused"));
}

#[tokio::test]
#[ignore = "requires a running Postgres at SPECTRE_POSTGRES_URL"]
async fn deleting_jobs_cascades_to_job_rows() {
    let pool = pool().await;
    let id = Uuid::new_v4();
    jobs::insert_job(&pool, id, "dsl", "playwright", "stdout")
        .await
        .unwrap();
    jobs::record_job_row(&pool, id, 0, &serde_json::json!({"a": 1}))
        .await
        .unwrap();

    sqlx::query("DELETE FROM jobs WHERE id = $1")
        .bind(id)
        .execute(&pool)
        .await
        .expect("delete");

    let count: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM job_rows WHERE job_id = $1")
        .bind(id)
        .fetch_one(&pool)
        .await
        .expect("count");
    assert_eq!(count.0, 0, "FK ON DELETE CASCADE should remove audit rows");
}
