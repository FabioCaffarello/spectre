// SPDX-License-Identifier: Apache-2.0

//! S3 client configuration sourced from the process environment.
//!
//! The engine reads `SPECTRE_S3_ENDPOINT` (optional),
//! `SPECTRE_S3_REGION` (optional), `SPECTRE_S3_ACCESS_KEY_ID`
//! (optional) and `SPECTRE_S3_SECRET_ACCESS_KEY` (optional). All
//! four are optional because the AWS SDK's default credential
//! chain (IAM role, AWS SSO, profile, etc.) covers the
//! production-typical shape; the engine is happy in
//! BYO-credentials mode and per-job CRD fields override the
//! engine defaults anyway (ADR-0024 §3).
//!
//! "Configured" vs "unconfigured" is determined by the **endpoint
//! plus credential set**: when none of the four env vars are set,
//! [`S3Config::from_env`] returns [`S3ConfigError::NotConfigured`].
//! The engine binary distinguishes that arm from real errors so
//! the startup log level is INFO, not WARN.

use std::env;

const ENDPOINT_ENV: &str = "SPECTRE_S3_ENDPOINT";
const REGION_ENV: &str = "SPECTRE_S3_REGION";
const ACCESS_KEY_ENV: &str = "SPECTRE_S3_ACCESS_KEY_ID";
const SECRET_KEY_ENV: &str = "SPECTRE_S3_SECRET_ACCESS_KEY";

/// Default region applied when neither the env var nor the CRD
/// override sets one. Mirrors the CRD's kubebuilder default of
/// `us-east-1`.
pub const DEFAULT_REGION: &str = "us-east-1";

/// Parsed S3 client configuration. All fields are optional at
/// the env layer; the per-job CRD override
/// (`S3Sink.Endpoint`, `S3Sink.Region`) takes precedence on the
/// dispatch path.
#[derive(Clone, Debug)]
pub struct S3Config {
    /// Custom endpoint URL — empty for real AWS S3, set to the
    /// `MinIO` / R2 / Wasabi URL for local dev or alternate
    /// providers.
    pub endpoint: Option<String>,

    /// Default region. Used when neither the per-job
    /// `S3Sink.Region` nor the CRD's kubebuilder default sets
    /// one. Defaults to [`DEFAULT_REGION`].
    pub region: String,

    /// Optional explicit access key. When set together with
    /// [`S3Config::secret_access_key`], overrides the SDK's
    /// default credential chain.
    pub access_key_id: Option<String>,

    /// Companion to [`S3Config::access_key_id`].
    pub secret_access_key: Option<String>,
}

/// Errors raised when parsing the S3 environment configuration.
#[derive(Debug, thiserror::Error)]
pub enum S3ConfigError {
    /// None of the `SPECTRE_S3_*` env vars are set. The engine
    /// binary treats this arm as INFO-level "BYO-credentials
    /// mode" rather than WARN-level misconfiguration —
    /// production deployments typically rely on the AWS default
    /// credential chain (IAM role / SSO / profile) and have no
    /// reason to set these env vars.
    #[error("no SPECTRE_S3_* env vars set")]
    NotConfigured,

    /// `SPECTRE_S3_ACCESS_KEY_ID` was set without
    /// `SPECTRE_S3_SECRET_ACCESS_KEY` (or vice versa). The two
    /// variables are a pair and either both must be set or
    /// neither.
    #[error(
        "incomplete S3 credentials: {ACCESS_KEY_ENV} and {SECRET_KEY_ENV} must both be set or both unset"
    )]
    IncompleteCredentials,
}

impl S3Config {
    /// Build a config from the `SPECTRE_S3_*` env vars.
    ///
    /// # Errors
    ///
    /// Returns [`S3ConfigError::NotConfigured`] when none of the
    /// env vars are set. Returns
    /// [`S3ConfigError::IncompleteCredentials`] when only one of
    /// the access-key-id / secret-access-key pair is set.
    pub fn from_env() -> Result<Self, S3ConfigError> {
        Self::from_lookup(|name| env::var(name).ok())
    }

