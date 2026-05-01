// SPDX-License-Identifier: Apache-2.0

//! DSL parser and validation.
//!
//! The DSL is YAML at `spectre: v1alpha1`. The grammar is:
//!
//! ```yaml
//! spectre: v1alpha1
//! driver: <name>          # currently: playwright
//! steps:
//!   - navigate: <url>
//!   - extract:
//!       selector: <css-selector>
//!       fields:
//!         <name>: <field-spec>
//!         ...
//! output:
//!   format: jsonl
//!   path: ./<file>        # or `-` for stdout
//! ```
//!
//! `<field-spec>` is one of:
//!
//! | spec          | resolves to                                            |
//! |---------------|--------------------------------------------------------|
//! | `textContent` | `Field { mode: MODE_TEXT_CONTENT, arg: "" }`           |
//! | `innerText`   | `Field { mode: MODE_INNER_TEXT,   arg: "" }`           |
//! | `innerHTML`   | `Field { mode: MODE_INNER_HTML,   arg: "" }`           |
//! | `outerHTML`   | `Field { mode: MODE_OUTER_HTML,   arg: "" }`           |
//! | `href`        | `Field { mode: MODE_ATTR,         arg: "href" }`       |
//! | `src`         | `Field { mode: MODE_ATTR,         arg: "src" }`        |
//! | `attr:<name>` | `Field { mode: MODE_ATTR,         arg: "<name>" }`     |
//!
//! `MODE_EVAL` is intentionally not exposed in the v1alpha1 DSL; see
//! ADR-0012 §6.
//!
//! Validation is hand-rolled: a YAML pass deserialises into a
//! `RawJob`, then a second pass produces the validated [`Job`] and
//! emits structured [`JobError`] values. Adding a new validation rule
//! is a one-line match arm; see ADR-0012 §2.

use std::collections::BTreeMap;
use std::path::PathBuf;

use serde::Deserialize;
use thiserror::Error;
use url::Url;

/// Hardcoded list of drivers the engine knows about in v1alpha1.
/// `seleniumbase` is admitted by ADR-0014 (the Python adapter's
/// initial landing) and `curl-impersonate` by ADR-0016 (the Go
/// HTTP-only adapter's handshake floor). The list is alphabetical
/// to match the byte-for-byte conformance pattern. A driver
/// registry replaces this list later in Phase 2 — see ADR-0012
/// §"Bad, because" for the hardcoded-list rationale.
pub const KNOWN_DRIVERS: &[&str] = &["curl-impersonate", "playwright", "seleniumbase"];

/// The protocol version string the DSL must declare under `spectre:`.
pub const DSL_VERSION: &str = "v1alpha1";

// -- Public, validated types ------------------------------------------------

/// A parsed and validated job ready to feed the planner.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Job {
    /// Selected driver name. Always one of [`KNOWN_DRIVERS`].
    pub driver: String,
    /// Ordered list of high-level steps.
    pub steps: Vec<Step>,
    /// Output configuration.
    pub output: OutputConfig,
}

impl Job {
    /// Parse and validate a YAML job definition.
    ///
    /// # Errors
    ///
    /// Returns a structured [`JobError`] on YAML syntax errors and on
    /// every validation failure (unknown driver, malformed URL, empty
    /// selector, unknown field-spec, etc.). The error's `Display`
    /// impl renders `<path>: <message>` for terminal use.
    pub fn from_yaml(source: &str) -> Result<Self, JobError> {
        let raw: RawJob = serde_yaml_ng::from_str(source).map_err(JobError::Yaml)?;
        raw.into_job()
    }
}

/// One DSL step. Each variant compiles to one or more protocol RPCs at
/// planning time.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Step {
    /// Navigate the session's page to the given URL.
    Navigate {
        /// Validated absolute URL (scheme, host present).
        url: Url,
    },
    /// Query for elements matching `selector` and extract `fields`
    /// from each match.
    Extract {
        /// CSS selector. v1alpha1 supports CSS only; `XPath` / text /
        /// attribute selectors are a v1alpha2 DSL concern.
        selector: String,
        /// Ordered list of fields to extract per matched element.
        /// The order is the YAML order — `BTreeMap` is used for
        /// deterministic comparison in tests.
        fields: Vec<Field>,
    },
}

