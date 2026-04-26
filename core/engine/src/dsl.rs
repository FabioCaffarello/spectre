// SPDX-License-Identifier: Apache-2.0

//! DSL parser and validation.
//!
//! Stub: this module is filled out in the next commit (Step 4).

#![allow(missing_docs)]

use thiserror::Error;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Job;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Step;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Field;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FieldMode {}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OutputConfig;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OutputFormat {}

#[derive(Debug, Error)]
pub enum JobError {
    #[error("dsl: not yet implemented")]
    Stub,
}
