// SPDX-License-Identifier: Apache-2.0

//! Engine-side consumer for the proxy-broker infra-service.
//!
//! Wraps the [`spectre_sdk_proxy_v1alpha1::ProxyClient`] with:
//!
//! - **`CircuitBreaker`** (per ADR-0037 §5.3) — opens after
//!   5 consecutive failures; 30s cooldown. When open, callers
//!   degrade per ADR-0037 §5.1 (proxy-broker is a required
//!   service; the engine fails the job rather than continuing
//!   without a proxy).
//! - **`Cache<String, Lease>`** keyed by session id (per
//!   ADR-0037 §4.2) — sticky leases skip the broker round
//!   trip on re-acquire within the lease's lifetime.
//! - **Batch routing** — `acquire_for_session(...)` takes one
//!   request; `acquire_batch(...)` exposes the batch RPC for
//!   the N-concurrent-session shape per ADR-0037 §4.1.
//!
//! This wrapper is **the template for every Wave 5-10 service
//! consumer**. `services::captcha::EngineCaptchaClient` (W5.2),
//! `services::schema_registry::EngineSchemaClient` (W6), etc.
//! follow the exact same shape: SDK + breaker + (per-scope
//! cache where the service has cacheable state).

use std::time::Duration;

use spectre_sdk_proxy_v1alpha1::{
    ProxyClient, ProxyClientError,
    pb::{
        AcquireBatchRequest, AcquireBatchResponse, AcquireRequest, BudgetStatusRequest,
        BudgetStatusResponse, Lease, ReleaseRequest, ReleaseResponse, ReportFailureRequest,
        ReportFailureResponse,
    },
};
use tracing::{debug, warn};

use super::cache::Cache;
use super::circuit_breaker::{CircuitBreaker, CircuitError};

/// Default proxy-broker circuit-breaker name (used in tracing
/// emissions + observability dashboards).
pub const BREAKER_NAME: &str = "proxy-broker";

/// Per-session sticky-lease cache TTL. Caps the in-memory
/// cache horizon so a leaked session id doesn't pin a lease
/// in the cache indefinitely. The cache exists for the
/// happy-path re-acquire-within-session shape; long-lived
/// caches fall through to the broker (which is the source of
/// truth for expiry).
pub const SESSION_CACHE_TTL: Duration = Duration::from_secs(15 * 60);

/// Engine-side proxy-broker consumer. Cheap to `Clone` — the
/// inner SDK channel, breaker, and cache are all `Arc`-backed.
#[derive(Clone)]
pub struct EngineProxyClient {
    sdk: ProxyClient,
    breaker: CircuitBreaker,
    session_cache: Cache<String, Lease>,
}

impl EngineProxyClient {
    /// Construct an engine-side consumer wrapping `sdk` with
    /// the default breaker policy + session cache TTL.
    #[must_use]
    pub fn new(sdk: ProxyClient) -> Self {
        Self {
            sdk,
            breaker: CircuitBreaker::new(BREAKER_NAME),
            session_cache: Cache::with_ttl(SESSION_CACHE_TTL),
        }
    }

    /// Construct with caller-supplied breaker + cache. Useful
    /// for tests substituting tighter timings; also future
    /// per-tenant cache isolation could plug in here.
    #[must_use]
    pub fn with_components(
        sdk: ProxyClient,
        breaker: CircuitBreaker,
        session_cache: Cache<String, Lease>,
    ) -> Self {
        Self {
            sdk,
            breaker,
            session_cache,
        }
    }

    /// Acquire a lease for `session_id`. Sticky requests check
    /// the cache first; cache miss falls through to the SDK
    /// via the breaker.
    ///
    /// # Errors
    ///
    /// `EngineProxyError::Open` when the breaker is open;
    /// `EngineProxyError::Underlying` for SDK-surfaced errors
    /// (transport, retried-status, application status).
    pub async fn acquire_for_session(
        &self,
        session_id: &str,
        req: AcquireRequest,
    ) -> Result<Lease, EngineProxyError> {
        if req.sticky {
            if let Some(lease) = self.session_cache.get(&session_id.to_string()).await {
                debug!(
                    session_id,
                    lease_id = %lease.lease_id,
                    "proxy: returning cached sticky lease"
                );
                return Ok(lease);
            }
        }
        let resp = self
            .breaker
            .call(|| self.sdk.acquire(req.clone()))
            .await
            .map_err(EngineProxyError::from_circuit)?;
        let lease = resp.lease.ok_or(EngineProxyError::MalformedResponse)?;
        if req.sticky {
            self.session_cache
                .insert(session_id.to_string(), lease.clone())
                .await;
        }
        Ok(lease)
    }

