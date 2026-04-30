// SPDX-License-Identifier: Apache-2.0

//! Engine-startup migration runner.
//!
//! Per ADR-0023 §13 the engine applies any unapplied migrations from
//! `engines/engine/migrations/` before registering the gRPC service. The
//! migrator is forward-only; once a `<timestamp>_<name>.sql` file is
//! committed and merged it is immutable. Schema evolution adds new
//! versioned files; sqlx records applied filenames in
//! `_sqlx_migrations` so re-runs are idempotent.
//!
//! Migration failure is engine-startup failure: the binary exits
//! non-zero, surfacing to `kubectl get pod` as a crash loop and to
//! `docker compose up` as a non-zero exit.

use sqlx::PgPool;
use sqlx::migrate::MigrateError;

/// Apply any unapplied migrations from `engines/engine/migrations/` to
/// `pool`. The macro embeds the SQL files at compile time, so the
/// binary carries its own schema.
///
/// # Errors
///
/// Returns the underlying [`MigrateError`] when a migration fails to
/// apply, when `_sqlx_migrations` reports a checksum mismatch (a
/// committed migration file was edited post-merge — forbidden per
/// §13), or when the database connection fails mid-migration.
pub async fn run_migrations(pool: &PgPool) -> Result<(), MigrateError> {
    sqlx::migrate!("./migrations").run(pool).await
}
