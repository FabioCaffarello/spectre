// SPDX-License-Identifier: Apache-2.0

//! Spectre engine core.
//!
//! This crate is the home for the DSL parser, type checker, planner,
//! and execution scheduler. It owns the contract with drivers via the
//! Driver Protocol (`spectre.driver.v1alpha1`).
//!
//! The crate is `v0.1.0-alpha.0` and intentionally minimal — the
//! current contents are limited to identity constants and a smoke
//! test. Substantive functionality lands in Phase 1 of the project
//! roadmap (see `docs/roadmap.md`).

#![cfg_attr(not(test), forbid(unsafe_code))]
#![warn(missing_docs)]

/// The Driver Protocol version this build of the engine speaks.
///
/// Drivers must declare a matching `protocol_version` in their
/// `driver.yaml`; the engine refuses to load drivers whose declared
/// version does not match this constant.
pub const PROTOCOL_VERSION: &str = "spectre.driver.v1alpha1";

/// The engine crate version, mirroring `Cargo.toml`.
pub const ENGINE_VERSION: &str = env!("CARGO_PKG_VERSION");

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn protocol_version_is_v1alpha1() {
        assert_eq!(PROTOCOL_VERSION, "spectre.driver.v1alpha1");
    }
}
