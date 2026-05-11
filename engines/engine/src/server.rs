// SPDX-License-Identifier: Apache-2.0

//! gRPC service implementation for `spectre.engine.v1alpha1.Engine`.
//!
//! The engine binary registers two services on a single TCP listener
//! (default `0.0.0.0:8090` — see [`bin/spectre.rs`](crate)):
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

use opentelemetry::trace::{FutureExt as _, SpanKind, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context as OtelContext, KeyValue, global};
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
    Completed, Failed, Row, RunJobRequest, RunJobResponse, S3SinkConfig, WebhookSinkConfig,
    run_job_response,
};
use crate::error::EngineError;
use crate::kafka::KafkaProducer;
use crate::output::OutputSink;
use crate::s3::S3Uploader;
use crate::telemetry::EngineMetrics;
use crate::telemetry::propagation;
use crate::webhook::WebhookClient;

/// `opentelemetry::global::tracer` name used by the engine's span
/// emissions per ADR-0031 §4.3.
const TRACER_NAME: &str = "spectre-engine";

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
    metrics: Arc<EngineMetrics>,
) -> EngineServer<EngineServiceImpl> {
    EngineServer::new(EngineServiceImpl::new(
        engine, db, kafka, s3, webhook, metrics,
    ))
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
    metrics: Arc<EngineMetrics>,
}

