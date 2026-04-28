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
//!   per ADR-0023 §2, publishes them to Kafka when
//!   `output_sink_kind = 'kafka'` per ADR-0023 §3 (R4.4), and
//!   forwards each row to the gRPC stream as a
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
//! in-flight scrape over an audit miss. Per-row Kafka publish
//! failures terminate the job with `error_code =
//! "KAFKA_PUBLISH_FAILED"` (ADR-0023 §3 — Kafka is the data
//! destination, not an audit aside, so a publish error is fatal).
//! Terminal-state write failures (`mark_completed` / `mark_failed`)
//! likewise log and emit the gRPC event regardless, so the client
//! sees a definitive end even if Postgres is briefly unreachable
//! mid-stream.
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
use crate::kafka::KafkaProducer;
use crate::output::OutputSink;
use crate::s3::S3Uploader;
use crate::webhook::WebhookClient;

/// Reusable factory for the fully-configured tonic service stack.
/// Wraps the engine in an `Arc` (so the streaming task can hold it
/// independently) and exposes the resulting `EngineServer` value.
///
/// - `kafka` is the (optional) shared `KafkaProducer`. `None` when
///   the engine started without a reachable broker; jobs whose
///   `output_sink_kind = 'kafka'` then fail fast with
///   `KAFKA_UNAVAILABLE` (ADR-0023 §3 R4.4 addendum).
/// - `s3` is the (optional) shared [`S3Uploader`]. `None` when the
///   engine started without `SPECTRE_S3_*` env (BYO-credentials
///   mode is INFO-level) or with an unparseable env; S3-sinked
///   jobs against `None` fail fast with `S3_UNAVAILABLE`
///   (ADR-0024 §5).
/// - `webhook` is the (always present) [`WebhookClient`]. The
///   client has no engine-level state; per-job admission happens
///   at the executor (ADR-0024 §5).
#[must_use]
pub fn engine_server(
    engine: Engine,
    db: Database,
    kafka: Option<Arc<KafkaProducer>>,
    s3: Option<Arc<S3Uploader>>,
    webhook: Arc<WebhookClient>,
) -> EngineServer<EngineServiceImpl> {
    EngineServer::new(EngineServiceImpl::new(engine, db, kafka, s3, webhook))
}

/// Implementation of `spectre.engine.v1alpha1.Engine`. Holds an
/// [`Engine`] (cheap to clone — it carries an [`AdapterRegistry`]
/// of strings), a [`Database`] handle (cheap to clone — wraps a
/// reference-counted `PgPool`), an optional shared
/// [`KafkaProducer`], an optional shared [`S3Uploader`], and the
/// always-present [`WebhookClient`]; all are shared with the
/// streaming task spawned per `RunJob`.
pub struct EngineServiceImpl {
    engine: Arc<Engine>,
    db: Database,
    kafka: Option<Arc<KafkaProducer>>,
    s3: Option<Arc<S3Uploader>>,
    webhook: Arc<WebhookClient>,
}

