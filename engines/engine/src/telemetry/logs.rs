// SPDX-License-Identifier: Apache-2.0

//! JSON-line structured stdout logger per ADR-0031 §3.4.
//!
//! Replaces the legacy `tracing-subscriber::fmt::compact()` text
//! layer (stderr, dev-style) with a JSON formatter emitting the
//! eleven mandatory fields ADR-0031 §3.4 codifies, plus any
//! event-supplied extras as additional context.
//!
//! Trace-context fields (`trace_id`, `span_id`) are read from
//! `opentelemetry::Context::current()` at event time — independent
//! of whether the surrounding span was created via the `OTel` API
//! directly (Cluster B: `engine.run_job`, `engine.execute_plan`,
//! adapter RPCs) or via the `tracing-opentelemetry` bridge
//! (Cluster D: `tracing::info_span!` macros, e.g. `engine.assemble_row`).
//! Both flows populate the global current context the same way.
//!
//! Field shape (ADR-0031 §3.4):
//!
//! | Field             | Source                                          |
//! |-------------------|-------------------------------------------------|
//! | `timestamp`       | `chrono::Utc::now()` RFC 3339 with ms precision |
//! | `level`           | event metadata level                            |
//! | `service`         | configured at layer init                        |
//! | `service_version` | configured at layer init                        |
//! | `caller`          | `<file>:<line>` from event metadata             |
//! | `message`         | event's `message` field                         |
//! | `trace_id`        | `Context::current().span().span_context()`      |
//! | `span_id`         | same                                            |
//! | `request_id`      | event's `request_id` field, else `null`         |
//! | `job_id`          | event's `job_id` field, else `null`             |
//! | `tenant_id`       | `null` in v1alpha1 (no multi-tenancy)           |
//! | `latency_ms`      | event's `latency_ms` field, omitted when absent |
//! | `error_code`      | event's `error_code` field on ERROR-level only  |
//!
//! Extra fields (anything not matching the mandatory schema above)
//! are emitted at the top level alongside the canonical fields.

use std::fmt;

use opentelemetry::trace::TraceContextExt as _;
use serde_json::{Map, Value};
use tracing::field::{Field, Visit};
use tracing::{Event, Level, Subscriber};
use tracing_subscriber::Layer;
use tracing_subscriber::fmt::FmtContext;
use tracing_subscriber::fmt::MakeWriter;
use tracing_subscriber::fmt::format::{FormatEvent, FormatFields, Writer};
use tracing_subscriber::registry::LookupSpan;

/// Build the JSON stdout layer for installation in the subscriber.
///
/// Returns a [`tracing_subscriber::Layer`] writing one JSON line per
/// event to `stdout`. `service_name` and `service_version` are
/// constants resolved at engine startup (`"spectre-engine"` and
/// the crate's `ENGINE_VERSION`).
#[must_use]
pub fn build_layer<S>(service_name: &'static str, service_version: &'static str) -> impl Layer<S>
where
    S: Subscriber + for<'a> LookupSpan<'a>,
{
    build_layer_with_writer(service_name, service_version, std::io::stdout)
}

/// Test-friendly variant of [`build_layer`] accepting a custom
/// [`MakeWriter`]. Production callers use [`build_layer`] (which
/// writes to stdout); tests inject a shared `Vec<u8>` buffer to
/// capture the JSON output for assertion.
pub fn build_layer_with_writer<S, W>(
    service_name: &'static str,
    service_version: &'static str,
    make_writer: W,
) -> impl Layer<S>
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    W: for<'w> MakeWriter<'w> + 'static + Send + Sync,
{
    tracing_subscriber::fmt::layer()
        .event_format(SpectreEventFormat {
            service_name,
            service_version,
        })
        .with_writer(make_writer)
}

struct SpectreEventFormat {
    service_name: &'static str,
    service_version: &'static str,
}

impl<S, N> FormatEvent<S, N> for SpectreEventFormat
where
    S: Subscriber + for<'a> LookupSpan<'a>,
    N: for<'a> FormatFields<'a> + 'static,
{
    fn format_event(
        &self,
        _ctx: &FmtContext<'_, S, N>,
        mut writer: Writer<'_>,
        event: &Event<'_>,
    ) -> fmt::Result {
        let mut visitor = SpectreVisitor::default();
        event.record(&mut visitor);

        let metadata = event.metadata();
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);

        // Pull trace_id / span_id from the active OTel context. With
        // the W3C propagator + tracer provider always registered
        // (Cluster A + B), `is_valid()` is `true` whenever the event
        // fires inside one of the engine's spans.
        let otel_ctx = opentelemetry::Context::current();
        let span_ref = otel_ctx.span();
        let span_ctx = span_ref.span_context();
        let (trace_id, span_id) = if span_ctx.is_valid() {
            (
                Value::String(span_ctx.trace_id().to_string()),
                Value::String(span_ctx.span_id().to_string()),
            )
        } else {
            (Value::Null, Value::Null)
        };

        let caller = format!(
            "{}:{}",
            metadata.file().unwrap_or("?"),
            metadata.line().unwrap_or(0),
        );

        let mut obj = Map::new();
        obj.insert("timestamp".to_owned(), Value::String(timestamp));
        obj.insert(
            "level".to_owned(),
            Value::String(level_str(*metadata.level()).to_owned()),
        );
        obj.insert(
            "service".to_owned(),
            Value::String(self.service_name.to_owned()),
        );
        obj.insert(
            "service_version".to_owned(),
            Value::String(self.service_version.to_owned()),
        );
        obj.insert("caller".to_owned(), Value::String(caller));
        obj.insert(
            "message".to_owned(),
            visitor
                .message
                .map_or(Value::String(String::new()), Value::String),
        );
        obj.insert("trace_id".to_owned(), trace_id);
        obj.insert("span_id".to_owned(), span_id);
        obj.insert(
            "request_id".to_owned(),
            visitor.request_id.map_or(Value::Null, Value::String),
        );
        obj.insert(
            "job_id".to_owned(),
            visitor.job_id.map_or(Value::Null, Value::String),
        );
        // ADR-0031 §3.4 — `tenant_id` emitted (null) for forward-
        // compat with multi-tenant deployments (v1beta1 scope).
        obj.insert("tenant_id".to_owned(), Value::Null);
        if let Some(latency_ms) = visitor.latency_ms {
            if let Some(num) = serde_json::Number::from_f64(latency_ms) {
                obj.insert("latency_ms".to_owned(), Value::Number(num));
            }
        }
        if metadata.level() == &Level::ERROR {
            if let Some(error_code) = visitor.error_code {
                obj.insert("error_code".to_owned(), Value::String(error_code));
            }
        }
        // Extra event fields (anything not in the canonical schema).
        for (k, v) in visitor.extra {
            // Don't let an extra field collide with a canonical one —
            // canonical fields are authoritative.
            obj.entry(k).or_insert(v);
        }

        let line = serde_json::to_string(&Value::Object(obj))
            .unwrap_or_else(|_| "{\"message\":\"json_serialise_failed\"}".to_owned());
        writeln!(writer, "{line}")
    }
}

