// SPDX-License-Identifier: Apache-2.0

//! W3.1 Cluster B end-to-end check: the engine's `Client` injects
//! the active `OTel` span's W3C `traceparent` into outgoing gRPC
//! metadata. Verified against an in-process fake `DriverServer`
//! that captures the metadata of every incoming RPC.
//!
//! No external dependency — the fake server binds an ephemeral
//! port and runs in the same process as the test.

#![cfg(test)]

use std::sync::{Arc, Mutex};
use std::time::Duration;

use opentelemetry::trace::{FutureExt as _, SpanKind, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context as OtelContext, global};
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::SdkTracerProvider;
use spectre_engine::client::Client;
use spectre_engine::proto::driver_server::{Driver, DriverServer};
use spectre_engine::proto::{
    CloseRequest, CloseResponse, ExtractRequest, ExtractResponse, InitializeRequest,
    InitializeResponse, NavigateRequest, NavigateResponse, QueryRequest, QueryResponse,
    ScreenshotRequest, ScreenshotResponse, SessionConfig,
};
use tokio::sync::oneshot;
use tonic::transport::Server;
use tonic::{Request, Response, Status};

#[derive(Default)]
struct FakeDriver {
    /// Captured metadata of the most recent incoming RPC.
    last_metadata: Arc<Mutex<Option<tonic::metadata::MetadataMap>>>,
}

#[tonic::async_trait]
impl Driver for FakeDriver {
    async fn initialize(
        &self,
        request: Request<InitializeRequest>,
    ) -> Result<Response<InitializeResponse>, Status> {
        *self.last_metadata.lock().unwrap() = Some(request.metadata().clone());
        Ok(Response::new(InitializeResponse {
            session_id: "fake-session".to_owned(),
            ..InitializeResponse::default()
        }))
    }
    async fn navigate(
        &self,
        request: Request<NavigateRequest>,
    ) -> Result<Response<NavigateResponse>, Status> {
        *self.last_metadata.lock().unwrap() = Some(request.metadata().clone());
        Ok(Response::new(NavigateResponse::default()))
    }
    async fn query(
        &self,
        request: Request<QueryRequest>,
    ) -> Result<Response<QueryResponse>, Status> {
        *self.last_metadata.lock().unwrap() = Some(request.metadata().clone());
        Ok(Response::new(QueryResponse::default()))
    }
    async fn extract(
        &self,
        request: Request<ExtractRequest>,
    ) -> Result<Response<ExtractResponse>, Status> {
        *self.last_metadata.lock().unwrap() = Some(request.metadata().clone());
        Ok(Response::new(ExtractResponse::default()))
    }
    async fn screenshot(
        &self,
        _request: Request<ScreenshotRequest>,
    ) -> Result<Response<ScreenshotResponse>, Status> {
        // Not exercised by the engine's Client surface; stub for
        // trait completeness.
        Err(Status::unimplemented("not used in trace propagation test"))
    }
    async fn close(
        &self,
        request: Request<CloseRequest>,
    ) -> Result<Response<CloseResponse>, Status> {
        *self.last_metadata.lock().unwrap() = Some(request.metadata().clone());
        Ok(Response::new(CloseResponse::default()))
    }
}

struct FakeServer {
    addr: String,
    captured: Arc<Mutex<Option<tonic::metadata::MetadataMap>>>,
    shutdown: Option<oneshot::Sender<()>>,
    join: Option<tokio::task::JoinHandle<()>>,
}

impl FakeServer {
    async fn spawn() -> Self {
        let captured: Arc<Mutex<Option<tonic::metadata::MetadataMap>>> = Arc::new(Mutex::new(None));
        let driver = FakeDriver {
            last_metadata: Arc::clone(&captured),
        };

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let incoming = tokio_stream::wrappers::TcpListenerStream::new(listener);

        let (tx, rx) = oneshot::channel::<()>();
        let join = tokio::spawn(async move {
            Server::builder()
                .add_service(DriverServer::new(driver))
                .serve_with_incoming_shutdown(incoming, async move {
                    let _ = rx.await;
                })
                .await
                .unwrap();
        });

        Self {
            addr: format!("http://{addr}"),
            captured,
            shutdown: Some(tx),
            join: Some(join),
        }
    }

    async fn shutdown(mut self) {
        if let Some(tx) = self.shutdown.take() {
            let _ = tx.send(());
        }
        if let Some(join) = self.join.take() {
            // Best-effort: give the server a tick to drain.
            let _ = tokio::time::timeout(Duration::from_secs(2), join).await;
        }
    }

    fn last_traceparent(&self) -> Option<String> {
        self.captured.lock().unwrap().as_ref().and_then(|md| {
            md.get("traceparent")
                .and_then(|v| v.to_str().ok().map(str::to_owned))
        })
    }
}

/// Install the W3C propagator + a real tracer provider so spans
/// generate valid IDs. The tracer has no exporter — spans complete
/// and drop without leaving the process, but their IDs propagate.
fn install_tracing() {
    global::set_text_map_propagator(TraceContextPropagator::new());
    let provider = SdkTracerProvider::builder()
        .with_resource(
            Resource::builder()
                .with_service_name("spectre-engine-test")
                .build(),
        )
        .build();
    global::set_tracer_provider(provider);
}

#[tokio::test]
async fn client_injects_traceparent_into_outgoing_metadata() {
    install_tracing();
    let server = FakeServer::spawn().await;

    // Build a synthetic parent span so the propagator has something
    // meaningful to inject (an unsampled span still produces a valid
    // traceparent for round-trip purposes).
    let tracer = global::tracer("trace-propagation-test");
    let parent_span = tracer
        .span_builder("test.parent")
        .with_kind(SpanKind::Internal)
        .start(&tracer);
    let parent_ctx = OtelContext::current_with_span(parent_span);
    let expected_trace_id = parent_ctx.span().span_context().trace_id().to_string();

    async {
        let client = Client::dial(&server.addr).await.expect("dial fake server");
        let _ = client
            .initialize(SessionConfig::default(), vec![])
            .await
            .expect("initialize ok");
    }
    .with_context(parent_ctx)
    .await;

    let header = server
        .last_traceparent()
        .expect("traceparent must be present in fake driver metadata");
    assert!(
        header.contains(&expected_trace_id),
        "traceparent {header:?} should contain parent trace id {expected_trace_id}",
    );

    server.shutdown().await;
}
