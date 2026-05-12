// SPDX-License-Identifier: Apache-2.0

//! Typed gRPC client wrapping the tonic-generated stubs.
//!
//! `ProxyClient` is `Clone`-cheap because the underlying
//! `tonic::transport::Channel` is `Clone`. Methods take `&self`
//! so the caller can share one `ProxyClient` across tokio
//! tasks without arc-wrapping.

use std::time::Duration;

use tonic::transport::Channel;
use tonic::{Code, Request};
use tracing::{Instrument as _, debug, info_span, warn};

use crate::error::ProxyClientError;
use crate::pb;

/// Default retry policy: 3 attempts total, 100ms initial
/// backoff doubled per retry, ceiling 2s.
pub const DEFAULT_RETRY_POLICY: RetryPolicy = RetryPolicy {
    max_attempts: 3,
    initial_backoff: Duration::from_millis(100),
    max_backoff: Duration::from_secs(2),
};

/// Per-call retry configuration. Constants for the common
/// shapes are exported at the crate root; callers needing
/// custom policy construct via the public fields.
#[derive(Debug, Clone, Copy)]
pub struct RetryPolicy {
    /// Total attempts including the first call. `1` disables
    /// retries.
    pub max_attempts: u32,
    /// Backoff before the first retry. Doubled per subsequent
    /// retry up to `max_backoff`.
    pub initial_backoff: Duration,
    /// Upper bound on backoff. Prevents pathological growth
    /// for high `max_attempts`.
    pub max_backoff: Duration,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        DEFAULT_RETRY_POLICY
    }
}

/// Typed gRPC client for `spectre.proxy.v1alpha1.Proxy`.
#[derive(Debug, Clone)]
pub struct ProxyClient {
    inner: pb::proxy_client::ProxyClient<Channel>,
    retry: RetryPolicy,
}

impl ProxyClient {
    /// Construct a `ProxyClient` from a tonic `Channel`. The
    /// caller is responsible for constructing the channel with
    /// the right TLS / endpoint / interceptors; the SDK does
    /// not impose channel-level policy.
    pub fn new(channel: Channel) -> Self {
        Self {
            inner: pb::proxy_client::ProxyClient::new(channel),
            retry: DEFAULT_RETRY_POLICY,
        }
    }

    /// Replace the client's retry policy. Returns the same
    /// client by value so it composes with `ProxyClient::new`
    /// in a single expression.
    #[must_use]
    pub fn with_retry_policy(mut self, retry: RetryPolicy) -> Self {
        self.retry = retry;
        self
    }

    /// Issue an `Acquire` RPC.
    ///
    /// # Errors
    ///
    /// Returns [`ProxyClientError::Transport`] for channel-level
    /// failures, [`ProxyClientError::Status`] for application
    /// errors that aren't retriable, or
    /// [`ProxyClientError::RetriesExhausted`] when all configured
    /// attempts failed with retriable errors.
    pub async fn acquire(
        &self,
        req: pb::AcquireRequest,
    ) -> Result<pb::AcquireResponse, ProxyClientError> {
        let span = info_span!(
            "spectre.proxy.v1alpha1.Proxy/Acquire",
            otel.kind = "client",
            tenant_id = %req.tenant_id,
            target_domain = %req.target_domain,
        );
        self.with_retry(|| {
            let mut inner = self.inner.clone();
            let req = req.clone();
            async move { inner.acquire(Request::new(req)).await }
        })
        .instrument(span)
        .await
        .map(tonic::Response::into_inner)
    }

    /// Issue an `AcquireBatch` RPC.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire`].
    pub async fn acquire_batch(
        &self,
        req: pb::AcquireBatchRequest,
    ) -> Result<pb::AcquireBatchResponse, ProxyClientError> {
        let span = info_span!(
            "spectre.proxy.v1alpha1.Proxy/AcquireBatch",
            otel.kind = "client",
            count = req.count,
        );
        self.with_retry(|| {
            let mut inner = self.inner.clone();
            let req = req.clone();
            async move { inner.acquire_batch(Request::new(req)).await }
        })
        .instrument(span)
        .await
        .map(tonic::Response::into_inner)
    }

    /// Issue a `Release` RPC.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire`].
    pub async fn release(
        &self,
        req: pb::ReleaseRequest,
    ) -> Result<pb::ReleaseResponse, ProxyClientError> {
        let span = info_span!(
            "spectre.proxy.v1alpha1.Proxy/Release",
            otel.kind = "client",
            lease_id = %req.lease_id,
            tenant_id = %req.tenant_id,
        );
        self.with_retry(|| {
            let mut inner = self.inner.clone();
            let req = req.clone();
            async move { inner.release(Request::new(req)).await }
        })
        .instrument(span)
        .await
        .map(tonic::Response::into_inner)
    }

