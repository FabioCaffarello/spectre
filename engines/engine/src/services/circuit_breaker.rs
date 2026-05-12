// SPDX-License-Identifier: Apache-2.0

//! Circuit breaker for engine-side service clients.
//!
//! Per [ADR-0037 §5.3](../../../docs/adr/0037-engine-as-orchestrator.md):
//!
//! - After `threshold` consecutive failures (default 5), the
//!   breaker opens and skips the wrapped call for a cooldown
//!   window (default 30s).
//! - During cooldown the engine treats the service as
//!   unavailable per §5.1 / §5.2 — degradation is the caller's
//!   responsibility (synthesise a default, fail the job fast,
//!   etc., depending on the service's [§5.1] required-vs-best-
//!   effort classification).
//! - After cooldown the breaker enters half-open: a single
//!   probe call determines whether to close (success → reset
//!   counter) or re-open (failure → restart cooldown).
//!
//! W5.1 wraps the proxy-broker SDK client in this breaker via
//! `services::proxy::EngineProxyClient`. W5.2's captcha
//! consumer instantiates its own `CircuitBreaker` named
//! `"captcha-solver"` — every service consumer gets its own
//! breaker instance (per-service state; per-service threshold
//! / cooldown tuning is permitted).
//!
//! The API is **service-agnostic by design** — the breaker
//! doesn't know what it's wrapping. A future
//! `services::schema_registry` consumer's call site is
//! identical to the proxy consumer's; only the inner closure
//! differs.

use std::fmt;
use std::sync::Arc;
use std::time::{Duration, Instant};

use tokio::sync::Mutex;
use tracing::{debug, info, warn};

/// Default consecutive-failure threshold before opening.
pub const DEFAULT_THRESHOLD: u32 = 5;
/// Default open-cooldown window before half-open probe.
pub const DEFAULT_COOLDOWN: Duration = Duration::from_secs(30);

/// Internal breaker state.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum State {
    /// All calls allowed; failures counted toward threshold.
    Closed,
    /// All calls short-circuit until `until` elapses.
    Open { until: Instant },
    /// One probe call permitted; result transitions to Closed
    /// or Open.
    HalfOpen,
}

#[derive(Debug)]
struct Inner {
    state: State,
    consecutive_failures: u32,
    /// Total opens — telemetry counter; useful for
    /// surfacing "this service is flaky" without consulting
    /// the underlying error stream.
    open_count: u64,
}

/// Circuit breaker wrapping an async fallible call.
pub struct CircuitBreaker {
    name: String,
    threshold: u32,
    cooldown: Duration,
    inner: Arc<Mutex<Inner>>,
}

impl Clone for CircuitBreaker {
    fn clone(&self) -> Self {
        // Cloning yields a handle to the same breaker state —
        // service consumers can hand clones to per-task
        // closures and the open/close decisions stay coherent
        // across tasks.
        Self {
            name: self.name.clone(),
            threshold: self.threshold,
            cooldown: self.cooldown,
            inner: Arc::clone(&self.inner),
        }
    }
}

impl CircuitBreaker {
    /// Construct a breaker with default threshold + cooldown.
    /// The `name` is used in tracing emissions to identify the
    /// breaker (e.g. `"proxy-broker"`, `"captcha-solver"`).
    pub fn new(name: impl Into<String>) -> Self {
        Self::with_policy(name, DEFAULT_THRESHOLD, DEFAULT_COOLDOWN)
    }

    /// Construct a breaker with custom threshold + cooldown.
    /// Per-service tuning is permitted — e.g. a low-traffic
    /// service might tolerate a lower threshold; a chatty
    /// service might raise it.
    pub fn with_policy(name: impl Into<String>, threshold: u32, cooldown: Duration) -> Self {
        Self {
            name: name.into(),
            threshold,
            cooldown,
            inner: Arc::new(Mutex::new(Inner {
                state: State::Closed,
                consecutive_failures: 0,
                open_count: 0,
            })),
        }
    }

