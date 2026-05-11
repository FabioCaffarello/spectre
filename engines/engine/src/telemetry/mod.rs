// SPDX-License-Identifier: Apache-2.0

//! Engine OpenTelemetry observability — first landing per ADR-0031
//! Wave 3 §9.
//!
//! Layout:
//!
//! - [`metrics`] — declares the `spectre_engine_*` instrument
//!   handles ADR-0031 §5.1 lists. Cluster A (this landing)
//!   registers names with the meter provider; per-call-site
//!   recordings land in Cluster C.
//! - [`server`] — axum `/metrics` sidecar exposing the Prometheus
//!   text registry on the uniform port 9090 per ADR-0031 §3.3.
//!
//! Trace + meter providers initialise in [`Telemetry::init`]:
//!
//! - **Meter** — always on. A [`prometheus::Registry`] receives both
//!   `OTel` meter readings (via `opentelemetry-prometheus`) and the
//!   process collector emitting `process_*` per ADR-0031 §5.5.
//! - **Tracer** — opt-in. When `OTEL_EXPORTER_OTLP_ENDPOINT` is
//!   set, an OTLP/gRPC exporter pushes spans to a collector per
//!   ADR-0031 §2.2; otherwise the provider is a no-op (info-log
//!   on startup, mirroring the Kafka / S3 optional pattern).
//!
//! `Logs` are intentionally NOT routed through `OTel` — ADR-0031
//! §2.4 defers `OTel` logs to v1beta1 while Rust SDK support
//! matures. The JSON stdout layer for `tracing-subscriber` lives
//! in Cluster D.

pub mod logs;
pub mod metrics;
pub mod propagation;
mod server;

use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result};
use opentelemetry::KeyValue;
use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::metrics::SdkMeterProvider;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::SdkTracerProvider;
use prometheus::Registry;
use tokio::task::JoinHandle;
use tracing::{info, warn};

pub use metrics::EngineMetrics;

/// Startup configuration extracted from environment variables.
///
/// Field semantics align with ADR-0031 §3.x. See
/// [`Self::from_env`] for the variables read.
#[derive(Debug, Clone)]
pub struct TelemetryConfig {
    /// `service.name` `OTel` resource attribute. Constant
    /// `"spectre-engine"` for v1alpha1.
    pub service_name: &'static str,
    /// `service.version` `OTel` resource attribute. Sourced from
    /// the crate's `ENGINE_VERSION` constant so it tracks the
    /// chart `appVersion`.
    pub service_version: &'static str,
    /// Bind address for the Prometheus `/metrics` sidecar.
    pub metrics_addr: SocketAddr,
    /// Optional OTLP/gRPC trace endpoint (e.g.
    /// `http://otel-collector:4317`). `None` → no-op tracer
    /// provider; traces are dropped before reaching an exporter.
    pub otlp_endpoint: Option<String>,
}

impl TelemetryConfig {
    /// Build a config by reading the runtime environment.
    ///
    /// Variables consulted:
    ///
    /// - `SPECTRE_METRICS_PORT` — sidecar port (default `9090`).
    /// - `OTEL_EXPORTER_OTLP_ENDPOINT` — `OTel`-standard variable;
    ///   empty / unset → no-op tracer.
    ///
    /// # Errors
    ///
    /// Returns an error when `SPECTRE_METRICS_PORT` is set to a
    /// non-`u16` value, or when the resulting bind address fails
    /// to parse.
    pub fn from_env(service_version: &'static str) -> Result<Self> {
        let port = std::env::var("SPECTRE_METRICS_PORT")
            .ok()
            .map(|s| {
                s.parse::<u16>()
                    .with_context(|| format!("invalid SPECTRE_METRICS_PORT (expected u16): {s:?}"))
            })
            .transpose()?
            .unwrap_or(9090);
        let metrics_addr: SocketAddr = format!("0.0.0.0:{port}")
            .parse()
            .with_context(|| format!("invalid metrics bind address for port {port}"))?;

        let otlp_endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .ok()
            .filter(|s| !s.is_empty());

        Ok(Self {
            service_name: "spectre-engine",
            service_version,
            metrics_addr,
            otlp_endpoint,
        })
    }
}

/// Live telemetry handles owned by the engine binary.
///
/// Holds the meter provider, the tracer provider, the shared metric
/// struct call sites record into, and the join handle of the
/// background sidecar task. Drop / shutdown is graceful:
/// [`Self::shutdown`] aborts the sidecar then flushes the providers.
pub struct Telemetry {
    /// Engine metric instruments shared with call sites
    /// (server / executor / client).
    pub metrics: Arc<EngineMetrics>,
    tracer_provider: SdkTracerProvider,
    meter_provider: SdkMeterProvider,
    sidecar: JoinHandle<()>,
}

