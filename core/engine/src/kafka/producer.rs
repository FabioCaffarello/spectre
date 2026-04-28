// SPDX-License-Identifier: Apache-2.0

//! Async Kafka producer wrapping rdkafka's `FutureProducer`.
//!
//! One `KafkaProducer` per engine process. Constructed at startup
//! by [`KafkaProducer::from_env`], shared across in-flight RPCs by
//! cloning the inner `Arc`. `FutureProducer` is internally
//! reference-counted and thread-safe.

use std::time::Duration;

use rdkafka::config::ClientConfig;
use rdkafka::message::{Header, OwnedHeaders};
use rdkafka::producer::{FutureProducer, FutureRecord, Producer};

use super::config::{KafkaConfig, KafkaConfigError};

/// Default delivery timeout for a single `publish_row`. Matches the
/// `message.timeout.ms` setting on the producer; librdkafka enforces
/// the same value internally.
const DELIVERY_TIMEOUT: Duration = Duration::from_secs(30);

/// Initial metadata-fetch timeout used by [`KafkaProducer::from_env`]
/// to validate broker reachability at startup. Short so the engine
/// fails fast on a misconfigured broker URL rather than blocking the
/// whole startup sequence.
const STARTUP_METADATA_TIMEOUT: Duration = Duration::from_secs(5);

