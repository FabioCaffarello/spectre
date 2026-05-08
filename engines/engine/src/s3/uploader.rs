// SPDX-License-Identifier: Apache-2.0

//! S3 uploader wrapping `aws_sdk_s3::Client`.
//!
//! One [`S3Uploader`] per engine process. Constructed at startup
//! by [`S3Uploader::from_env`], shared across in-flight RPCs by
//! cloning the inner `Arc`. The SDK's `Client` is internally
//! reference-counted; it carries the connection pool and the
//! resolved credential / region context.

use std::sync::Arc;

use aws_config::BehaviorVersion;
use aws_credential_types::Credentials;
use aws_sdk_s3::config::Region;
use aws_sdk_s3::error::{ProvideErrorMetadata, SdkError};
use aws_sdk_s3::primitives::ByteStream;
use aws_sdk_s3::{Client, Config};
use uuid::Uuid;

use super::config::{S3Config, S3ConfigError};

const STATIC_CREDENTIALS_PROVIDER: &str = "spectre-static-env";

/// Errors raised when constructing an [`S3Uploader`] or
/// performing an upload. Maps onto the engine's error-code
/// taxonomy: `from_env` errors surface as `S3_UNAVAILABLE` (or
/// the explicit `NotConfigured` arm — INFO-level), and upload
/// errors surface as `S3_UPLOAD_FAILED`.
#[derive(Debug, thiserror::Error)]
pub enum S3Error {
    /// None of the `SPECTRE_S3_*` env vars are set. The engine
    /// binary treats this as INFO-level "BYO-credentials mode"
    /// — see [`super::config::S3ConfigError::NotConfigured`].
    #[error("s3 not configured: {0}")]
    NotConfigured(#[source] S3ConfigError),

    /// Environment-level configuration error other than the
    /// soft `NotConfigured` arm (e.g. only one of the
    /// access-key pair was set).
    #[error("s3 config: {0}")]
    Config(#[source] S3ConfigError),

    /// `PutObject` failed: network blip, 5xx, throttling,
    /// permissions, etc. The job terminates with
    /// `S3_UPLOAD_FAILED`.
    #[error("s3 put_object failed: {0}")]
    Upload(String),
}

/// Engine-level wrapper around a single `aws_sdk_s3::Client`.
pub struct S3Uploader {
    client: Client,
    endpoint_label: String,
}

impl S3Uploader {
    /// Construct an uploader from the `SPECTRE_S3_*` env vars.
    ///
    /// The construction itself does **not** dial S3 (no
    /// HEAD-bucket probe) — buckets are per-job CRD inputs and
    /// the engine has no global "default bucket" to validate.
    /// Per-job rejections happen in the dispatch path.
    ///
    /// # Errors
    ///
    /// - [`S3Error::NotConfigured`] when none of the env vars
    ///   are set. The engine binary distinguishes this arm and
    ///   logs INFO rather than WARN (ADR-0024 §5).
    /// - [`S3Error::Config`] for other env-level errors (e.g.
    ///   only one of the access-key pair was set).
    pub fn from_env() -> Result<Self, S3Error> {
        let cfg = S3Config::from_env().map_err(|e| match e {
            S3ConfigError::NotConfigured => S3Error::NotConfigured(e),
            e @ S3ConfigError::IncompleteCredentials => S3Error::Config(e),
        })?;
        Ok(Self::from_config(&cfg))
    }

    /// Construct an uploader directly from a parsed
    /// [`S3Config`]. Public to keep the integration test surface
    /// minimal — production callers use [`S3Uploader::from_env`].
    #[must_use]
    pub fn from_config(cfg: &S3Config) -> Self {
        let mut builder = Config::builder()
            .behavior_version(BehaviorVersion::latest())
            .region(Region::new(cfg.region.clone()))
            .force_path_style(true); // MinIO-friendly; ignored by AWS

        if let Some(endpoint) = cfg.endpoint.as_deref() {
            builder = builder.endpoint_url(endpoint);
        }

        if let (Some(access), Some(secret)) = (
            cfg.access_key_id.as_deref(),
            cfg.secret_access_key.as_deref(),
        ) {
            let creds = Credentials::new(access, secret, None, None, STATIC_CREDENTIALS_PROVIDER);
            builder = builder.credentials_provider(creds);
        }

        let client = Client::from_conf(builder.build());

        let endpoint_label = cfg
            .endpoint
            .clone()
            .unwrap_or_else(|| format!("aws (region={})", cfg.region));

        Self {
            client,
            endpoint_label,
        }
    }

    /// Endpoint string suitable for startup log lines. For real
    /// AWS S3 (no custom endpoint) returns
    /// `"aws (region=<r>)"`; for `MinIO` / R2 / etc. returns the
    /// configured URL.
    #[must_use]
    pub fn endpoint_label(&self) -> &str {
        &self.endpoint_label
    }

    /// Upload `body` to `bucket` / `key` as a single
    /// `PutObject`. `body` is the JSONL-formatted job output,
    /// uploaded with `Content-Type: application/x-ndjson`.
    ///
    /// `bucket` and `key` come from the per-job
    /// `RunJobRequest.s3` config; the dispatch path renders
    /// `key` from the CRD's template (see [`render_key`]) before
    /// calling this method.
    ///
    /// # Errors
    ///
    /// Returns [`S3Error::Upload`] when the SDK reports any
    /// failure: dispatch error (network), service error (4xx /
    /// 5xx from S3), or response-parse failure. The engine
    /// surfaces this to the gRPC client as a `Failed` event with
    /// `error_code = "S3_UPLOAD_FAILED"`.
    pub async fn upload_jsonl(
        &self,
        bucket: &str,
        key: &str,
        body: Vec<u8>,
    ) -> Result<(), S3Error> {
        let stream = ByteStream::from(body);
        self.client
            .put_object()
            .bucket(bucket)
            .key(key)
            .content_type("application/x-ndjson")
            .body(stream)
            .send()
            .await
            .map(|_| ())
            .map_err(|e| S3Error::Upload(format_put_object_error(&e)))
    }
}

/// Render an `SdkError<PutObjectError>` into a diagnostic string
/// for `S3Error::Upload`. The SDK's `Display` only renders the
/// category (`"service error"` / `"dispatch failure"` / …);
/// match on the variant to surface HTTP status, error code, and
/// error message so smoke failures are debuggable from the run
/// log alone (production-smoke mini-phase 2026-05-07 — every
/// failure since R7.2 surfaced as a bare "service error" with no
/// way to tell auth-vs-bucket-vs-region from the kubectl output).
fn format_put_object_error<E, R>(err: &SdkError<E, R>) -> String
where
    E: std::error::Error + ProvideErrorMetadata,
{
    match err {
        SdkError::ServiceError(svc) => {
            let inner = svc.err();
            format!(
                "service error: code={} message={}",
                inner.code().unwrap_or("(none)"),
                inner.message().unwrap_or("(none)"),
            )
        }
        SdkError::DispatchFailure(_) => format!("dispatch failure: {err}"),
        SdkError::TimeoutError(_) => "timeout".to_owned(),
        SdkError::ResponseError(_) => format!("response parse error: {err}"),
        SdkError::ConstructionFailure(_) => format!("construction failure: {err}"),
        _ => format!("{err}"),
    }
}

/// Wrapper that exposes [`S3Uploader::from_env`]'s signature for
/// the engine binary while letting the binary share the inner
/// uploader as `Arc<S3Uploader>` across in-flight RPCs.
impl S3Uploader {
    /// Wrap `self` in an `Arc` for shared ownership.
    #[must_use]
    pub fn into_shared(self) -> Arc<Self> {
        Arc::new(self)
    }
}

/// Render the CRD's S3 key template by substituting `{{.JobID}}`
/// with the supplied UUID. Other tokens pass through verbatim.
/// Hand-rolled rather than pulling a template engine — only the
/// one token is supported in v1alpha1 (ADR-0024 §3); v1alpha2
/// may add `{{.Driver}}`, `{{.Timestamp}}`, etc.
#[must_use]
pub fn render_key(template: &str, job_id: Uuid) -> String {
    template.replace("{{.JobID}}", &job_id.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn render_key_substitutes_job_id() {
        let id = Uuid::parse_str("3f2504e0-4f89-11d3-9a0c-0305e82c3301").unwrap();
        let rendered = render_key("scrapes/{{.JobID}}/rows.jsonl", id);
        assert_eq!(
            rendered,
            "scrapes/3f2504e0-4f89-11d3-9a0c-0305e82c3301/rows.jsonl"
        );
    }

    #[test]
    fn render_key_passes_through_literal() {
        let id = Uuid::nil();
        assert_eq!(render_key("static/key", id), "static/key");
    }

    #[test]
    fn render_key_substitutes_multiple_occurrences() {
        let id = Uuid::parse_str("3f2504e0-4f89-11d3-9a0c-0305e82c3301").unwrap();
        let rendered = render_key("{{.JobID}}/{{.JobID}}", id);
        assert!(rendered.contains("3f2504e0-4f89-11d3-9a0c-0305e82c3301"));
        // Two substitutions joined by '/'
        assert_eq!(rendered.matches("3f2504e0").count(), 2);
    }

    #[test]
    fn render_key_does_not_recognise_other_tokens() {
        // {{.Driver}} is reserved for v1alpha2 (ADR-0024 §3); in
        // v1alpha1 it passes through verbatim so users don't get
        // a silently-empty substitution.
        let id = Uuid::nil();
        let out = render_key("d/{{.Driver}}/k", id);
        assert_eq!(out, "d/{{.Driver}}/k");
    }

    #[test]
    fn from_config_is_infallible_with_valid_inputs() {
        // Construction does not dial S3, so it succeeds for any
        // shape of S3Config. Only the upload call surfaces
        // network-level errors.
        let cfg = S3Config {
            endpoint: Some("http://localhost:9000".to_owned()),
            region: "us-east-1".to_owned(),
            access_key_id: Some("AKIATEST".to_owned()),
            secret_access_key: Some("secret".to_owned()),
        };
        let uploader = S3Uploader::from_config(&cfg);
        assert_eq!(uploader.endpoint_label(), "http://localhost:9000");
    }

    #[test]
    fn endpoint_label_falls_back_to_region_for_real_aws() {
        let cfg = S3Config {
            endpoint: None,
            region: "eu-west-1".to_owned(),
            access_key_id: None,
            secret_access_key: None,
        };
        let uploader = S3Uploader::from_config(&cfg);
        assert_eq!(uploader.endpoint_label(), "aws (region=eu-west-1)");
    }
}
