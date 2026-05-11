// SPDX-License-Identifier: Apache-2.0

//! Engine metric instruments per ADR-0031 §5.1.
//!
//! Cluster A (this landing) registers the five `spectre_engine_*`
//! names with the meter provider so they appear in `/metrics` even
//! when no job has run. The sixth name in §5.1
//! (`spectre_engine_circuit_breaker_state{service,state}`) lands
//! with the circuit breaker itself in Wave 5 per ADR-0037 §5.3 —
//! declaring an unused gauge here would surface the name without
//! samples, which is misleading.
//!
//! Cluster C wires per-call-site recordings at
//! `server::stream_run_job` (active / completed / rows), at
//! `executor.rs` (step duration), and at `client.rs`
//! (service-call duration).

use opentelemetry::metrics::{Counter, Histogram, Meter, UpDownCounter};

/// Engine metric handles, registered once at startup and shared
/// via `Arc<EngineMetrics>` to recording call sites.
pub struct EngineMetrics {
    /// `spectre_engine_jobs_active` — gauge of currently-executing
    /// jobs. Incremented on `run_job` accept; decremented on the
    /// stream's terminal event.
    pub jobs_active: UpDownCounter<i64>,
    /// `spectre_engine_jobs_completed_total{result}` — counter of
    /// jobs that reached a terminal event. `result` ∈
    /// `{success, failure, cancelled}`. `timeout` is reserved for
    /// the Wave 5 circuit-breaker landing.
    pub jobs_completed_total: Counter<u64>,
    /// `spectre_engine_step_duration_seconds` — histogram of
    /// per-step duration. OTel-default buckets.
    pub step_duration_seconds: Histogram<f64>,
    /// `spectre_engine_step_service_call_duration_seconds{service}` —
    /// histogram of adapter-RPC duration. `service` label values:
    /// `playwright`, `seleniumbase`, `curl_impersonate`.
    pub step_service_call_duration_seconds: Histogram<f64>,
    /// `spectre_engine_rows_emitted_total{sink}` — counter of
    /// rows emitted to a sink. `sink` ∈
    /// `{stdout, kafka, s3, webhook}`.
    pub rows_emitted_total: Counter<u64>,
}

impl EngineMetrics {
    /// Register every instrument against `meter`. Idempotent within
    /// a process (calling twice with the same meter returns the
    /// same underlying instrument by `OTel` SDK contract).
    #[must_use]
    pub fn register(meter: &Meter) -> Self {
        Self {
            jobs_active: meter
                .i64_up_down_counter("spectre_engine_jobs_active")
                .with_description("Currently-executing jobs in the engine.")
                .build(),
            jobs_completed_total: meter
                .u64_counter("spectre_engine_jobs_completed_total")
                .with_description("Total jobs completed, partitioned by `result` label.")
                .build(),
            step_duration_seconds: meter
                .f64_histogram("spectre_engine_step_duration_seconds")
                .with_description("Per-step duration in seconds (OTel default buckets).")
                .with_unit("s")
                .build(),
            step_service_call_duration_seconds: meter
                .f64_histogram("spectre_engine_step_service_call_duration_seconds")
                .with_description("Per-step adapter-RPC duration, partitioned by `service` label.")
                .with_unit("s")
                .build(),
            rows_emitted_total: meter
                .u64_counter("spectre_engine_rows_emitted_total")
                .with_description("Rows emitted to a sink, partitioned by `sink` label.")
                .build(),
        }
    }
}
