// SPDX-License-Identifier: Apache-2.0

//! Planner: compile a validated [`Job`](crate::Job) into a [`Plan`].
//!
//! A `Plan` is data: an ordered list of [`PlanStep`]s, the required
//! capability set, and the validated [`OutputConfig`]. The executor
//! consumes a `Plan` and runs it; the planner does not run anything
//! itself.
//!
//! The capability mapping in v1alpha1 is intentionally narrow: every
//! plan requires `"navigation"`, and a plan whose extract uses
//! `MODE_EVAL` adds `"js_execution"`. The granular `extract_*` and
//! `query_*` capability names declared by the Playwright manifest are
//! descriptive only — see ADR-0010 §3 and ADR-0012 §3.

use std::collections::HashSet;

use thiserror::Error;

use crate::dsl::{Field as DslField, FieldMode, Job, OutputConfig, Step};
use crate::proto;

// -- Plan types ------------------------------------------------------------

/// Compiled job ready for the executor.
#[derive(Debug, Clone, PartialEq)]
pub struct Plan {
    /// Driver to launch (e.g. `"playwright"`).
    pub driver: String,
    /// Capability names the plan requires the driver to declare.
    pub required_capabilities: HashSet<String>,
    /// Ordered protocol-level steps to run.
    pub steps: Vec<PlanStep>,
    /// Output configuration carried through verbatim.
    pub output: OutputConfig,
}

/// One protocol-level step. The executor unrolls
/// [`PlanStep::ExtractEach`] into one `Extract` RPC per `ElementRef`
/// returned by the prior `Query`.
#[derive(Debug, Clone, PartialEq)]
pub enum PlanStep {
    /// `Initialize` — opens a session.
    Initialize {
        /// `SessionConfig` to send. Defaults across the board in
        /// v1alpha1; the DSL exposes none of these knobs yet.
        config: proto::SessionConfig,
    },
    /// `Navigate` — point the session's page at `url`.
    Navigate {
        /// Target URL (absolute, http or https).
        url: String,
        /// Wait condition the driver applies before returning.
        wait_until: proto::WaitCondition,
    },
    /// `Query` — find elements matching `selector`.
    Query {
        /// Selector text passed to the driver.
        selector: String,
        /// Selector kind. v1alpha1 DSL produces `Css` only.
        kind: proto::SelectorKind,
        /// Match limit. Zero means "no limit"; matches the proto
        /// contract.
        limit: u32,
    },
    /// For each `ElementRef` returned by the prior `Query`, run
    /// `Extract` with these fields.
    ExtractEach {
        /// Fields to extract per element. Proto-level types so the
        /// executor can pass them directly to the RPC.
        fields: Vec<proto::Field>,
    },
    /// `Close` — drop the session and any held resources.
    Close,
}

// -- Errors ----------------------------------------------------------------

/// Error type for planning. The two cases the planner can fail on
/// are: a capability needed by the plan is not declared by the
/// driver, and an internal invariant violation (e.g. a planned
/// `ExtractEach` without a preceding `Query`).
#[derive(Debug, Error, PartialEq)]
pub enum PlanError {
    /// One or more required capabilities are missing from the
    /// driver's declared list.
    #[error("driver does not declare required capabilities: {missing:?}")]
    CapabilityMissing {
        /// The missing names, sorted for deterministic output.
        missing: Vec<String>,
    },
}

// -- Planning --------------------------------------------------------------

/// Capability name the planner always requires.
pub const CAPABILITY_NAVIGATION: &str = "navigation";

/// Capability gate for `MODE_EVAL` extracts. Mirrors ADR-0010 §3.
pub const CAPABILITY_JS_EXECUTION: &str = "js_execution";

