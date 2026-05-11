// SPDX-License-Identifier: Apache-2.0

//! W3C Trace Context propagation across the gRPC boundary.
//!
//! ADR-0031 §4.1 commits the platform to W3C Trace Context
//! (`traceparent` + `tracestate` headers) for cross-service trace
//! continuity. The propagator itself is registered globally in
//! [`super::Telemetry::init`]; this module adapts that propagator
//! to tonic's [`MetadataMap`] on each side of the engine ↔ adapter
//! boundary.
//!
//! Server-side flow (engine `run_job`):
//!
//! ```ignore
//! let parent_ctx = propagation::extract_parent(request.metadata());
//! let span = tracer
//!     .span_builder("engine.run_job")
//!     .start_with_context(&tracer, &parent_ctx);
//! let ctx = opentelemetry::Context::current_with_span(span);
//! let _guard = ctx.attach();
//! ```
//!
//! Client-side flow (engine ↔ adapter dial in [`crate::client`]):
//!
//! ```ignore
//! let mut req = tonic::Request::new(message);
//! propagation::inject_current(req.metadata_mut());
//! ```

use opentelemetry::Context;
use opentelemetry::global;
use opentelemetry::propagation::{Extractor, Injector};
use tonic::metadata::{KeyAndValueRef, MetadataKey, MetadataMap, MetadataValue};
use tracing::warn;

/// Extract the W3C parent trace context from incoming gRPC metadata.
///
/// Returns the current (empty) context if no `traceparent` is
/// present, malformed, or otherwise unrecoverable. The `OTel`
/// `TraceContextPropagator` documents that behaviour — invalid
/// headers do not propagate as errors; they yield an empty context
/// so downstream spans start a fresh trace.
#[must_use]
pub fn extract_parent(metadata: &MetadataMap) -> Context {
    let extractor = MetadataExtractor(metadata);
    global::get_text_map_propagator(|propagator| propagator.extract(&extractor))
}

/// Inject the current OpenTelemetry context into outgoing gRPC
/// metadata. Idempotent — calling twice overwrites prior headers.
pub fn inject_current(metadata: &mut MetadataMap) {
    let context = Context::current();
    let mut injector = MetadataInjector(metadata);
    global::get_text_map_propagator(|propagator| {
        propagator.inject_context(&context, &mut injector);
    });
}

struct MetadataExtractor<'a>(&'a MetadataMap);

impl Extractor for MetadataExtractor<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        self.0.get(key).and_then(|value| value.to_str().ok())
    }

    fn keys(&self) -> Vec<&str> {
        self.0
            .iter()
            .filter_map(|kv| match kv {
                KeyAndValueRef::Ascii(name, _) => Some(name.as_str()),
                // Binary metadata keys are not used for W3C Trace
                // Context — `traceparent` + `tracestate` are ASCII.
                KeyAndValueRef::Binary(_, _) => None,
            })
            .collect()
    }
}

struct MetadataInjector<'a>(&'a mut MetadataMap);

impl Injector for MetadataInjector<'_> {
    fn set(&mut self, key: &str, value: String) {
        match (
            MetadataKey::from_bytes(key.as_bytes()),
            MetadataValue::try_from(value.as_str()),
        ) {
            (Ok(name), Ok(val)) => {
                self.0.insert(name, val);
            }
            (key_res, value_res) => {
                // Both branches are unreachable for the W3C
                // propagator's own header set (`traceparent`,
                // `tracestate`) — both are lowercase-ASCII names and
                // ASCII-printable values. A diagnostic warn keeps a
                // breadcrumb if a future propagator adds an exotic
                // header.
                warn!(
                    key,
                    key_err = ?key_res.err(),
                    value_err = ?value_res.err(),
                    "skipped propagator header (invalid metadata key/value)",
                );
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use opentelemetry::trace::{
        SpanContext, SpanId, TraceContextExt as _, TraceFlags, TraceId, TraceState,
    };
    use opentelemetry_sdk::propagation::TraceContextPropagator;

    fn install_propagator() {
        // `set_text_map_propagator` is idempotent at the global
        // level; calling it from multiple tests is safe.
        global::set_text_map_propagator(TraceContextPropagator::new());
    }

    #[test]
    fn extract_returns_empty_context_when_traceparent_missing() {
        install_propagator();
        let metadata = MetadataMap::new();
        let ctx = extract_parent(&metadata);
        // An empty context has no associated span; trace_id is the
        // zero value. `span().span_context().is_valid()` is the
        // canonical check.
        assert!(!ctx.span().span_context().is_valid());
    }

    #[test]
    fn inject_then_extract_round_trips_trace_id() {
        install_propagator();

        // Build a synthetic parent context and attach it.
        let parent_span = SpanContext::new(
            TraceId::from_hex("0123456789abcdef0123456789abcdef").unwrap(),
            SpanId::from_hex("0123456789abcdef").unwrap(),
            TraceFlags::SAMPLED,
            true, // remote — emulates an upstream call
            TraceState::default(),
        );
        let parent_ctx = Context::new().with_remote_span_context(parent_span.clone());
        let _guard = parent_ctx.attach();

        let mut metadata = MetadataMap::new();
        inject_current(&mut metadata);

        // `traceparent` is the W3C standard header name.
        let header = metadata
            .get("traceparent")
            .expect("traceparent injected")
            .to_str()
            .unwrap();
        assert!(
            header.contains("0123456789abcdef0123456789abcdef"),
            "traceparent should contain the parent trace id: got {header}",
        );

        let extracted = extract_parent(&metadata);
        let extracted_span = extracted.span().span_context().clone();
        assert_eq!(extracted_span.trace_id(), parent_span.trace_id());
    }
}
