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
//! ### Streaming
//!
//! `RunJob` is server-streaming. The implementation spawns the
//! executor on a dedicated tokio task and writes through a
//! [`ChannelSink`] that converts each `serde_json::Value` row into a
//! `RunJobResponse.Row` event on an unbounded channel. After the
//! plan completes the task sends a terminal `Completed` event;
//! errors surface as `Failed`. The receiver is wrapped in a
//! [`UnboundedReceiverStream`] and returned to tonic.
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

use crate::db::Database;
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
    #[allow(dead_code)] // Wired in Step 4; consumed by RunJob in Step 5.
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
        let job_id = if job_id.is_empty() {
            Uuid::new_v4().to_string()
        } else {
            job_id
        };
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

        // Parse + plan eagerly so the most common configuration
        // mistakes (malformed YAML, unknown driver) surface as a
        // synchronous gRPC error, not a `Failed` event the client
        // has to dig out of the stream.
        let job: Job = Engine::parse_job(&job_dsl)
            .map_err(|e| Status::invalid_argument(format!("parse: {e}")))?;
        let plan = Engine::plan_job(&job);

        let engine = Arc::clone(&self.engine);
        let (tx, rx) = mpsc::unbounded_channel::<Result<RunJobResponse, Status>>();

        info!(
            job_id = %job_id,
            driver = %plan.driver,
            output_sink_kind = %output_sink_kind,
            "RunJob accepted",
        );

        tokio::spawn(async move {
            let mut sink = ChannelSink::new(tx.clone());
            let outcome = engine.run_plan_with_sink(&plan, &mut sink).await;
            let event = match outcome {
                Ok(rows_extracted) => {
                    info!(
                        job_id = %job_id,
                        rows = rows_extracted,
                        "RunJob completed",
                    );
                    let rows_extracted = i64::try_from(rows_extracted).unwrap_or(i64::MAX);
                    run_job_response::Event::Completed(Completed { rows_extracted })
                }
                Err(e) => {
                    warn!(job_id = %job_id, error = %e, "RunJob failed");
                    run_job_response::Event::Failed(Failed {
                        error_code: error_code(&e),
                        error_message: e.to_string(),
                    })
                }
            };
            if tx.send(Ok(RunJobResponse { event: Some(event) })).is_err() {
                // Client dropped the stream before the terminal
                // event landed. Nothing to clean up — the channel
                // closes when this task ends.
                error!(job_id = %job_id, "client closed RunJob stream before terminal event");
            }
        });

        Ok(Response::new(Box::pin(UnboundedReceiverStream::new(rx))))
    }
}

/// Output sink that forwards each row as a `RunJobResponse.Row`
/// event on an unbounded mpsc channel.
struct ChannelSink {
    tx: mpsc::UnboundedSender<Result<RunJobResponse, Status>>,
}

impl ChannelSink {
    fn new(tx: mpsc::UnboundedSender<Result<RunJobResponse, Status>>) -> Self {
        Self { tx }
    }
}

impl OutputSink for ChannelSink {
    fn write_row(&mut self, row: &serde_json::Value) -> Result<(), EngineError> {
        let json_line =
            serde_json::to_string(row).map_err(|e| EngineError::Output(e.to_string()))?;
        let event = RunJobResponse {
            event: Some(run_job_response::Event::Row(Row { json_line })),
        };
        self.tx
            .send(Ok(event))
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
    fn channel_sink_forwards_row_as_event() {
        let (tx, mut rx) = mpsc::unbounded_channel();
        let mut sink = ChannelSink::new(tx);
        sink.write_row(&serde_json::json!({"title": "hi"})).unwrap();

        let msg = rx.try_recv().expect("event");
        let event = msg.unwrap().event.unwrap();
        match event {
            run_job_response::Event::Row(Row { json_line }) => {
                assert_eq!(json_line, r#"{"title":"hi"}"#);
            }
            other => panic!("expected Row, got {other:?}"),
        }
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
