// SPDX-License-Identifier: Apache-2.0

//! TLS detection from environment variables.
//!
//! `TlsConfig::from_env` is the single ingress point: it reads the
//! three `SPECTRE_TLS_*_PATH` vars, classifies the result into
//! `TlsMode`, and rejects the partial-state combinations the chart
//! cannot produce. The engine binary calls this at startup before
//! binding the gRPC server.

use std::env;
use std::path::PathBuf;

use thiserror::Error;

const CERT_PATH_ENV: &str = "SPECTRE_TLS_CERT_PATH";
const KEY_PATH_ENV: &str = "SPECTRE_TLS_KEY_PATH";
const CA_PATH_ENV: &str = "SPECTRE_TLS_CA_PATH";

/// Whether the engine accepts plaintext or mTLS gRPC traffic.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TlsMode {
    /// No TLS material configured. The engine binds plaintext
    /// gRPC — the v1alpha1 posture preserved when
    /// `certManager.enabled: false` in the chart.
    Plaintext,

    /// Mutual TLS — the engine presents its server certificate
    /// and rejects clients without a valid certificate issued by
    /// the configured trust bundle.
    Mutual {
        /// PEM-encoded server certificate path.
        cert_path: PathBuf,
        /// PEM-encoded server private key path.
        key_path: PathBuf,
        /// PEM-encoded trust bundle path (peer CA).
        ca_path: PathBuf,
    },
}

/// Configuration handle the binary builds at startup.
#[derive(Debug, Clone)]
pub struct TlsConfig {
    /// Resolved mode (Plaintext or Mutual).
    pub mode: TlsMode,
}

/// Errors surfaced by `TlsConfig::from_env`. Each variant maps to
/// a specific operator-side misconfiguration the chart's gate
/// (`_helpers.tpl::spectre.tlsEnv`) ought to prevent.
#[derive(Debug, Error)]
pub enum TlsConfigError {
    /// One or two of the three vars are set, but not all three.
    /// The chart wires all three or none — a partial state is a
    /// hand-rolled misconfig.
    #[error(
        "TLS env partially configured: {set_vars} set, {unset_vars} unset. \
         All three of {cert_env}, {key_env}, {ca_env} must be set together \
         (mTLS) or all unset (plaintext)."
    )]
    PartialConfig {
        /// Comma-separated list of set vars.
        set_vars: String,
        /// Comma-separated list of unset vars.
        unset_vars: String,
        /// The cert-path env name (echoed for the operator).
        cert_env: &'static str,
        /// The key-path env name.
        key_env: &'static str,
        /// The ca-path env name.
        ca_env: &'static str,
    },
}

impl TlsConfig {
    /// Detect the mode from `SPECTRE_TLS_*_PATH` env vars.
    ///
    /// Returns `Plaintext` when all three are unset, `Mutual` when
    /// all three are set, and `PartialConfig` otherwise. The
    /// partial case is fail-fast — the operator binary exits with
    /// code 1 so the Pod's Events stream surfaces the misconfig.
    ///
    /// # Errors
    ///
    /// Returns `TlsConfigError::PartialConfig` if one or two of
    /// the three env vars are set but not all three.
    pub fn from_env() -> Result<Self, TlsConfigError> {
        Self::from_getter(|name| env::var(name).ok())
    }

