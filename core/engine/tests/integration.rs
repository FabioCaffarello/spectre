// SPDX-License-Identifier: Apache-2.0

//! End-to-end integration test for the engine pipeline.
//!
//! Composes every layer from PR1-PR7:
//!
//! 1. A small `axum`-free `hyper` HTTP server bound to
//!    `127.0.0.1:<random>` serves a deterministic `/elements` page.
//!    Mirrors the Python fixture's
//!    `tools/conformance/src/spectre_conformance/http_fixture.py`.
//! 2. A Spectre DSL job navigates to that fixture's URL and extracts
//!    text + `data-test` attributes.
//! 3. The engine launches the Playwright adapter as a subprocess,
//!    dials it over UDS, runs the plan, and writes JSONL.
//! 4. We read the file back and assert: at least one row, each row
//!    has the expected fields, the values are non-empty.
//!
//! Marked `#[ignore]` so the default `cargo test` does not try to
//! build the Playwright adapter or launch Chromium. Run via
//! `cargo test -- --ignored` in a job that has the browser cache
//! populated. See ADR-0012 §"Bad, because" for the duplicated
//! fixture rationale and the gating rationale.

use std::convert::Infallible;
use std::net::{SocketAddr, TcpListener};
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{Context, Result};
use http_body_util::Full;
use hyper::body::Bytes;
use hyper::service::service_fn;
use hyper::{Request, Response, StatusCode};
use hyper_util::rt::{TokioExecutor, TokioIo};
use hyper_util::server::conn::auto::Builder as ServerBuilder;
use spectre_engine::Engine;
use tempfile::TempDir;

const ELEMENTS_HTML: &[u8] = b"<!doctype html><html><head><meta charset=\"utf-8\"><title>elements</title></head><body><h1 id=\"title\">Elements Page</h1><ul id=\"items\"><li class=\"item\">first</li><li class=\"item\">second</li><li class=\"item\">third</li></ul><a id=\"link\" href=\"https://example.com/target\">visit</a><div id=\"badge\" data-test=\"primary\">Primary</div></body></html>";

struct LocalServer {
    addr: SocketAddr,
    shutdown: tokio::sync::oneshot::Sender<()>,
    join: tokio::task::JoinHandle<()>,
}

impl LocalServer {
    #[allow(clippy::unused_async)]
    async fn spawn() -> Result<Self> {
        // Bind synchronously to grab a port, then hand the listener to
        // a Tokio acceptor.
        let listener = TcpListener::bind(("127.0.0.1", 0))?;
        listener.set_nonblocking(true)?;
        let addr = listener.local_addr()?;
        let listener = tokio::net::TcpListener::from_std(listener)?;

        let (tx, mut rx) = tokio::sync::oneshot::channel::<()>();

        let join = tokio::spawn(async move {
            let builder = Arc::new(ServerBuilder::new(TokioExecutor::new()));
            loop {
                tokio::select! {
                    _ = &mut rx => return,
                    res = listener.accept() => {
                        let Ok((stream, _peer)) = res else { continue };
                        let builder = builder.clone();
                        tokio::spawn(async move {
                            let svc = service_fn(handle);
                            let _ = builder.serve_connection(TokioIo::new(stream), svc).await;
                        });
                    }
                }
            }
        });

        Ok(Self {
            addr,
            shutdown: tx,
            join,
        })
    }

    fn base_url(&self) -> String {
        format!("http://{}", self.addr)
    }

    async fn stop(self) {
        let _ = self.shutdown.send(());
        let _ = self.join.await;
    }
}

async fn handle(req: Request<hyper::body::Incoming>) -> Result<Response<Full<Bytes>>, Infallible> {
    let path = req.uri().path();
    if path == "/elements" {
        return Ok(Response::builder()
            .status(StatusCode::OK)
            .header("content-type", "text/html; charset=utf-8")
            .body(Full::new(Bytes::from_static(ELEMENTS_HTML)))
            .unwrap());
    }
    Ok(Response::builder()
        .status(StatusCode::NOT_FOUND)
        .body(Full::new(Bytes::from_static(b"not found")))
        .unwrap())
}

fn playwright_available() -> bool {
    std::env::var("PLAYWRIGHT_AVAILABLE").is_ok_and(|v| !v.is_empty() && v != "0")
}

fn workspace_adapters_path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join("..")
        .join("adapters")
}

#[ignore = "requires PLAYWRIGHT_AVAILABLE=1 and a built Playwright adapter with Chromium"]
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn end_to_end_extracts_rows_against_local_fixture() -> Result<()> {
    if !playwright_available() {
        // The harness is set so a missing env var skips politely
        // without a hard failure. Rust's `#[ignore]` handles the
        // default skip; the env-var check defends against direct
        // `cargo test -- --ignored` invocations on dev machines
        // without Chromium.
        eprintln!("PLAYWRIGHT_AVAILABLE not set; skipping");
        return Ok(());
    }

    let server = LocalServer::spawn().await?;
    let url = format!("{}/elements", server.base_url());

    let tmp = TempDir::new()?;
    let job_path = tmp.path().join("job.yaml");
    let out_path = tmp.path().join("rows.jsonl");
    let yaml = format!(
        "spectre: v1alpha1\ndriver: playwright\nsteps:\n  - navigate: {url}\n  - extract:\n      selector: li.item\n      fields:\n        text: textContent\noutput:\n  format: jsonl\n  path: {}\n",
        out_path.display()
    );
    std::fs::write(&job_path, &yaml).context("write job.yaml")?;

    let adapters = workspace_adapters_path();
    let engine = Engine::new(Some(&adapters));
    let rows = engine.run_job(&yaml, tmp.path()).await?;
    server.stop().await;

    assert!(rows >= 3, "expected >= 3 rows, got {rows}");
    let contents = std::fs::read_to_string(&out_path)?;
    let mut count = 0usize;
    for line in contents.lines() {
        let v: serde_json::Value =
            serde_json::from_str(line).with_context(|| format!("parsing row: {line}"))?;
        let text = v
            .get("text")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        assert!(!text.is_empty(), "empty text in row: {line}");
        count += 1;
    }
    assert_eq!(count, rows);
    Ok(())
}