/// A single column to extract from a matched element.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Field {
    /// User-given name; becomes the JSON key in output rows.
    pub name: String,
    /// What to read from the element.
    pub mode: FieldMode,
}

/// The seven field modes the v1alpha1 DSL exposes. Each corresponds
/// to a `proto::field::Mode` value at planning time.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FieldMode {
    /// `textContent` — the element's `textContent` property.
    TextContent,
    /// `innerText` — the element's rendered text (excludes hidden
    /// children, respects CSS).
    InnerText,
    /// `innerHTML` — the element's serialised inner HTML.
    InnerHtml,
    /// `outerHTML` — the element's serialised outer HTML.
    OuterHtml,
    /// `attr:<name>` — read the named attribute. Includes the `href`
    /// and `src` shortcuts.
    Attr(String),
}

/// Output configuration.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OutputConfig {
    /// Always JSONL in v1alpha1; the validation rejects others.
    pub format: OutputFormat,
    /// Path as written in the YAML. Resolution against the job
    /// file's directory happens at execution time (see ADR-0012 §5).
    /// The literal `-` means stdout.
    pub path: String,
}

/// Output formats the DSL accepts.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OutputFormat {
    /// JSON Lines (RFC-less but ubiquitous; one JSON object per
    /// newline-terminated line). See <https://jsonlines.org/>.
    Jsonl,
}

// -- Errors ----------------------------------------------------------------

/// Error type for DSL parsing and validation. The `Display` impl
/// renders `<path>: <message>` so terminal users get one
/// self-contained line per error.
#[derive(Debug, Error)]
pub enum JobError {
    /// Underlying YAML syntax error from `serde_yaml_ng`. Contains
    /// the original error including line/column.
    #[error("yaml: {0}")]
    Yaml(serde_yaml_ng::Error),

    /// `spectre:` key is missing or set to a value other than the
    /// hardcoded protocol version constant.
    #[error("unknown protocol version {found:?}; expected {expected:?}")]
    UnknownProtocol {
        /// What the user wrote.
        found: String,
        /// What the engine expects.
        expected: &'static str,
    },

    /// `driver:` is missing or names a driver not in [`KNOWN_DRIVERS`].
    #[error("driver: unknown driver {found:?} (known: {known:?})")]
    UnknownDriver {
        /// What the user wrote.
        found: String,
        /// The hardcoded set of known driver names.
        known: Vec<String>,
    },

    /// A field-spec string is not in the seven-shape table.
    #[error("{path}: unknown field-spec {spec:?}")]
    UnknownFieldSpec {
        /// Field path inside the YAML, e.g. `steps[1].extract.fields.title`.
        path: String,
        /// The unrecognised spec string.
        spec: String,
    },

    /// `navigate:` URL is malformed or relative.
    #[error("{path}: invalid url {url:?}: {reason}")]
    InvalidUrl {
        /// Field path inside the YAML.
        path: String,
        /// What the user wrote.
        url: String,
        /// Underlying parser error or scheme rejection reason.
        reason: String,
    },

    /// Catch-all for structural validation errors. The `path` field
    /// names the position in the YAML; `message` is the diagnostic.
    #[error("{path}: {message}")]
    Invalid {
        /// Field path inside the YAML.
        path: String,
        /// Diagnostic message.
        message: String,
    },
}

impl JobError {
    fn invalid(path: impl Into<String>, message: impl Into<String>) -> Self {
        JobError::Invalid {
            path: path.into(),
            message: message.into(),
        }
    }
}

// -- Raw deserialisation types ---------------------------------------------

#[derive(Debug, Deserialize)]
struct RawJob {
    spectre: Option<String>,
    driver: Option<String>,
    #[serde(default)]
    steps: Vec<RawStep>,
    output: Option<RawOutput>,
}

#[derive(Debug, Deserialize)]
struct RawStep {
    navigate: Option<String>,
    extract: Option<RawExtract>,
}

#[derive(Debug, Deserialize)]
struct RawExtract {
    selector: Option<String>,
    #[serde(default)]
    fields: BTreeMap<String, String>,
}

#[derive(Debug, Deserialize)]
struct RawOutput {
    format: Option<String>,
    path: Option<String>,
}

