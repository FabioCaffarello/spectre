// SPDX-License-Identifier: Apache-2.0

//! SDK-level error types.
//!
//! Maps the three failure modes a typed gRPC SDK call can
//! surface: transport-level (channel can't be reached at all),
//! application-level (server returned a `tonic::Status`), and
//! retry-exhaustion (the SDK retried up to its limit and the
//! last attempt still failed).

/// Errors returned by `ProxyClient` methods.
#[derive(Debug, thiserror::Error)]
pub enum ProxyClientError {
    /// Transport-level failure. The gRPC channel cannot be
    /// reached (DNS resolution, TCP refused, TLS handshake
    /// rejected) or surfaced an internal channel error.
    #[error("transport error: {0}")]
    Transport(#[from] tonic::transport::Error),

    /// Server-returned `tonic::Status`. Application-level
    /// errors (invalid argument, permission denied) flow
    /// through this variant; the caller inspects `.code()` to
    /// decide on per-error handling.
    #[error("gRPC status: {0}")]
    Status(#[from] tonic::Status),

    /// SDK retried `attempts` times and the last attempt
    /// still failed. The underlying error is boxed so the
    /// retry-exhaustion path doesn't pin the SDK to a specific
    /// inner-error shape.
    #[error("retries exhausted after {attempts} attempts; last error: {source}")]
    RetriesExhausted {
        /// Number of attempts that were made (1 + retries).
        attempts: u32,
        /// The error that caused the final attempt to fail.
        #[source]
        source: Box<dyn std::error::Error + Send + Sync>,
    },
}
