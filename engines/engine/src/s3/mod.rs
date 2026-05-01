// SPDX-License-Identifier: Apache-2.0

//! S3 sink integration (R5.1 — ADR-0024 §3).
//!
//! When the engine's [`crate::server::EngineServiceImpl`] receives
//! a `RunJob` whose `output_sink_kind` is `'s3'`, this module's
//! [`S3Uploader`] uploads the job's full extracted output as a
//! single JSONL object to the configured bucket / key. The
//! uploader is initialised once at engine startup and shared
//! across in-flight RPCs as `Arc<S3Uploader>`; the inner
//! `aws_sdk_s3::Client` is internally reference-counted and
//! thread-safe.
//!
//! ### Admission gating (per ADR-0024 §5)
//!
//! S3 is OPTIONAL. The engine binary calls
//! [`S3Uploader::from_env`] at startup; on success the
//! `EngineServiceImpl` holds `Some(uploader)` and accepts
//! S3-sinked jobs, on failure (or env-unset) it logs and holds
//! `None`. The startup log level differs from Kafka's R4.4
//! pattern: env-unset is INFO (BYO-credentials mode is the
//! production-typical shape), env-configured-but-unreachable is
//! WARN. S3 jobs against `None` fail fast at job-start with
//! `S3_UNAVAILABLE`. The pre-flight rejects empty bucket / key
//! with `S3_FIELD_REQUIRED`.
//!
//! ### Buffering and delivery semantics (per ADR-0024 §3)
//!
//! Per-job in-memory accumulation, single `PutObject` at job
//! completion. The body is JSON Lines (one row per line, trailing
//! newline) with `Content-Type: application/x-ndjson`.
//! Empty-result jobs upload an empty object so the post-job
//! presence-or-absence of the key is a reliable signal.
//!
//! At-least-once delivery. A successful `PutObject` returns 200 OK
//! with an `ETag` — the job is then durable. Re-driving a
//! `ScrapeJob` overwrites the same key (S3 PUT is replace-not-
//! append) when the key template does not include `{{.JobID}}`;
//! including the template makes re-drives go to a fresh key
//! because the new `ScrapeJob` has a fresh UID.

pub mod config;
pub mod uploader;

pub use config::{S3Config, S3ConfigError};
pub use uploader::{S3Error, S3Uploader, render_key};
