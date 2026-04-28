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
//! full `grpc://host:port` URI. Both formats are normalised to an
//! `http://host:port` URI for the underlying [`Endpoint`]. TLS is
//! out of scope for v1alpha1 — see ADR-0022 §6.
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

use tonic::Request;
use tonic::transport::{Channel, Endpoint};

use crate::error::EngineError;
use crate::proto;
use crate::proto::driver_client::DriverClient;

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
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Transport`] if the endpoint cannot be
    /// parsed or the underlying TCP connection cannot be established
    /// within [`DEFAULT_CONNECT_TIMEOUT`].
    pub async fn dial(endpoint: &str) -> Result<Self, EngineError> {
        Self::dial_with_timeout(endpoint, DEFAULT_CONNECT_TIMEOUT).await
    }

    /// Like [`Self::dial`] with a caller-supplied connect timeout.
    ///
    /// # Errors
    ///
    /// Same as [`Self::dial`].
    pub async fn dial_with_timeout(
        endpoint: &str,
        connect_timeout: Duration,
    ) -> Result<Self, EngineError> {
        let uri = normalise_endpoint(endpoint);
        let channel = Endpoint::from_shared(uri)
            .map_err(|e| EngineError::Transport(format!("endpoint: {e}")))?
            .connect_timeout(connect_timeout)
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
        let mut client = DriverClient::new(self.channel.clone());
        let req = Request::new(proto::InitializeRequest {
            protocol_version: proto::PROTOCOL_VERSION.to_owned(),
            session: Some(config),
            requested_capabilities,
        });
        let resp = client.initialize(req).await?.into_inner();
        check_driver_error(resp.error.as_ref())?;
        let caps = resp.capabilities.unwrap_or_default();
        Ok(InitializeOutcome {
            session_id: resp.session_id,
            capability_names: caps.names,
        })
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
        let mut client = DriverClient::new(self.channel.clone());
        let req = Request::new(proto::NavigateRequest {
            session_id: session_id.to_owned(),
            url: url.to_owned(),
            wait: wait_until as i32,
            timeout: None,
        });
        let resp = client.navigate(req).await?.into_inner();
        check_driver_error(resp.error.as_ref())?;
        Ok(())
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
        let mut client = DriverClient::new(self.channel.clone());
        let req = Request::new(proto::QueryRequest {
            session_id: session_id.to_owned(),
            selector: selector.to_owned(),
            kind: kind as i32,
            limit,
        });
        let resp = client.query(req).await?.into_inner();
        check_driver_error(resp.error.as_ref())?;
        Ok(resp.elements)
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
        let mut client = DriverClient::new(self.channel.clone());
        let req = Request::new(proto::ExtractRequest {
            session_id: session_id.to_owned(),
            element: Some(element),
            fields,
        });
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

    /// Send `Close` for the given session.
    ///
    /// # Errors
    ///
    /// Same as [`Self::initialize`].
    pub async fn close(&self, session_id: &str) -> Result<(), EngineError> {
        let mut client = DriverClient::new(self.channel.clone());
        let req = Request::new(proto::CloseRequest {
            session_id: session_id.to_owned(),
        });
        let resp = client.close(req).await?.into_inner();
        check_driver_error(resp.error.as_ref())?;
        Ok(())
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

fn normalise_endpoint(input: &str) -> String {
    let trimmed = input.trim();
    if trimmed.starts_with("http://") || trimmed.starts_with("https://") {
        trimmed.to_owned()
    } else if let Some(rest) = trimmed.strip_prefix("grpc://") {
        format!("http://{rest}")
    } else if let Some(rest) = trimmed.strip_prefix("grpcs://") {
        format!("https://{rest}")
    } else {
        format!("http://{trimmed}")
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
            normalise_endpoint("127.0.0.1:9091"),
            "http://127.0.0.1:9091"
        );
    }

    #[test]
    fn normalise_endpoint_passes_http_through() {
        assert_eq!(
            normalise_endpoint("http://playwright:9091"),
            "http://playwright:9091"
        );
    }

    #[test]
    fn normalise_endpoint_rewrites_grpc_scheme() {
        assert_eq!(
            normalise_endpoint("grpc://playwright:9091"),
            "http://playwright:9091"
        );
    }

    #[test]
    fn normalise_endpoint_rewrites_grpcs_scheme() {
        assert_eq!(
            normalise_endpoint("grpcs://playwright:9091"),
            "https://playwright:9091"
        );
    }

    #[test]
    fn normalise_endpoint_trims_whitespace() {
        assert_eq!(
            normalise_endpoint("  127.0.0.1:9091  "),
            "http://127.0.0.1:9091"
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