impl EngineServiceImpl {
    /// Construct a service implementation wrapping `engine`,
    /// holding a [`Database`] handle for ADR-0023 §2 persistence,
    /// the optional sink-level state ([`KafkaProducer`],
    /// [`S3Uploader`]) for ADR-0023 §3 + ADR-0024 §3, the
    /// always-present [`WebhookClient`] for ADR-0024 §4, and the
    /// shared [`EngineMetrics`] handle for ADR-0031 §5.1 recordings.
    #[must_use]
    pub fn new(
        engine: Engine,
        db: Database,
        kafka: Option<Arc<KafkaProducer>>,
        s3: Option<Arc<S3Uploader>>,
        webhook: Arc<WebhookClient>,
        metrics: Arc<EngineMetrics>,
    ) -> Self {
        Self {
            engine: Arc::new(engine),
            db,
            kafka,
            s3,
            webhook,
            metrics,
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
        // ADR-0031 §4.1: extract the W3C `traceparent` parent context
        // from incoming gRPC metadata. Missing / invalid headers yield
        // an empty context; the new span below becomes a fresh root.
        let parent_ctx = propagation::extract_parent(request.metadata());

        // Open the root span for this RunJob. ADR-0031 §4.2 places
        // `engine.run_job` directly below the caller's span (typically
        // `operator.reconcile_scrapejob`); §4.3 names it. SpanKind
        // `Server` follows OTel RPC semantic conventions for the
        // gRPC handler-side span.
        //
        // The async body wraps with `with_context(run_ctx)` instead of
        // holding a `_run_guard = ctx.attach()` across awaits — the
        // `ContextGuard` is `!Send`, which would violate tonic's
        // `Send`-bounded handler future.
        let tracer = global::tracer(TRACER_NAME);
        let run_span = tracer
            .span_builder("engine.run_job")
            .with_kind(SpanKind::Server)
            .start_with_context(&tracer, &parent_ctx);
        let run_ctx = OtelContext::current_with_span(run_span);
        let task_ctx = run_ctx.clone();
        self.run_job_inner(request, task_ctx)
            .with_context(run_ctx)
            .await
    }
}

impl EngineServiceImpl {
    /// Body of `Engine.run_job` extracted so the surrounding handler
    /// can wrap it in [`opentelemetry::trace::FutureExt::with_context`]
    /// — the `OTel` context guard is `!Send` and cannot be held across
    /// the awaits inside this body, so the wrapper attaches /
    /// detaches the context on every poll instead.
    async fn run_job_inner(
        &self,
        request: Request<RunJobRequest>,
        task_ctx: OtelContext,
    ) -> Result<Response<<Self as EngineService>::RunJobStream>, Status> {
        let tracer = global::tracer(TRACER_NAME);
        let RunJobRequest {
            job_dsl,
            job_id,
            output_sink_kind,
            kafka_topic,
            s3: s3_config,
            webhook: webhook_config,
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
        // has to dig out of the stream. Each step gets its own span
        // per ADR-0031 §4.3 (non-RPC operation naming pattern
        // `<service>.<operation>`).
        let job: Job = tracer
            .in_span("engine.parse_dsl", |_cx| Engine::parse_job(&job_dsl))
            .map_err(|e| Status::invalid_argument(format!("parse: {e}")))?;
        let plan = tracer.in_span("engine.generate_plan", |_cx| Engine::plan_job(&job));

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

        // Record `job_id` on the run span now that we know the UUID
        // (ADR-0031 §3.4 + §4.x: `job_id` is a cross-cutting attribute
        // surfaced on every span and event inside the job context).
        // `Context::current()` is the run_ctx the surrounding
        // `with_context` wrapper attached.
        OtelContext::current()
            .span()
            .set_attribute(KeyValue::new("job_id", job_uuid.to_string()));

        info!(
            job_id = %job_uuid,
            driver = %plan.driver,
            output_sink_kind = %output_sink_kind,
            "RunJob accepted",
        );

        let engine = Arc::clone(&self.engine);
        let pool = self.db.pool.clone();
        let kafka = self.kafka.clone();
        let s3 = self.s3.clone();
        let webhook = Arc::clone(&self.webhook);
        let metrics = Arc::clone(&self.metrics);
        let driver = plan.driver.clone();
        let (response_tx, response_rx) =
            mpsc::unbounded_channel::<Result<RunJobResponse, Status>>();

        // `spectre_engine_jobs_active` (ADR-0031 §5.1): increment now
        // that the job is past pre-flight (parse + plan + insert all
        // succeeded). `stream_run_job` always decrements on exit so
        // the gauge balances on every path including panics — the
        // executor task's panic surfaces as an `Internal` outcome,
        // not an early return.
        metrics.jobs_active.add(1, &[]);

        // Carry the run span's OTel context into the spawned task so
        // every child span (`engine.execute_plan`, adapter RPC spans)
        // inherits the run_job span as parent. `with_context` wraps
        // the spawned future so the context attaches / detaches on
        // each poll — no `!Send` guard across awaits.
        tokio::spawn(
            stream_run_job(StreamRunJobArgs {
                engine,
                pool,
                kafka,
                s3,
                webhook,
                metrics,
                plan,
                job_uuid,
                driver,
                output_sink_kind,
                kafka_topic,
                s3_config,
                webhook_config,
                response_tx,
            })
            .with_context(task_ctx),
        );

        Ok(Response::new(Box::pin(UnboundedReceiverStream::new(
            response_rx,
        ))))
    }
}

/// Argument bundle for [`stream_run_job`]. Replaces an
/// 11-argument function signature now that R5.1 has added S3 +
/// webhook plumbing alongside the existing Kafka path. ADR-0019 §5
/// R5.1 addendum records the analogous decision for the
/// reconciler-side `JobRunner.Run`.
struct StreamRunJobArgs {
    engine: Arc<Engine>,
    pool: sqlx::PgPool,
    kafka: Option<Arc<KafkaProducer>>,
    s3: Option<Arc<S3Uploader>>,
    webhook: Arc<WebhookClient>,
    metrics: Arc<EngineMetrics>,
    plan: crate::plan::Plan,
    job_uuid: Uuid,
    driver: String,
    output_sink_kind: String,
    kafka_topic: String,
    s3_config: Option<S3SinkConfig>,
    webhook_config: Option<WebhookSinkConfig>,
    response_tx: mpsc::UnboundedSender<Result<RunJobResponse, Status>>,
}

/// Drives one `RunJob` to completion: spawns the executor on a child
/// task, drains rows off the executor's channel (persisting and
/// publishing as the sink demands, forwarding to gRPC), then writes
/// the terminal `mark_completed` / `mark_failed` UPDATE and emits
/// the matching gRPC event.
#[allow(clippy::too_many_lines)]
async fn stream_run_job(args: StreamRunJobArgs) {
    let StreamRunJobArgs {
        engine,
        pool,
        kafka,
        s3,
        webhook,
        metrics,
        plan,
        job_uuid,
        driver,
        output_sink_kind,
        kafka_topic,
        s3_config,
        webhook_config,
        response_tx,
    } = args;

    // Pre-flight admission: short-circuit before touching the
    // executor when the sink is kafka/s3/webhook but the
    // engine-level state is missing (kafka, s3) or the per-job
    // fields are invalid (kafka topic, s3 bucket/key, webhook
    // URL). Yields the terminal Failed event, writes
    // `mark_failed`, returns.
    if output_sink_kind == "kafka" {
        if kafka.is_none() {
            terminate_with_failure(
                &pool,
                &metrics,
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
                &metrics,
                job_uuid,
                "KAFKA_TOPIC_REQUIRED",
                "kafka_topic is empty; set ScrapeJob.spec.outputSink.kafka.topic",
                &response_tx,
            )
            .await;
            return;
        }
    } else if output_sink_kind == "s3" {
        if s3.is_none() {
            terminate_with_failure(
                &pool,
                &metrics,
                job_uuid,
                "S3_UNAVAILABLE",
                "s3 uploader is not available; set SPECTRE_S3_ENDPOINT (or rely on \
                 the AWS default credential chain) and restart engine to enable \
                 OutputSink.S3 jobs",
                &response_tx,
            )
            .await;
            return;
        }
        let s3_cfg = s3_config.as_ref();
        let bucket = s3_cfg.map(|c| c.bucket.as_str()).unwrap_or_default();
        let key = s3_cfg.map(|c| c.key.as_str()).unwrap_or_default();
        if bucket.trim().is_empty() || key.trim().is_empty() {
            terminate_with_failure(
                &pool,
                &metrics,
                job_uuid,
                "S3_FIELD_REQUIRED",
                "s3 bucket and key must be non-empty; set \
                 ScrapeJob.spec.outputSink.s3.bucket and .key",
                &response_tx,
            )
            .await;
            return;
        }
    } else if output_sink_kind == "webhook" {
        let webhook_cfg = webhook_config.as_ref();
        let url = webhook_cfg.map(|c| c.url.as_str()).unwrap_or_default();
        if url.trim().is_empty() {
            terminate_with_failure(
                &pool,
                &metrics,
                job_uuid,
                "WEBHOOK_FIELD_REQUIRED",
                "webhook url is empty; set ScrapeJob.spec.outputSink.webhook.url",
                &response_tx,
            )
            .await;
            return;
        }
    }

    let (row_tx, mut row_rx) = mpsc::unbounded_channel::<serde_json::Value>();

    // The `engine.execute_plan` span (ADR-0031 §4.2) wraps the
    // executor's lifetime so child adapter RPC spans inherit it as
    // parent. The span enters the spawned task's context via
    // `with_context`; the drainer loop below stays under the
    // `engine.run_job` span (its caller-side parent) since the row
    // loop is not part of the executor's driver-loop work — it
    // serialises and dispatches rows that already left the executor.
    let tracer = global::tracer(TRACER_NAME);
    let execute_span = tracer
        .span_builder("engine.execute_plan")
        .with_kind(SpanKind::Internal)
        .start(&tracer);
    let executor_ctx = OtelContext::current_with_span(execute_span);

    // Executor task: owns the sink (and therefore `row_tx`), so the
    // channel closes when the executor returns and the drainer's
    // `recv()` returns `None`.
    let executor_metrics = Arc::clone(&metrics);
    let executor_handle = tokio::spawn(
        async move {
            let mut sink = ChannelSink::new(row_tx);
            engine
                .run_plan_with_sink(&plan, &mut sink, &executor_metrics)
                .await
        }
        .with_context(executor_ctx),
    );

    let persist_rows = output_sink_kind == "stdout";
    let publish_kafka = output_sink_kind == "kafka";
    let buffer_s3 = output_sink_kind == "s3";
    let publish_webhook = output_sink_kind == "webhook";
    let mut row_index: i64 = 0;
    // Tagged sink-error: (error_code, error_message). Any per-row
    // sink failure short-circuits the drain loop; the terminal
    // event maps the code into the gRPC `Failed` payload.
    let mut sink_publish_error: Option<(String, String)> = None;

    // S3 in-memory buffer (per ADR-0024 §3). One JSONL line per
    // row, single PutObject at end. Empty-result jobs upload an
    // empty object to preserve the presence-or-absence signal.
    let mut s3_buffer: Vec<u8> = Vec::new();

    // Per-job webhook session (per ADR-0024 §4). Only constructed
    // when the sink is webhook; an early validation error here
    // surfaces as WEBHOOK_FIELD_REQUIRED before the loop runs.
    let mut webhook_session = if publish_webhook {
        match webhook_session_for(&webhook, webhook_config.as_ref(), job_uuid, &driver) {
            Ok(sess) => Some(sess),
            Err((code, msg)) => {
                terminate_with_failure(&pool, &metrics, job_uuid, &code, &msg, &response_tx).await;
                return;
            }
        }
    } else {
        None
    };

    while let Some(value) = row_rx.recv().await {
        // ADR-0031 §4.2 lists a per-row `engine.assemble_row` span;
        // its OTel-direct form would need a `Context::attach()`
        // guard held across the awaits inside the loop body, which
        // is `!Send` and incompatible with the multi-thread tokio
        // scheduler. Cluster D installs the `tracing-opentelemetry`
        // bridge — at that point `tracing::info_span!` becomes an
        // OTel span, child of `engine.execute_plan` via tracing's
        // own span tree, no !Send guard required. Per-row span lands
        // with that bridge.
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

        if publish_kafka && sink_publish_error.is_none() {
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
                    sink_publish_error = Some(("KAFKA_PUBLISH_FAILED".to_owned(), e.to_string()));
                }
            }
        }

