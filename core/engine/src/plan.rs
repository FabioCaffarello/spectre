// SPDX-License-Identifier: Apache-2.0

//! Planner and capability resolution.
//!
//! Stub: filled out in Step 5.

#![allow(missing_docs)]

use thiserror::Error;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Plan;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PlanStep {}

#[derive(Debug, Error)]
pub enum PlanError {
    #[error("plan: not yet implemented")]
    Stub,
}
