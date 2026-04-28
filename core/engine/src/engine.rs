// SPDX-License-Identifier: Apache-2.0

//! Top-level Engine API.
//!
//! [`Engine::run_job`] is the orchestrator: parse → plan → launch
//! driver → dial → execute → shutdown. Public entry point used by the
//! `spectre` binary at `src/bin/spectre.rs` (ADR-0013).

use std::path::{Path, PathBuf};

use tracing::{debug, info};

use crate::client::Client;
use crate::dsl::{Job, OutputConfig, OutputFormat, resolve_output_path};
use crate::error::EngineError;
use crate::executor::Executor;
use crate::launcher::{
    DEFAULT_READY_TIMEOUT, DriverHandle, launch as launch_driver, load_declared_capabilities,
    manifest_path_for, resolve_adapters_path,
};
use crate::output::{JsonlFileSink, OutputSink, StdoutSink};
use crate::plan::{Plan, plan as plan_job, validate_capabilities};

/// Top-level engine. Hold one per concurrent job; the engine is
/// inexpensive — it carries only configuration.
#[derive(Debug, Clone)]
pub struct Engine {
    adapters_path: PathBuf,
}

impl Engine {
    /// Construct an engine that resolves adapters via
    /// [`resolve_adapters_path`].
    #[must_use]
    pub fn new(adapters_path_override: Option<&Path>) -> Self {
        Self {
            adapters_path: resolve_adapters_path(adapters_path_override),
        }
    }

    /// Path the engine resolves adapters from.
    #[must_use]
    pub fn adapters_path(&self) -> &Path {
        &self.adapters_path
    }

    /// Parse `yaml` and return the validated [`Job`] without running
    /// anything.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Job`] on parse or validation failure.
    pub fn parse_job(yaml: &str) -> Result<Job, EngineError> {
        Ok(Job::from_yaml(yaml)?)
    }

    /// Compile a parsed [`Job`] into a [`Plan`].
    #[must_use]
    pub fn plan_job(job: &Job) -> Plan {
        plan_job(job)
    }

    /// Parse, plan, and check the plan's required capabilities against
    /// the driver's declared list. Returns the validated [`Plan`]
    /// without launching anything.
    ///
    /// Used by `spectre validate` to surface configuration errors
    /// (unknown driver, malformed YAML, missing capability) without
    /// paying for a subprocess launch.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Job`] on parse/validation failure,
    /// [`EngineError::Launcher`] if the driver manifest cannot be
    /// read or parsed, and [`EngineError::Plan`] if the plan's
    /// required capabilities are not a subset of the driver's
    /// declared list.
    pub fn validate_only(&self, yaml: &str) -> Result<Plan, EngineError> {
        let job = Self::parse_job(yaml)?;
        let plan = Self::plan_job(&job);
        let manifest = manifest_path_for(&self.adapters_path, &plan.driver);
        let declared = load_declared_capabilities(&manifest)?;
        validate_capabilities(&plan, &declared)?;
        Ok(plan)
    }

    /// Run a job from YAML bytes. Resolves the output path against
    /// `job_dir` (the directory containing the job file). Returns the
    /// total number of rows written.
    ///
    /// # Errors
    ///
    /// Surfaces every variant of [`EngineError`] depending on what
    /// fails: parse, plan, launcher, transport, capability, driver,
    /// output, or I/O.
    pub async fn run_job(&self, yaml: &str, job_dir: &Path) -> Result<usize, EngineError> {
        let job = Self::parse_job(yaml)?;
        let plan = Self::plan_job(&job);
        self.run_plan(plan, job_dir).await
    }

    /// Run a pre-built [`Plan`]. Use this when the caller has already
    /// parsed the job and wants to mutate the plan (e.g. to override
    /// the output path) before execution.
    ///
    /// # Errors
    ///
    /// Same surface as [`Engine::run_job`].
    pub async fn run_plan(&self, plan: Plan, job_dir: &Path) -> Result<usize, EngineError> {
        info!(driver = %plan.driver, steps = plan.steps.len(), "running plan");
        debug!(?plan, "compiled plan");

        let mut sink = make_sink(&plan.output, job_dir)?;

        let handle: DriverHandle =
            launch_driver(&plan.driver, &self.adapters_path, DEFAULT_READY_TIMEOUT).await?;
        let client = Client::dial(&handle.socket_path().to_string_lossy()).await?;

        let outcome = Executor::run(&plan, &client, sink.as_mut()).await;
        handle.shutdown().await;
        outcome
    }
}

fn make_sink(output: &OutputConfig, job_dir: &Path) -> Result<Box<dyn OutputSink>, EngineError> {
    let OutputFormat::Jsonl = output.format;
    if let Some(path) = resolve_output_path(&output.path, job_dir) {
        Ok(Box::new(JsonlFileSink::create(&path)?))
    } else {
        Ok(Box::new(StdoutSink::new()))
    }
}