/// Errors raised when constructing a producer or publishing a row.
#[derive(Debug, thiserror::Error)]
pub enum KafkaError {
    /// Environment-level configuration error (missing or malformed).
    #[error("kafka config: {0}")]
    Config(#[from] KafkaConfigError),

    /// librdkafka could not construct the producer (bad client config).
    #[error("kafka: failed to construct producer: {0}")]
    Init(#[source] rdkafka::error::KafkaError),

    /// librdkafka could not reach the broker at startup.
    #[error("kafka: broker unreachable at startup: {0}")]
    Unreachable(String),

    /// A `publish_row` call did not deliver before the timeout or
    /// the broker rejected it.
    #[error("kafka: publish failed: {0}")]
    Publish(String),
}

/// Engine-level wrapper around a single `FutureProducer`.
pub struct KafkaProducer {
    producer: FutureProducer,
    brokers: String,
}

impl KafkaProducer {
    /// Construct a producer from `SPECTRE_KAFKA_BROKERS` (and the
    /// optional tunables) and verify the broker is reachable.
    ///
    /// Producer config (per ADR-0023 §3 R4.4 addendum):
    ///
    /// - `acks=all` — full ISR acknowledgment for durability
    /// - `enable.idempotence=true` — librdkafka idempotent producer,
    ///   no duplicate writes from intra-session retries
    /// - `compression.type=snappy` — moderate compression, low CPU
    /// - `linger.ms=<config>` — small batch window for low-latency
    ///   dev (override via `SPECTRE_KAFKA_LINGER_MS`)
    /// - `message.timeout.ms=30000` — matches [`DELIVERY_TIMEOUT`]
    ///
    /// # Errors
    ///
    /// - [`KafkaError::Config`] when the env vars are missing or
    ///   malformed.
    /// - [`KafkaError::Init`] when librdkafka rejects the client
    ///   config (typically bad URL syntax).
    /// - [`KafkaError::Unreachable`] when the metadata fetch fails
    ///   within [`STARTUP_METADATA_TIMEOUT`] — surfaced to the
    ///   binary which logs a warning and continues without Kafka
    ///   per ADR-0023 §6.
    pub async fn from_env() -> Result<Self, KafkaError> {
        let cfg = KafkaConfig::from_env()?;
        Self::from_config(cfg).await
    }

    /// Construct directly from a [`KafkaConfig`]. Public to keep
    /// the integration test surface minimal — production callers
    /// use [`KafkaProducer::from_env`].
    ///
    /// # Errors
    ///
    /// See [`KafkaProducer::from_env`] (sans the
    /// [`KafkaError::Config`] variant — `from_config` already has
    /// the parsed config).
    pub async fn from_config(cfg: KafkaConfig) -> Result<Self, KafkaError> {
        let producer: FutureProducer = ClientConfig::new()
            .set("bootstrap.servers", &cfg.brokers)
            .set("acks", "all")
            .set("enable.idempotence", "true")
            .set("compression.type", "snappy")
            .set("linger.ms", cfg.linger_ms.to_string())
            .set("message.timeout.ms", "30000")
            .create()
            .map_err(KafkaError::Init)?;

        // Validate broker reachability via fetch_metadata. The call
        // is blocking so it runs on the blocking pool; on success
        // the producer is ready, on failure the engine binary keeps
        // the warning + continue path (ADR-0023 §6).
        let probe_producer = producer.clone();
        let brokers_for_log = cfg.brokers.clone();
        tokio::task::spawn_blocking(move || {
            probe_producer
                .client()
                .fetch_metadata(None, STARTUP_METADATA_TIMEOUT)
                .map(|_| ())
                .map_err(|e| format!("{brokers_for_log}: {e}"))
        })
        .await
        .map_err(|e| KafkaError::Unreachable(format!("startup probe panicked: {e}")))?
        .map_err(KafkaError::Unreachable)?;

        Ok(Self {
            producer,
            brokers: cfg.brokers,
        })
    }

    /// Bootstrap brokers list this producer was constructed with.
    /// Exposed for log lines on startup.
    #[must_use]
    pub fn brokers(&self) -> &str {
        &self.brokers
    }

    /// Publish one extracted row to `topic`.
    ///
    /// Per ADR-0023 §3:
    ///
    /// - The partition key is the job UUID, so all rows for a job
    ///   land on the same partition (in-order delivery within a job).
    /// - Headers carry `job_id`, `row_index`, `driver`, `timestamp`
    ///   so consumers route or filter without parsing the body.
    /// - `body` is the JSONL row encoded as UTF-8.
    ///
    /// # Errors
    ///
    /// Returns [`KafkaError::Publish`] when librdkafka could not
    /// deliver before [`DELIVERY_TIMEOUT`] or the broker rejected
    /// the message (size limit, ACL violation, etc.). The engine
    /// surfaces this to the gRPC client as a `Failed` event with
    /// `error_code = "KAFKA_PUBLISH_FAILED"`.
    pub async fn publish_row(
        &self,
        topic: &str,
        job_id: &str,
        row_index: i64,
        driver: &str,
        timestamp: &str,
        body: &[u8],
    ) -> Result<(), KafkaError> {
        let row_index_str = row_index.to_string();
        let headers = OwnedHeaders::new()
            .insert(Header {
                key: "job_id",
                value: Some(job_id.as_bytes()),
            })
            .insert(Header {
                key: "row_index",
                value: Some(row_index_str.as_bytes()),
            })
            .insert(Header {
                key: "driver",
                value: Some(driver.as_bytes()),
            })
            .insert(Header {
                key: "timestamp",
                value: Some(timestamp.as_bytes()),
            });

        let record: FutureRecord<'_, str, [u8]> = FutureRecord::to(topic)
            .key(job_id)
            .payload(body)
            .headers(headers);

        self.producer
            .send(record, DELIVERY_TIMEOUT)
            .await
            .map(|_| ())
            .map_err(|(e, _msg)| KafkaError::Publish(e.to_string()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn delivery_timeout_matches_message_timeout_ms() {
        assert_eq!(DELIVERY_TIMEOUT.as_millis(), 30_000);
    }

    #[test]
    fn startup_metadata_timeout_is_short() {
        // Engine startup fails fast; an unreachable broker should
        // not block the binary for tens of seconds before falling
        // back to "kafka unavailable".
        assert!(STARTUP_METADATA_TIMEOUT.as_secs() <= 10);
    }
}
