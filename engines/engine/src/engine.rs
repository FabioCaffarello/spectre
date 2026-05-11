// SPDX-License-Identifier: Apache-2.0

//! Top-level Engine API.
//!
//! [`Engine::run_plan_with_sink`] is the orchestrator: resolve
//! driver → dial adapter → execute → write rows. The engine no longer
//! spawns adapters as subprocesses; per ADR-0020 §3 they are
//! long-running services dialed over TCP. Adapter discovery flows
//! through [`AdapterRegistry`](crate::registry::AdapterRegistry).
//!
//! The engine is stateless across calls in v1alpha1; PostgreSQL-backed
//! state lands in R4.2.

use std::sync::Arc;

use tracing::{debug, info};

use crate::client::Client;
use crate::dsl::Job;
use crate::error::EngineError;
use crate::executor::Executor;
use crate::output::OutputSink;
use crate::plan::{Plan, plan as plan_job};
use crate::registry::AdapterRegistry;
use crate::telemetry::EngineMetrics;

/// Top-level engine. Hold one per process; cheap to clone (it
/// carries the registry by value, which is itself a small `HashMap`).
#[derive(Debug, Clone)]
pub struct Engine {
    registry: AdapterRegistry,
}

impl Engine {
    /// Construct an engine reading adapter endpoints from the
    /// process environment via [`AdapterRegistry::from_env`].
    #[must_use]
    pub fn from_env() -> Self {
        Self {
            registry: AdapterRegistry::from_env(),
        }
    }

    /// Construct an engine wrapping the supplied registry. Intended
    /// for tests and embedded use; production callers use
    /// [`Self::from_env`].
    #[must_use]
    pub fn with_registry(registry: AdapterRegistry) -> Self {
        Self { registry }
    }

    /// The registry the engine uses for driver discovery.
    #[must_use]
    pub fn registry(&self) -> &AdapterRegistry {
        &self.registry
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

    /// Run a pre-built [`Plan`] with the supplied output sink.
    /// Resolves the plan's `driver` against the registry, dials the
    /// adapter over TCP, and drives the executor against `sink`.
    /// Returns the total number of rows written.
    ///
    /// This is the canonical entry point used by the gRPC server's
    /// `RunJob` handler (see [`crate::server`]).
    ///
    /// # Errors
    ///
    /// Returns every variant of [`EngineError`] depending on what
    /// fails: capability validation, transport dial, driver-side
    /// error, output, or I/O. Specifically returns
    /// [`EngineError::UnknownDriver`] when the plan's driver is not
    /// registered.
    pub async fn run_plan_with_sink(
        &self,
        plan: &Plan,
        sink: &mut dyn OutputSink,
        metrics: &Arc<EngineMetrics>,
    ) -> Result<usize, EngineError> {
        let endpoint = self.registry.resolve(&plan.driver)?;
        info!(
            driver = %plan.driver,
            endpoint = %endpoint,
            steps = plan.steps.len(),
            "running plan"
        );
        debug!(?plan, "compiled plan");
        let client = Client::dial(endpoint).await?;
        Executor::run(plan, &client, sink, metrics, service_label(&plan.driver)).await
    }
}

/// Normalise the DSL driver name into the canonical `service` metric
/// label per ADR-0031 §3.4 (`lower_snake_case`). The mapping is fixed
/// for v1alpha1 (three adapters); new drivers add cases here.
fn service_label(driver: &str) -> &'static str {
    match driver {
        "playwright" => "playwright",
        "seleniumbase" => "seleniumbase",
        "curl-impersonate" => "curl_impersonate",
        _ => "unknown",
    }
}

#[cfg(test)]
mod tests {
    use super::service_label;

    #[test]
    fn service_label_normalises_hyphenated_driver_name() {
        // ADR-0031 §3.4: metric labels are `lower_snake_case`. The
        // hyphen in the DSL driver name must become an underscore.
        assert_eq!(service_label("curl-impersonate"), "curl_impersonate");
    }

    #[test]
    fn service_label_passes_through_lowercase_alpha_names() {
        assert_eq!(service_label("playwright"), "playwright");
        assert_eq!(service_label("seleniumbase"), "seleniumbase");
    }

    #[test]
    fn service_label_falls_back_for_unregistered_driver() {
        assert_eq!(service_label("future-driver"), "unknown");
        assert_eq!(service_label(""), "unknown");
    }
}