impl EngineServiceImpl {
    /// Construct a service implementation wrapping `engine`,
    /// holding a [`Database`] handle for ADR-0023 §2 persistence,
    /// the optional sink-level state ([`KafkaProducer`],
    /// [`S3Uploader`]) for ADR-0023 §3 + ADR-0024 §3, and the
    /// always-present [`WebhookClient`] for ADR-0024 §4.
    #[must_use]
    pub fn new(
        engine: Engine,
        db: Database,
        kafka: Option<Arc<KafkaProducer>>,
        s3: Option<Arc<S3Uploader>>,
        webhook: Arc<WebhookClient>,
    ) -> Self {
        Self {
            engine: Arc::new(engine),
            db,
            kafka,
            s3,
            webhook,
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
            kafka_topic,
            ..
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
        // The row is written for every sink kind (Kafka jobs included)
        // so a Postgres reader can answer "where did the output go?"
        // (ADR-0023 §2 — `output_sink_kind` is recorded for every
        // job; only `job_rows` audit is gated on stdout).
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
        let kafka = self.kafka.clone();
        let driver = plan.driver.clone();
        let (response_tx, response_rx) =
            mpsc::unbounded_channel::<Result<RunJobResponse, Status>>();

        tokio::spawn(stream_run_job(
            engine,
            pool,
            kafka,
            plan,
            job_uuid,
            driver,
            output_sink_kind,
            kafka_topic,
            response_tx,
        ));

        Ok(Response::new(Box::pin(UnboundedReceiverStream::new(
            response_rx,
        ))))
    }
}

/// Drives one `RunJob` to completion: spawns the executor on a child
/// task, drains rows off the executor's channel (persisting and
/// publishing as the sink demands, forwarding to gRPC), then writes
/// the terminal `mark_completed` / `mark_failed` UPDATE and emits
/// the matching gRPC event.
#[allow(clippy::too_many_arguments, clippy::too_many_lines)]
async fn stream_run_job(
    engine: Arc<Engine>,
    pool: sqlx::PgPool,
    kafka: Option<Arc<KafkaProducer>>,
    plan: crate::plan::Plan,
    job_uuid: Uuid,
    driver: String,
    output_sink_kind: String,
    kafka_topic: String,
    response_tx: mpsc::UnboundedSender<Result<RunJobResponse, Status>>,
) {
    // Pre-flight kafka admission: short-circuit before touching the
    // executor when the sink is kafka but the producer is missing
    // or the topic is empty. Yields the terminal Failed event,
    // writes `mark_failed`, returns.
    if output_sink_kind == "kafka" {
        if kafka.is_none() {
            terminate_with_failure(
                &pool,
                job_uuid,
                "KAFKA_UNAVAILABLE",
                "kafka producer is not available; set SPECTRE_KAFKA_BROKERS \
                 and restart engine to enable OutputSink.Kafka jobs",
                &response_tx,
            )
            .await;
            return;
        }
        if kafka_topic.trim().is_empty() {
            terminate_with_failure(
                &pool,
                job_uuid,
                "KAFKA_TOPIC_REQUIRED",
                "kafka_topic is empty; set ScrapeJob.spec.outputSink.kafka.topic",
                &response_tx,
            )
            .await;
            return;
        }
    }

    let (row_tx, mut row_rx) = mpsc::unbounded_channel::<serde_json::Value>();

    // Executor task: owns the sink (and therefore `row_tx`), so the
    // channel closes when the executor returns and the drainer's
    // `recv()` returns `None`.
    let executor_handle = tokio::spawn(async move {
        let mut sink = ChannelSink::new(row_tx);
        engine.run_plan_with_sink(&plan, &mut sink).await
    });

    let persist_rows = output_sink_kind == "stdout";
    let publish_kafka = output_sink_kind == "kafka";
    let mut row_index: i64 = 0;
    let mut kafka_publish_error: Option<String> = None;

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
                row_index = row_index.saturating_add(1);
                continue;
            }
        };

        if publish_kafka && kafka_publish_error.is_none() {
            // `kafka` is `Some` here: the pre-flight check above
            // returned for the `None` case. Publishes are awaited
            // sequentially per row — librdkafka's internal queue +
            // delivery futures handle concurrency under the hood.
            if let Some(producer) = kafka.as_ref() {
                let timestamp = chrono::Utc::now().to_rfc3339();
                if let Err(e) = producer
                    .publish_row(
                        &kafka_topic,
                        &job_uuid.to_string(),
                        row_index,
                        &driver,
                        &timestamp,
                        json_line.as_bytes(),
                    )
                    .await
                {
                    warn!(
                        job_id = %job_uuid,
                        row_index,
                        topic = %kafka_topic,
                        error = %e,
                        "kafka publish failed; aborting drain",
                    );
                    kafka_publish_error = Some(e.to_string());
                }
            }
        }

        // Forward the row to the gRPC stream regardless of sink so
        // the control plane can mirror it into operator stdout for
        // `kubectl logs` (ADR-0019 §6). Stops forwarding after the
        // first kafka error so the client sees the failure quickly.
        if kafka_publish_error.is_none() {
            // `is_err()` (client dropped) is acknowledged but not
            // acted on: we keep draining so the executor doesn't
            // block and so remaining rows still persist to
            // `job_rows`.
            let _ = response_tx.send(Ok(RunJobResponse {
                event: Some(run_job_response::Event::Row(Row { json_line })),
            }));
        }
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

    // Kafka publish failure overrides a successful executor outcome
    // — the executor finished but the destination state is
    // incomplete, so the job did not "succeed" in the §3 sense.
    let final_outcome = if let Some(message) = kafka_publish_error {
        Err(EngineError::Output(format!("kafka publish: {message}")))
    } else {
        outcome
    };

    let event = build_terminal_event(&pool, job_uuid, final_outcome).await;

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

/// Pre-flight rejection helper. Writes `mark_failed` and emits the
/// terminal Failed event with the supplied code + message. Used
/// for the kafka admission shortcuts that fail before the executor
/// runs.
async fn terminate_with_failure(
    pool: &sqlx::PgPool,
    job_uuid: Uuid,
    code: &str,
    message: &str,
    response_tx: &mpsc::UnboundedSender<Result<RunJobResponse, Status>>,
) {
    if let Err(db_err) = db_jobs::mark_failed(pool, job_uuid, message).await {
        warn!(
            job_id = %job_uuid,
            error = %db_err,
            "mark_failed failed at pre-flight rejection; gRPC terminal event still sent",
        );
    }
    warn!(
        job_id = %job_uuid,
        code = code,
        error = message,
        "RunJob rejected at pre-flight",
    );
    let _ = response_tx.send(Ok(RunJobResponse {
        event: Some(run_job_response::Event::Failed(Failed {
            error_code: code.to_owned(),
            error_message: message.to_owned(),
        })),
    }));
}

/// Output sink that forwards each row as a `serde_json::Value` on an
/// unbounded mpsc channel. The drainer task in `run_job` is
/// responsible for serialising the row into JSON for the gRPC
/// `Row.json_line` payload and for the per-sink side-effects
/// (appending to `job_rows` for stdout, publishing to Kafka for
/// kafka — ADR-0023 §2 / §3).
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