/// Compile a validated [`Job`] into a [`Plan`].
///
/// The current implementation never returns `Err` — capability
/// validation is a separate function ([`validate_capabilities`]) so
/// the planner can produce a `Plan` even before the engine has dialled
/// the driver and learned the declared capability list.
#[must_use]
pub fn plan(job: &Job) -> Plan {
    let mut steps: Vec<PlanStep> = Vec::with_capacity(job.steps.len() + 2);
    let mut required: HashSet<String> = HashSet::new();
    required.insert(CAPABILITY_NAVIGATION.to_owned());

    steps.push(PlanStep::Initialize {
        config: proto::SessionConfig::default(),
    });

    for step in &job.steps {
        match step {
            Step::Navigate { url } => {
                steps.push(PlanStep::Navigate {
                    url: url.to_string(),
                    wait_until: proto::WaitCondition::Load,
                });
            }
            Step::Extract { selector, fields } => {
                steps.push(PlanStep::Query {
                    selector: selector.clone(),
                    kind: proto::SelectorKind::Css,
                    limit: 0,
                });
                let proto_fields: Vec<proto::Field> =
                    fields.iter().map(dsl_field_to_proto).collect();
                steps.push(PlanStep::ExtractEach {
                    fields: proto_fields,
                });
            }
        }
    }

    steps.push(PlanStep::Close);

    // js_execution is the only mode that gates at the engine layer.
    // The DSL does not expose MODE_EVAL today (ADR-0012 §6), so this
    // loop is a no-op for v1alpha1 jobs — but the structure is here
    // so v1alpha2's `eval:` field-spec needs only the DSL change, not
    // a planner change.
    for s in &steps {
        if let PlanStep::ExtractEach { fields } = s {
            for f in fields {
                if f.mode == proto::field::Mode::Eval as i32 {
                    required.insert(CAPABILITY_JS_EXECUTION.to_owned());
                }
            }
        }
    }

    Plan {
        driver: job.driver.clone(),
        required_capabilities: required,
        steps,
        output: job.output.clone(),
    }
}

/// Verify that `declared` is a superset of `plan.required_capabilities`.
/// Returns `Err(PlanError::CapabilityMissing { missing })` listing the
/// missing names in alphabetical order, or `Ok(())` on success.
///
/// # Errors
///
/// Returns [`PlanError::CapabilityMissing`] if one or more required
/// capabilities are not present in `declared`.
pub fn validate_capabilities(plan: &Plan, declared: &[String]) -> Result<(), PlanError> {
    let declared_set: HashSet<&str> = declared.iter().map(String::as_str).collect();
    let mut missing: Vec<String> = plan
        .required_capabilities
        .iter()
        .filter(|name| !declared_set.contains(name.as_str()))
        .cloned()
        .collect();
    if missing.is_empty() {
        Ok(())
    } else {
        missing.sort();
        Err(PlanError::CapabilityMissing { missing })
    }
}

// -- Helpers ---------------------------------------------------------------

fn dsl_field_to_proto(f: &DslField) -> proto::Field {
    let (mode, arg) = match &f.mode {
        FieldMode::TextContent => (proto::field::Mode::TextContent, String::new()),
        FieldMode::InnerText => (proto::field::Mode::InnerText, String::new()),
        FieldMode::InnerHtml => (proto::field::Mode::InnerHtml, String::new()),
        FieldMode::OuterHtml => (proto::field::Mode::OuterHtml, String::new()),
        FieldMode::Attr(name) => (proto::field::Mode::Attr, name.clone()),
    };
    proto::Field {
        name: f.name.clone(),
        mode: mode as i32,
        arg,
    }
}

#[cfg(test)]
#[allow(clippy::needless_raw_string_hashes)]
mod tests {
    use super::*;
    use crate::dsl::Job;

    const HELLO_HACKERNEWS: &str = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://news.ycombinator.com
  - extract:
      selector: .titleline > a
      fields:
        title: textContent
        url: href
output:
  format: jsonl
  path: ./stories.jsonl
"#;

    #[test]
    fn plan_for_hello_hackernews_has_expected_steps() {
        let job = Job::from_yaml(HELLO_HACKERNEWS).unwrap();
        let plan = plan(&job);
        assert_eq!(plan.driver, "playwright");
        assert_eq!(plan.steps.len(), 5, "init + nav + query + extract + close");
        match &plan.steps[0] {
            PlanStep::Initialize { .. } => {}
            other => panic!("expected Initialize, got {other:?}"),
        }
        match &plan.steps[1] {
            PlanStep::Navigate { url, wait_until } => {
                assert_eq!(url, "https://news.ycombinator.com/");
                assert_eq!(*wait_until, proto::WaitCondition::Load);
            }
            other => panic!("expected Navigate, got {other:?}"),
        }
        match &plan.steps[2] {
            PlanStep::Query {
                selector,
                kind,
                limit,
            } => {
                assert_eq!(selector, ".titleline > a");
                assert_eq!(*kind, proto::SelectorKind::Css);
                assert_eq!(*limit, 0);
            }
            other => panic!("expected Query, got {other:?}"),
        }
        match &plan.steps[3] {
            PlanStep::ExtractEach { fields } => {
                assert_eq!(fields.len(), 2);
                let by_name: std::collections::HashMap<&str, &proto::Field> =
                    fields.iter().map(|f| (f.name.as_str(), f)).collect();
                let title = by_name["title"];
                assert_eq!(title.mode, proto::field::Mode::TextContent as i32);
                assert!(title.arg.is_empty());
                let url_field = by_name["url"];
                assert_eq!(url_field.mode, proto::field::Mode::Attr as i32);
                assert_eq!(url_field.arg, "href");
            }
            other => panic!("expected ExtractEach, got {other:?}"),
        }
        match &plan.steps[4] {
            PlanStep::Close => {}
            other => panic!("expected Close, got {other:?}"),
        }
    }

