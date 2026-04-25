// SPDX-License-Identifier: Apache-2.0

//! Spectre engine core.
//!
//! This crate is the home for the DSL parser, type checker, planner,
//! and execution scheduler. It owns the contract with drivers via the
//! Driver Protocol (`spectre.driver.v1alpha1`), generated at build
//! time by `build.rs`.
//!
//! The crate is `v0.1.0-alpha.0` and intentionally minimal —
//! substantive functionality lands in Phase 1 of the project roadmap
//! (see `docs/roadmap.md`).

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
        // Smoke test: instantiate one generated type to confirm the
        // tonic/prost output compiled and was included successfully.
        let caps = proto::Capabilities::default();
        assert!(caps.names.is_empty());
    }
}
