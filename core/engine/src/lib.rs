// SPDX-License-Identifier: Apache-2.0

//! Spectre engine core.
//!
//! This crate hosts the DSL parser, the planner, the gRPC client,
//! the executor, and the gRPC server that together compile a
//! `job.yaml` into a sequence of Driver Protocol RPCs against a
//! running adapter service.
//!
//! ```text
//! YAML  ─►  Job  ─►  Plan  ─►  RPC sequence  ─►  JSONL rows
//!          [dsl]   [plan]    [client + executor]   [output sink]
//! ```
//!
//! The crate is `v0.1.0-alpha.0`. The driver protocol is
//! `spectre.driver.v1alpha1` (frozen) and the internal engine
//! protocol is `spectre.engine.v1alpha1` (unstable). See ADR-0004
//! and ADR-0012.
//!
//! Public entry points:
//!
//! - [`Engine::run_plan_with_sink`] — resolve driver, dial the
//!   adapter, drive the executor against the supplied sink. Used by
//!   [`server::EngineServiceImpl`] to back the streaming `RunJob`
//!   RPC.
//! - [`Job::from_yaml`] — parse and validate a YAML job in isolation
//!   (no driver dial). Useful for editors and validators.
//! - [`plan::plan`] — turn a validated [`Job`] into a [`Plan`]
//!   without running it.
//!
//! See `docs/adr/0012-engine-dsl-and-execution-pipeline.md` for the
//! decisions that shaped each layer.

#![cfg_attr(not(test), forbid(unsafe_code))]
#![warn(missing_docs)]

/// Generated bindings for `spectre.driver.v1alpha1`.
///
/// The contents of this module are written to `OUT_DIR` by
/// `build.rs` via `tonic-build` and `prost-build` and included
/// inline. `PROTOCOL_VERSION` is emitted alongside the generated
/// types so the version constant has the same provenance as the
/// types it qualifies.
#[allow(missing_docs, clippy::all, clippy::pedantic, clippy::nursery)]
pub mod proto {
    tonic::include_proto!("spectre.driver.v1alpha1");
    include!(concat!(env!("OUT_DIR"), "/protocol_version.rs"));
}

/// Generated bindings for `spectre.engine.v1alpha1` — the internal
/// Engine service the engine binary exposes (R2.3) and the control
/// plane consumes (R3.1). Adapter authors do not implement this
/// protocol; it is the wire boundary between the control plane and
/// the engine, not between the engine and adapters.
#[allow(missing_docs, clippy::all, clippy::pedantic, clippy::nursery)]
pub mod engine_proto {
    tonic::include_proto!("spectre.engine.v1alpha1");
}

pub mod client;
pub mod db;
pub mod dsl;
pub mod error;
pub mod executor;
pub mod kafka;
pub mod output;
pub mod plan;
pub mod registry;
pub mod s3;
pub mod server;

mod engine;

pub use dsl::{Field, FieldMode, Job, JobError, OutputConfig, OutputFormat, Step};
pub use engine::Engine;
pub use error::EngineError;
pub use executor::Executor;
pub use plan::{Plan, PlanError, PlanStep};
pub use proto::PROTOCOL_VERSION;

/// The engine crate version, mirroring `Cargo.toml`.
pub const ENGINE_VERSION: &str = env!("CARGO_PKG_VERSION");

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn protocol_version_is_v1alpha1() {
        assert_eq!(PROTOCOL_VERSION, "spectre.driver.v1alpha1");
    }

    #[test]
    fn generated_types_are_reachable() {
        let caps = proto::Capabilities::default();
        assert!(caps.names.is_empty());
    }
}
