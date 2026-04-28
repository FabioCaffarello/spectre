// SPDX-License-Identifier: Apache-2.0

//! gRPC service implementation for `spectre.engine.v1alpha1.Engine`.
//!
//! The engine binary registers two services on a single TCP listener
//! (default `0.0.0.0:9090` — see [`bin/spectre.rs`](crate)):
//!
//! * the `Engine` service this module implements;
//! * `grpc.health.v1.Health` from `tonic-health`, returning
//!   `SERVING` from process startup so a `grpc_health_probe` (or a
//!   Compose / Kubernetes liveness probe) can readiness-poll the
//!   binary the same way the three reference adapters do
//!   (ADR-0021 §6).
//!
//! ### Streaming and persistence
//!
//! `RunJob` is server-streaming. After parse + plan succeed the
//! service inserts a `jobs` row at status `'running'` (ADR-0023 §2)
//! and rejects synchronously with `Internal` if the insert fails —
//! the client never sees a stream from a job the database does not
//! know about. After the insert, two tokio tasks run concurrently:
//!
//! * an *executor task* that drives [`Engine::run_plan_with_sink`]
//!   against a [`ChannelSink`] forwarding each
//!   [`serde_json::Value`] row to an unbounded mpsc channel;
//! * a *drainer task* (this future) that pulls rows off the channel,
//!   appends them to `job_rows` when `output_sink_kind = 'stdout'`
//!   per ADR-0023 §2, and forwards each row to the gRPC stream as a
//!   `RunJobResponse.Row` event.
//!
//! When the executor finishes, `ChannelSink` (and its sender) drop;
//! the drainer's `recv` returns `None`; the drainer awaits the
//! executor's outcome, writes a terminal `mark_completed` /
//! `mark_failed` UPDATE, and emits the matching `Completed` /
//! `Failed` gRPC event.
//!
//! Per-row Postgres write failures (`record_job_row`) log a warning
//! and continue — an audit gap is preferred to aborting an
//! in-flight scrape over an audit miss. Terminal-state write
//! failures (`mark_completed` / `mark_failed`) likewise log and
//! emit the gRPC event regardless, so the client sees a definitive
//! end even if Postgres is briefly unreachable mid-stream.
//!
//! Backpressure is intentionally naive in v1alpha1 (master prompt
//! §4.4): the channel is unbounded, so a slow client buffers in
//! memory rather than slowing the executor. R3.1's control plane
//! consumes the stream as fast as it produces; production
//! deployments expecting long stalls are a v1alpha2 concern.

use std::pin::Pin;
use std::sync::Arc;

use tokio::sync::mpsc;
use tokio_stream::Stream;
use tokio_stream::wrappers::UnboundedReceiverStream;
use tonic::{Request, Response, Status};
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::db::{Database, jobs as db_jobs};
use crate::dsl::Job;
use crate::engine::Engine;
use crate::engine_proto::engine_server::{Engine as EngineService, EngineServer};
use crate::engine_proto::{
    Completed, Failed, Row, RunJobRequest, RunJobResponse, run_job_response,
};
use crate::error::EngineError;
use crate::output::OutputSink;

/// Reusable factory for the fully-configured tonic service stack.
/// Wraps the engine in an `Arc` (so the streaming task can hold it
/// independently) and exposes the resulting `EngineServer` value.
#[must_use]
pub fn engine_server(engine: Engine, db: Database) -> EngineServer<EngineServiceImpl> {
    EngineServer::new(EngineServiceImpl::new(engine, db))
}

/// Implementation of `spectre.engine.v1alpha1.Engine`. Holds an
/// [`Engine`] (cheap to clone — it carries an [`AdapterRegistry`]
/// of strings) and a [`Database`] handle (cheap to clone — wraps a
/// reference-counted `PgPool`); both are shared with the streaming
/// task spawned per `RunJob`.
pub struct EngineServiceImpl {
    engine: Arc<Engine>,
    db: Database,
}

impl EngineServiceImpl {
    /// Construct a service implementation wrapping `engine` and
    /// holding a [`Database`] handle for ADR-0023 §2 persistence.
    #[must_use]
    pub fn new(engine: Engine, db: Database) -> Self {
        Self {
            engine: Arc::new(engine),
            db,
        }
    }
}

type ResponseStream = Pin<Box<dyn Stream<Item = Result<RunJobResponse, Status>> + Send + 'static>>;

#[tonic::async_trait]
impl EngineService for EngineServiceImpl {
    type RunJobStream = ResponseStream;

