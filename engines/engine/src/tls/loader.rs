// SPDX-License-Identifier: Apache-2.0

//! PEM loading + `tonic::transport::ServerTlsConfig` construction.
//!
//! tonic 0.13's `Identity::from_pem(cert, key)` + `Certificate::
//! from_pem(ca)` accepts PEM bytes directly, so this module reads
//! the three files supplied by `TlsMode::Mutual` and hands them to
//! tonic. No rustls intermediate types are needed for this path —
//! `rustls_pemfile` would be needed only if we built a custom
//! `rustls::ServerConfig` (the dynamic-resolver path, deferred to
//! tonic 0.14 per `mod.rs`).

use std::path::Path;

use thiserror::Error;
use tonic::transport::{Certificate, ClientTlsConfig, Identity, ServerTlsConfig};

/// Errors raised while loading TLS material from disk.
#[derive(Debug, Error)]
pub enum LoadError {
    /// One of the three files could not be read. The path is
    /// surfaced verbatim so the operator can confirm the
    /// mount-path is the chart's `/etc/spectre/tls/`.
    #[error("TLS file read failed for {role} at {path}: {source}")]
    Io {
        /// Which of cert / key / ca is missing.
        role: &'static str,
        /// The file path we attempted.
        path: String,
        /// The underlying `std::io` error.
        #[source]
        source: std::io::Error,
    },
}

/// Build a tonic `ServerTlsConfig` for mTLS from PEM file paths.
///
/// The returned config requires client authentication
/// (`client_ca_root` set, `client_auth_optional` left default
/// `false`). Plaintext deployments don't call this — the binary
/// branches on `TlsMode` before reaching here.
///
/// # Errors
///
/// Returns `LoadError::Io` when any of the three PEM files cannot
/// be read. The variant's `role` field surfaces which file
/// (`"cert"`, `"key"`, or `"ca"`) was the first to fail so the
/// operator can correlate the message with the Secret mount.
pub fn build_server_tls_config(
    cert_path: &Path,
    key_path: &Path,
    ca_path: &Path,
) -> Result<ServerTlsConfig, LoadError> {
    let cert_pem = std::fs::read(cert_path).map_err(|source| LoadError::Io {
        role: "cert",
        path: cert_path.display().to_string(),
        source,
    })?;
    let key_pem = std::fs::read(key_path).map_err(|source| LoadError::Io {
        role: "key",
        path: key_path.display().to_string(),
        source,
    })?;
    let ca_pem = std::fs::read(ca_path).map_err(|source| LoadError::Io {
        role: "ca",
        path: ca_path.display().to_string(),
        source,
    })?;

    let identity = Identity::from_pem(&cert_pem, &key_pem);
    let ca = Certificate::from_pem(&ca_pem);

    Ok(ServerTlsConfig::new().identity(identity).client_ca_root(ca))
}

/// Build a tonic `ClientTlsConfig` for outbound mTLS from PEM
/// file paths.
///
/// The returned config presents the engine's identity to the
/// peer (`identity` set from cert + key) and verifies the peer's
/// server certificate against the configured trust bundle
/// (`ca_certificate` set). ADR-0032 §4.2: engine ↔ adapter mTLS
/// reuses the same engine cert that W3.3 uses for the
/// operator → engine path (the engine cert's default cert-manager
/// `usages` cover both server auth and client auth).
///
/// SNI / `domain_name` verification is left at its default — tonic
/// derives it from the `Endpoint`'s URI host, which the chart
/// renders to the adapter's K8s Service DNS (matching the
/// adapter cert's SAN list per `spectre.certificate` helper).
///
/// # Errors
///
/// Returns `LoadError::Io` when any of the three PEM files cannot
/// be read. Same surface as `build_server_tls_config` so the
/// operator can correlate the error with the Secret mount.
pub fn build_client_tls_config(
    cert_path: &Path,
    key_path: &Path,
    ca_path: &Path,
) -> Result<ClientTlsConfig, LoadError> {
    let cert_pem = std::fs::read(cert_path).map_err(|source| LoadError::Io {
        role: "cert",
        path: cert_path.display().to_string(),
        source,
    })?;
    let key_pem = std::fs::read(key_path).map_err(|source| LoadError::Io {
        role: "key",
        path: key_path.display().to_string(),
        source,
    })?;
    let ca_pem = std::fs::read(ca_path).map_err(|source| LoadError::Io {
        role: "ca",
        path: ca_path.display().to_string(),
        source,
    })?;

    let identity = Identity::from_pem(&cert_pem, &key_pem);
    let ca = Certificate::from_pem(&ca_pem);

    Ok(ClientTlsConfig::new().identity(identity).ca_certificate(ca))
}

/// Install rustls's process-level `CryptoProvider` so subsequent
/// TLS handshakes (tonic's server-side path; reqwest's webhook
/// client; aws-sdk-s3's signed-request stack) can locate a
/// crypto backend.
///
/// rustls 0.23 requires `CryptoProvider::install_default()` to
/// run before any TLS code touches the global slot. With multiple
/// rustls consumers in the engine binary (sqlx, reqwest,
/// aws-sdk-s3, and tonic via the `tls-ring` feature) auto-
/// detection bails with the message about the process-level
/// `CryptoProvider` being indeterminable. The documented call-to-
/// action is to install the provider explicitly. `ring` matches
/// the `tls-ring` tonic feature added in W3.3 (Cluster B).
///
/// `install_default` returns `Err` if a provider was already
/// installed; we ignore the error so re-invocations are safe. In
/// practice this function runs once at startup.
pub fn install_crypto_provider() {
    let _ = rustls::crypto::ring::default_provider().install_default();
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write as _;
    use tempfile::TempDir;

    fn write_file(dir: &TempDir, name: &str, content: &[u8]) -> std::path::PathBuf {
        let path = dir.path().join(name);
        let mut f = std::fs::File::create(&path).expect("tempfile create");
        f.write_all(content).expect("tempfile write");
        path
    }

    #[test]
    fn build_server_tls_config_io_error_surfaces_role_and_path() {
        let dir = TempDir::new().expect("tempdir");
        let missing = dir.path().join("does-not-exist.crt");
        let other = write_file(&dir, "other.pem", b"placeholder");

        let err =
            build_server_tls_config(&missing, &other, &other).expect_err("missing cert must fail");
        match err {
            LoadError::Io { role, path, .. } => {
                assert_eq!(role, "cert");
                assert!(path.ends_with("does-not-exist.crt"));
            }
        }
    }

    // Building a real ServerTlsConfig requires valid PEM material
    // — synthesising a self-signed cert for a unit test would pull
    // in a fresh dependency (rcgen) just for this assertion. The
    // integration test in `tests/tls_integration.rs` covers the
    // happy path against cert-manager-issued material in
    // mtls-smoke.
}