impl RawJob {
    fn into_job(self) -> Result<Job, JobError> {
        // Protocol version
        let spectre = self
            .spectre
            .ok_or_else(|| JobError::invalid("spectre", "missing top-level `spectre:` key"))?;
        if spectre != DSL_VERSION {
            return Err(JobError::UnknownProtocol {
                found: spectre,
                expected: DSL_VERSION,
            });
        }

        // Driver
        let driver = self
            .driver
            .ok_or_else(|| JobError::invalid("driver", "missing `driver:` key"))?;
        if !KNOWN_DRIVERS.contains(&driver.as_str()) {
            return Err(JobError::UnknownDriver {
                found: driver,
                known: KNOWN_DRIVERS.iter().map(|s| (*s).to_string()).collect(),
            });
        }

        // Steps
        if self.steps.is_empty() {
            return Err(JobError::invalid("steps", "must contain at least one step"));
        }
        let mut steps = Vec::with_capacity(self.steps.len());
        for (i, raw) in self.steps.into_iter().enumerate() {
            steps.push(raw_step_into_step(i, raw)?);
        }

        // Output
        let raw_out = self
            .output
            .ok_or_else(|| JobError::invalid("output", "missing `output:` block"))?;
        let format = match raw_out.format.as_deref() {
            Some("jsonl") => OutputFormat::Jsonl,
            Some(other) => {
                return Err(JobError::invalid(
                    "output.format",
                    format!("unknown output format {other:?}; expected \"jsonl\""),
                ));
            }
            None => return Err(JobError::invalid("output.format", "missing `format:` key")),
        };
        let path = raw_out
            .path
            .ok_or_else(|| JobError::invalid("output.path", "missing `path:` key"))?;
        if path.is_empty() {
            return Err(JobError::invalid("output.path", "must not be empty"));
        }

        Ok(Job {
            driver,
            steps,
            output: OutputConfig { format, path },
        })
    }
}

fn raw_step_into_step(index: usize, raw: RawStep) -> Result<Step, JobError> {
    let path = format!("steps[{index}]");
    match (raw.navigate, raw.extract) {
        (Some(url), None) => {
            let parsed = Url::parse(&url).map_err(|e| JobError::InvalidUrl {
                path: format!("{path}.navigate"),
                url: url.clone(),
                reason: e.to_string(),
            })?;
            if parsed.scheme() != "http" && parsed.scheme() != "https" {
                return Err(JobError::InvalidUrl {
                    path: format!("{path}.navigate"),
                    url,
                    reason: format!(
                        "scheme {:?} not allowed; expected http or https",
                        parsed.scheme()
                    ),
                });
            }
            Ok(Step::Navigate { url: parsed })
        }
        (None, Some(extract)) => {
            let selector = extract.selector.ok_or_else(|| {
                JobError::invalid(format!("{path}.extract"), "missing `selector:`")
            })?;
            if selector.trim().is_empty() {
                return Err(JobError::invalid(
                    format!("{path}.extract.selector"),
                    "must not be empty",
                ));
            }
            if extract.fields.is_empty() {
                return Err(JobError::invalid(
                    format!("{path}.extract.fields"),
                    "must contain at least one field",
                ));
            }
            let mut fields = Vec::with_capacity(extract.fields.len());
            for (name, spec) in extract.fields {
                let mode = parse_field_spec(&spec).ok_or_else(|| JobError::UnknownFieldSpec {
                    path: format!("{path}.extract.fields.{name}"),
                    spec: spec.clone(),
                })?;
                fields.push(Field { name, mode });
            }
            Ok(Step::Extract { selector, fields })
        }
        (Some(_), Some(_)) => Err(JobError::invalid(
            path,
            "step must have exactly one of `navigate` or `extract`, not both",
        )),
        (None, None) => Err(JobError::invalid(
            path,
            "step must have `navigate` or `extract`",
        )),
    }
}

fn parse_field_spec(spec: &str) -> Option<FieldMode> {
    match spec {
        "textContent" => Some(FieldMode::TextContent),
        "innerText" => Some(FieldMode::InnerText),
        "innerHTML" => Some(FieldMode::InnerHtml),
        "outerHTML" => Some(FieldMode::OuterHtml),
        "href" => Some(FieldMode::Attr("href".into())),
        "src" => Some(FieldMode::Attr("src".into())),
        other if other.starts_with("attr:") => {
            let name = other.trim_start_matches("attr:").trim();
            if name.is_empty() {
                None
            } else {
                Some(FieldMode::Attr(name.to_owned()))
            }
        }
        _ => None,
    }
}