    async fn run_job(
        &self,
        request: Request<RunJobRequest>,
    ) -> Result<Response<Self::RunJobStream>, Status> {
        let RunJobRequest {
            job_dsl,
            job_id,
            output_sink_kind,
        } = request.into_inner();
        // Empty defaults to "stdout" so clients that predate R4.2
        // (notably the engine's own integration tests and any
        // hand-written grpcurl call) continue to work without
        // setting the field. ADR-0023 §2 enumerates the four
        // canonical values; anything else is rejected at the schema
        // CHECK constraint when the engine writes the row.
        let output_sink_kind = if output_sink_kind.is_empty() {
            "stdout".to_owned()
        } else {
            output_sink_kind
        };

        // R4.2: `jobs.id` is `UUID PRIMARY KEY`, so the correlation
        // id the client supplies must be a UUID. The control plane
        // forwards `ScrapeJob.UID` (a Kubernetes-issued RFC 4122
        // UUID) here. An empty value triggers a fresh `Uuid::new_v4`
        // for compatibility with hand-written gRPC clients; a
        // non-empty non-UUID value is rejected synchronously.
        let job_uuid = if job_id.is_empty() {
            Uuid::new_v4()
        } else {
            Uuid::parse_str(&job_id)
                .map_err(|e| Status::invalid_argument(format!("job_id must be a UUID: {e}")))?
        };

        // Parse + plan eagerly so the most common configuration
        // mistakes (malformed YAML, unknown driver) surface as a
        // synchronous gRPC error, not a `Failed` event the client
        // has to dig out of the stream.
        let job: Job = Engine::parse_job(&job_dsl)
            .map_err(|e| Status::invalid_argument(format!("parse: {e}")))?;
        let plan = Engine::plan_job(&job);

        // Persist the `jobs` row before opening the stream. A
        // Postgres failure here is reported synchronously as
        // Internal — the client never sees a half-recorded job.
        db_jobs::insert_job(
            &self.db.pool,
            job_uuid,
            &job_dsl,
            &plan.driver,
            &output_sink_kind,
        )
        .await
        .map_err(|e| Status::internal(format!("postgres insert_job: {e}")))?;

        info!(
            job_id = %job_uuid,
            driver = %plan.driver,
            output_sink_kind = %output_sink_kind,
            "RunJob accepted",
        );

        let engine = Arc::clone(&self.engine);
        let pool = self.db.pool.clone();
        let (response_tx, response_rx) =
            mpsc::unbounded_channel::<Result<RunJobResponse, Status>>();

        tokio::spawn(stream_run_job(
            engine,
            pool,
            plan,
            job_uuid,
            output_sink_kind,
            response_tx,
        ));

        Ok(Response::new(Box::pin(UnboundedReceiverStream::new(
            response_rx,
        ))))
    }
}

/// Drives one `RunJob` to completion: spawns the executor on a child
/// task, drains rows off the executor's channel (persisting and
/// forwarding), then writes the terminal `mark_completed` /
/// `mark_failed` UPDATE and emits the matching gRPC event.
async fn stream_run_job(
    engine: Arc<Engine>,
    pool: sqlx::PgPool,
    plan: crate::plan::Plan,
    job_uuid: Uuid,
    output_sink_kind: String,
    response_tx: mpsc::UnboundedSender<Result<RunJobResponse, Status>>,
) {
    let (row_tx, mut row_rx) = mpsc::unbounded_channel::<serde_json::Value>();

    // Executor task: owns the sink (and therefore `row_tx`), so the
    // channel closes when the executor returns and the drainer's
    // `recv()` returns `None`.
    let executor_handle = tokio::spawn(async move {
        let mut sink = ChannelSink::new(row_tx);
        engine.run_plan_with_sink(&plan, &mut sink).await
    });

    let persist_rows = output_sink_kind == "stdout";
    let mut row_index: i64 = 0;
    while let Some(value) = row_rx.recv().await {
        if persist_rows {
            if let Err(e) = db_jobs::record_job_row(&pool, job_uuid, row_index, &value).await {
                warn!(
                    job_id = %job_uuid,
                    row_index,
                    error = %e,
                    "record_job_row failed; continuing without abort",
                );
            }
        }
        let json_line = match serde_json::to_string(&value) {
            Ok(line) => line,
            Err(e) => {
                error!(job_id = %job_uuid, error = %e, "row serialisation failed");
                continue;
            }
        };
        // `is_err()` (client dropped) is acknowledged but not acted
        // on: we keep draining so the executor doesn't block and so
        // remaining rows still persist to `job_rows`.
        let _ = response_tx.send(Ok(RunJobResponse {
            event: Some(run_job_response::Event::Row(Row { json_line })),
        }));
        row_index = row_index.saturating_add(1);
    }

    let outcome = match executor_handle.await {
        Ok(o) => o,
        Err(join_err) => {
            error!(
                job_id = %job_uuid,
                error = %join_err,
                "executor task panicked",
            );
            Err(EngineError::Internal(format!(
                "executor task panicked: {join_err}"
            )))
        }
    };

    let event = build_terminal_event(&pool, job_uuid, outcome).await;

    if response_tx
        .send(Ok(RunJobResponse { event: Some(event) }))
        .is_err()
    {
        error!(job_id = %job_uuid, "client closed RunJob stream before terminal event");
    }
}