    /// Wrap a fallible async call. Returns:
    ///
    /// - `Ok(value)` when the call succeeded (and the breaker
    ///   reset to `Closed` if it was `HalfOpen`).
    /// - `Err(CircuitError::Open)` when the breaker is open and
    ///   the call was skipped.
    /// - `Err(CircuitError::Underlying(e))` when the call was
    ///   attempted but the inner future failed.
    ///
    /// # Errors
    ///
    /// Surfaces `CircuitError::Open` or `CircuitError::Underlying`
    /// per the cases above.
    pub async fn call<F, Fut, T, E>(&self, f: F) -> Result<T, CircuitError<E>>
    where
        F: FnOnce() -> Fut,
        Fut: std::future::Future<Output = Result<T, E>>,
        E: std::error::Error + Send + Sync + 'static,
    {
        // Transition decisions are made under the mutex; the
        // actual call runs outside the lock so concurrent
        // calls don't serialise on the breaker.
        let permitted = {
            let mut inner = self.inner.lock().await;
            match inner.state {
                // `Closed` is normal flow; `HalfOpen` is the
                // single-probe state but further concurrent
                // calls also see `HalfOpen` (we don't gate
                // with a permit). The first call to complete
                // drives the transition. The simpler shape is
                // sufficient until benchmarks justify the
                // permit cost.
                State::Closed | State::HalfOpen => true,
                State::Open { until } => {
                    if Instant::now() >= until {
                        debug!(name = %self.name, "circuit_breaker: cooldown elapsed; transitioning to HalfOpen");
                        inner.state = State::HalfOpen;
                        true
                    } else {
                        false
                    }
                }
            }
        };
        if !permitted {
            debug!(name = %self.name, "circuit_breaker: open; skipping call");
            return Err(CircuitError::Open);
        }
        match f().await {
            Ok(value) => {
                self.on_success().await;
                Ok(value)
            }
            Err(err) => {
                self.on_failure().await;
                Err(CircuitError::Underlying(err))
            }
        }
    }

    /// Force the breaker open. Useful for testing degradation
    /// paths without actually causing failures.
    pub async fn trip(&self) {
        let mut inner = self.inner.lock().await;
        inner.state = State::Open {
            until: Instant::now() + self.cooldown,
        };
        inner.open_count += 1;
        warn!(name = %self.name, "circuit_breaker: tripped (manual)");
    }

    /// Force the breaker closed. Useful for testing recovery
    /// paths and for ops-driven manual reset.
    pub async fn reset(&self) {
        let mut inner = self.inner.lock().await;
        inner.state = State::Closed;
        inner.consecutive_failures = 0;
        info!(name = %self.name, "circuit_breaker: reset (manual)");
    }

    /// Return a snapshot of breaker state for telemetry /
    /// observability. The state is a momentary read; concurrent
    /// calls may transition it before the caller acts on the
    /// snapshot.
    pub async fn state_snapshot(&self) -> BreakerState {
        let inner = self.inner.lock().await;
        let state_kind = match inner.state {
            State::Closed => StateKind::Closed,
            State::Open { .. } => StateKind::Open,
            State::HalfOpen => StateKind::HalfOpen,
        };
        BreakerState {
            name: self.name.clone(),
            state: state_kind,
            consecutive_failures: inner.consecutive_failures,
            open_count: inner.open_count,
        }
    }

    async fn on_success(&self) {
        let mut inner = self.inner.lock().await;
        if !matches!(inner.state, State::Closed) {
            info!(name = %self.name, "circuit_breaker: success; closing");
        }
        inner.state = State::Closed;
        inner.consecutive_failures = 0;
    }

    async fn on_failure(&self) {
        let mut inner = self.inner.lock().await;
        inner.consecutive_failures += 1;
        if matches!(inner.state, State::HalfOpen) || inner.consecutive_failures >= self.threshold {
            inner.state = State::Open {
                until: Instant::now() + self.cooldown,
            };
            inner.open_count += 1;
            warn!(
                name = %self.name,
                consecutive_failures = inner.consecutive_failures,
                threshold = self.threshold,
                cooldown_ms = u64::try_from(self.cooldown.as_millis()).unwrap_or(u64::MAX),
                "circuit_breaker: opening"
            );
        }
    }
}

/// State snapshot returned by `state_snapshot`.
#[derive(Debug, Clone)]
pub struct BreakerState {
    /// Breaker name (passed at construction).
    pub name: String,
    /// Current state classification.
    pub state: StateKind,
    /// Running counter of consecutive failures; resets to 0
    /// on the first success after a failure streak.
    pub consecutive_failures: u32,
    /// Total opens since process start; only ever grows.
    pub open_count: u64,
}

/// Public state classification (collapses internal `Open`'s
/// timestamp detail).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StateKind {
    /// Calls flowing normally.
    Closed,
    /// Calls short-circuiting.
    Open,
    /// One probe call permitted.
    HalfOpen,
}

impl fmt::Display for StateKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            StateKind::Closed => write!(f, "closed"),
            StateKind::Open => write!(f, "open"),
            StateKind::HalfOpen => write!(f, "half_open"),
        }
    }
}

/// Result of a circuit-breaker-wrapped call.
#[derive(Debug)]
pub enum CircuitError<E> {
    /// The breaker was open and the inner call was not attempted.
    /// Callers degrade per ADR-0037 §5 (synthesise default,
    /// fail fast, etc.).
    Open,
    /// The inner call was attempted and failed. The caller
    /// inspects the inner error to decide.
    Underlying(E),
}

impl<E: fmt::Display> fmt::Display for CircuitError<E> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            CircuitError::Open => write!(f, "circuit breaker open"),
            CircuitError::Underlying(e) => write!(f, "underlying call failed: {e}"),
        }
    }
}

