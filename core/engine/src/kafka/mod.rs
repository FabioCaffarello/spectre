// SPDX-License-Identifier: Apache-2.0

//! Kafka producer integration (R4.4 — ADR-0023 §3).
//!
//! When the engine's [`crate::server::EngineServiceImpl`] receives a
//! `RunJob` whose `output_sink_kind` is `'kafka'`, this module's
//! [`KafkaProducer`] writes one message per extracted row to the
//! configured topic. The producer is initialised once at engine
//! startup and shared across in-flight RPCs as
//! `Arc<KafkaProducer>` — `FutureProducer` is thread-safe and
//! designed for concurrent use.
//!
//! ### Admission gating
//!
//! Per ADR-0023 §6, Kafka is OPTIONAL. The engine binary calls
//! [`KafkaProducer::from_env`] at startup; on success the
//! `EngineServiceImpl` holds `Some(producer)` and accepts kafka
//! sink jobs, on failure it logs a warning, holds `None`, and
//! continues. Kafka-sinked jobs against a `None` producer fail
//! fast at job-start time with `error_code = "KAFKA_UNAVAILABLE"`
//! — equivalent UX to admission rejection without the cost of a
//! validating webhook (ADR-0023 §3 R4.4 addendum).
//!
//! ### Delivery semantics
//!
//! At-least-once. `enable.idempotence=true` prevents duplicate
//! writes from intra-session retries; engine crash mid-job may
//! leave partial Kafka state that re-driving the job duplicates.
//! Consumer-side idempotency (`(job_id, row_index)` is a
//! deduplication key) is the documented user responsibility.

pub mod config;
pub mod producer;

pub use config::KafkaConfig;
pub use producer::{KafkaError, KafkaProducer};
