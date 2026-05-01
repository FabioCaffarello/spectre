// SPDX-License-Identifier: Apache-2.0

//! HTTP webhook client + per-job session.
//!
//! [`WebhookClient`] holds a single shared `reqwest::Client`
//! for the engine's lifetime — connection pooling and
//! keep-alive amortise across all webhook-sinked jobs.
//! [`WebhookSession`] is constructed per `RunJob` and owns the
//! buffered batch + the per-job headers (job ID, driver).

use std::time::Duration;

use reqwest::header::{HeaderMap, HeaderName, HeaderValue};

use super::config::{Method, WebhookConfig};

const USER_AGENT_PREFIX: &str = concat!("spectre-engine/", env!("CARGO_PKG_VERSION"));
const HEADER_JOB_ID: &str = "x-spectre-job-id";
const HEADER_DRIVER: &str = "x-spectre-driver";
const HEADER_BATCH_SIZE: &str = "x-spectre-batch-size";
const HEADER_ROW_COUNT: &str = "x-spectre-row-count";
const CONTENT_TYPE: &str = "application/x-ndjson";

/// Maximum POST attempts (initial + 2 retries). Hard-coded in
/// v1alpha1 per ADR-0024 §4 + §8.
const MAX_ATTEMPTS: u32 = 3;

/// Initial backoff delay in milliseconds. Doubled per attempt;
/// `200 → 400 → 800` ms.
const BASE_BACKOFF_MS: u64 = 200;

/// Per-request total timeout. Generous default for slow webhook
/// receivers; v1alpha2 may add an env override.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(30);

/// Errors raised while delivering rows to a webhook receiver.
#[derive(Debug, thiserror::Error)]
pub enum WebhookError {
    /// Per-job config (url / method / `batch_size`) failed
    /// validation.
    #[error("webhook config: {0}")]
    Config(#[from] super::config::WebhookConfigError),

    /// Failed to construct a [`HeaderValue`] from a per-job
    /// input (e.g. invalid UTF-8 in the driver name). Treated
    /// as an internal invariant violation — the inputs are
    /// always ASCII in practice.
    #[error("webhook: header construction failed: {0}")]
    InvalidHeader(String),

    /// All retries exhausted — the receiver returned a transient
    /// error every time. The body excerpt is a short prefix of
    /// the last response body for diagnostic purposes.
    #[error("webhook: post failed after {attempts} attempts: {status} {body_excerpt}")]
    PostFailed {
        /// Number of attempts made (always `MAX_ATTEMPTS`).
        attempts: u32,
        /// Last status code seen, or 0 for transport errors.
        status: u16,
        /// Truncated body excerpt for diagnostic context.
        body_excerpt: String,
    },

    /// The receiver returned a fatal status (4xx other than
    /// 429). No retries.
    #[error("webhook: fatal {status}: {body_excerpt}")]
    FatalStatus {
        /// HTTP status code.
        status: u16,
        /// Truncated body excerpt.
        body_excerpt: String,
    },

    /// Transport failure with no retries remaining (e.g.
    /// malformed URL, body-encoding error). Surfaced as
    /// `WEBHOOK_POST_FAILED` with attempt count 1.
    #[error("webhook: transport: {0}")]
    Transport(String),
}

/// Engine-level webhook client. Wraps a single connection-pooled
/// `reqwest::Client`. Construction is **infallible** — there is
/// no broker-style validation step at engine startup.
pub struct WebhookClient {
    inner: reqwest::Client,
}

impl WebhookClient {
    /// Construct a new webhook client. Builds a single
    /// connection-pooled `reqwest::Client` with the engine's
    /// canonical `User-Agent` and the per-request timeout
    /// applied at the builder layer (so it carries through to
    /// every session).
    ///
    /// # Panics
    ///
    /// Panics only if `reqwest::Client::builder` fails — which
    /// only happens for invalid TLS configuration that the
    /// engine's compile-time feature flags rule out. In practice
    /// this constructor never fails.
    #[must_use]
    pub fn new() -> Self {
        let inner = reqwest::Client::builder()
            .user_agent(USER_AGENT_PREFIX)
            .timeout(REQUEST_TIMEOUT)
            .build()
            .expect("reqwest client builder");
        Self { inner }
    }