/// Resolve `path` from `OutputConfig` against `base_dir` (the
/// directory containing the job file). Returns `None` for the literal
/// stdout sentinel `-`. See ADR-0012 §5 for the rationale.
#[must_use]
pub fn resolve_output_path(path: &str, base_dir: &std::path::Path) -> Option<PathBuf> {
    if path == "-" {
        return None;
    }
    let p = std::path::Path::new(path);
    if p.is_absolute() {
        Some(p.to_path_buf())
    } else {
        Some(base_dir.join(p))
    }
}

#[cfg(test)]
#[allow(
    clippy::needless_raw_string_hashes,
    clippy::match_wildcard_for_single_variants
)]
mod tests {
    use super::*;

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
    fn parses_hello_hackernews() {
        let job = Job::from_yaml(HELLO_HACKERNEWS).expect("parse should succeed");
        assert_eq!(job.driver, "playwright");
        assert_eq!(job.steps.len(), 2);
        match &job.steps[0] {
            Step::Navigate { url } => {
                assert_eq!(url.as_str(), "https://news.ycombinator.com/");
            }
            _ => panic!("expected Navigate"),
        }
        match &job.steps[1] {
            Step::Extract { selector, fields } => {
                assert_eq!(selector, ".titleline > a");
                assert_eq!(fields.len(), 2);
                let names: Vec<&str> = fields.iter().map(|f| f.name.as_str()).collect();
                assert!(names.contains(&"title"));
                assert!(names.contains(&"url"));
                for f in fields {
                    match (f.name.as_str(), &f.mode) {
                        ("title", FieldMode::TextContent) => {}
                        ("url", FieldMode::Attr(arg)) if arg == "href" => {}
                        (name, mode) => panic!("unexpected field {name}: {mode:?}"),
                    }
                }
            }
            _ => panic!("expected Extract"),
        }
        assert_eq!(job.output.format, OutputFormat::Jsonl);
        assert_eq!(job.output.path, "./stories.jsonl");
    }

    #[test]
    fn round_trip_via_yaml_string() {
        // Re-parsing the same YAML produces an equal Job. We do not
        // serialise the validated Job back to YAML — the round-trip is
        // "the parser is deterministic given the same input".
        let a = Job::from_yaml(HELLO_HACKERNEWS).expect("parse a");
        let b = Job::from_yaml(HELLO_HACKERNEWS).expect("parse b");
        assert_eq!(a, b);
    }

