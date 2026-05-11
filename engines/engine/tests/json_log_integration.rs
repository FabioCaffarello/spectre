// SPDX-License-Identifier: Apache-2.0

//! W3.1 Cluster D end-to-end check: a `tracing::info!` event
//! produces a single JSON line with the eleven mandatory fields
//! ADR-0031 §3.4 codifies, populated from the active `OTel` context
//! when one is current.
//!
//! Each test installs a private subscriber via
//! `tracing::subscriber::with_default`, so the global subscriber
//! the engine binary installs at startup does not interfere and
//! the tests are parallel-safe.

#![cfg(test)]

use std::io::{self, Write};
use std::sync::{Arc, Mutex};

use opentelemetry::trace::{FutureExt as _, SpanKind, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context as OtelContext, global};
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::SdkTracerProvider;
use serde_json::Value;
use spectre_engine::telemetry::logs;
use tracing_subscriber::fmt::MakeWriter;
use tracing_subscriber::layer::SubscriberExt as _;

#[derive(Clone, Default)]
struct SharedWriter {
    buf: Arc<Mutex<Vec<u8>>>,
}

impl SharedWriter {
    fn captured(&self) -> String {
        let bytes = self.buf.lock().unwrap();
        String::from_utf8_lossy(&bytes).into_owned()
    }
}

impl Write for SharedWriter {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.buf.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

impl<'a> MakeWriter<'a> for SharedWriter {
    type Writer = SharedWriter;
    fn make_writer(&'a self) -> Self::Writer {
        self.clone()
    }
}

fn install_propagator_and_tracer() {
    global::set_text_map_propagator(TraceContextPropagator::new());
    let provider = SdkTracerProvider::builder()
        .with_resource(
            Resource::builder()
                .with_service_name("spectre-engine-test")
                .build(),
        )
        .build();
    global::set_tracer_provider(provider);
}

#[test]
fn info_event_outside_span_emits_eleven_mandatory_fields() {
    let writer = SharedWriter::default();
    let layer = logs::build_layer_with_writer::<tracing_subscriber::Registry, _>(
        "spectre-engine",
        "test-version",
        writer.clone(),
    );
    let subscriber = tracing_subscriber::registry().with(layer);

    tracing::subscriber::with_default(subscriber, || {
        tracing::info!(job_id = "job-1", request_id = "req-1", "hello");
    });

    let line = writer.captured();
    let line = line.trim_end_matches('\n');
    let parsed: Value =
        serde_json::from_str(line).unwrap_or_else(|e| panic!("expected JSON: {e}; raw: {line}"));
    let obj = parsed.as_object().expect("JSON object");

    // Eleven mandatory fields per ADR-0031 §3.4.
    for required in [
        "timestamp",
        "level",
        "service",
        "service_version",
        "caller",
        "message",
        "trace_id",
        "span_id",
        "request_id",
        "job_id",
        "tenant_id",
    ] {
        assert!(
            obj.contains_key(required),
            "missing field {required} in {obj:?}"
        );
    }

    assert_eq!(obj["level"], "INFO");
    assert_eq!(obj["service"], "spectre-engine");
    assert_eq!(obj["service_version"], "test-version");
    assert_eq!(obj["message"], "hello");
    assert_eq!(obj["job_id"], "job-1");
    assert_eq!(obj["request_id"], "req-1");
    // tenant_id is always null in v1alpha1 (multi-tenancy is v1beta1).
    assert!(obj["tenant_id"].is_null());
    // No active OTel span → trace_id / span_id null.
    assert!(obj["trace_id"].is_null());
    assert!(obj["span_id"].is_null());
}

#[tokio::test]
async fn info_event_inside_otel_span_carries_trace_and_span_ids() {
    install_propagator_and_tracer();
    let writer = SharedWriter::default();
    let layer = logs::build_layer_with_writer::<tracing_subscriber::Registry, _>(
        "spectre-engine",
        "test-version",
        writer.clone(),
    );
    let subscriber = tracing_subscriber::registry().with(layer);

    // The default-scope guard scopes the subscriber to the current
    // thread only; for the multi-thread tokio runtime to use it,
    // wrap the body with `set_default`.
    let _guard = tracing::subscriber::set_default(subscriber);

    let tracer = global::tracer("spectre-engine");
    let span = tracer
        .span_builder("test.parent")
        .with_kind(SpanKind::Internal)
        .start(&tracer);
    let ctx = OtelContext::current_with_span(span);
    let expected_trace_id = ctx.span().span_context().trace_id().to_string();
    let expected_span_id = ctx.span().span_context().span_id().to_string();

    async {
        tracing::info!(job_id = "job-2", "inside otel span");
    }
    .with_context(ctx)
    .await;

    let line = writer.captured();
    let line = line.trim_end_matches('\n');
    let parsed: Value =
        serde_json::from_str(line).unwrap_or_else(|e| panic!("expected JSON: {e}; raw: {line}"));
    let obj = parsed.as_object().unwrap();

    assert_eq!(obj["trace_id"], expected_trace_id);
    assert_eq!(obj["span_id"], expected_span_id);
    assert_eq!(obj["job_id"], "job-2");
    assert_eq!(obj["message"], "inside otel span");
}

#[test]
fn error_event_carries_error_code_field() {
    let writer = SharedWriter::default();
    let layer = logs::build_layer_with_writer::<tracing_subscriber::Registry, _>(
        "spectre-engine",
        "test-version",
        writer.clone(),
    );
    let subscriber = tracing_subscriber::registry().with(layer);

    tracing::subscriber::with_default(subscriber, || {
        tracing::error!(error_code = "TRANSPORT", "dial failed");
    });

    let line = writer.captured();
    let line = line.trim_end_matches('\n');
    let parsed: Value = serde_json::from_str(line).unwrap();
    let obj = parsed.as_object().unwrap();

    assert_eq!(obj["level"], "ERROR");
    assert_eq!(obj["error_code"], "TRANSPORT");
}

#[test]
fn info_event_omits_error_code_field_at_info_level() {
    let writer = SharedWriter::default();
    let layer = logs::build_layer_with_writer::<tracing_subscriber::Registry, _>(
        "spectre-engine",
        "test-version",
        writer.clone(),
    );
    let subscriber = tracing_subscriber::registry().with(layer);

    // `error_code` on an INFO event should NOT surface as the
    // canonical field — that surface is ERROR-only per ADR-0031
    // §6.1 ("error-level events include `error_code` field").
    // It falls through to the extras bucket instead.
    tracing::subscriber::with_default(subscriber, || {
        tracing::info!(error_code = "TRANSPORT", "diagnostic");
    });

    let line = writer.captured();
    let line = line.trim_end_matches('\n');
    let parsed: Value = serde_json::from_str(line).unwrap();
    let obj = parsed.as_object().unwrap();

    assert_eq!(obj["level"], "INFO");
    // The canonical-when-ERROR field is absent at INFO level (no
    // separate key under our schema; the visitor records it
    // canonically only when level is ERROR).
    assert!(
        !obj.contains_key("error_code"),
        "INFO event must not carry `error_code` as a top-level field: {obj:?}"
    );
}
