// SPDX-License-Identifier: Apache-2.0

//! Axum-based metrics sidecar exposing `/metrics` per ADR-0031 §3.3.
//!
//! The sidecar serves the [`prometheus::Registry`] in `OpenMetrics`
//! text format. `OTel` meter readings reach the registry via
//! `opentelemetry-prometheus`; process-level metrics reach it via
//! `prometheus::process_collector::ProcessCollector`. Both populate
//! the same `gather()` call so a scrape returns the union.
//!
//! No TLS, no authn / authz — ADR-0031 §3.3 commits to a sidecar
//! exposing `OpenMetrics`; service-to-service mTLS (ADR-0032)
//! handles transport security in W3.3. The sidecar binds `0.0.0.0`
//! so Kubernetes-native scrape works without per-pod address
//! gymnastics.

use std::net::SocketAddr;

use anyhow::{Context, Result};
use axum::Router;
use axum::extract::State;
use axum::http::{StatusCode, header};
use axum::response::IntoResponse;
use axum::routing::get;
use prometheus::{Encoder, Registry, TextEncoder};
use tokio::task::JoinHandle;
use tracing::error;

/// Spawn the `/metrics` HTTP server on `addr`.
///
/// Returns the join handle of the background tokio task; callers
/// abort the handle on shutdown. A failed bind is a startup error
/// (the engine has no "no metrics" mode in v1alpha2; ADR-0031 §3.3
/// makes the surface mandatory).
///
/// # Errors
///
/// Returns an error when the TCP listener fails to bind on `addr`
/// (port already in use, insufficient permissions, etc.).
pub async fn spawn(addr: SocketAddr, registry: Registry) -> Result<JoinHandle<()>> {
    let app = Router::new()
        .route("/metrics", get(metrics_handler))
        .with_state(registry);
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .with_context(|| format!("bind metrics sidecar on {addr}"))?;
    let handle = tokio::spawn(async move {
        if let Err(e) = axum::serve(listener, app).await {
            error!(error = %e, "metrics sidecar terminated unexpectedly");
        }
    });
    Ok(handle)
}

async fn metrics_handler(State(registry): State<Registry>) -> impl IntoResponse {
    let metric_families = registry.gather();
    let encoder = TextEncoder::new();
    let mut buf = Vec::new();
    if let Err(e) = encoder.encode(&metric_families, &mut buf) {
        error!(error = %e, "prometheus encode failed");
        return (
            StatusCode::INTERNAL_SERVER_ERROR,
            "encode failed".to_owned(),
        )
            .into_response();
    }
    ([(header::CONTENT_TYPE, encoder.format_type())], buf).into_response()
}
