// SPDX-License-Identifier: Apache-2.0

//! Kafka producer configuration sourced from the process environment.
//!
//! The engine reads `SPECTRE_KAFKA_BROKERS` (required) and
//! `SPECTRE_KAFKA_LINGER_MS` (optional) at startup. The brokers
//! string follows the librdkafka convention — comma-separated
//! `host:port` entries (ADR-0023 §12).

use std::env;

const BROKERS_ENV: &str = "SPECTRE_KAFKA_BROKERS";
const LINGER_MS_ENV: &str = "SPECTRE_KAFKA_LINGER_MS";

/// Default `linger.ms` for the producer. Small enough to keep
/// per-row latency low for dev workloads; production deployments
/// override via [`LINGER_MS_ENV`] when batching is preferred.
pub const DEFAULT_LINGER_MS: u32 = 10;

/// Parsed Kafka producer configuration.
#[derive(Clone, Debug)]
pub struct KafkaConfig {
    /// Comma-separated broker list, suitable for librdkafka's
    /// `bootstrap.servers` setting.
    pub brokers: String,
    /// Producer-side batching window in milliseconds.
    pub linger_ms: u32,
}

/// Errors raised when the environment lacks the required Kafka
/// configuration or carries malformed values.
#[derive(Debug, thiserror::Error)]
pub enum KafkaConfigError {
    /// `SPECTRE_KAFKA_BROKERS` was not set in the process environment.
    #[error("{BROKERS_ENV} must be set")]
    MissingBrokers,

    /// `SPECTRE_KAFKA_LINGER_MS` was set but did not parse as a `u32`.
    #[error("{LINGER_MS_ENV} must be a non-negative integer (got {value:?})")]
    InvalidLingerMs {
        /// The raw value the env var carried.
        value: String,
    },
}

impl KafkaConfig {
    /// Build a config from `SPECTRE_KAFKA_BROKERS` (required) and
    /// `SPECTRE_KAFKA_LINGER_MS` (optional, defaults to
    /// [`DEFAULT_LINGER_MS`]).
    ///
    /// # Errors
    ///
    /// Returns [`KafkaConfigError::MissingBrokers`] when the
    /// brokers env var is unset and
    /// [`KafkaConfigError::InvalidLingerMs`] when the linger env
    /// var is set but unparseable.
    pub fn from_env() -> Result<Self, KafkaConfigError> {
        let brokers = env::var(BROKERS_ENV).map_err(|_| KafkaConfigError::MissingBrokers)?;
        if brokers.trim().is_empty() {
            return Err(KafkaConfigError::MissingBrokers);
        }
        let linger_ms = match env::var(LINGER_MS_ENV) {
            Ok(raw) => raw
                .parse::<u32>()
                .map_err(|_| KafkaConfigError::InvalidLingerMs { value: raw })?,
            Err(_) => DEFAULT_LINGER_MS,
        };
        Ok(Self { brokers, linger_ms })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn config_struct_holds_supplied_values() {
        let cfg = KafkaConfig {
            brokers: "kafka:9092".to_owned(),
            linger_ms: 25,
        };
        assert_eq!(cfg.brokers, "kafka:9092");
        assert_eq!(cfg.linger_ms, 25);
    }

    #[test]
    fn default_linger_is_ten() {
        // Documents the §3 R4.4 producer-config commitment so a
        // downstream tunable change cannot drift silently.
        assert_eq!(DEFAULT_LINGER_MS, 10);
    }

    #[test]
    fn config_error_displays_env_var_name() {
        let err = KafkaConfigError::MissingBrokers;
        assert!(err.to_string().contains(BROKERS_ENV));
    }

    #[test]
    fn invalid_linger_error_carries_value() {
        let err = KafkaConfigError::InvalidLingerMs {
            value: "abc".to_owned(),
        };
        assert!(err.to_string().contains(LINGER_MS_ENV));
        assert!(err.to_string().contains("abc"));
    }
}
