// SPDX-License-Identifier: Apache-2.0

//! Integration tests for `spectre_engine::webhook::WebhookClient`.
//!
//! Each test runs against an in-process `axum` server bound to an
//! ephemeral port — no Compose dependency, runs unconditionally
//! under `cargo test`. The engine crate already pulls hyper
//! transitively via tonic 0.13, so axum's added cost is small.
//!
//! Mirrors R4.4's `kafka_integration.rs` pattern but without the
//! `#[ignore]` gate: the test server lives in the test process,
//! so contributors don't need Docker for these tests to pass.
//!
//! ADR-0024 §4 records the per-row vs batched semantics, the
//! retry policy, and the header schema this suite verifies.

#![cfg(test)]

use std::net::SocketAddr;
use std::sync::Arc;
use std::sync::atomic::{AtomicU32, Ordering};

use axum::body::Bytes;
use axum::extract::State;
use axum::http::{HeaderMap, StatusCode};
use axum::routing::post;
use axum::{Router, response::IntoResponse};
use spectre_engine::webhook::{WebhookClient, WebhookConfig};
use tokio::sync::Mutex;

#[derive(Clone, Default)]
struct CapturedRequest {
    headers: HeaderMap,
    body: Vec<u8>,
}

/// Server fixture that collects every received request and lets a
/// test inspect them after the engine session finalises.
#[derive(Clone, Default)]
struct Captured {
    requests: Arc<Mutex<Vec<CapturedRequest>>>,
}

async fn capture_handler(
    State(state): State<Captured>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    let mut buf = state.requests.lock().await;
    buf.push(CapturedRequest {
        headers,
        body: body.to_vec(),
    });
    StatusCode::OK
}

