// SPDX-License-Identifier: Apache-2.0

//! `spectre-sdk-proxy-v1alpha1` — Rust SDK for the Spectre
//! `proxy-broker` infra-service (slot 1 per ADR-0036 §3.1).
//!
//! Provides a typed gRPC client wrapping the tonic-generated
//! stubs with:
//!
//! - **Retry policy** — configurable max attempts + exponential
//!   backoff for transient gRPC errors (`UNAVAILABLE`,
//!   `DEADLINE_EXCEEDED`, `INTERNAL`). Application-level errors
//!   (`INVALID_ARGUMENT`, `PERMISSION_DENIED`, etc.) propagate
//!   without retry.
//! - **Tracing hooks** — every SDK call opens a `tracing`
//!   `client_span!` so the OpenTelemetry context propagates
//!   downstream via the engine's `otelgrpc` stats handler when
//!   the channel was constructed with `with_otel_propagation`.
//!
//! The SDK does NOT include:
//!
//! - Connection pooling — the caller manages the tonic channel.
//! - TLS configuration — the caller provides `ClientTlsConfig`
//!   when constructing the channel (per ADR-0032 §4.3 the chart
//!   sets `SPECTRE_TLS_*_PATH` and the engine builds the config
//!   via its `tls::build_client_tls_config`).
//! - Service-specific business logic — the broker handles
//!   provider selection, cooldown semantics, ban tracking.
//!
//! Per ADR-0027 §5, this SDK lands in W5.1 because the engine
//! is the v1alpha2 consumer; Go / Python / TypeScript SDKs land
//! when their respective consumers materialise (likely Wave 6+
//! or never, per D14).

#![cfg_attr(not(test), forbid(unsafe_code))]
#![warn(missing_docs)]

/// Generated bindings for `spectre.proxy.v1alpha1`.
///
/// The contents of this module are written to `OUT_DIR` by
/// `build.rs` via `tonic-build` and included inline.
#[allow(missing_docs, clippy::all, clippy::pedantic, clippy::nursery)]
pub mod pb {
    tonic::include_proto!("spectre.proxy.v1alpha1");
}

pub mod client;
pub mod error;

pub use client::{ProxyClient, RetryPolicy};
pub use error::ProxyClientError;
