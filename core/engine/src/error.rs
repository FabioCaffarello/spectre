// SPDX-License-Identifier: Apache-2.0

//! Engine error taxonomy.
//!
//! `EngineError` is the public failure type returned by
//! [`Engine::run_job`](crate::Engine::run_job) and the executor. It
//! carries enough structure that a caller can react programmatically
//! (e.g. `EngineError::CapabilityMissing` is a different kind of
//! failure from `EngineError::Driver`), and its `Display` impl is
//! tuned to be useful at a terminal without further unwrapping.
//!
//! See ADR-0012 for the decision history.

use thiserror::Error;

use crate::dsl::JobError;
use crate::plan::PlanError;

/// Top-level error type for the engine pipeline.
#[derive(Debug, Error)]
pub enum EngineError {
    /// DSL parsing or validation failed.
    #[error(transparent)]
    Job(#[from] JobError),

    /// Planning failed (e.g. requested capability not declared).
    #[error(transparent)]
    Plan(#[from] PlanError),

    /// gRPC transport-layer error (channel build, request dispatch,
    /// HTTP/2 framing).
    #[error("transport error: {0}")]
    Transport(String),

    /// The driver returned a structured `DriverError` from an RPC.
    /// `code` is the proto enum name (e.g. `CODE_TARGET_UNREACHABLE`)
    /// and `message` is the driver-supplied diagnostic.
    #[error("driver returned {code}: {message}")]
    Driver {
        /// The driver-supplied error code, as a proto enum name.
        code: String,
        /// The driver-supplied human-readable message.
        message: String,
    },

    /// The driver's declared capability list is missing one or more
    /// of the names the plan requires. The job cannot run as
    /// specified.
    #[error("capability missing: driver does not declare {missing:?}")]
    CapabilityMissing {
        /// The capability names the plan required but the driver did
        /// not declare.
        missing: Vec<String>,
    },

    /// The DSL referenced a driver name not registered in the
    /// engine's [`AdapterRegistry`](crate::registry::AdapterRegistry).
    /// In v1alpha1 the registered names are `playwright`,
    /// `seleniumbase`, and `curl-impersonate`.
    #[error("unknown driver: {0}")]
    UnknownDriver(String),

    /// Writing a row to the configured output sink failed.
    #[error("output error: {0}")]
    Output(String),

    /// I/O error while reading input files (job YAML, driver
    /// manifest) or writing output.
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),

    /// Catch-all for unexpected internal conditions. Production code
    /// should prefer one of the more specific variants; this exists
    /// for cases where a more specific variant would only ever carry
    /// a string.
    #[error("internal: {0}")]
    Internal(String),
}

impl From<tonic::transport::Error> for EngineError {
    fn from(e: tonic::transport::Error) -> Self {
        EngineError::Transport(e.to_string())
    }
}

impl From<tonic::Status> for EngineError {
    fn from(s: tonic::Status) -> Self {
        EngineError::Transport(format!("{}: {}", s.code(), s.message()))
    }
}