    /// Acquire N leases in one call per ADR-0037 §4.1.
    /// Currently bypasses the session cache (batched leases
    /// span N sessions). The caller can later `insert` each
    /// lease into the cache under the corresponding session
    /// id if it wants sticky behaviour for the batch.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire_for_session`].
    pub async fn acquire_batch(
        &self,
        req: AcquireBatchRequest,
    ) -> Result<AcquireBatchResponse, EngineProxyError> {
        self.breaker
            .call(|| self.sdk.acquire_batch(req.clone()))
            .await
            .map_err(EngineProxyError::from_circuit)
    }

    /// Release a lease. Invalidates the session-cache entry
    /// when the caller knows the session id; otherwise the
    /// entry expires via the cache TTL.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire_for_session`].
    pub async fn release(
        &self,
        req: ReleaseRequest,
        session_id: Option<&str>,
    ) -> Result<ReleaseResponse, EngineProxyError> {
        if let Some(sid) = session_id {
            self.session_cache.invalidate(&sid.to_string()).await;
        }
        self.breaker
            .call(|| self.sdk.release(req.clone()))
            .await
            .map_err(EngineProxyError::from_circuit)
    }

    /// Report a proxy failure. Also invalidates the session
    /// cache entry — a failing lease shouldn't be re-served
    /// from cache for subsequent sticky requests.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire_for_session`].
    pub async fn report_failure(
        &self,
        req: ReportFailureRequest,
        session_id: Option<&str>,
    ) -> Result<ReportFailureResponse, EngineProxyError> {
        if let Some(sid) = session_id {
            self.session_cache.invalidate(&sid.to_string()).await;
        }
        self.breaker
            .call(|| self.sdk.report_failure(req.clone()))
            .await
            .map_err(EngineProxyError::from_circuit)
    }

    /// Query budget for a scope.
    ///
    /// # Errors
    ///
    /// Same envelope as [`Self::acquire_for_session`].
    pub async fn budget_status(
        &self,
        req: BudgetStatusRequest,
    ) -> Result<BudgetStatusResponse, EngineProxyError> {
        self.breaker
            .call(|| self.sdk.budget_status(req.clone()))
            .await
            .map_err(EngineProxyError::from_circuit)
    }

    /// Expose a clone of the breaker for ops-driven manual
    /// reset + state inspection from a future admin RPC.
    #[must_use]
    pub fn breaker(&self) -> CircuitBreaker {
        self.breaker.clone()
    }
}

/// Engine-side error wrapper. Collapses `CircuitError` +
/// `ProxyClientError` into a single shape the engine surfaces
/// to job execution.
#[derive(Debug, thiserror::Error)]
pub enum EngineProxyError {
    /// Circuit breaker is open; broker treated as unavailable
    /// per ADR-0037 §5.1.
    #[error("proxy-broker circuit open")]
    Open,
    /// SDK-surfaced error (transport / retried status / app status).
    #[error("proxy-broker SDK error: {0}")]
    Underlying(#[from] ProxyClientError),
    /// Broker returned a success response with no payload.
    /// Wire-contract bug; the caller fails fast.
    #[error("proxy-broker returned response without lease payload")]
    MalformedResponse,
}

impl EngineProxyError {
    fn from_circuit(err: CircuitError<ProxyClientError>) -> Self {
        match err {
            CircuitError::Open => {
                warn!("proxy: circuit breaker open; broker treated as unavailable");
                EngineProxyError::Open
            }
            CircuitError::Underlying(e) => EngineProxyError::Underlying(e),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The EngineProxyClient's SDK + breaker + cache integration
    // is exercised end-to-end by the smoke verifier in
    // `tools/test/verify-proxy-broker-smoke.sh` (Cluster G) and
    // by the cache + breaker module's own unit tests. The
    // unit tests here cover the shape we can verify without a
    // real broker channel.

    #[test]
    fn breaker_name_constant_matches_observability_convention() {
        // The breaker name surfaces in tracing emissions and
        // future per-service metric labels. Pin it so a typo
        // doesn't silently break dashboard queries.
        assert_eq!(BREAKER_NAME, "proxy-broker");
    }

    #[test]
    fn session_cache_ttl_is_capped_to_15_minutes() {
        // The TTL caps in-memory exposure of leaked session
        // ids. Codify the policy here so a future drive-by
        // tweak surfaces in review.
        assert_eq!(SESSION_CACHE_TTL, Duration::from_secs(900));
    }
}
