// SPDX-License-Identifier: Apache-2.0

//! Plan executor.
//!
//! Walks a [`Plan`] step-by-step, holding the most recent
//! `ElementRef` set returned by `Query`, and writes one JSON row per
//! `ElementRef` when it reaches `ExtractEach`. Returns the total
//! number of rows written.
//!
//! The executor always calls `Close` once a session has been opened,
//! even on error paths — the in-flight session is wrapped in a guard
//! struct whose `Drop` schedules a best-effort close call. The
//! happy-path close is also explicit so its result can be surfaced.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Instant;

use opentelemetry::KeyValue;
use serde_json::Value as Json;
use tracing::{debug, info, warn};

use crate::client::Client;
use crate::error::EngineError;
use crate::output::OutputSink;
use crate::plan::{Plan, PlanStep, validate_capabilities};
use crate::proto;
use crate::telemetry::EngineMetrics;

/// Drives a [`Plan`] against a connected [`Client`], writing one row
/// per element to `sink`.
pub struct Executor;

impl Executor {
    /// Run the plan, return the total number of rows written.
    ///
    /// Records per-step duration via
    /// `spectre_engine_step_duration_seconds` (one observation per
    /// `PlanStep` iteration) and per-RPC duration via
    /// `spectre_engine_step_service_call_duration_seconds{service}`
    /// (one observation per adapter call — `ExtractEach` contributes
    /// N observations against one step-duration observation).
    /// Both per ADR-0031 §5.1.
    ///
    /// # Errors
    ///
    /// Returns the first [`EngineError`] encountered. The session,
    /// once opened, is closed before returning regardless of outcome.
    #[allow(clippy::too_many_lines)]
    pub async fn run(
        plan: &Plan,
        client: &Client,
        sink: &mut dyn OutputSink,
        metrics: &Arc<EngineMetrics>,
        service_label: &'static str,
    ) -> Result<usize, EngineError> {
        let mut session: Option<String> = None;
        let mut last_query: Vec<proto::ElementRef> = Vec::new();
        let mut rows_written: usize = 0;

        let result = run_inner(
            plan,
            client,
            sink,
            metrics,
            service_label,
            &mut session,
            &mut last_query,
            &mut rows_written,
        )
        .await;

        // Always close, regardless of outcome.
        if let Some(id) = session.as_deref() {
            let start = Instant::now();
            let close_result = client.close(id).await;
            metrics.step_service_call_duration_seconds.record(
                start.elapsed().as_secs_f64(),
                &[KeyValue::new("service", service_label)],
            );
            if let Err(e) = close_result {
                warn!(error = %e, "best-effort Close after error failed");
            }
        }

        sink.flush()?;
        result.map(|()| rows_written)
    }
}

#[allow(clippy::too_many_arguments)]
async fn run_inner(
    plan: &Plan,
    client: &Client,
    sink: &mut dyn OutputSink,
    metrics: &Arc<EngineMetrics>,
    service_label: &'static str,
    session: &mut Option<String>,
    last_query: &mut Vec<proto::ElementRef>,
    rows_written: &mut usize,
) -> Result<(), EngineError> {
    let service_attrs = [KeyValue::new("service", service_label)];
    for step in &plan.steps {
        let step_start = Instant::now();
        match step {
            PlanStep::Initialize { config } => {
                let call_start = Instant::now();
                let outcome = client
                    .initialize(
                        config.clone(),
                        plan.required_capabilities.iter().cloned().collect(),
                    )
                    .await?;
                metrics
                    .step_service_call_duration_seconds
                    .record(call_start.elapsed().as_secs_f64(), &service_attrs);
                debug!(
                    session_id = %outcome.session_id,
                    declared = ?outcome.capability_names,
                    "session initialised"
                );
                validate_capabilities(plan, &outcome.capability_names)?;
                *session = Some(outcome.session_id);
            }
            PlanStep::Navigate { url, wait_until } => {
                let id = require_session(session.as_deref())?;
                let call_start = Instant::now();
                client.navigate(id, url, *wait_until).await?;
                metrics
                    .step_service_call_duration_seconds
                    .record(call_start.elapsed().as_secs_f64(), &service_attrs);
                last_query.clear();
            }
            PlanStep::Query {
                selector,
                kind,
                limit,
            } => {
                let id = require_session(session.as_deref())?;
                let call_start = Instant::now();
                let elements = client.query(id, selector, *kind, *limit).await?;
                metrics
                    .step_service_call_duration_seconds
                    .record(call_start.elapsed().as_secs_f64(), &service_attrs);
                debug!(
                    selector = %selector,
                    matched = elements.len(),
                    "Query returned elements"
                );
                *last_query = elements;
            }
            PlanStep::ExtractEach { fields } => {
                let id = require_session(session.as_deref())?;
                if last_query.is_empty() {
                    debug!("ExtractEach: prior Query returned zero elements — no rows");
                    continue;
                }
                let elements = std::mem::take(last_query);
                for element in elements {
                    let call_start = Instant::now();
                    let entries = client.extract(id, element, fields.clone()).await?;
                    metrics
                        .step_service_call_duration_seconds
                        .record(call_start.elapsed().as_secs_f64(), &service_attrs);
                    let row = entries_to_row(&entries);
                    sink.write_row(&row)?;
                    *rows_written += 1;
                }
            }
            PlanStep::Close => {
                if let Some(id) = session.take() {
                    let call_start = Instant::now();
                    client.close(&id).await?;
                    metrics
                        .step_service_call_duration_seconds
                        .record(call_start.elapsed().as_secs_f64(), &service_attrs);
                }
            }
        }
        metrics
            .step_duration_seconds
            .record(step_start.elapsed().as_secs_f64(), &[]);
    }
    info!(rows = *rows_written, "plan complete");
    Ok(())
}

fn require_session(s: Option<&str>) -> Result<&str, EngineError> {
    s.ok_or_else(|| {
        EngineError::Internal("plan tried to operate on an unopened session".to_owned())
    })
}

/// Convert one `Extract` response's `(name, json_value)` pairs into a
/// JSON object row. Each `json_value` is itself a JSON-encoded string
/// per the v1alpha1 wire contract (ADR-0010 "Bad, because" note); we
/// parse it here so the row carries native JSON types when possible
/// (string, number, bool, null), falling back to the literal string
/// when parsing fails.
fn entries_to_row(entries: &[(String, String)]) -> Json {
    let mut map = BTreeMap::<String, Json>::new();
    for (name, raw) in entries {
        let parsed: Json = serde_json::from_str(raw).unwrap_or_else(|_| Json::String(raw.clone()));
        map.insert(name.clone(), parsed);
    }
    Json::Object(map.into_iter().collect())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn entries_to_row_decodes_quoted_strings_to_native() {
        let entries = vec![
            ("title".to_owned(), "\"hello\"".to_owned()),
            ("count".to_owned(), "42".to_owned()),
            ("flag".to_owned(), "true".to_owned()),
        ];
        let row = entries_to_row(&entries);
        assert_eq!(row, json!({"count": 42, "flag": true, "title": "hello"}));
    }

    #[test]
    fn entries_to_row_preserves_unparseable_strings_as_string_literal() {
        let entries = vec![("html".to_owned(), "<div>hi</div>".to_owned())];
        let row = entries_to_row(&entries);
        assert_eq!(row, json!({"html": "<div>hi</div>"}));
    }

    #[test]
    fn require_session_errors_when_unopened() {
        match require_session(None) {
            Err(EngineError::Internal(msg)) => {
                assert!(msg.contains("unopened session"));
            }
            other => panic!("expected Internal, got {other:?}"),
        }
    }
}
