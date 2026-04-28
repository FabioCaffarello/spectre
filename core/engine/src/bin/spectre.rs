// SPDX-License-Identifier: Apache-2.0

//! `spectre` — the engine gRPC service binary.
//!
//! Binds a TCP listener (default `0.0.0.0:9090`, override via
//! `SPECTRE_ENGINE_PORT`), registers
//! `spectre.engine.v1alpha1.Engine` (the streaming `RunJob` RPC)
//! and `grpc.health.v1.Health` (returning `SERVING` from process
//! startup), and serves until SIGTERM/SIGINT.
//!
//! Adapter discovery is via environment variables read at startup;
//! see [`spectre_engine::registry`]. Defaults bind the three
//! reference adapters to `127.0.0.1:909{1,2,3}` so a developer
//! running engine + adapter on the same workstation gets a working
//! configuration with no setup. Compose (R6.2) and Helm (R7.1)
//! override the variables to point at deployed service names.
//!
//! The CLI subcommands (`run`, `validate`, standalone `version`)
//! that ADR-0013 introduced were retired in R2.3; the binary now
//! does one thing — start the gRPC service. ADR-0020 §3 records the
//! supersession.

use std::env;
use std::net::SocketAddr;
use std::process::ExitCode;
use std::sync::Arc;

use anyhow::{Context, Result};
use spectre_engine::db::{Database, run_migrations};
use spectre_engine::kafka::KafkaProducer;
use spectre_engine::registry::AdapterRegistry;
use spectre_engine::server::engine_server;
use spectre_engine::{ENGINE_VERSION, Engine, PROTOCOL_VERSION};
use tonic::transport::Server;
use tonic_health::ServingStatus;
use tracing::{info, warn};
use tracing_subscriber::EnvFilter;

const DEFAULT_PORT: u16 = 9090;
const PORT_ENV: &str = "SPECTRE_ENGINE_PORT";

#[tokio::main(flavor = "multi_thread")]
async fn main() -> ExitCode {
    init_tracing();

    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("error: {e:#}");
            ExitCode::from(1)
        }
    }
}

async fn run() -> Result<()> {
    let port = parse_port()?;
    let addr: SocketAddr = format!("0.0.0.0:{port}")
        .parse()
        .with_context(|| format!("invalid bind address for port {port}"))?;

    // Postgres dial + migrations run before the gRPC service is
    // registered. ADR-0023 §6 + §13: an unreachable database, a
    // failed migration, or an absent SPECTRE_POSTGRES_URL is a
    // startup-time engine failure (no degraded mode).
    let db = Database::from_env()
        .await
        .context("postgres: dial / pool construction failed at startup")?;
    run_migrations(&db.pool)
        .await
        .context("postgres: migration apply failed at startup")?;
    info!(
        max_conns = db.pool.options().get_max_connections(),
        "postgres ready"
    );

    // Kafka producer dial. ADR-0023 §6 makes Kafka OPTIONAL: the
    // engine continues startup even when the broker is unreachable
    // or unconfigured. Jobs with `OutputSink.Kafka` later fail fast
    // at job-start time with `error_code = "KAFKA_UNAVAILABLE"`
    // (ADR-0023 §3 R4.4 addendum) — equivalent UX to admission
    // rejection without the cost of a validating webhook.
    let kafka: Option<Arc<KafkaProducer>> = match KafkaProducer::from_env().await {
        Ok(producer) => {
            info!(brokers = producer.brokers(), "kafka producer ready");
            Some(Arc::new(producer))
        }
        Err(e) => {
            warn!(
                error = %e,
                "kafka producer unavailable; OutputSink.Kafka jobs will fail fast \
                 with KAFKA_UNAVAILABLE — set SPECTRE_KAFKA_BROKERS and restart \
                 the engine to enable the kafka sink",
            );
            None
        }
    };

    let registry = AdapterRegistry::from_env();
    log_registry(&registry);

    let engine = Engine::with_registry(registry);
    let svc = engine_server(engine, db, kafka);

    let (health_reporter, health_service) = tonic_health::server::health_reporter();
    health_reporter
        .set_service_status("", ServingStatus::Serving)
        .await;
    health_reporter
        .set_service_status("spectre.engine.v1alpha1.Engine", ServingStatus::Serving)
        .await;

    info!(
        version = ENGINE_VERSION,
        protocol = PROTOCOL_VERSION,
        addr = %addr,
        "spectre engine listening"
    );

    Server::builder()
        .add_service(svc)
        .add_service(health_service)
        .serve_with_shutdown(addr, shutdown_signal())
        .await
        .context("gRPC server terminated")?;

    info!("spectre engine shut down");
    Ok(())
}

fn parse_port() -> Result<u16> {
    match env::var(PORT_ENV) {
        Ok(s) => s
            .parse::<u16>()
            .with_context(|| format!("invalid {PORT_ENV} value (expected u16): {s:?}")),
        Err(_) => Ok(DEFAULT_PORT),
    }
}

fn log_registry(registry: &AdapterRegistry) {
    for (driver, endpoint) in registry.iter() {
        info!(driver, endpoint, "adapter endpoint registered");
    }
}

async fn shutdown_signal() {
    let ctrl_c = async {
        if let Err(e) = tokio::signal::ctrl_c().await {
            warn!(error = %e, "ctrl-c handler failed");
        }
    };

    #[cfg(unix)]
    let terminate = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut sig) => {
                sig.recv().await;
            }
            Err(e) => {
                warn!(error = %e, "SIGTERM handler failed");
                std::future::pending::<()>().await;
            }
        }
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        () = ctrl_c => info!("received SIGINT"),
        () = terminate => info!("received SIGTERM"),
    }
}

fn init_tracing() {
    let filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| EnvFilter::new("spectre_engine=info,spectre=info"));
    let _ = tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(filter)
        .with_target(false)
        .compact()
        .try_init();
}