/// Persist the terminal state (`mark_completed` or `mark_failed`)
/// and produce the matching gRPC `Event` variant. Postgres write
/// failures here log but do not change the gRPC outcome — the
/// client always sees a definitive end.
async fn build_terminal_event(
    pool: &sqlx::PgPool,
    job_uuid: Uuid,
    outcome: Result<usize, EngineError>,
) -> run_job_response::Event {
    match outcome {
        Ok(rows_extracted) => {
            let rows_i64 = i64::try_from(rows_extracted).unwrap_or(i64::MAX);
            if let Err(e) = db_jobs::mark_completed(pool, job_uuid, rows_i64).await {
                warn!(
                    job_id = %job_uuid,
                    error = %e,
                    "mark_completed failed; gRPC terminal event still sent",
                );
            }
            info!(
                job_id = %job_uuid,
                rows = rows_i64,
                "RunJob completed",
            );
            run_job_response::Event::Completed(Completed {
                rows_extracted: rows_i64,
            })
        }
        Err(e) => {
            let error_message = e.to_string();
            if let Err(db_err) = db_jobs::mark_failed(pool, job_uuid, &error_message).await {
                warn!(
                    job_id = %job_uuid,
                    error = %db_err,
                    "mark_failed failed; gRPC terminal event still sent",
                );
            }
            warn!(
                job_id = %job_uuid,
                error = %error_message,
                "RunJob failed",
            );
            run_job_response::Event::Failed(Failed {
                error_code: error_code(&e),
                error_message,
            })
        }
    }
}

/// Output sink that forwards each row as a `serde_json::Value` on an
/// unbounded mpsc channel. The drainer task in `run_job` is
/// responsible for serialising the row into JSON for the gRPC
/// `Row.json_line` payload and (for stdout-sinked jobs) appending
/// the row to `job_rows` per ADR-0023 §2.
struct ChannelSink {
    tx: mpsc::UnboundedSender<serde_json::Value>,
}

impl ChannelSink {
    fn new(tx: mpsc::UnboundedSender<serde_json::Value>) -> Self {
        Self { tx }
    }
}

impl OutputSink for ChannelSink {
    fn write_row(&mut self, row: &serde_json::Value) -> Result<(), EngineError> {
        // Cloning is necessary because the executor borrows `row`
        // immutably and the drainer needs an owned value to
        // serialise + persist asynchronously.
        self.tx
            .send(row.clone())
            .map_err(|_| EngineError::Output("RunJob stream closed by client".to_owned()))
    }

    fn flush(&mut self) -> Result<(), EngineError> {
        Ok(())
    }
}

fn error_code(err: &EngineError) -> String {
    match err {
        EngineError::Job(_) => "JOB",
        EngineError::Plan(_) => "PLAN",
        EngineError::Transport(_) => "TRANSPORT",
        EngineError::Driver { .. } => "DRIVER",
        EngineError::CapabilityMissing { .. } => "CAPABILITY_MISSING",
        EngineError::UnknownDriver(_) => "UNKNOWN_DRIVER",
        EngineError::Output(_) => "OUTPUT",
        EngineError::Io(_) => "IO",
        EngineError::Internal(_) => "INTERNAL",
    }
    .to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn channel_sink_forwards_row_through_channel() {
        let (tx, mut rx) = mpsc::unbounded_channel();
        let mut sink = ChannelSink::new(tx);
        sink.write_row(&serde_json::json!({"title": "hi"})).unwrap();

        let value = rx.try_recv().expect("row");
        assert_eq!(value, serde_json::json!({"title": "hi"}));
    }

    #[test]
    fn channel_sink_reports_closed_channel() {
        let (tx, rx) = mpsc::unbounded_channel();
        drop(rx);
        let mut sink = ChannelSink::new(tx);
        let err = sink
            .write_row(&serde_json::json!({"x": 1}))
            .expect_err("must error");
        match err {
            EngineError::Output(msg) => assert!(msg.contains("stream closed")),
            other => panic!("expected Output, got {other:?}"),
        }
    }

    #[test]
    fn error_code_covers_every_variant() {
        let cases: &[EngineError] = &[
            EngineError::Transport("x".into()),
            EngineError::Driver {
                code: "X".into(),
                message: "y".into(),
            },
            EngineError::UnknownDriver("missing".into()),
            EngineError::CapabilityMissing {
                missing: vec!["a".into()],
            },
            EngineError::Output("o".into()),
            EngineError::Internal("i".into()),
        ];
        for c in cases {
            let code = error_code(c);
            assert!(!code.is_empty(), "missing code for {c:?}");
        }
    }
}
