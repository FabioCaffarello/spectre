// SPDX-License-Identifier: Apache-2.0

//! gRPC client over TCP.
//!
//! Wraps `tonic`'s generated [`proto::driver_client::DriverClient`]
//! behind a small surface so the rest of the engine never sees raw
//! protobuf request/response types.
//!
//! # Endpoint format
//!
//! [`Client::dial`] accepts either a bare `host:port` string or a
//! full `grpc://host:port` URI. Plaintext dials normalise to
//! `http://host:port`; TLS dials normalise to `https://host:port`
//! (W3.3-era TLS-out-of-scope comment retired by W3.4). When a
//! [`ClientTlsConfig`] is supplied, the channel is encrypted and
//! SNI is derived from the URI's host — the chart renders adapter
//! endpoints with DNS names matching the per-service Certificate
//! SAN list so verification succeeds without per-dial overrides.
//!
//! # Connect/gRPC interop
//!
//! The Playwright adapter uses `@connectrpc/connect-node`, which
//! serves both Connect Protocol and gRPC over HTTP/2 on the same
//! handler. Tonic speaks gRPC. The handler picks the right protocol
//! per-request from headers, so no client-side configuration is
//! needed.
//!
//! # Timeouts
//!
//! Connect timeout defaults to 5 seconds; per-request timeouts are
//! the responsibility of the caller (or absent — most driver RPCs
//! finish in milliseconds, and the conformance suite has not flagged
//! a need). Tunable via [`Client::dial_with_timeout`] if needed.

use std::time::Duration;

use opentelemetry::trace::{FutureExt as _, SpanKind, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context as OtelContext, global};
use tonic::Request;
use tonic::transport::{Channel, ClientTlsConfig, Endpoint};

use crate::error::EngineError;
use crate::proto;
use crate::proto::driver_client::DriverClient;
use crate::telemetry::propagation;

/// `OTel` tracer name used for client-side spans per ADR-0031 §4.3.
const TRACER_NAME: &str = "spectre-engine";

/// Build a fresh client-kind span for a single adapter RPC. Returns
/// the wrapping context the caller threads through
/// [`opentelemetry::trace::FutureExt::with_context`] so the per-RPC
/// span is current when `propagation::inject_current` runs.
///
/// Span naming follows ADR-0031 §4.3's RPC convention —
/// `<package>.<service>/<rpc>` — using the driver protocol's proto
/// path `spectre.driver.v1alpha1.Driver`.
fn client_span(rpc: &'static str) -> OtelContext {
    let tracer = global::tracer(TRACER_NAME);
    let span = tracer
        .span_builder(rpc)
        .with_kind(SpanKind::Client)
        .start(&tracer);
    OtelContext::current_with_span(span)
}

/// Default connect timeout for [`Client::dial`]. Picked to be
/// generous over a Compose / Kubernetes service boundary while still
/// surfacing dead endpoints in interactive use. See ADR-0022 §4.
pub const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(5);

/// Engine-side gRPC client.
///
/// The client is `Clone`-cheap because [`Channel`] is `Clone`. The
/// inner `DriverClient` is recreated per call so we never hold a
/// shared mutable reference; this also keeps the request-extension
/// state independent across concurrent calls.
#[derive(Clone)]
pub struct Client {
    channel: Channel,
}

impl Client {
    /// Dial the gRPC server listening at `endpoint`. Accepts
    /// `host:port`, `http://host:port`, or `grpc://host:port`.
    /// Plaintext transport.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Transport`] if the endpoint cannot be
    /// parsed or the underlying TCP connection cannot be established
    /// within [`DEFAULT_CONNECT_TIMEOUT`].
    pub async fn dial(endpoint: &str) -> Result<Self, EngineError> {
        Self::dial_with_tls(endpoint, None, DEFAULT_CONNECT_TIMEOUT).await
    }

    /// Like [`Self::dial`] with a caller-supplied connect timeout.
    /// Plaintext transport.
    ///
    /// # Errors
    ///
    /// Same as [`Self::dial`].
    pub async fn dial_with_timeout(
        endpoint: &str,
        connect_timeout: Duration,
    ) -> Result<Self, EngineError> {
        Self::dial_with_tls(endpoint, None, connect_timeout).await
    }

    /// Dial with an optional [`ClientTlsConfig`]. When `tls` is
    /// `Some`, the channel performs an mTLS handshake (ADR-0032
    /// §4.2 engine → adapter path); when `None`, the dial uses
    /// plaintext gRPC (the v1alpha1 default).
    ///
    /// # Errors
    ///
    /// Same as [`Self::dial`], plus
    /// [`EngineError::Transport`] when tonic rejects the supplied
    /// [`ClientTlsConfig`] (e.g. invalid PEM material).
    pub async fn dial_with_tls(
        endpoint: &str,
        tls: Option<&ClientTlsConfig>,
        connect_timeout: Duration,
    ) -> Result<Self, EngineError> {
        let uri = normalise_endpoint(endpoint, tls.is_some());
        let mut builder = Endpoint::from_shared(uri)
            .map_err(|e| EngineError::Transport(format!("endpoint: {e}")))?
            .connect_timeout(connect_timeout);
        if let Some(t) = tls {
            builder = builder
                .tls_config(t.clone())
                .map_err(|e| EngineError::Transport(format!("tls_config: {e}")))?;
        }
        let channel = builder
            .connect()
            .await
            .map_err(|e| EngineError::Transport(format!("connect: {e}")))?;

        Ok(Self { channel })
    }

