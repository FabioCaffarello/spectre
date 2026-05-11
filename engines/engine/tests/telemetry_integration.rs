// SPDX-License-Identifier: Apache-2.0

//! Integration tests for `spectre_engine::telemetry`.
//!
//! W3.1 Cluster A acceptance criteria: the `/metrics` sidecar
//! binds, serves `OpenMetrics` text, and the registered engine
//! instruments appear after recording samples. Verified against
//! an ephemeral port so the suite is parallel-safe and needs no
//! external dependency.
//!
//! `Telemetry::init` also exercises the `OTel` meter provider's
//! integration with the shared Prometheus registry — a missing
//! `opentelemetry-prometheus` exporter contract would fail the
//! scrape assertion here.

#![cfg(test)]

use std::time::Duration;

use opentelemetry::KeyValue;
use spectre_engine::telemetry::{Telemetry, TelemetryConfig};

/// Bind an ephemeral TCP port, return the port number, and release
/// it so the metrics sidecar can claim it. The brief window between
/// release and re-bind is the standard "ephemeral port" race common
/// to integration tests; on a developer / CI host with no contention
/// for the port pool it is reliable in practice.
async fn pick_port() -> u16 {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    drop(listener);
    port
}

#[tokio::test]
async fn metrics_sidecar_serves_engine_instruments() {
    let port = pick_port().await;
    let config = TelemetryConfig {
        service_name: "spectre-engine-test",
        service_version: "test",
        metrics_addr: format!("127.0.0.1:{port}").parse().unwrap(),
        otlp_endpoint: None,
    };

    let telemetry = Telemetry::init(config)
        .await
        .expect("Telemetry::init should succeed without OTLP endpoint");

    // Bump every instrument once so each appears in the scrape body.
    // Counter / histogram / up-down-counter all surface in the
    // Prometheus registry only after at least one observation.
    telemetry.metrics.jobs_active.add(1, &[]);
    telemetry
        .metrics
        .jobs_completed_total
        .add(1, &[KeyValue::new("result", "success")]);
    telemetry.metrics.step_duration_seconds.record(0.01, &[]);
    telemetry
        .metrics
        .step_service_call_duration_seconds
        .record(0.02, &[KeyValue::new("service", "playwright")]);
    telemetry
        .metrics
        .rows_emitted_total
        .add(1, &[KeyValue::new("sink", "stdout")]);

    // Give the OTel meter provider's pipeline a tick to flow through.
    tokio::time::sleep(Duration::from_millis(50)).await;

    let body = reqwest::get(format!("http://127.0.0.1:{port}/metrics"))
        .await
        .expect("scrape /metrics")
        .error_for_status()
        .expect("/metrics returns 2xx")
        .text()
        .await
        .expect("read /metrics body");

    // Every registered name must appear, modulo Prometheus's
    // counter suffix elision (`_total` is preserved by the
    // opentelemetry-prometheus exporter as of 0.32).
    for needle in [
        "spectre_engine_jobs_active",
        "spectre_engine_jobs_completed_total",
        "spectre_engine_step_duration_seconds",
        "spectre_engine_step_service_call_duration_seconds",
        "spectre_engine_rows_emitted_total",
    ] {
        assert!(
            body.contains(needle),
            "missing metric {needle} in scrape body:\n{body}"
        );
    }

    telemetry.shutdown().await;
}

#[tokio::test]
async fn init_succeeds_with_otlp_endpoint_unset() {
    // The no-op tracer path mirrors the Kafka / S3 optional pattern —
    // an unconfigured endpoint is INFO-logged at startup, not an error.
    let port = pick_port().await;
    let config = TelemetryConfig {
        service_name: "spectre-engine-test",
        service_version: "test",
        metrics_addr: format!("127.0.0.1:{port}").parse().unwrap(),
        otlp_endpoint: None,
    };

    let telemetry = Telemetry::init(config)
        .await
        .expect("Telemetry::init must succeed with otlp_endpoint=None");
    telemetry.shutdown().await;
}