fn level_str(level: Level) -> &'static str {
    match level {
        Level::ERROR => "ERROR",
        Level::WARN => "WARN",
        Level::INFO => "INFO",
        Level::DEBUG => "DEBUG",
        Level::TRACE => "TRACE",
    }
}

#[derive(Default)]
struct SpectreVisitor {
    message: Option<String>,
    request_id: Option<String>,
    job_id: Option<String>,
    error_code: Option<String>,
    latency_ms: Option<f64>,
    extra: Vec<(String, Value)>,
}

impl SpectreVisitor {
    fn record_canonical_string(&mut self, name: &str, value: String) -> bool {
        match name {
            "message" => {
                self.message = Some(value);
                true
            }
            "request_id" => {
                self.request_id = Some(value);
                true
            }
            "job_id" => {
                self.job_id = Some(value);
                true
            }
            "error_code" => {
                self.error_code = Some(value);
                true
            }
            _ => false,
        }
    }
}

impl Visit for SpectreVisitor {
    fn record_debug(&mut self, field: &Field, value: &dyn fmt::Debug) {
        // `info!(field = %display_value, ...)` and
        // `info!(field = ?debug_value, ...)` both route to
        // `record_debug` via tracing's `DisplayValue` / `DebugValue`
        // adapters. The output string already reflects the user's
        // chosen formatter (Display for `%`, Debug for `?`).
        let raw = format!("{value:?}");
        let value = strip_quotes(&raw);
        if !self.record_canonical_string(field.name(), value.to_owned()) {
            self.extra
                .push((field.name().to_owned(), Value::String(value.to_owned())));
        }
    }

    fn record_str(&mut self, field: &Field, value: &str) {
        if !self.record_canonical_string(field.name(), value.to_owned()) {
            self.extra
                .push((field.name().to_owned(), Value::String(value.to_owned())));
        }
    }

    fn record_i64(&mut self, field: &Field, value: i64) {
        self.extra
            .push((field.name().to_owned(), Value::Number(value.into())));
    }

    fn record_u64(&mut self, field: &Field, value: u64) {
        self.extra
            .push((field.name().to_owned(), Value::Number(value.into())));
    }

    fn record_f64(&mut self, field: &Field, value: f64) {
        if field.name() == "latency_ms" {
            self.latency_ms = Some(value);
        } else if let Some(num) = serde_json::Number::from_f64(value) {
            self.extra
                .push((field.name().to_owned(), Value::Number(num)));
        }
    }

    fn record_bool(&mut self, field: &Field, value: bool) {
        self.extra
            .push((field.name().to_owned(), Value::Bool(value)));
    }
}

/// Strip a single layer of surrounding ASCII double quotes if the
/// Debug formatter wrapped a string literal in them — common when
/// `record_debug` is called with a `String` whose Debug includes
/// the quotes. Idempotent on values without outer quotes.
fn strip_quotes(s: &str) -> &str {
    if s.len() >= 2 && s.starts_with('"') && s.ends_with('"') {
        &s[1..s.len() - 1]
    } else {
        s
    }
}

#[cfg(test)]
mod tests {
    use super::strip_quotes;

    #[test]
    fn strip_quotes_removes_single_surrounding_pair() {
        assert_eq!(strip_quotes("\"hello\""), "hello");
    }

    #[test]
    fn strip_quotes_leaves_unquoted_value_intact() {
        assert_eq!(strip_quotes("hello"), "hello");
        assert_eq!(strip_quotes("42"), "42");
    }

    #[test]
    fn strip_quotes_does_not_strip_internal_quotes() {
        assert_eq!(strip_quotes("\"a\"\"b\""), "a\"\"b");
    }

    #[test]
    fn strip_quotes_handles_short_inputs() {
        assert_eq!(strip_quotes(""), "");
        assert_eq!(strip_quotes("\""), "\"");
        assert_eq!(strip_quotes("\"\""), "");
    }
}