    /// Detect the mode from a caller-supplied env getter.
    ///
    /// Tests pass a closure backed by a `HashMap` so the parsing
    /// logic can be exercised without mutating the process env
    /// (the crate's `unsafe_code = "forbid"` lint blocks
    /// `env::set_var` under the Rust 2024 edition).
    ///
    /// # Errors
    ///
    /// Returns `TlsConfigError::PartialConfig` if the getter
    /// returns a set value for one or two — but not all three —
    /// of the three env-var names.
    pub fn from_getter<F>(getter: F) -> Result<Self, TlsConfigError>
    where
        F: Fn(&str) -> Option<String>,
    {
        let cert = getter(CERT_PATH_ENV).filter(|s| !s.is_empty());
        let key = getter(KEY_PATH_ENV).filter(|s| !s.is_empty());
        let ca = getter(CA_PATH_ENV).filter(|s| !s.is_empty());

        match (cert, key, ca) {
            (None, None, None) => Ok(Self {
                mode: TlsMode::Plaintext,
            }),
            (Some(c), Some(k), Some(a)) => Ok(Self {
                mode: TlsMode::Mutual {
                    cert_path: PathBuf::from(c),
                    key_path: PathBuf::from(k),
                    ca_path: PathBuf::from(a),
                },
            }),
            (cert, key, ca) => {
                let mut set = Vec::with_capacity(3);
                let mut unset = Vec::with_capacity(3);
                for (name, value) in [
                    (CERT_PATH_ENV, cert.as_ref()),
                    (KEY_PATH_ENV, key.as_ref()),
                    (CA_PATH_ENV, ca.as_ref()),
                ] {
                    if value.is_some() {
                        set.push(name);
                    } else {
                        unset.push(name);
                    }
                }
                Err(TlsConfigError::PartialConfig {
                    set_vars: set.join(", "),
                    unset_vars: unset.join(", "),
                    cert_env: CERT_PATH_ENV,
                    key_env: KEY_PATH_ENV,
                    ca_env: CA_PATH_ENV,
                })
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn getter_from(map: HashMap<&'static str, &'static str>) -> impl Fn(&str) -> Option<String> {
        move |name| map.get(name).map(|v| (*v).to_owned())
    }

    #[test]
    fn from_getter_all_unset_is_plaintext() {
        let config =
            TlsConfig::from_getter(getter_from(HashMap::new())).expect("all-unset must succeed");
        assert_eq!(config.mode, TlsMode::Plaintext);
    }

    #[test]
    fn from_getter_all_set_is_mutual() {
        let map: HashMap<&'static str, &'static str> = HashMap::from_iter([
            (CERT_PATH_ENV, "/etc/spectre/tls/tls.crt"),
            (KEY_PATH_ENV, "/etc/spectre/tls/tls.key"),
            (CA_PATH_ENV, "/etc/spectre/tls/ca.crt"),
        ]);
        let config = TlsConfig::from_getter(getter_from(map)).expect("all-set must succeed");
        match config.mode {
            TlsMode::Mutual {
                cert_path,
                key_path,
                ca_path,
            } => {
                assert_eq!(cert_path, PathBuf::from("/etc/spectre/tls/tls.crt"));
                assert_eq!(key_path, PathBuf::from("/etc/spectre/tls/tls.key"));
                assert_eq!(ca_path, PathBuf::from("/etc/spectre/tls/ca.crt"));
            }
            other @ TlsMode::Plaintext => panic!("expected Mutual, got {other:?}"),
        }
    }

    #[test]
    fn from_getter_partial_is_error() {
        let map: HashMap<&'static str, &'static str> = HashMap::from_iter([
            (CERT_PATH_ENV, "/etc/spectre/tls/tls.crt"),
            (KEY_PATH_ENV, "/etc/spectre/tls/tls.key"),
            // CA deliberately unset
        ]);
        let err = TlsConfig::from_getter(getter_from(map)).expect_err("partial must error");
        match err {
            TlsConfigError::PartialConfig {
                set_vars,
                unset_vars,
                ..
            } => {
                assert!(set_vars.contains(CERT_PATH_ENV));
                assert!(set_vars.contains(KEY_PATH_ENV));
                assert!(unset_vars.contains(CA_PATH_ENV));
            }
        }
    }

    #[test]
    fn from_getter_empty_string_is_partial() {
        let map: HashMap<&'static str, &'static str> = HashMap::from_iter([
            (CERT_PATH_ENV, "/etc/spectre/tls/tls.crt"),
            (KEY_PATH_ENV, ""), // empty
            (CA_PATH_ENV, "/etc/spectre/tls/ca.crt"),
        ]);
        let err = TlsConfig::from_getter(getter_from(map)).expect_err("empty-key must error");
        assert!(matches!(err, TlsConfigError::PartialConfig { .. }));
    }
}