    /// Build a config from a pluggable env-lookup closure.
    /// Production code calls [`S3Config::from_env`]; tests
    /// inject a `HashMap` lookup so process env stays
    /// untouched (the engine crate's `unsafe_code = "forbid"`
    /// lint rejects the `unsafe { env::set_var(...) }` calls
    /// the 2024-edition stdlib now requires).
    ///
    /// # Errors
    ///
    /// See [`S3Config::from_env`].
    pub fn from_lookup<F>(lookup: F) -> Result<Self, S3ConfigError>
    where
        F: Fn(&str) -> Option<String>,
    {
        let endpoint = trimmed(lookup(ENDPOINT_ENV));
        let region = trimmed(lookup(REGION_ENV));
        let access_key_id = trimmed(lookup(ACCESS_KEY_ENV));
        let secret_access_key = trimmed(lookup(SECRET_KEY_ENV));

        if endpoint.is_none()
            && region.is_none()
            && access_key_id.is_none()
            && secret_access_key.is_none()
        {
            return Err(S3ConfigError::NotConfigured);
        }

        if access_key_id.is_some() != secret_access_key.is_some() {
            return Err(S3ConfigError::IncompleteCredentials);
        }

        Ok(Self {
            endpoint,
            region: region.unwrap_or_else(|| DEFAULT_REGION.to_owned()),
            access_key_id,
            secret_access_key,
        })
    }
}

fn trimmed(raw: Option<String>) -> Option<String> {
    raw.and_then(|s| {
        let t = s.trim();
        if t.is_empty() {
            None
        } else {
            Some(t.to_owned())
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn lookup<'a>(
        map: &'a HashMap<&'static str, &'static str>,
    ) -> impl Fn(&str) -> Option<String> + 'a {
        move |k: &str| map.get(k).map(|v| (*v).to_owned())
    }

    #[test]
    fn from_lookup_returns_not_configured_when_all_unset() {
        let map: HashMap<&str, &str> = HashMap::new();
        match S3Config::from_lookup(lookup(&map)) {
            Err(S3ConfigError::NotConfigured) => {}
            other => panic!("expected NotConfigured, got {other:?}"),
        }
    }

    #[test]
    fn from_lookup_parses_endpoint_only() {
        let mut map = HashMap::new();
        map.insert(ENDPOINT_ENV, "http://minio:9000");
        let cfg = S3Config::from_lookup(lookup(&map)).expect("ok");
        assert_eq!(cfg.endpoint.as_deref(), Some("http://minio:9000"));
        assert_eq!(cfg.region, DEFAULT_REGION);
        assert!(cfg.access_key_id.is_none());
    }

    #[test]
    fn from_lookup_parses_full_credentials() {
        let mut map = HashMap::new();
        map.insert(ENDPOINT_ENV, "http://minio:9000");
        map.insert(REGION_ENV, "eu-west-1");
        map.insert(ACCESS_KEY_ENV, "AKIATEST");
        map.insert(SECRET_KEY_ENV, "secret");
        let cfg = S3Config::from_lookup(lookup(&map)).expect("ok");
        assert_eq!(cfg.endpoint.as_deref(), Some("http://minio:9000"));
        assert_eq!(cfg.region, "eu-west-1");
        assert_eq!(cfg.access_key_id.as_deref(), Some("AKIATEST"));
        assert_eq!(cfg.secret_access_key.as_deref(), Some("secret"));
    }

    #[test]
    fn from_lookup_rejects_lone_access_key() {
        let mut map = HashMap::new();
        map.insert(ACCESS_KEY_ENV, "AKIATEST");
        match S3Config::from_lookup(lookup(&map)) {
            Err(S3ConfigError::IncompleteCredentials) => {}
            other => panic!("expected IncompleteCredentials, got {other:?}"),
        }
    }

    #[test]
    fn from_lookup_rejects_lone_secret_key() {
        let mut map = HashMap::new();
        map.insert(SECRET_KEY_ENV, "secret");
        match S3Config::from_lookup(lookup(&map)) {
            Err(S3ConfigError::IncompleteCredentials) => {}
            other => panic!("expected IncompleteCredentials, got {other:?}"),
        }
    }

    #[test]
    fn whitespace_only_treated_as_unset() {
        let mut map = HashMap::new();
        map.insert(ENDPOINT_ENV, "   ");
        match S3Config::from_lookup(lookup(&map)) {
            Err(S3ConfigError::NotConfigured) => {}
            other => panic!("expected NotConfigured for whitespace-only endpoint, got {other:?}"),
        }
    }

    #[test]
    fn default_region_is_us_east_1() {
        // Documents the §3 commitment so a downstream change
        // cannot drift silently.
        assert_eq!(DEFAULT_REGION, "us-east-1");
    }

    #[test]
    fn region_only_uses_default_endpoint_chain() {
        // Region-only env (no endpoint) goes against real AWS S3
        // with the SDK's default endpoint resolution.
        let mut map = HashMap::new();
        map.insert(REGION_ENV, "eu-central-1");
        let cfg = S3Config::from_lookup(lookup(&map)).expect("ok");
        assert!(cfg.endpoint.is_none());
        assert_eq!(cfg.region, "eu-central-1");
    }
}