    #[test]
    fn plan_for_hello_hackernews_requires_only_navigation() {
        let job = Job::from_yaml(HELLO_HACKERNEWS).unwrap();
        let plan = plan(&job);
        let expected: HashSet<String> = ["navigation".to_owned()].into_iter().collect();
        assert_eq!(plan.required_capabilities, expected);
    }

    #[test]
    fn attr_field_maps_to_mode_attr_with_named_arg() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://example.com
  - extract:
      selector: img
      fields:
        id: "attr:data-id"
output:
  format: jsonl
  path: ./out.jsonl
"#;
        let job = Job::from_yaml(yaml).unwrap();
        let plan = plan(&job);
        let extract = plan
            .steps
            .iter()
            .find_map(|s| match s {
                PlanStep::ExtractEach { fields } => Some(fields),
                _ => None,
            })
            .unwrap();
        assert_eq!(extract[0].mode, proto::field::Mode::Attr as i32);
        assert_eq!(extract[0].arg, "data-id");
    }

    #[test]
    fn validate_capabilities_succeeds_when_declared_includes_required() {
        let job = Job::from_yaml(HELLO_HACKERNEWS).unwrap();
        let plan = plan(&job);
        let declared = vec!["navigation".to_owned(), "extract_text".to_owned()];
        assert!(validate_capabilities(&plan, &declared).is_ok());
    }

    #[test]
    fn validate_capabilities_rejects_seleniumbase_full_page_screenshot() {
        // ADR-0015 §5: the SeleniumBase adapter declares two of the
        // three Playwright screenshot capabilities, omitting
        // ``screenshot_full_page``. A plan that requires the
        // capability against a driver whose declared list mirrors
        // the SeleniumBase manifest must be rejected by the engine
        // planner before any browser launches.
        //
        // The DSL does not yet emit ``screenshot_full_page`` as a
        // required capability (Screenshot is not exposed in the DSL
        // surface), so we synthesise the requirement manually — the
        // assertion is on the validator, not on plan emission.
        let job = Job::from_yaml(HELLO_HACKERNEWS).unwrap();
        let mut plan = plan(&job);
        plan.required_capabilities
            .insert("screenshot_full_page".to_owned());
        let declared = vec![
            "extract_attribute".to_owned(),
            "extract_eval".to_owned(),
            "extract_html".to_owned(),
            "extract_text".to_owned(),
            "js_execution".to_owned(),
            "navigation".to_owned(),
            "query_attribute".to_owned(),
            "query_css".to_owned(),
            "query_text".to_owned(),
            "query_xpath".to_owned(),
            "screenshot_element".to_owned(),
            "screenshot_viewport".to_owned(),
        ];
        match validate_capabilities(&plan, &declared) {
            Err(PlanError::CapabilityMissing { missing }) => {
                assert_eq!(missing, vec!["screenshot_full_page".to_owned()]);
            }
            Ok(()) => panic!("expected CapabilityMissing for screenshot_full_page"),
        }
    }

    #[test]
    fn validate_capabilities_reports_missing_names_alphabetically() {
        // Synthesise a Plan with an extra required capability so we
        // can exercise the missing path. The DSL never emits
        // js_execution today; we add it manually.
        let job = Job::from_yaml(HELLO_HACKERNEWS).unwrap();
        let mut plan = plan(&job);
        plan.required_capabilities.insert("js_execution".to_owned());
        plan.required_capabilities.insert("zzz_extra".to_owned());
        let declared: Vec<String> = vec!["extract_text".to_owned()];
        match validate_capabilities(&plan, &declared) {
            Err(PlanError::CapabilityMissing { missing }) => {
                assert_eq!(
                    missing,
                    vec![
                        "js_execution".to_owned(),
                        "navigation".to_owned(),
                        "zzz_extra".to_owned(),
                    ]
                );
            }
            Ok(()) => panic!("expected CapabilityMissing"),
        }
    }
}