    /// Send `Initialize` and return the session id and declared
    /// capability names.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Transport`] on transport-level failure
    /// or [`EngineError::Driver`] if the response carries a
    /// [`proto::DriverError`] with a non-zero code.
    pub async fn initialize(
        &self,
        config: proto::SessionConfig,
        requested_capabilities: Vec<String>,
    ) -> Result<InitializeOutcome, EngineError> {
        let ctx = client_span("spectre.driver.v1alpha1.Driver/Initialize");
        let channel = self.channel.clone();
        async move {
            let mut client = DriverClient::new(channel);
            let mut req = Request::new(proto::InitializeRequest {
                protocol_version: proto::PROTOCOL_VERSION.to_owned(),
                session: Some(config),
                requested_capabilities,
            });
            propagation::inject_current(req.metadata_mut());
            let resp = client.initialize(req).await?.into_inner();
            check_driver_error(resp.error.as_ref())?;
            let caps = resp.capabilities.unwrap_or_default();
            Ok(InitializeOutcome {
                session_id: resp.session_id,
                capability_names: caps.names,
            })
        }
        .with_context(ctx)
        .await
    }

    /// Send `Navigate` for the given session.
    ///
    /// # Errors
    ///
    /// Same as [`Self::initialize`].
    pub async fn navigate(
        &self,
        session_id: &str,
        url: &str,
        wait_until: proto::WaitCondition,
    ) -> Result<(), EngineError> {
        let ctx = client_span("spectre.driver.v1alpha1.Driver/Navigate");
        let channel = self.channel.clone();
        let session_id = session_id.to_owned();
        let url = url.to_owned();
        async move {
            let mut client = DriverClient::new(channel);
            let mut req = Request::new(proto::NavigateRequest {
                session_id,
                url,
                wait: wait_until as i32,
                timeout: None,
            });
            propagation::inject_current(req.metadata_mut());
            let resp = client.navigate(req).await?.into_inner();
            check_driver_error(resp.error.as_ref())?;
            Ok(())
        }
        .with_context(ctx)
        .await
    }

    /// Send `Query` and return the matched `ElementRef`s.
    ///
    /// # Errors
    ///
    /// Same as [`Self::initialize`].
    pub async fn query(
        &self,
        session_id: &str,
        selector: &str,
        kind: proto::SelectorKind,
        limit: u32,
    ) -> Result<Vec<proto::ElementRef>, EngineError> {
        let ctx = client_span("spectre.driver.v1alpha1.Driver/Query");
        let channel = self.channel.clone();
        let session_id = session_id.to_owned();
        let selector = selector.to_owned();
        async move {
            let mut client = DriverClient::new(channel);
            let mut req = Request::new(proto::QueryRequest {
                session_id,
                selector,
                kind: kind as i32,
                limit,
            });
            propagation::inject_current(req.metadata_mut());
            let resp = client.query(req).await?.into_inner();
            check_driver_error(resp.error.as_ref())?;
            Ok(resp.elements)
        }
        .with_context(ctx)
        .await
    }

    /// Send `Extract` against a single `ElementRef` and return its
    /// per-field values as `(name, json_value)` pairs.
    ///
    /// # Errors
    ///
    /// Same as [`Self::initialize`].
    pub async fn extract(
        &self,
        session_id: &str,
        element: proto::ElementRef,
        fields: Vec<proto::Field>,
    ) -> Result<Vec<(String, String)>, EngineError> {
        let ctx = client_span("spectre.driver.v1alpha1.Driver/Extract");
        let channel = self.channel.clone();
        let session_id = session_id.to_owned();
        async move {
            let mut client = DriverClient::new(channel);
            let mut req = Request::new(proto::ExtractRequest {
                session_id,
                element: Some(element),
                fields,
            });
            propagation::inject_current(req.metadata_mut());
            let resp = client.extract(req).await?.into_inner();
            check_driver_error(resp.error.as_ref())?;
            let entries = resp
                .values
                .map(|v| v.fields)
                .unwrap_or_default()
                .into_iter()
                .map(|e| (e.name, e.json_value))
                .collect();
            Ok(entries)
        }
        .with_context(ctx)
        .await
    }

