// SPDX-License-Identifier: Apache-2.0

//! Postgres persistence for engine job state.
//!
//! ADR-0023 §2 commits the engine to writing one row to `jobs` per
//! admitted `ScrapeJob` run and (for `OutputSink.Stdout` jobs) one
//! row to `job_rows` per extracted row. This module owns the
//! [`Database`] handle that wraps a [`sqlx::PgPool`], the
//! [`run_migrations`] entry point that the binary calls before
//! serving traffic (§13), and the typed query functions in [`jobs`]
//! that the gRPC service calls on every state transition.
//!
//! Connection lifecycle is per-process: one pool, sized via
//! `SPECTRE_POSTGRES_MAX_CONNS` (default 5), constructed at engine
//! startup and shared across the streaming `RunJob` task graph.
//! Postgres unavailability at startup is a startup failure (§6); the
//! gRPC service never registers if the dial fails.

pub mod jobs;
mod migrations;

pub use migrations::run_migrations;

use std::env;

use sqlx::postgres::{PgPool, PgPoolOptions};
use thiserror::Error;

/// Default connection-pool size. Sized for v1alpha1's
/// single-reconciler operator (one in-flight `RunJob` plus headroom
/// for status writes); tunable via `SPECTRE_POSTGRES_MAX_CONNS`.
const DEFAULT_MAX_CONNECTIONS: u32 = 5;

const URL_ENV: &str = "SPECTRE_POSTGRES_URL";
const MAX_CONNS_ENV: &str = "SPECTRE_POSTGRES_MAX_CONNS";

/// Errors surfaced when constructing a [`Database`] from environment.
#[derive(Debug, Error)]
pub enum DatabaseInitError {
    /// `SPECTRE_POSTGRES_URL` was not set in the process environment.
    #[error("{URL_ENV} must be set")]
    MissingUrl,

    /// `SPECTRE_POSTGRES_MAX_CONNS` was set but did not parse as a
    /// positive `u32`.
    #[error("{MAX_CONNS_ENV} must be a positive integer (got {value:?})")]
    InvalidMaxConns {
        /// The raw value the env var carried.
        value: String,
    },

    /// Pool construction or initial connection failed.
    #[error("postgres connect: {0}")]
    Connect(#[from] sqlx::Error),
}

/// Thin wrapper around a [`sqlx::PgPool`]. Cloning is cheap — the
/// underlying pool is reference-counted internally — so handlers
/// hold their own copy.
#[derive(Clone)]
pub struct Database {
    /// Connection pool exposed for query functions.
    pub pool: PgPool,
}

impl Database {
    /// Construct a [`Database`] from `SPECTRE_POSTGRES_URL` and
    /// (optional) `SPECTRE_POSTGRES_MAX_CONNS`. The pool eagerly
    /// dials Postgres so a misconfigured deployment fails at startup
    /// rather than at first query.
    ///
    /// # Errors
    ///
    /// Returns [`DatabaseInitError::MissingUrl`] if the URL env var is
    /// unset, [`DatabaseInitError::InvalidMaxConns`] if the optional
    /// max-connections env var is malformed, or
    /// [`DatabaseInitError::Connect`] if the dial fails.
    pub async fn from_env() -> Result<Self, DatabaseInitError> {
        let url = env::var(URL_ENV).map_err(|_| DatabaseInitError::MissingUrl)?;
        let max_conns = match env::var(MAX_CONNS_ENV) {
            Ok(raw) => raw
                .parse::<u32>()
                .map_err(|_| DatabaseInitError::InvalidMaxConns { value: raw })?,
            Err(_) => DEFAULT_MAX_CONNECTIONS,
        };

        let pool = PgPoolOptions::new()
            .max_connections(max_conns)
            .connect(&url)
            .await?;
        Ok(Self { pool })
    }
}
