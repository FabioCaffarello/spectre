// SPDX-License-Identifier: Apache-2.0

//! Per-job webhook sink configuration.
//!
//! Unlike Kafka and S3, webhook config is **per-job** — sourced
//! from each `RunJobRequest.webhook` field, not from the process
//! environment. This module parses the gRPC field shape into a
//! validated [`WebhookConfig`] for the dispatch path.

use std::str::FromStr;

/// HTTP method for webhook delivery. The CRD's
/// `WebhookSink.Method` enum admits POST and PUT; the engine
/// validates defence-in-depth.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Method {
    /// HTTP POST. Default when the CRD field is empty.
    Post,
    /// HTTP PUT. Less common; supported because the CRD admits it.
    Put,
}

impl Method {
    /// Wire-format method string suitable for `reqwest`.
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Method::Post => "POST",
            Method::Put => "PUT",
        }
    }
}

impl FromStr for Method {
    type Err = WebhookConfigError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s.trim().to_ascii_uppercase().as_str() {
            "" | "POST" => Ok(Method::Post),
            "PUT" => Ok(Method::Put),
            other => Err(WebhookConfigError::InvalidMethod(other.to_owned())),
        }
    }
}

/// Errors raised when validating per-job webhook config.
#[derive(Debug, thiserror::Error)]
pub enum WebhookConfigError {
    /// `WebhookSink.URL` was empty or whitespace-only. The CRD's
    /// `Pattern=^https?://.+$` and `MinLength=1` enforce this at
    /// admission; defence-in-depth in the engine catches a
    /// regenerated CRD without the rule.
    #[error("webhook url must be non-empty")]
    EmptyUrl,

    /// The supplied method was neither POST nor PUT (case-
    /// insensitive). The CRD's `Enum=POST;PUT` enforces this at
    /// admission.
    #[error("webhook method must be POST or PUT (got {0:?})")]
    InvalidMethod(String),

    /// `WebhookSink.BatchSize` was negative. The CRD's
    /// `Minimum=0` enforces this at admission.
    #[error("webhook batch_size must be >= 0 (got {0})")]
    NegativeBatchSize(i32),
}

/// Parsed per-job webhook configuration.
#[derive(Clone, Debug)]
pub struct WebhookConfig {
    /// The receiver URL.
    pub url: String,

    /// Method (`POST` or `PUT`).
    pub method: Method,

    /// Number of rows per HTTP request. `0` means one row per
    /// request (the CRD default).
    pub batch_size: u32,
}

impl WebhookConfig {
    /// Parse and validate the gRPC config message into a
    /// [`WebhookConfig`].
    ///
    /// # Errors
    ///
    /// - [`WebhookConfigError::EmptyUrl`] when `url` is empty.
    /// - [`WebhookConfigError::InvalidMethod`] when `method`
    ///   is non-empty and not POST/PUT.
    /// - [`WebhookConfigError::NegativeBatchSize`] when
    ///   `batch_size` is negative.
    pub fn parse(url: &str, method: &str, batch_size: i32) -> Result<Self, WebhookConfigError> {
        if url.trim().is_empty() {
            return Err(WebhookConfigError::EmptyUrl);
        }
        let method = Method::from_str(method)?;
        let batch_size = u32::try_from(batch_size)
            .map_err(|_| WebhookConfigError::NegativeBatchSize(batch_size))?;
        Ok(Self {
            url: url.trim().to_owned(),
            method,
            batch_size,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_accepts_valid_post() {
        let cfg = WebhookConfig::parse("https://example.com/spectre", "POST", 0).expect("ok");
        assert_eq!(cfg.url, "https://example.com/spectre");
        assert_eq!(cfg.method, Method::Post);
        assert_eq!(cfg.batch_size, 0);
    }

    #[test]
    fn parse_defaults_empty_method_to_post() {
        let cfg = WebhookConfig::parse("https://example.com/spectre", "", 5).expect("ok");
        assert_eq!(cfg.method, Method::Post);
        assert_eq!(cfg.batch_size, 5);
    }

    #[test]
    fn parse_accepts_put_lowercase() {
        let cfg = WebhookConfig::parse("https://example.com/spectre", "put", 0).expect("ok");
        assert_eq!(cfg.method, Method::Put);
    }

    #[test]
    fn parse_rejects_empty_url() {
        match WebhookConfig::parse("   ", "POST", 0) {
            Err(WebhookConfigError::EmptyUrl) => {}
            other => panic!("expected EmptyUrl, got {other:?}"),
        }
    }

    #[test]
    fn parse_rejects_invalid_method() {
        match WebhookConfig::parse("https://example.com/", "DELETE", 0) {
            Err(WebhookConfigError::InvalidMethod(m)) => assert_eq!(m, "DELETE"),
            other => panic!("expected InvalidMethod, got {other:?}"),
        }
    }

    #[test]
    fn parse_rejects_negative_batch_size() {
        match WebhookConfig::parse("https://example.com/", "POST", -1) {
            Err(WebhookConfigError::NegativeBatchSize(-1)) => {}
            other => panic!("expected NegativeBatchSize(-1), got {other:?}"),
        }
    }

    #[test]
    fn method_as_str_is_uppercase() {
        assert_eq!(Method::Post.as_str(), "POST");
        assert_eq!(Method::Put.as_str(), "PUT");
    }

    #[test]
    fn parse_trims_url_whitespace() {
        let cfg = WebhookConfig::parse("  https://example.com/  ", "POST", 0).expect("ok");
        assert_eq!(cfg.url, "https://example.com/");
    }
}
