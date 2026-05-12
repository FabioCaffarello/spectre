// SPDX-License-Identifier: Apache-2.0

//! mTLS configuration for the engine's inbound gRPC server.
//!
//! ADR-0032 §4.1 makes operator ↔ engine traffic the platform's
//! first authenticated path. The engine acts as the server in this
//! pair: it loads its own identity (`/etc/spectre/tls/tls.crt` +
//! `tls.key`) and the trust bundle (`/etc/spectre/tls/ca.crt`)
//! that authenticates the operator's client cert, then hands the
//! material to `tonic::transport::Server::tls_config`.
//!
//! Env-var contract:
//!
//! - `SPECTRE_TLS_CERT_PATH` — server certificate (PEM).
//! - `SPECTRE_TLS_KEY_PATH`  — server private key (PEM, PKCS#8 or
//!   PKCS#1).
//! - `SPECTRE_TLS_CA_PATH`   — peer trust bundle (PEM bundle).
//!
//! All three set → `TlsMode::Mutual`; all three unset →
//! `TlsMode::Plaintext`; partial → fail-fast (the chart sets all
//! three together via `_helpers.tpl::spectre.tlsEnv`, so partial
//! state is operator misconfiguration). Exit code 1 surfaces in
//! the Pod's Events stream so the misconfig is visible from
//! `kubectl get events`.
//!
//! `ReloadingCertResolver`-style dynamic reload (ADR-0032 §5.1)
//! requires `rustls::ServerConfig::with_cert_resolver`, which
//! tonic 0.13.1's `ServerTlsConfig` does not expose. v1alpha1
//! first auth PR uses static-load-at-startup; cert rotation
//! triggers a Pod restart via the chart's annotation pattern.
//! ADR-0032 §5.2 already accepts that operational shape for
//! Python. The dynamic-resolver path lands in Wave 5+ gated on a
//! tonic 0.14 migration.

pub mod config;
pub mod loader;

pub use config::{TlsConfig, TlsConfigError, TlsMode};
pub use loader::{LoadError, build_server_tls_config, install_crypto_provider};