    /// Issue a `ReportFailure` RPC.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire`].
    pub async fn report_failure(
        &self,
        req: pb::ReportFailureRequest,
    ) -> Result<pb::ReportFailureResponse, ProxyClientError> {
        let span = info_span!(
            "spectre.proxy.v1alpha1.Proxy/ReportFailure",
            otel.kind = "client",
            lease_id = %req.lease_id,
            tenant_id = %req.tenant_id,
        );
        self.with_retry(|| {
            let mut inner = self.inner.clone();
            let req = req.clone();
            async move { inner.report_failure(Request::new(req)).await }
        })
        .instrument(span)
        .await
        .map(tonic::Response::into_inner)
    }

    /// Issue a `BudgetStatus` RPC.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire`].
    pub async fn budget_status(
        &self,
        req: pb::BudgetStatusRequest,
    ) -> Result<pb::BudgetStatusResponse, ProxyClientError> {
        let span = info_span!(
            "spectre.proxy.v1alpha1.Proxy/BudgetStatus",
            otel.kind = "client",
        );
        self.with_retry(|| {
            let mut inner = self.inner.clone();
            let req = req.clone();
            async move { inner.budget_status(Request::new(req)).await }
        })
        .instrument(span)
        .await
        .map(tonic::Response::into_inner)
    }

    /// Generic retry wrapper used by every RPC. Calls `f`,
    /// inspects the result, and re-attempts on retriable
    /// errors (UNAVAILABLE / DEADLINE_EXCEEDED / INTERNAL) up
    /// to `retry.max_attempts` times with exponential backoff
    /// capped at `retry.max_backoff`.
    async fn with_retry<F, Fut, T>(&self, mut f: F) -> Result<T, ProxyClientError>
    where
        F: FnMut() -> Fut,
        Fut: std::future::Future<Output = Result<T, tonic::Status>>,
    {
        let mut last_status: Option<tonic::Status> = None;
        let mut backoff = self.retry.initial_backoff;

        for attempt in 1..=self.retry.max_attempts {
            match f().await {
                Ok(value) => return Ok(value),
                Err(status) if is_retriable(&status) && attempt < self.retry.max_attempts => {
                    warn!(
                        attempt,
                        code = ?status.code(),
                        backoff_ms = backoff.as_millis() as u64,
                        "retriable error; backing off"
                    );
                    tokio::time::sleep(backoff).await;
                    backoff = (backoff * 2).min(self.retry.max_backoff);
                    last_status = Some(status);
                }
                Err(status) if is_retriable(&status) => {
                    // Last attempt — surface as
                    // RetriesExhausted so the caller can
                    // distinguish "tried but couldn't" from
                    // "first attempt rejected by server".
                    return Err(ProxyClientError::RetriesExhausted {
                        attempts: attempt,
                        source: Box::new(status),
                    });
                }
                Err(status) => {
                    // Non-retriable error — propagate
                    // directly without entering RetriesExhausted.
                    debug!(
                        attempt,
                        code = ?status.code(),
                        "non-retriable error; propagating"
                    );
                    return Err(ProxyClientError::Status(status));
                }
            }
        }
        // Unreachable in practice — the for loop covers every
        // attempt and either returns or sets `last_status`.
        // The fall-through is here for the type checker.
        Err(ProxyClientError::RetriesExhausted {
            attempts: self.retry.max_attempts,
            source: last_status
                .map(|s| Box::new(s) as Box<dyn std::error::Error + Send + Sync>)
                .unwrap_or_else(|| {
                    Box::new(tonic::Status::internal(
                        "retry loop exhausted without status",
                    ))
                }),
        })
    }
}

/// Classify a gRPC status as retriable. UNAVAILABLE +
/// DEADLINE_EXCEEDED + INTERNAL are the transient categories;
/// other codes (INVALID_ARGUMENT, PERMISSION_DENIED, etc.)
/// indicate the caller / config is wrong and a retry won't fix
/// it.
fn is_retriable(status: &tonic::Status) -> bool {
    matches!(
        status.code(),
        Code::Unavailable | Code::DeadlineExceeded | Code::Internal
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_retry_policy_is_three_attempts() {
        let p = RetryPolicy::default();
        assert_eq!(p.max_attempts, 3);
        assert_eq!(p.initial_backoff, Duration::from_millis(100));
        assert_eq!(p.max_backoff, Duration::from_secs(2));
    }

    #[test]
    fn retriable_status_codes_match_transient_set() {
        assert!(is_retriable(&tonic::Status::unavailable("svc down")));
        assert!(is_retriable(&tonic::Status::deadline_exceeded("slow")));
        assert!(is_retriable(&tonic::Status::internal("boom")));
        assert!(!is_retriable(&tonic::Status::invalid_argument("bad req")));
        assert!(!is_retriable(&tonic::Status::permission_denied("nope")));
        assert!(!is_retriable(&tonic::Status::not_found("404")));
        assert!(!is_retriable(&tonic::Status::failed_precondition("state")));
    }
}
