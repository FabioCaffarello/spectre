// SPDX-License-Identifier: Apache-2.0

//! HTTP webhook sink integration (R5.1 — ADR-0024 §4).
//!
//! When the engine's [`crate::server::EngineServiceImpl`] receives
//! a `RunJob` whose `output_sink_kind` is `'webhook'`, this
//! module's [`WebhookClient`] POSTs (or PUTs) extracted rows to
//! the configured URL — one HTTP request per row when
//! `BatchSize == 0` (CRD default), or N-row batches when
//! `BatchSize > 0`. The client is initialised once at engine
//! startup and shared across in-flight RPCs as
//! `Arc<WebhookClient>`; the inner `reqwest::Client` is
//! connection-pooled and thread-safe.
//!
//! ### Admission gating (per ADR-0024 §5)
//!
//! Webhook is **per-job**, not engine-level. The
//! `WebhookClient::new()` constructor is infallible — there is
//! no global state for the engine to validate at startup. The
//! `Arc<WebhookClient>` field on `EngineServiceImpl` is always
//! `Some(...)`. Admission happens at the executor when the first
//! POST attempt connects (or fails to). The pre-flight rejects
//! empty URL with `WEBHOOK_FIELD_REQUIRED`.
//!
//! ### Retry and delivery semantics (per ADR-0024 §4)
//!
//! At-least-once. Bounded exponential-backoff retry policy for
//! transient errors:
//!
//! - 3 attempts total
//! - Base delay 200 ms; doubled per attempt with jitter
//! - Retryable: connection refused, 5xx, 429
//! - Fatal on first failure: 4xx other than 429, malformed URL
//!
//! After retries exhaust, the job terminates with
//! `WEBHOOK_POST_FAILED`. Per-job [`WebhookSession`] state holds
//! the buffered batch; `BatchSize == 0` (CRD default) flushes
//! after every row, `BatchSize > 0` flushes when N rows are
//! collected or on `finalise()`.
//!
//! ### Header schema (per ADR-0024 §4)
//!
//! Every request carries:
//!
//! - `User-Agent: spectre-engine/<version>`
//! - `Content-Type: application/x-ndjson`
//! - `X-Spectre-Job-Id` — job UUID
//! - `X-Spectre-Driver` — `playwright` / `seleniumbase` /
//!   `curl-impersonate`
//! - `X-Spectre-Batch-Size` — only when `batch_size > 0`
//! - `X-Spectre-Row-Count` — number of rows in this body
//!
//! Auth headers (`Authorization`, `X-Hub-Signature`, etc.) are
//! NOT added — auth is v1alpha2.

pub mod client;
pub mod config;

pub use client::{WebhookClient, WebhookError, WebhookSession};
pub use config::{Method, WebhookConfig};
