// SPDX-License-Identifier: Apache-2.0

//! Adapter discovery via environment variables.
//!
//! Replaces the subprocess launcher (retired in R2.3). The engine no
//! longer spawns adapters; it dials them as long-running services
//! over TCP. Each driver name in the DSL maps to a `host:port`
//! endpoint resolved at engine startup from a per-driver environment
//! variable. See ADR-0021 §5.
//!
//! # Conventions
//!
//! | Driver name        | Environment variable                 | Default port |
//! |--------------------|--------------------------------------|--------------|
//! | `playwright`       | `SPECTRE_PLAYWRIGHT_ENDPOINT`        | 9091         |
//! | `seleniumbase`     | `SPECTRE_SELENIUMBASE_ENDPOINT`      | 9092         |
//! | `curl-impersonate` | `SPECTRE_CURL_IMPERSONATE_ENDPOINT`  | 9093         |
//!
//! Defaults bind to `127.0.0.1` so a developer running the engine and
//! a single adapter on the same workstation gets a working
//! configuration with no env-var setup. Production deployments
//! (Compose in R6.2, Helm in R7.1) override the variables to point
//! at the deployed service hostnames.
//!
//! # No pre-dial
//!
//! `from_env` does not connect to any endpoint; it only stores the
//! resolved address. Lazy dialing avoids startup-order coupling
//! between engine and adapters in Compose / Kubernetes — both
//! services can come up in any order, and the engine's first
//! `RunJob` call against an unreached adapter surfaces a transport
//! error rather than a fatal startup failure.

use std::collections::HashMap;
use std::env;

use crate::error::EngineError;

/// Environment variable controlling the Playwright adapter endpoint.
pub const PLAYWRIGHT_ENDPOINT_ENV: &str = "SPECTRE_PLAYWRIGHT_ENDPOINT";
/// Environment variable controlling the `SeleniumBase` adapter endpoint.
pub const SELENIUMBASE_ENDPOINT_ENV: &str = "SPECTRE_SELENIUMBASE_ENDPOINT";
/// Environment variable controlling the curl-impersonate adapter endpoint.
pub const CURL_IMPERSONATE_ENDPOINT_ENV: &str = "SPECTRE_CURL_IMPERSONATE_ENDPOINT";

/// Local-development default for the Playwright adapter endpoint.
pub const PLAYWRIGHT_DEFAULT_ENDPOINT: &str = "127.0.0.1:9091";
/// Local-development default for the `SeleniumBase` adapter endpoint.
pub const SELENIUMBASE_DEFAULT_ENDPOINT: &str = "127.0.0.1:9092";
/// Local-development default for the curl-impersonate adapter endpoint.
pub const CURL_IMPERSONATE_DEFAULT_ENDPOINT: &str = "127.0.0.1:9093";

/// Driver-name → endpoint mapping populated from environment variables.
///
/// Held by the engine for the duration of the process; cheap to
/// clone (it carries a small `HashMap` of strings).
#[derive(Debug, Clone)]
pub struct AdapterRegistry {
    endpoints: HashMap<String, String>,
}

impl AdapterRegistry {
    /// Build a registry from the process environment. Falls back to
    /// the per-driver default endpoint when the corresponding
    /// variable is unset. Defaults are documented above.
    #[must_use]
    pub fn from_env() -> Self {
        let mut endpoints = HashMap::with_capacity(3);
        endpoints.insert(
            "playwright".to_owned(),
            env_or_default(PLAYWRIGHT_ENDPOINT_ENV, PLAYWRIGHT_DEFAULT_ENDPOINT),
        );
        endpoints.insert(
            "seleniumbase".to_owned(),
            env_or_default(SELENIUMBASE_ENDPOINT_ENV, SELENIUMBASE_DEFAULT_ENDPOINT),
        );
        endpoints.insert(
            "curl-impersonate".to_owned(),
            env_or_default(
                CURL_IMPERSONATE_ENDPOINT_ENV,
                CURL_IMPERSONATE_DEFAULT_ENDPOINT,
            ),
        );
        Self { endpoints }
    }

    /// Build a registry from an explicit map. Intended for unit
    /// tests; production callers use [`Self::from_env`].
    #[must_use]
    pub fn from_map(endpoints: HashMap<String, String>) -> Self {
        Self { endpoints }
    }

    /// Resolve a driver name to its registered endpoint.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::UnknownDriver`] when `driver` is not in
    /// the registry — the DSL referenced a name no adapter is
    /// configured for. The set of registered names is the three
    /// reference adapters in v1alpha1.
    pub fn resolve(&self, driver: &str) -> Result<&str, EngineError> {
        self.endpoints
            .get(driver)
            .map(String::as_str)
            .ok_or_else(|| EngineError::UnknownDriver(driver.to_owned()))
    }

    /// Iterate every registered `(driver, endpoint)` pair. Useful
    /// for startup logging.
    pub fn iter(&self) -> impl Iterator<Item = (&str, &str)> {
        self.endpoints
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
    }
}

fn env_or_default(var: &str, default: &str) -> String {
    env::var(var).unwrap_or_else(|_| default.to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn from_map_resolves_registered_drivers() {
        let mut endpoints = HashMap::new();
        endpoints.insert("playwright".to_owned(), "playwright:9091".to_owned());
        endpoints.insert("seleniumbase".to_owned(), "seleniumbase:9092".to_owned());
        let registry = AdapterRegistry::from_map(endpoints);

        assert_eq!(registry.resolve("playwright").unwrap(), "playwright:9091");
        assert_eq!(
            registry.resolve("seleniumbase").unwrap(),
            "seleniumbase:9092"
        );
    }

    #[test]
    fn unknown_driver_surfaces_unknown_driver_error() {
        let registry = AdapterRegistry::from_map(HashMap::new());
        match registry.resolve("nope") {
            Err(EngineError::UnknownDriver(name)) => assert_eq!(name, "nope"),
            other => panic!("expected UnknownDriver, got {other:?}"),
        }
    }

    #[test]
    fn iter_yields_every_registered_pair() {
        let mut endpoints = HashMap::new();
        endpoints.insert("a".to_owned(), "1".to_owned());
        endpoints.insert("b".to_owned(), "2".to_owned());
        let registry = AdapterRegistry::from_map(endpoints);

        let mut pairs: Vec<(String, String)> = registry
            .iter()
            .map(|(k, v)| (k.to_owned(), v.to_owned()))
            .collect();
        pairs.sort();
        assert_eq!(
            pairs,
            vec![
                ("a".to_owned(), "1".to_owned()),
                ("b".to_owned(), "2".to_owned()),
            ]
        );
    }
}
