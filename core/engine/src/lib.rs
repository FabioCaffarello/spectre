// SPDX-License-Identifier: Apache-2.0

//! Spectre engine core.
//!
//! This crate hosts the DSL parser, the planner, the gRPC client, and
//! the executor that together compile a `job.yaml` into a sequence of
//! Driver Protocol RPCs against a launched adapter subprocess.
//!
//! ```text
//! YAML  ─►  Job  ─►  Plan  ─►  RPC sequence  ─►  JSONL rows
//!          [dsl]   [plan]    [client + executor]   [output]
//! ```
//!
//! The crate is `v0.1.0-alpha.0`. The protocol is `v1alpha1` — frozen
//! and unstable; see ADR-0004 and ADR-0012.
//!
//! Public entry points:
//!
//! - [`Engine::run_job`] — parse, plan, launch, execute, return the
//!   total number of rows written.
//! - [`Job::from_yaml`] — parse and validate a YAML job in isolation
//!   (no driver launch). Useful for editors and validators.
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

pub mod client;
pub mod dsl;
pub mod error;
pub mod executor;
pub mod launcher;
pub mod output;
pub mod plan;

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
