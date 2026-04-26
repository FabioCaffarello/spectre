// SPDX-License-Identifier: Apache-2.0

//! gRPC client over a Unix domain socket.
//!
//! Wraps `tonic`'s generated [`proto::driver_client::DriverClient`]
//! behind a small surface so the rest of the engine never sees raw
//! protobuf request/response types.
//!
//! # Connect/gRPC interop
//!
//! The Playwright adapter uses `@connectrpc/connect-node`, which
//! serves both Connect Protocol and gRPC over HTTP/2 on the same
//! handler. Tonic speaks gRPC. The handler picks the right protocol
//! per-request from headers, so no client-side configuration is
//! needed.
//!
//! # `:authority` header
//!
//! Node's `http2` server, when bound to a UDS, requires the
//! `:authority` pseudo-header to be `localhost`. The Python harness
//! handled this with `("grpc.default_authority", "localhost")`
//! (ADR-0008). Tonic exposes the equivalent through
//! [`tonic::transport::Endpoint::origin`]; we set
//! `http://localhost/` so the H2 framing carries the right authority.
//!
//! # UDS connector
//!
//! Tonic does not have a built-in UDS transport. The recipe is the
//! one from `tonic/examples/uds`: an `Endpoint` whose `connect_with_connector`
//! is given a `tower::service_fn` that resolves a `tokio::net::UnixStream`
//! to the configured path and wraps it in `hyper_util::rt::TokioIo`.

use std::path::{Path, PathBuf};

use hyper_util::rt::TokioIo;
use tokio::net::UnixStream;
use tonic::Request;
use tonic::transport::{Channel, Endpoint, Uri};
use tower::service_fn;

use crate::error::EngineError;
use crate::proto;
use crate::proto::driver_client::DriverClient;

/// Engine-side gRPC client.
///
/// The client is `Clone`-cheap because [`Channel`] is `Clone`. The
/// inner `DriverClient` is recreated per call so we never hold a
/// shared mutable reference; this also keeps the request-extension
/// state independent across concurrent calls (PR7 is sequential, but
/// the structure is forward-compatible).
#[derive(Clone)]
pub struct Client {
    channel: Channel,
}

impl Client {
    /// Dial the gRPC server listening on the given UDS path.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Transport`] if the channel could not be
    /// constructed or the underlying [`Endpoint`] rejected the
    /// authority URI.
    pub async fn dial(socket: &Path) -> Result<Self, EngineError> {
        let socket: PathBuf = socket.to_path_buf();
        // The URI here is purely informational — tonic uses it to
        // populate the `:authority` pseudo-header on outgoing H2
        // streams. The connector below ignores it. Node's http2/UDS
        // server insists on `localhost`. See module docs.
        let endpoint = Endpoint::try_from("http://localhost")
            .map_err(|e| EngineError::Transport(format!("endpoint: {e}")))?;

        let channel = endpoint
            .connect_with_connector(service_fn(move |_uri: Uri| {
                let socket = socket.clone();
                async move {
                    let stream = UnixStream::connect(&socket).await?;
                    Ok::<_, std::io::Error>(TokioIo::new(stream))
                }
            }))
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