    #[test]
    fn rejects_empty_selector() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://example.com
  - extract:
      selector: ""
      fields:
        title: textContent
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::Invalid { path, .. }) => {
                assert_eq!(path, "steps[1].extract.selector");
            }
            other => panic!("expected Invalid for empty selector, got {other:?}"),
        }
    }

    #[test]
    fn rejects_unknown_field_spec() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://example.com
  - extract:
      selector: a
      fields:
        x: weird
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::UnknownFieldSpec { path, spec }) => {
                assert_eq!(path, "steps[1].extract.fields.x");
                assert_eq!(spec, "weird");
            }
            other => panic!("expected UnknownFieldSpec, got {other:?}"),
        }
    }

    #[test]
    fn rejects_unknown_driver() {
        let yaml = r#"
spectre: v1alpha1
driver: not-a-driver
steps:
  - navigate: https://example.com
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::UnknownDriver { found, .. }) => assert_eq!(found, "not-a-driver"),
            other => panic!("expected UnknownDriver, got {other:?}"),
        }
    }

    #[test]
    fn accepts_seleniumbase_driver() {
        // ADR-0014: KNOWN_DRIVERS includes `seleniumbase`. A job
        // declaring it must parse successfully.
        let yaml = r#"
spectre: v1alpha1
driver: seleniumbase
steps:
  - navigate: https://example.com
output:
  format: jsonl
  path: ./out.jsonl
"#;
        let job = Job::from_yaml(yaml).expect("seleniumbase must be a known driver");
        assert_eq!(job.driver, "seleniumbase");
    }

    #[test]
    fn accepts_curl_impersonate_driver() {
        // ADR-0016: KNOWN_DRIVERS includes `curl-impersonate`. A
        // job declaring it must parse successfully even though
        // only the HTTP-only navigation
        // capability is honoured by the underlying adapter — the
        // engine's planner is responsible for capability checks
        // (validate_capabilities), not the DSL parser.
        let yaml = r#"
spectre: v1alpha1
driver: curl-impersonate
steps:
  - navigate: https://example.com
output:
  format: jsonl
  path: ./out.jsonl
"#;
        let job = Job::from_yaml(yaml).expect("curl-impersonate must be a known driver");
        assert_eq!(job.driver, "curl-impersonate");
    }

    #[test]
    fn known_drivers_contains_three_phase2_adapters() {
        // Guard against accidental shrinkage of the list. The order
        // is alphabetical because the byte-for-byte conformance
        // assertion compares lists, and a stable order survives
        // editor reordering.
        assert_eq!(
            KNOWN_DRIVERS,
            &["curl-impersonate", "playwright", "seleniumbase"]
        );
    }

    #[test]
    fn rejects_unknown_protocol_version() {
        let yaml = r#"
spectre: v0
driver: playwright
steps:
  - navigate: https://example.com
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::UnknownProtocol { found, expected }) => {
                assert_eq!(found, "v0");
                assert_eq!(expected, DSL_VERSION);
            }
            other => panic!("expected UnknownProtocol, got {other:?}"),
        }
    }

    #[test]
    fn rejects_relative_url() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: not-a-url
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::InvalidUrl { path, .. }) => {
                assert_eq!(path, "steps[0].navigate");
            }
            other => panic!("expected InvalidUrl, got {other:?}"),
        }
    }

    #[test]
    fn rejects_non_http_scheme() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: file:///etc/passwd
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::InvalidUrl { reason, .. }) => {
                assert!(reason.contains("scheme"), "reason={reason}");
            }
            other => panic!("expected InvalidUrl scheme rejection, got {other:?}"),
        }
    }

    #[test]
    fn parses_attr_shorthand() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://example.com
  - extract:
      selector: img
      fields:
        id: "attr:data-id"
        s: "src"
output:
  format: jsonl
  path: ./out.jsonl
"#;
        let job = Job::from_yaml(yaml).expect("parse");
        if let Step::Extract { fields, .. } = &job.steps[1] {
            for f in fields {
                match (f.name.as_str(), &f.mode) {
                    ("id", FieldMode::Attr(arg)) if arg == "data-id" => {}
                    ("s", FieldMode::Attr(arg)) if arg == "src" => {}
                    (n, m) => panic!("unexpected field {n}: {m:?}"),
                }
            }
        } else {
            panic!("expected Extract");
        }
    }

    #[test]
    fn rejects_empty_attr_target() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://example.com
  - extract:
      selector: a
      fields:
        x: "attr:"
output:
  format: jsonl
  path: ./out.jsonl
"#;
        assert!(matches!(
            Job::from_yaml(yaml),
            Err(JobError::UnknownFieldSpec { .. })
        ));
    }

    #[test]
    fn rejects_step_with_both_kinds() {
        let yaml = r#"
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://example.com
    extract:
      selector: a
      fields:
        x: textContent
output:
  format: jsonl
  path: ./out.jsonl
"#;
        match Job::from_yaml(yaml) {
            Err(JobError::Invalid { path, message }) => {
                assert_eq!(path, "steps[0]");
                assert!(message.contains("exactly one"));
            }
            other => panic!("expected Invalid, got {other:?}"),
        }
    }

    #[test]
    fn resolve_output_path_handles_stdout_sentinel() {
        let p = std::path::Path::new("/tmp/job");
        assert!(resolve_output_path("-", p).is_none());
    }

    #[test]
    fn resolve_output_path_relative() {
        let p = std::path::Path::new("/tmp/job");
        let out = resolve_output_path("./stories.jsonl", p).unwrap();
        assert_eq!(out, std::path::Path::new("/tmp/job/./stories.jsonl"));
    }

    #[test]
    fn resolve_output_path_absolute() {
        let p = std::path::Path::new("/tmp/job");
        let out = resolve_output_path("/var/log/x.jsonl", p).unwrap();
        assert_eq!(out, std::path::Path::new("/var/log/x.jsonl"));
    }
}