        if buffer_s3 && sink_publish_error.is_none() {
            // Append the row to the in-memory JSONL buffer. The
            // single PutObject runs after the executor finishes
            // — see end-of-function block. ADR-0024 §3.
            s3_buffer.extend_from_slice(json_line.as_bytes());
            s3_buffer.push(b'\n');
        }

        if publish_webhook && sink_publish_error.is_none() {
            if let Some(sess) = webhook_session.as_mut() {
                if let Err(e) = sess.push_row(json_line.clone()).await {
                    warn!(
                        job_id = %job_uuid,
                        row_index,
                        error = %e,
                        "webhook publish failed; aborting drain",
                    );
                    sink_publish_error = Some(("WEBHOOK_POST_FAILED".to_owned(), e.to_string()));
                }
            }
        }

        // Forward the row to the gRPC stream regardless of sink so
        // the control plane can mirror it into operator stdout for
        // `kubectl logs` (ADR-0019 §6). Stops forwarding after the
        // first sink error so the client sees the failure quickly.
        if sink_publish_error.is_none() {
            // `is_err()` (client dropped) is acknowledged but not
            // acted on: we keep draining so the executor doesn't
            // block and so remaining rows still persist to
            // `job_rows`.
            let _ = response_tx.send(Ok(RunJobResponse {
                event: Some(run_job_response::Event::Row(Row { json_line })),
            }));
            // `spectre_engine_rows_emitted_total{sink}` (ADR-0031
            // §5.1): one increment per row reaching the gRPC stream.
            // Tied to `is_none()` because a sink error short-circuits
            // forwarding — rows after that point are not "emitted".
            metrics
                .rows_emitted_total
                .add(1, &[KeyValue::new("sink", output_sink_kind.clone())]);
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

    // After the executor finishes successfully and no per-row sink
    // error fired, do the end-of-job sink work: S3 PutObject or
    // webhook session finalise. Surface failures the same way
    // per-row failures surface (override successful outcome with
    // `S3_UPLOAD_FAILED` / `WEBHOOK_POST_FAILED`).
    if sink_publish_error.is_none() && outcome.is_ok() {
        // Kafka publishes happen per-row inside the drain loop above;
        // by the time we get here every row's delivery future already
        // resolved Ok. Log a `kafka publish complete` event mirroring
        // s3's `s3 upload ok` so a green RunJob log line confirms the
        // sink-side work landed (production-smoke mini-phase 2026-05-07
        // — without this, a kafka ScrapeJob with zero published rows
        // looks identical to a kafka job with thirty published rows in
        // the engine log).
        if publish_kafka {
            info!(
                job_id = %job_uuid,
                topic = %kafka_topic,
                rows = row_index,
                "kafka publish complete",
            );
        }

        if buffer_s3 {
            if let (Some(uploader), Some(cfg)) = (s3.as_ref(), s3_config.as_ref()) {
                let key = crate::s3::render_key(&cfg.key, job_uuid);
                let endpoint_label = uploader.endpoint_label().to_owned();
                if let Err(e) = uploader
                    .upload_jsonl(&cfg.bucket, &key, s3_buffer.clone())
                    .await
                {
                    warn!(
                        job_id = %job_uuid,
                        bucket = %cfg.bucket,
                        key = %key,
                        endpoint = %endpoint_label,
                        error = %e,
                        "s3 upload failed",
                    );
                    sink_publish_error = Some(("S3_UPLOAD_FAILED".to_owned(), e.to_string()));
                } else {
                    info!(
                        job_id = %job_uuid,
                        bucket = %cfg.bucket,
                        key = %key,
                        bytes = s3_buffer.len(),
                        "s3 upload ok",
                    );
                }
            }
        }

        if publish_webhook {
            if let Some(sess) = webhook_session.as_mut() {
                if let Err(e) = sess.finalise().await {
                    warn!(
                        job_id = %job_uuid,
                        error = %e,
                        "webhook finalise failed",
                    );
                    sink_publish_error = Some(("WEBHOOK_POST_FAILED".to_owned(), e.to_string()));
                }
            }
        }
    }

    // Sink-publish failure overrides a successful executor outcome
    // — the executor finished but the destination state is
    // incomplete, so the job did not "succeed" in the §3 / §4 sense.
    let final_outcome = if let Some((code, message)) = sink_publish_error {
        Err(EngineError::SinkPublish { code, message })
    } else {
        outcome
    };

    // `spectre_engine_jobs_completed_total{result}` (ADR-0031 §5.1):
    // record the terminal result label *before* dispatching the gRPC
    // event so the gauge balance survives even when the response_tx
    // send fails (client closed the stream).
    let result_label = if final_outcome.is_ok() {
        "success"
    } else {
        "failure"
    };
    metrics
        .jobs_completed_total
        .add(1, &[KeyValue::new("result", result_label)]);
    metrics.jobs_active.add(-1, &[]);

    let event = build_terminal_event(&pool, job_uuid, final_outcome).await;

    if response_tx
        .send(Ok(RunJobResponse { event: Some(event) }))
        .is_err()
    {
        error!(job_id = %job_uuid, "client closed RunJob stream before terminal event");
    }
}

/// Build a per-job webhook session from the gRPC config message.
/// Returns the validated session on success, or a (code, message)
/// pair the dispatch path uses to call [`terminate_with_failure`].
fn webhook_session_for<'c>(
    client: &'c WebhookClient,
    cfg: Option<&WebhookSinkConfig>,
    job_uuid: Uuid,
    driver: &str,
) -> Result<crate::webhook::WebhookSession<'c>, (String, String)> {
    let Some(cfg) = cfg else {
        return Err((
            "WEBHOOK_FIELD_REQUIRED".to_owned(),
            "webhook config missing on RunJobRequest".to_owned(),
        ));
    };
    let parsed = crate::webhook::WebhookConfig::parse(&cfg.url, &cfg.method, cfg.batch_size)
        .map_err(|e| ("WEBHOOK_FIELD_REQUIRED".to_owned(), e.to_string()))?;
    client
        .session(parsed, &job_uuid.to_string(), driver)
        .map_err(|e| ("WEBHOOK_POST_FAILED".to_owned(), e.to_string()))
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
///
/// Records the matching `spectre_engine_jobs_active` decrement and
/// `spectre_engine_jobs_completed_total{result="failure"}` increment
/// per ADR-0031 §5.1 — every early-return path that bypassed the
/// main outcome handler still balances the gauge.
async fn terminate_with_failure(
    pool: &sqlx::PgPool,
    metrics: &EngineMetrics,
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
    metrics.jobs_active.add(-1, &[]);
    metrics
        .jobs_completed_total
        .add(1, &[KeyValue::new("result", "failure")]);
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
        EngineError::Job(_) => "JOB".to_owned(),
        EngineError::Plan(_) => "PLAN".to_owned(),
        EngineError::Transport(_) => "TRANSPORT".to_owned(),
        EngineError::Driver { .. } => "DRIVER".to_owned(),
        EngineError::CapabilityMissing { .. } => "CAPABILITY_MISSING".to_owned(),
        EngineError::UnknownDriver(_) => "UNKNOWN_DRIVER".to_owned(),
        EngineError::Output(_) => "OUTPUT".to_owned(),
        EngineError::Io(_) => "IO".to_owned(),
        EngineError::Internal(_) => "INTERNAL".to_owned(),
        // The sink-publish variant carries its own canonical code
        // (KAFKA_PUBLISH_FAILED / S3_UPLOAD_FAILED /
        // WEBHOOK_POST_FAILED). ADR-0023 §3 + ADR-0024 §3 / §4.
        EngineError::SinkPublish { code, .. } => code.clone(),
    }
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
            EngineError::SinkPublish {
                code: "KAFKA_PUBLISH_FAILED".into(),
                message: "broker unreachable".into(),
            },
        ];
        for c in cases {
            let code = error_code(c);
            assert!(!code.is_empty(), "missing code for {c:?}");
        }
    }

    #[test]
    fn sink_publish_error_code_is_canonical_string() {
        // Per ADR-0023 §3 + ADR-0024 §3 / §4, the SinkPublish
        // variant's `code` field IS the gRPC `Failed.error_code`
        // — error_code returns it verbatim.
        let s3 = EngineError::SinkPublish {
            code: "S3_UPLOAD_FAILED".into(),
            message: "AccessDenied".into(),
        };
        assert_eq!(error_code(&s3), "S3_UPLOAD_FAILED");
        let webhook = EngineError::SinkPublish {
            code: "WEBHOOK_POST_FAILED".into(),
            message: "503: bad gateway".into(),
        };
        assert_eq!(error_code(&webhook), "WEBHOOK_POST_FAILED");
    }
}