impl Telemetry {
    /// Initialise tracer + meter + sidecar HTTP server.
    ///
    /// Order matters: the Prometheus registry + meter provider
    /// land before the tracer provider so meter recording is
    /// available even when OTLP is unconfigured. The sidecar
    /// binds last so a failed bind does not leak a meter
    /// provider into the global slot.
    ///
    /// # Errors
    ///
    /// Returns an error when the `opentelemetry-prometheus`
    /// exporter fails to build, when the process collector fails
    /// to register, when the OTLP span exporter fails to build
    /// (only when `otlp_endpoint` is `Some`), or when the metrics
    /// sidecar fails to bind its TCP listener.
    pub async fn init(config: TelemetryConfig) -> Result<Self> {
        let resource = Resource::builder()
            .with_attribute(KeyValue::new("service.name", config.service_name))
            .with_attribute(KeyValue::new("service.version", config.service_version))
            .with_attribute(KeyValue::new("service.namespace", "spectre"))
            .build();

        // Prometheus registry shared between OTel meter exporter
        // and the process collector. ADR-0031 §5.5 mandates the
        // OTel-standard `process_*` family is never disabled.
        let registry = Registry::new();
        let prom_exporter = opentelemetry_prometheus::exporter()
            .with_registry(registry.clone())
            .build()
            .context("opentelemetry-prometheus exporter init")?;
        let meter_provider = SdkMeterProvider::builder()
            .with_reader(prom_exporter)
            .with_resource(resource.clone())
            .build();

        // ADR-0031 §5.5 mandates `process_*` metrics. The
        // `prometheus` crate's process collector is gated on
        // `target_os = "linux"` (Linux `/proc` filesystem reads).
        // Production engine images are Linux; macOS host builds
        // (developer workstations) emit `runtime_*` from the OTel
        // SDK but skip `process_*`. The asymmetry is documented in
        // ADR-0031 §11 ("Continuous profiling" deferral neighbour).
        #[cfg(target_os = "linux")]
        {
            let process_collector = prometheus::process_collector::ProcessCollector::for_self();
            registry
                .register(Box::new(process_collector))
                .context("register prometheus process collector")?;
        }

        // Instruments register against the local meter provider so
        // the Prometheus reader's collection pipeline sees their
        // observations. `SdkMeterProvider::clone` produces an
        // independent provider with its own reader state, so a
        // `set_meter_provider(clone)` followed by
        // `global::meter().counter(...)` writes to a *different*
        // provider than the one feeding the scrape — leaving the
        // registry showing `target_info` only. We register first,
        // then promote the same provider to the global slot for
        // the tracing-opentelemetry bridge (Cluster B) and any
        // third-party crate auto-emitting via the global meter.
        let metrics = Arc::new(EngineMetrics::register(
            &meter_provider.meter("spectre-engine"),
        ));
        opentelemetry::global::set_meter_provider(meter_provider.clone());

        // W3C Trace Context propagator goes global unconditionally —
        // ADR-0031 §4.1 requires `traceparent` extraction / injection
        // on every gRPC boundary, regardless of whether the deployment
        // pushes spans to a collector. With no exporter, spans still
        // generate valid IDs and propagate downstream; the collector
        // path simply has no consumer.
        opentelemetry::global::set_text_map_propagator(TraceContextPropagator::new());

        // The tracer provider is always real — never a no-op — so
        // generated span IDs are valid even without an OTLP endpoint
        // configured. The exporter attaches only when configured;
        // without it, spans complete and drop with no export side
        // effect. This is the same shape the propagator path needs:
        // a deployment with no collector still preserves trace_id
        // through the engine.
        let mut tracer_builder = SdkTracerProvider::builder().with_resource(resource);
        let exporter_status = if let Some(endpoint) = config.otlp_endpoint.as_deref() {
            let exporter = opentelemetry_otlp::SpanExporter::builder()
                .with_tonic()
                .with_endpoint(endpoint)
                .with_timeout(Duration::from_secs(5))
                .build()
                .context("otlp span exporter init")?;
            tracer_builder = tracer_builder.with_batch_exporter(exporter);
            Some(endpoint.to_owned())
        } else {
            None
        };
        let tracer_provider = tracer_builder.build();
        opentelemetry::global::set_tracer_provider(tracer_provider.clone());
        if let Some(endpoint) = exporter_status {
            info!(endpoint = %endpoint, "otlp trace exporter ready");
        } else {
            info!(
                "OTEL_EXPORTER_OTLP_ENDPOINT unset; spans generate \
                 valid IDs and propagate, but are not exported (set \
                 OTEL_EXPORTER_OTLP_ENDPOINT to enable OTLP/gRPC push \
                 per ADR-0031 §2.2)"
            );
        }

        let sidecar = server::spawn(config.metrics_addr, registry).await?;
        info!(addr = %config.metrics_addr, "metrics sidecar listening");

        Ok(Self {
            metrics,
            tracer_provider,
            meter_provider,
            sidecar,
        })
    }

    /// Gracefully shut down providers and the sidecar.
    ///
    /// Sequence:
    ///
    /// 1. Abort the sidecar task so `/metrics` stops serving.
    /// 2. Shut down the meter provider — flushes any in-flight
    ///    metric exports (no-op for the Prometheus pull model
    ///    but kept for parity with future OTLP metric push).
    /// 3. Shut down the tracer provider — flushes the batch span
    ///    processor (best-effort; a SIGKILL will still drop spans).
    pub async fn shutdown(self) {
        self.sidecar.abort();
        let _ = self.sidecar.await;
        if let Err(e) = self.meter_provider.shutdown() {
            warn!(error = %e, "meter provider shutdown failed");
        }
        if let Err(e) = self.tracer_provider.shutdown() {
            warn!(error = %e, "tracer provider shutdown failed");
        }
    }
}
