// SPDX-License-Identifier: Apache-2.0

//! Orchestrator-layer scaffolding for engine-side service
//! clients per [ADR-0037](../../../docs/adr/0037-engine-as-orchestrator.md).
//!
//! Three primitives compose every Wave 5-10 service consumer:
//!
//! - **`cache`** — per-scope in-memory cache for stable values
//!   (sticky leases per session, schemas per job, capabilities
//!   per target with TTL, etc.). Per ADR-0037 §4.2.
//! - **`circuit_breaker`** — per-service breaker for graceful
//!   degradation when a downstream goes flaky. Per ADR-0037 §5.3.
//! - **`proxy`** — the W5.1 concrete consumer wrapping the
//!   `spectre-sdk-proxy-v1alpha1` crate with the two
//!   primitives above. `EngineProxyClient` is the type the
//!   engine binary holds + threads through `Engine`.
//!
//! ### Service-agnostic by design
//!
//! `cache` and `circuit_breaker` are deliberately generic
//! over their value / error types — `Cache<K, V>` and
//! `CircuitBreaker` don't know what they're caching / wrapping.
//! Future Wave 5-10 service consumers (`captcha`,
//! `schema_registry`, `input_broker`, etc.) reuse the exact
//! same primitives; only the per-service `services::<svc>.rs`
//! orchestration wrapper differs.
//!
//! The API design discipline: ask "would `services/captcha.rs`
//! use this exactly the same way?" If the answer requires
//! per-service knowledge, refactor the primitive before
//! shipping. The W5.1 review of this module is the gate that
//! enforces it.

pub mod cache;
pub mod circuit_breaker;
pub mod proxy;

pub use cache::Cache;
pub use circuit_breaker::{
    BreakerState, CircuitBreaker, CircuitError, DEFAULT_COOLDOWN, DEFAULT_THRESHOLD, StateKind,
};
pub use proxy::{EngineProxyClient, EngineProxyError};