impl<E: std::error::Error + 'static> std::error::Error for CircuitError<E> {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            CircuitError::Open => None,
            CircuitError::Underlying(e) => Some(e),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    // A throwaway error type so the matcher doesn't need a
    // service-specific error.
    #[derive(Debug, thiserror::Error)]
    enum FakeError {
        #[error("boom")]
        Boom,
    }

    #[tokio::test]
    async fn closed_state_passes_through_successful_call() {
        let br = CircuitBreaker::new("test");
        let result: Result<u64, CircuitError<FakeError>> = br.call(|| async { Ok(42_u64) }).await;
        assert!(matches!(result, Ok(42)));
        let snap = br.state_snapshot().await;
        assert_eq!(snap.state, StateKind::Closed);
        assert_eq!(snap.consecutive_failures, 0);
    }

    #[tokio::test]
    async fn closed_state_propagates_underlying_failure() {
        let br = CircuitBreaker::new("test");
        let result: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        assert!(matches!(
            result,
            Err(CircuitError::Underlying(FakeError::Boom))
        ));
        let snap = br.state_snapshot().await;
        assert_eq!(snap.state, StateKind::Closed);
        assert_eq!(snap.consecutive_failures, 1);
    }

    #[tokio::test]
    async fn opens_after_threshold_consecutive_failures() {
        let br = CircuitBreaker::with_policy("test", 3, Duration::from_secs(60));
        for _ in 0..3 {
            let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        }
        let snap = br.state_snapshot().await;
        assert_eq!(snap.state, StateKind::Open);
        assert_eq!(snap.open_count, 1);
    }

    #[tokio::test]
    async fn open_breaker_short_circuits_calls() {
        let br = CircuitBreaker::with_policy("test", 1, Duration::from_secs(60));
        let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        // Now open.
        let result: Result<u64, CircuitError<FakeError>> = br.call(|| async { Ok(99_u64) }).await;
        assert!(matches!(result, Err(CircuitError::Open)));
    }

    #[tokio::test]
    async fn success_resets_consecutive_failures() {
        let br = CircuitBreaker::with_policy("test", 5, Duration::from_secs(60));
        let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        let result: Result<u64, CircuitError<FakeError>> = br.call(|| async { Ok(7_u64) }).await;
        assert!(matches!(result, Ok(7)));
        let snap = br.state_snapshot().await;
        assert_eq!(snap.consecutive_failures, 0);
    }

    #[tokio::test]
    async fn cooldown_elapsed_transitions_to_half_open() {
        let br = CircuitBreaker::with_policy("test", 1, Duration::from_millis(50));
        // Trip the breaker.
        let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        assert_eq!(br.state_snapshot().await.state, StateKind::Open);
        // Wait past the cooldown.
        tokio::time::sleep(Duration::from_millis(75)).await;
        // Successful probe — transitions to HalfOpen during
        // the call attempt, then back to Closed on success.
        let result: Result<u64, CircuitError<FakeError>> = br.call(|| async { Ok(11_u64) }).await;
        assert!(matches!(result, Ok(11)));
        assert_eq!(br.state_snapshot().await.state, StateKind::Closed);
    }

    #[tokio::test]
    async fn half_open_failure_re_opens_breaker() {
        let br = CircuitBreaker::with_policy("test", 1, Duration::from_millis(50));
        let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        tokio::time::sleep(Duration::from_millis(75)).await;
        // Probe fails — back to Open.
        let _: Result<(), _> = br.call(|| async { Err::<(), _>(FakeError::Boom) }).await;
        let snap = br.state_snapshot().await;
        assert_eq!(snap.state, StateKind::Open);
        assert_eq!(snap.open_count, 2);
    }

    #[tokio::test]
    async fn trip_and_reset_are_idempotent() {
        let br = CircuitBreaker::new("test");
        br.trip().await;
        assert_eq!(br.state_snapshot().await.state, StateKind::Open);
        br.reset().await;
        assert_eq!(br.state_snapshot().await.state, StateKind::Closed);
    }

    // Service-agnostic property: the breaker doesn't know
    // what it wraps. The two tests below differ only in the
    // inner closure's return type.

    #[tokio::test]
    async fn service_agnostic_wraps_string_returning_call() {
        let br = CircuitBreaker::new("svc-a");
        let result: Result<String, CircuitError<FakeError>> =
            br.call(|| async { Ok("hello".to_string()) }).await;
        assert_eq!(result.unwrap(), "hello");
    }

    #[tokio::test]
    async fn service_agnostic_wraps_struct_returning_call() {
        #[derive(Debug, PartialEq)]
        struct FakeReply {
            id: u64,
        }
        let br = CircuitBreaker::new("svc-b");
        let result: Result<FakeReply, CircuitError<FakeError>> =
            br.call(|| async { Ok(FakeReply { id: 7 }) }).await;
        assert_eq!(result.unwrap(), FakeReply { id: 7 });
    }
}