/// Spin up an axum server on an ephemeral port. Returns the URL +
/// the captured-requests fixture and a shutdown sender. The
/// server runs until the shutdown sender drops or `send(())` is
/// called.
async fn spawn_server() -> (String, Captured, tokio::sync::oneshot::Sender<()>) {
    let captured = Captured::default();
    let app = Router::new()
        .route("/spectre", post(capture_handler))
        .with_state(captured.clone());

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.expect("bind");
    let addr: SocketAddr = listener.local_addr().expect("addr");
    let url = format!("http://{addr}/spectre");

    let (shutdown_tx, shutdown_rx) = tokio::sync::oneshot::channel::<()>();
    tokio::spawn(async move {
        let _ = axum::serve(listener, app)
            .with_graceful_shutdown(async {
                let _ = shutdown_rx.await;
            })
            .await;
    });

    // Tiny yield so the server is ready to accept the first
    // connection (tokio's TcpListener::bind is synchronous, but
    // axum::serve runs on a spawned task).
    tokio::task::yield_now().await;

    (url, captured, shutdown_tx)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn per_row_post_carries_header_schema() {
    let (url, captured, _shutdown) = spawn_server().await;

    let client = WebhookClient::new();
    let cfg = WebhookConfig::parse(&url, "POST", 0).expect("config");
    let mut sess = client
        .session(cfg, "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "playwright")
        .expect("session");

    sess.push_row(r#"{"row":1}"#.to_owned()).await.expect("push 1");
    sess.push_row(r#"{"row":2}"#.to_owned()).await.expect("push 2");
    sess.finalise().await.expect("finalise");

    let received = captured.requests.lock().await;
    assert_eq!(received.len(), 2, "expected 2 per-row POSTs");

    for (idx, req) in received.iter().enumerate() {
        // Headers carry the v1alpha1 schema.
        let job_id = req
            .headers
            .get("x-spectre-job-id")
            .map(|h| h.to_str().unwrap_or_default())
            .unwrap_or_default();
        assert_eq!(job_id, "3f2504e0-4f89-11d3-9a0c-0305e82c3301");

        let driver = req
            .headers
            .get("x-spectre-driver")
            .map(|h| h.to_str().unwrap_or_default())
            .unwrap_or_default();
        assert_eq!(driver, "playwright");

        let row_count = req
            .headers
            .get("x-spectre-row-count")
            .map(|h| h.to_str().unwrap_or_default())
            .unwrap_or_default();
        assert_eq!(row_count, "1", "per-row POST carries x-spectre-row-count=1");

        // Body is JSONL line + trailing newline.
        let body = std::str::from_utf8(&req.body).expect("utf-8 body");
        let expected_row = idx + 1;
        assert_eq!(body, format!(r#"{{"row":{expected_row}}}{}"#, "\n"));

        // Per-row mode: no x-spectre-batch-size header.
        assert!(
            req.headers.get("x-spectre-batch-size").is_none(),
            "per-row mode must not carry x-spectre-batch-size",
        );
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn batched_post_emits_ceil_n_over_batch_requests() {
    let (url, captured, _shutdown) = spawn_server().await;

    let client = WebhookClient::new();
    let cfg = WebhookConfig::parse(&url, "POST", 3).expect("config");
    let mut sess = client.session(cfg, "job-7", "playwright").expect("session");

    // Push 7 rows; expect ceil(7/3) = 3 requests with body row
    // counts [3, 3, 1].
    for i in 0..7 {
        sess.push_row(format!(r#"{{"i":{i}}}"#)).await.expect("push");
    }
    sess.finalise().await.expect("finalise");

    let received = captured.requests.lock().await;
    assert_eq!(received.len(), 3, "expected 3 batched POSTs for 7 rows / batch=3");

    let row_counts: Vec<&str> = received
        .iter()
        .map(|r| {
            r.headers
                .get("x-spectre-row-count")
                .map(|h| h.to_str().unwrap_or_default())
                .unwrap_or_default()
        })
        .collect();
    assert_eq!(row_counts, vec!["3", "3", "1"]);

    // x-spectre-batch-size is set on every request when batching.
    for r in received.iter() {
        let batch_size = r
            .headers
            .get("x-spectre-batch-size")
            .map(|h| h.to_str().unwrap_or_default())
            .unwrap_or_default();
        assert_eq!(batch_size, "3");
    }

    // First request body has 3 newline-separated rows.
    let first_body = std::str::from_utf8(&received[0].body).expect("utf-8");
    assert_eq!(first_body, r#"{"i":0}
{"i":1}
{"i":2}
"#);
}

#[derive(Clone)]
struct FlakyState {
    attempts: Arc<AtomicU32>,
    /// Number of 5xx responses to emit before flipping to 200.
    fail_count: u32,
}

async fn flaky_handler(
    State(state): State<FlakyState>,
    _headers: HeaderMap,
    _body: Bytes,
) -> impl IntoResponse {
    let n = state.attempts.fetch_add(1, Ordering::SeqCst);
    if n < state.fail_count {
        StatusCode::SERVICE_UNAVAILABLE
    } else {
        StatusCode::OK
    }
}

async fn spawn_flaky_server(
    fail_count: u32,
) -> (String, Arc<AtomicU32>, tokio::sync::oneshot::Sender<()>) {
    let attempts = Arc::new(AtomicU32::new(0));
    let state = FlakyState {
        attempts: Arc::clone(&attempts),
        fail_count,
    };
    let app = Router::new().route("/spectre", post(flaky_handler)).with_state(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.expect("bind");
    let addr: SocketAddr = listener.local_addr().expect("addr");
    let url = format!("http://{addr}/spectre");
    let (shutdown_tx, shutdown_rx) = tokio::sync::oneshot::channel::<()>();
    tokio::spawn(async move {
        let _ = axum::serve(listener, app)
            .with_graceful_shutdown(async {
                let _ = shutdown_rx.await;
            })
            .await;
    });
    tokio::task::yield_now().await;
    (url, attempts, shutdown_tx)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn retries_on_transient_5xx_then_succeeds() {
    // Server returns 503 twice then 200. The retry policy
    // (3 attempts, exp backoff) should succeed on attempt 3.
    let (url, attempts, _shutdown) = spawn_flaky_server(2).await;

    let client = WebhookClient::new();
    let cfg = WebhookConfig::parse(&url, "POST", 0).expect("config");
    let mut sess = client.session(cfg, "job-retry", "playwright").expect("session");

    sess.push_row(r#"{"row":1}"#.to_owned()).await.expect("push retried row");
    sess.finalise().await.expect("finalise");

    assert_eq!(attempts.load(Ordering::SeqCst), 3, "expected 2 retries before success");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn retries_exhaust_yields_post_failed() {
    // Server returns 503 forever. After 3 attempts the client
    // surfaces WebhookError::PostFailed.
    let (url, attempts, _shutdown) = spawn_flaky_server(u32::MAX).await;

    let client = WebhookClient::new();
    let cfg = WebhookConfig::parse(&url, "POST", 0).expect("config");
    let mut sess = client.session(cfg, "job-fail", "playwright").expect("session");

    let result = sess.push_row(r#"{"row":1}"#.to_owned()).await;
    let err = result.expect_err("must fail after retries exhaust");
    let msg = err.to_string();
    assert!(
        msg.contains("after 3 attempts"),
        "expected 'after 3 attempts' in error, got: {msg}",
    );
    assert!(msg.contains("503"), "expected status 503 in error, got: {msg}");
    assert_eq!(
        attempts.load(Ordering::SeqCst),
        3,
        "must have exhausted exactly 3 attempts",
    );
}

#[derive(Clone)]
struct FixedStatusState {
    status: StatusCode,
}

fn fixed_status_handler(
    State(state): State<FixedStatusState>,
    _headers: HeaderMap,
    _body: Bytes,
) -> impl IntoResponse {
    state.status
}

async fn spawn_fixed_status_server(
    status: StatusCode,
) -> (String, Arc<AtomicU32>, tokio::sync::oneshot::Sender<()>) {
    let attempts = Arc::new(AtomicU32::new(0));
    let attempts_for_handler = Arc::clone(&attempts);
    let state = FixedStatusState { status };
    let app = Router::new()
        .route(
            "/spectre",
            post(move |s: State<FixedStatusState>, h: HeaderMap, b: Bytes| {
                let attempts = Arc::clone(&attempts_for_handler);
                async move {
                    attempts.fetch_add(1, Ordering::SeqCst);
                    fixed_status_handler(s, h, b)
                }
            }),
        )
        .with_state(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.expect("bind");
    let addr: SocketAddr = listener.local_addr().expect("addr");
    let url = format!("http://{addr}/spectre");
    let (shutdown_tx, shutdown_rx) = tokio::sync::oneshot::channel::<()>();
    tokio::spawn(async move {
        let _ = axum::serve(listener, app)
            .with_graceful_shutdown(async {
                let _ = shutdown_rx.await;
            })
            .await;
    });
    tokio::task::yield_now().await;
    (url, attempts, shutdown_tx)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn fatal_4xx_does_not_retry() {
    // Server returns 401 Unauthorized. 4xx other than 429 is
    // fatal on the first attempt; the client surfaces
    // WebhookError::FatalStatus immediately.
    let (url, attempts, _shutdown) = spawn_fixed_status_server(StatusCode::UNAUTHORIZED).await;

    let client = WebhookClient::new();
    let cfg = WebhookConfig::parse(&url, "POST", 0).expect("config");
    let mut sess = client.session(cfg, "job-fatal", "playwright").expect("session");

    let result = sess.push_row(r#"{"row":1}"#.to_owned()).await;
    let err = result.expect_err("must fail fast on 4xx");
    let msg = err.to_string();
    assert!(msg.contains("401"), "expected 401 in error, got: {msg}");
    assert!(
        msg.contains("fatal"),
        "expected 'fatal' in error, got: {msg}",
    );
    assert_eq!(
        attempts.load(Ordering::SeqCst),
        1,
        "must not retry on fatal 4xx",
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn put_method_routes_to_put_handler() {
    use axum::routing::put;
    // Custom server that only accepts PUT — exercises the
    // method dispatch.
    let captured = Captured::default();
    let app = Router::new()
        .route("/spectre", put(capture_handler))
        .with_state(captured.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.expect("bind");
    let addr: SocketAddr = listener.local_addr().expect("addr");
    let url = format!("http://{addr}/spectre");
    let (_shutdown_tx, shutdown_rx) = tokio::sync::oneshot::channel::<()>();
    tokio::spawn(async move {
        let _ = axum::serve(listener, app)
            .with_graceful_shutdown(async {
                let _ = shutdown_rx.await;
            })
            .await;
    });
    tokio::task::yield_now().await;

    let client = WebhookClient::new();
    let cfg = WebhookConfig::parse(&url, "PUT", 0).expect("config");
    let mut sess = client.session(cfg, "job-put", "playwright").expect("session");
    sess.push_row(r#"{"row":1}"#.to_owned()).await.expect("push");
    sess.finalise().await.expect("finalise");

    let received = captured.requests.lock().await;
    assert_eq!(received.len(), 1, "expected 1 PUT request");
}