    /// Send `Close` for the given session.
    ///
    /// # Errors
    ///
    /// Same as [`Self::initialize`].
    pub async fn close(&self, session_id: &str) -> Result<(), EngineError> {
        let ctx = client_span("spectre.driver.v1alpha1.Driver/Close");
        let channel = self.channel.clone();
        let session_id = session_id.to_owned();
        async move {
            let mut client = DriverClient::new(channel);
            let mut req = Request::new(proto::CloseRequest { session_id });
            propagation::inject_current(req.metadata_mut());
            let resp = client.close(req).await?.into_inner();
            check_driver_error(resp.error.as_ref())?;
            Ok(())
        }
        .with_context(ctx)
        .await
    }
}

/// What [`Client::initialize`] returns.
#[derive(Debug, Clone)]
pub struct InitializeOutcome {
    /// Session id assigned by the driver. Threaded through every
    /// subsequent RPC.
    pub session_id: String,
    /// Capability names the driver declared. Returned to the caller
    /// for capability validation against the plan.
    pub capability_names: Vec<String>,
}

fn normalise_endpoint(input: &str, tls: bool) -> String {
    let trimmed = input.trim();
    let scheme = if tls { "https" } else { "http" };
    if let Some(rest) = trimmed.strip_prefix("http://") {
        format!("{scheme}://{rest}")
    } else if let Some(rest) = trimmed.strip_prefix("https://") {
        format!("{scheme}://{rest}")
    } else if let Some(rest) = trimmed.strip_prefix("grpc://") {
        format!("{scheme}://{rest}")
    } else if let Some(rest) = trimmed.strip_prefix("grpcs://") {
        format!("{scheme}://{rest}")
    } else {
        format!("{scheme}://{trimmed}")
    }
}

fn check_driver_error(err: Option<&proto::DriverError>) -> Result<(), EngineError> {
    let Some(e) = err else { return Ok(()) };
    let code = proto::driver_error::Code::try_from(e.code)
        .unwrap_or(proto::driver_error::Code::Unspecified);
    if code == proto::driver_error::Code::Unspecified && e.message.is_empty() {
        // Default-constructed DriverError counts as "no error".
        return Ok(());
    }
    Err(EngineError::Driver {
        code: code.as_str_name().to_owned(),
        message: e.message.clone(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalise_endpoint_accepts_bare_host_port() {
        assert_eq!(
            normalise_endpoint("127.0.0.1:8091", false),
            "http://127.0.0.1:8091"
        );
    }

    #[test]
    fn normalise_endpoint_passes_http_through() {
        assert_eq!(
            normalise_endpoint("http://playwright:8091", false),
            "http://playwright:8091"
        );
    }

    #[test]
    fn normalise_endpoint_rewrites_grpc_scheme() {
        assert_eq!(
            normalise_endpoint("grpc://playwright:8091", false),
            "http://playwright:8091"
        );
    }

    #[test]
    fn normalise_endpoint_rewrites_grpcs_scheme_under_plaintext() {
        // grpcs:// in the input is normalised to the active
        // scheme — tls=false demotes it to http://. The dial
        // would then fail at TLS-handshake-from-plaintext-server
        // if the operator actually meant grpcs://; we trust the
        // env-driven endpoint convention to be consistent with
        // the engine's resolved TLS mode.
        assert_eq!(
            normalise_endpoint("grpcs://playwright:8091", false),
            "http://playwright:8091"
        );
    }

    #[test]
    fn normalise_endpoint_promotes_to_https_when_tls() {
        assert_eq!(
            normalise_endpoint("playwright:8091", true),
            "https://playwright:8091"
        );
        assert_eq!(
            normalise_endpoint("http://playwright:8091", true),
            "https://playwright:8091"
        );
        assert_eq!(
            normalise_endpoint("grpc://playwright:8091", true),
            "https://playwright:8091"
        );
    }

    #[test]
    fn normalise_endpoint_trims_whitespace() {
        assert_eq!(
            normalise_endpoint("  127.0.0.1:8091  ", false),
            "http://127.0.0.1:8091"
        );
    }

    #[test]
    fn unspecified_default_error_is_treated_as_ok() {
        let err = proto::DriverError::default();
        assert!(check_driver_error(Some(&err)).is_ok());
    }

    #[test]
    fn missing_error_is_ok() {
        assert!(check_driver_error(None).is_ok());
    }

    #[test]
    fn populated_error_maps_to_driver_variant() {
        let err = proto::DriverError {
            code: proto::driver_error::Code::TargetUnreachable as i32,
            message: "no route to host".into(),
            ..Default::default()
        };
        match check_driver_error(Some(&err)) {
            Err(EngineError::Driver { code, message }) => {
                assert_eq!(code, "CODE_TARGET_UNREACHABLE");
                assert_eq!(message, "no route to host");
            }
            other => panic!("expected Driver, got {other:?}"),
        }
    }

    #[test]
    fn unspecified_with_message_is_still_an_error() {
        let err = proto::DriverError {
            code: proto::driver_error::Code::Unspecified as i32,
            message: "weird".into(),
            ..Default::default()
        };
        assert!(check_driver_error(Some(&err)).is_err());
    }
}
