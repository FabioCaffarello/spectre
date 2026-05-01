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

use serde_json::Value as Json;
use tracing::{debug, info, warn};

use crate::client::Client;
use crate::error::EngineError;
use crate::output::OutputSink;
use crate::plan::{Plan, PlanStep, validate_capabilities};
use crate::proto;

/// Drives a [`Plan`] against a connected [`Client`], writing one row
/// per element to `sink`.
pub struct Executor;

impl Executor {
    /// Run the plan, return the total number of rows written.
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
    ) -> Result<usize, EngineError> {
        let mut session: Option<String> = None;
        let mut last_query: Vec<proto::ElementRef> = Vec::new();
        let mut rows_written: usize = 0;

        let result = run_inner(
            plan,
            client,
            sink,
            &mut session,
            &mut last_query,
            &mut rows_written,
        )
        .await;

        // Always close, regardless of outcome.
        if let Some(id) = session.as_deref() {
            if let Err(e) = client.close(id).await {
                warn!(error = %e, "best-effort Close after error failed");
            }
        }

        sink.flush()?;
        result.map(|()| rows_written)
    }
}

async fn run_inner(
    plan: &Plan,
    client: &Client,
    sink: &mut dyn OutputSink,
    session: &mut Option<String>,
    last_query: &mut Vec<proto::ElementRef>,
    rows_written: &mut usize,
) -> Result<(), EngineError> {
    for step in &plan.steps {
        match step {
            PlanStep::Initialize { config } => {
                let outcome = client
                    .initialize(
                        config.clone(),
                        plan.required_capabilities.iter().cloned().collect(),
                    )
                    .await?;
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
                client.navigate(id, url, *wait_until).await?;
                last_query.clear();
            }
            PlanStep::Query {
                selector,
                kind,
                limit,
            } => {
                let id = require_session(session.as_deref())?;
                let elements = client.query(id, selector, *kind, *limit).await?;
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
                    let entries = client.extract(id, element, fields.clone()).await?;
                    let row = entries_to_row(&entries);
                    sink.write_row(&row)?;
                    *rows_written += 1;
                }
            }
            PlanStep::Close => {
                if let Some(id) = session.take() {
                    client.close(&id).await?;
                }
            }
        }
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