    /// Open a per-job session. Each session owns the buffered
    /// batch and the per-job headers (job UUID, driver).
    ///
    /// # Errors
    ///
    /// Surfaces [`WebhookError::InvalidHeader`] if the supplied
    /// `job_id` or `driver` strings cannot be encoded as
    /// `HeaderValue` — in practice never (UUIDs and the three
    /// reference adapter names are ASCII).
    pub fn session(
        &self,
        cfg: WebhookConfig,
        job_id: &str,
        driver: &str,
    ) -> Result<WebhookSession<'_>, WebhookError> {
        let mut base_headers = HeaderMap::new();
        let job_id_value = HeaderValue::from_str(job_id)
            .map_err(|e| WebhookError::InvalidHeader(e.to_string()))?;
        let driver_value = HeaderValue::from_str(driver)
            .map_err(|e| WebhookError::InvalidHeader(e.to_string()))?;
        let job_id_name = HeaderName::from_static(HEADER_JOB_ID);
        let driver_name = HeaderName::from_static(HEADER_DRIVER);
        base_headers.insert(job_id_name, job_id_value);
        base_headers.insert(driver_name, driver_value);

        Ok(WebhookSession {
            client: self,
            cfg,
            base_headers,
            batch: Vec::new(),
        })
    }
}

impl Default for WebhookClient {
    fn default() -> Self {
        Self::new()
    }
}

/// Per-job session state. Accumulates rows in a `Vec<String>`
/// until the configured batch fills (or [`WebhookSession::finalise`]
/// is called), then flushes as a single HTTP request.
pub struct WebhookSession<'c> {
    client: &'c WebhookClient,
    cfg: WebhookConfig,
    base_headers: HeaderMap,
    batch: Vec<String>,
}

impl WebhookSession<'_> {
    /// Push one JSON-Lines-formatted row into the batch. When
    /// the per-row mode is selected (`batch_size == 0`) flushes
    /// immediately. When batched, flushes only when the batch
    /// reaches the configured size.
    ///
    /// # Errors
    ///
    /// Returns the same variants as
    /// [`WebhookSession::flush`] when the auto-flush triggers.
    pub async fn push_row(&mut self, json_line: String) -> Result<(), WebhookError> {
        self.batch.push(json_line);
        let target = if self.cfg.batch_size == 0 {
            1
        } else {
            self.cfg.batch_size as usize
        };
        if self.batch.len() >= target {
            self.flush().await?;
        }
        Ok(())
    }

    /// Flush any buffered rows. Called automatically when the
    /// batch fills and explicitly by the dispatch path at the
    /// end of a job.
    ///
    /// # Errors
    ///
    /// See [`WebhookError`] variants. Transport errors retry
    /// up to [`MAX_ATTEMPTS`] with exponential backoff;
    /// non-429 4xx is fatal on the first attempt.
    pub async fn flush(&mut self) -> Result<(), WebhookError> {
        if self.batch.is_empty() {
            return Ok(());
        }
        let body = self.batch.join("\n") + "\n";
        let row_count = self.batch.len();
        let mut headers = self.base_headers.clone();
        if self.cfg.batch_size > 0 {
            headers.insert(
                HeaderName::from_static(HEADER_BATCH_SIZE),
                HeaderValue::from(self.cfg.batch_size),
            );
        }
        let row_count_value = u32::try_from(row_count).unwrap_or(u32::MAX);
        headers.insert(
            HeaderName::from_static(HEADER_ROW_COUNT),
            HeaderValue::from(row_count_value),
        );

        self.client
            .post_with_retry(&self.cfg.url, self.cfg.method, headers, body)
            .await?;
        self.batch.clear();
        Ok(())
    }

    /// Final flush at end-of-job. Equivalent to [`Self::flush`]
    /// but reads more clearly at the dispatch site.
    ///
    /// # Errors
    ///
    /// See [`WebhookError`] variants.
    pub async fn finalise(&mut self) -> Result<(), WebhookError> {
        self.flush().await
    }

    /// Report whether any rows are buffered. Exposed for unit
    /// testing of the auto-flush boundary.
    #[cfg(test)]
    fn batch_len(&self) -> usize {
        self.batch.len()
    }
}

impl WebhookClient {
    /// Send `body` to `url` via `method` with bounded retry on
    /// transient errors. Used by [`WebhookSession::flush`].
    async fn post_with_retry(
        &self,
        url: &str,
        method: Method,
        headers: HeaderMap,
        body: String,
    ) -> Result<(), WebhookError> {
        let body_bytes = body.into_bytes();
        let mut last_status: u16 = 0;
        let mut last_excerpt: String = String::new();

        for attempt in 0..MAX_ATTEMPTS {
            let mut request = match method {
                Method::Post => self.inner.post(url),
                Method::Put => self.inner.put(url),
            };
            request = request
                .headers(headers.clone())
                .header("content-type", CONTENT_TYPE)
                .body(body_bytes.clone());

            match request.send().await {
                Ok(resp) => {
                    let status = resp.status();
                    if status.is_success() {
                        return Ok(());
                    }
                    let code = status.as_u16();
                    let body_text = resp.text().await.unwrap_or_default();
                    last_status = code;
                    last_excerpt = truncate(&body_text, 256);
                    if status.is_client_error() && code != 429 {
                        return Err(WebhookError::FatalStatus {
                            status: code,
                            body_excerpt: last_excerpt,
                        });
                    }
                    // 5xx or 429 — retry if attempts remain.
                }
                Err(e) => {
                    last_status = 0;
                    last_excerpt = format!("transport: {e}");
                    if !is_retryable_transport(&e) {
                        return Err(WebhookError::Transport(e.to_string()));
                    }
                }
            }

            if attempt + 1 < MAX_ATTEMPTS {
                let backoff = backoff_ms(attempt);
                tokio::time::sleep(Duration::from_millis(backoff)).await;
            }
        }

        Err(WebhookError::PostFailed {
            attempts: MAX_ATTEMPTS,
            status: last_status,
            body_excerpt: last_excerpt,
        })
    }
}

/// Compute the backoff delay for `attempt` (0-indexed): 200 →
/// 400 → 800 ms. Conservative jitter (no random crate dep): a
/// 10% wobble derived from the system clock keeps thundering
/// herds in check without adding rand to the engine.
fn backoff_ms(attempt: u32) -> u64 {
    let base = BASE_BACKOFF_MS << attempt;
    // Jitter range: ±10% of base, derived from system time
    // nanoseconds so independent processes don't synchronise.
    let jitter = (base / 10).max(1);
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| u64::from(d.subsec_nanos()));
    let offset = nanos % (jitter * 2);
    base + offset.saturating_sub(jitter)
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() <= max {
        s.to_owned()
    } else {
        format!("{}…", &s[..max])
    }
}

/// Decide whether a transport-layer error is worth retrying.
/// Treats connect / timeout / body-stream errors as retryable;
/// builder / URL-parse errors are fatal.
fn is_retryable_transport(e: &reqwest::Error) -> bool {
    e.is_timeout() || e.is_connect() || e.is_request() || e.is_body()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn user_agent_carries_version() {
        assert!(USER_AGENT_PREFIX.starts_with("spectre-engine/"));
    }

    #[test]
    fn backoff_progression_doubles() {
        let a0 = BASE_BACKOFF_MS;
        let a1 = BASE_BACKOFF_MS << 1;
        let a2 = BASE_BACKOFF_MS << 2;
        assert_eq!(a0, 200);
        assert_eq!(a1, 400);
        assert_eq!(a2, 800);
    }

    #[test]
    fn truncate_short_string_passthrough() {
        assert_eq!(truncate("abc", 10), "abc");
    }

    #[test]
    fn truncate_long_string_appends_ellipsis() {
        let s = "0123456789";
        let out = truncate(s, 4);
        assert_eq!(out, "0123…");
    }

    #[test]
    fn webhook_client_default_constructs() {
        let _ = WebhookClient::default();
    }

    #[tokio::test]
    async fn session_buffers_until_batch_fills() {
        let client = WebhookClient::new();
        let cfg = WebhookConfig::parse(
            // URL won't be dialled — flush is not triggered until
            // batch reaches 3.
            "https://localhost.invalid/spectre",
            "POST",
            3,
        )
        .expect("config");
        let mut sess = client
            .session(cfg, "job-uuid", "playwright")
            .expect("session");
        sess.push_row(r#"{"row":1}"#.to_owned())
            .await
            .expect("push 1");
        sess.push_row(r#"{"row":2}"#.to_owned())
            .await
            .expect("push 2");
        // Two rows buffered; no flush yet.
        assert_eq!(sess.batch_len(), 2);
    }

    #[test]
    fn finalise_on_empty_batch_is_noop() {
        // Wraps the future to keep the test sync; flush returns
        // Ok(()) immediately when batch is empty.
        let client = WebhookClient::new();
        let cfg = WebhookConfig::parse("https://localhost.invalid/", "POST", 0).expect("cfg");
        let mut sess = client.session(cfg, "j", "playwright").expect("sess");
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("rt");
        rt.block_on(async { sess.finalise().await.expect("finalise") });
    }
}
