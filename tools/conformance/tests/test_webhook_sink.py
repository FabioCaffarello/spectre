"""Webhook sink conformance test (R5.1 / ADR-0024 §4).

Validates the engine's ``OutputSink.Webhook`` path: jobs
targeting the webhook sink POST extracted rows to the
configured URL with the v1alpha1 header schema and per-row /
batched body framing.

Pattern (engine-level, not adapter-level):

  1. Compose stack must have Postgres running (skipif if not);
     no MinIO / Kafka dependency for this sink.
  2. Start an in-process aiohttp server on an ephemeral port
     that records every request received and replies 200.
  3. Spawn the Playwright adapter via ``DriverHarness``.
  4. Spawn the engine binary as a subprocess with
     ``SPECTRE_PLAYWRIGHT_ENDPOINT`` pointing at the adapter;
     no webhook-specific env required (ADR-0024 §5 — webhook
     has no engine-level state).
  5. Submit a ``RunJob`` over the engine's gRPC port with
     ``output_sink_kind="webhook"`` and ``url`` pointing at
     the in-process server.
  6. Drain the server's recorded requests; assert request
     count, headers (X-Spectre-Job-Id, X-Spectre-Driver,
     X-Spectre-Row-Count), and body framing.

Two scenarios:
  - Per-row: ``BatchSize=0`` → one HTTP request per row.
  - Batched: ``BatchSize=2`` → ceil(N/2) HTTP requests with
    multiple JSONL lines per body.

ADR references:
  - ADR-0024 §4 (per-row vs batched, retry policy, header schema)
  - ADR-0024 §5 (admission gating asymmetry)
"""

from __future__ import annotations

import asyncio
import os
import socket
import subprocess
import threading
import time
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import grpc
import pytest
from aiohttp import web
from grpc_health.v1 import health_pb2, health_pb2_grpc
from spectre.engine.v1alpha1 import engine_pb2, engine_pb2_grpc

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

REPO_ROOT = Path(__file__).resolve().parents[3]
PLAYWRIGHT_DIR = REPO_ROOT / "adapters" / "playwright"
PLAYWRIGHT_DIST = PLAYWRIGHT_DIR / "dist" / "index.js"
ENGINE_BIN = REPO_ROOT / "core" / "engine" / "target" / "release" / "spectre"

DEFAULT_POSTGRES_URL = "postgres://spectre:spectre_dev_password@localhost:5432/spectre"

ENGINE_READY_TIMEOUT_S = 30.0
PER_TEST_DEADLINE_S = 120.0


def _postgres_url() -> str:
    return os.environ.get("SPECTRE_POSTGRES_URL") or DEFAULT_POSTGRES_URL


def _postgres_reachable() -> bool:
    """Best-effort: is the configured Postgres available?"""
    url = _postgres_url()
    try:
        host_port = url.split("@", 1)[1].split("/", 1)[0]
        host, port_str = host_port.rsplit(":", 1)
        port = int(port_str)
    except (IndexError, ValueError):
        return False
    try:
        with socket.create_connection((host, port), timeout=2.0):
            return True
    except OSError:
        return False


def _allocate_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


def _wait_for_health(endpoint: str, timeout_s: float) -> None:
    """Poll ``grpc.health.v1.Health.Check`` until SERVING."""
    deadline = time.monotonic() + timeout_s
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            with grpc.insecure_channel(endpoint) as ch:
                stub = health_pb2_grpc.HealthStub(ch)
                response = stub.Check(health_pb2.HealthCheckRequest(service=""), timeout=1.0)
                if response.status == health_pb2.HealthCheckResponse.SERVING:
                    return
        except grpc.RpcError as err:
            last_error = err
        time.sleep(0.2)
    raise RuntimeError(
        f"engine at {endpoint} did not signal SERVING within {timeout_s:.0f}s "
        f"(last error: {last_error})"
    )


class WebhookReceiver:
    """In-process aiohttp server that records every POST and
    replies 200. Run on a background thread with its own asyncio
    event loop so the synchronous pytest test can drain results
    after the engine session finalises.
    """

    def __init__(self) -> None:
        self.requests: list[dict[str, Any]] = []
        self._port: int | None = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._runner: web.AppRunner | None = None
        self._thread: threading.Thread | None = None
        self._ready = threading.Event()

    @property
    def port(self) -> int:
        if self._port is None:
            raise RuntimeError("receiver not started")
        return self._port

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}/spectre"

    async def _handler(self, request: web.Request) -> web.Response:
        body = await request.read()
        self.requests.append(
            {
                "headers": {k: v for k, v in request.headers.items()},
                "body": body.decode("utf-8", errors="replace"),
            },
        )
        return web.Response(status=200, text="ok")

    def _run(self) -> None:
        loop = asyncio.new_event_loop()
        self._loop = loop
        asyncio.set_event_loop(loop)
        port = _allocate_free_port()
        self._port = port

        async def boot() -> None:
            app = web.Application()
            app.router.add_post("/spectre", self._handler)
            runner = web.AppRunner(app)
            await runner.setup()
            site = web.TCPSite(runner, "127.0.0.1", port)
            await site.start()
            self._runner = runner
            self._ready.set()

        loop.run_until_complete(boot())
        loop.run_forever()

    def start(self) -> None:
        thread = threading.Thread(target=self._run, daemon=True)
        thread.start()
        self._thread = thread
        if not self._ready.wait(timeout=10.0):
            raise RuntimeError("webhook receiver did not become ready within 10s")

    def stop(self) -> None:
        if self._loop is None or self._runner is None or self._thread is None:
            return

        async def shutdown() -> None:
            assert self._runner is not None
            await self._runner.cleanup()

        future = asyncio.run_coroutine_threadsafe(shutdown(), self._loop)
        try:
            future.result(timeout=5.0)
        except Exception:
            pass
        self._loop.call_soon_threadsafe(self._loop.stop)
        self._thread.join(timeout=5.0)


@pytest.fixture
def receiver() -> Iterator[WebhookReceiver]:
    rec = WebhookReceiver()
    rec.start()
    try:
        yield rec
    finally:
        rec.stop()


@pytest.fixture
def engine_for_webhook() -> Iterator[str]:
    """Spawn the engine + Playwright adapter wired to the
    receiver fixture's URL. The receiver itself is supplied per
    test so the URL changes (in-process server, ephemeral port).
    """
    if not PLAYWRIGHT_DIST.exists():
        pytest.skip(f"playwright adapter not built at {PLAYWRIGHT_DIST}; run `just pw-build` first")
    if not ENGINE_BIN.exists():
        pytest.skip(f"engine binary not built at {ENGINE_BIN}; run `just spectre-build` first")
    if not _postgres_reachable():
        pytest.skip(f"postgres not reachable at {_postgres_url()}; run `docker compose up -d`")

    pw = DriverHarness.from_driver_yaml(PLAYWRIGHT_DIR / "driver.yaml")
    pw.start()

    engine_port = _allocate_free_port()
    engine_endpoint = f"127.0.0.1:{engine_port}"
    env = {
        **os.environ,
        "SPECTRE_ENGINE_PORT": str(engine_port),
        "SPECTRE_POSTGRES_URL": _postgres_url(),
        "SPECTRE_PLAYWRIGHT_ENDPOINT": pw.endpoint,
        "RUST_LOG": os.environ.get("RUST_LOG", "spectre_engine=info,spectre=info"),
    }

    proc = subprocess.Popen(  # noqa: S603 — internal binary
        [str(ENGINE_BIN)],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )

    try:
        _wait_for_health(engine_endpoint, ENGINE_READY_TIMEOUT_S)
        yield engine_endpoint
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5.0)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=2.0)
        pw.stop()


def _build_dsl(local_http_server: LocalHttpServer) -> str:
    return (
        "spectre: v1alpha1\n"
        "driver: playwright\n"
        "steps:\n"
        f"  - navigate: {local_http_server.base_url}/elements\n"
        "  - extract:\n"
        "      selector: li.item\n"
        "      fields:\n"
        "        text: textContent\n"
        "output:\n"
        "  format: jsonl\n"
        "  path: ./out.jsonl\n"
    )


def _run_webhook_job(
    engine_endpoint: str,
    dsl: str,
    job_id: uuid.UUID,
    url: str,
    batch_size: int,
) -> tuple[int, int | None, tuple[str, str] | None]:
    rows_seen = 0
    completed_count: int | None = None
    failure_detail: tuple[str, str] | None = None

    with grpc.insecure_channel(engine_endpoint) as ch:
        stub = engine_pb2_grpc.EngineStub(ch)
        request = engine_pb2.RunJobRequest(
            job_dsl=dsl,
            job_id=str(job_id),
            output_sink_kind="webhook",
            webhook=engine_pb2.WebhookSinkConfig(
                url=url,
                method="POST",
                batch_size=batch_size,
            ),
        )
        for response in stub.RunJob(request, timeout=PER_TEST_DEADLINE_S - 10):
            event_kind = response.WhichOneof("event")
            if event_kind == "row":
                rows_seen += 1
            elif event_kind == "completed":
                completed_count = response.completed.rows_extracted
                break
            elif event_kind == "failed":
                failure_detail = (
                    response.failed.error_code,
                    response.failed.error_message,
                )
                break

    return rows_seen, completed_count, failure_detail


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_webhook_sink_per_row_post_with_header_schema(
    local_http_server: LocalHttpServer,
    engine_for_webhook: str,
    receiver: WebhookReceiver,
) -> None:
    """Engine POSTs one request per row with the v1alpha1 header schema."""
    dsl = _build_dsl(local_http_server)
    job_id = uuid.uuid4()

    rows_seen, completed_count, failure_detail = _run_webhook_job(
        engine_for_webhook, dsl, job_id, receiver.url, batch_size=0,
    )

    assert failure_detail is None, f"engine reported Failed: {failure_detail}"
    assert completed_count is not None, "engine stream ended without terminal event"
    assert rows_seen == 3, f"expected 3 rows from /elements li.item; engine streamed {rows_seen}"
    assert completed_count == rows_seen

    # The receiver records every request the engine sent.
    assert len(receiver.requests) == rows_seen, (
        f"expected {rows_seen} per-row POSTs, got {len(receiver.requests)}"
    )

    for idx, req in enumerate(receiver.requests):
        headers = {k.lower(): v for k, v in req["headers"].items()}
        assert headers.get("x-spectre-job-id") == str(job_id), (
            f"X-Spectre-Job-Id = {headers.get('x-spectre-job-id')!r}, want {job_id}"
        )
        assert headers.get("x-spectre-driver") == "playwright", (
            f"X-Spectre-Driver = {headers.get('x-spectre-driver')!r}, want playwright"
        )
        assert headers.get("x-spectre-row-count") == "1", (
            f"X-Spectre-Row-Count for per-row mode must be 1, got {headers.get('x-spectre-row-count')!r}"
        )
        # Per-row mode never sets x-spectre-batch-size.
        assert "x-spectre-batch-size" not in headers, (
            f"per-row mode must not set X-Spectre-Batch-Size; idx={idx} headers={headers}"
        )
        assert headers.get("user-agent", "").startswith("spectre-engine/"), (
            f"User-Agent = {headers.get('user-agent')!r}, want spectre-engine/<version>"
        )

        # Body is one JSONL line + trailing newline.
        body = req["body"]
        assert body.endswith("\n"), f"body must end with newline, got {body!r}"
        assert body.count("\n") == 1, f"per-row body should have exactly 1 newline, got {body!r}"


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_webhook_sink_batches_rows_when_batch_size_set(
    local_http_server: LocalHttpServer,
    engine_for_webhook: str,
    receiver: WebhookReceiver,
) -> None:
    """Engine POSTs ceil(N/batch_size) requests with batched bodies."""
    dsl = _build_dsl(local_http_server)
    job_id = uuid.uuid4()

    rows_seen, completed_count, failure_detail = _run_webhook_job(
        engine_for_webhook, dsl, job_id, receiver.url, batch_size=2,
    )

    assert failure_detail is None, f"engine reported Failed: {failure_detail}"
    assert completed_count == 3, f"expected 3 rows total, got Completed = {completed_count}"
    assert rows_seen == 3

    # Three rows + batch_size=2 → ceil(3/2) = 2 requests with body
    # row counts [2, 1].
    assert len(receiver.requests) == 2, (
        f"expected 2 batched POSTs for 3 rows / batch=2, got {len(receiver.requests)}"
    )
    counts = []
    for req in receiver.requests:
        headers_lower = {k.lower(): v for k, v in req["headers"].items()}
        counts.append(int(headers_lower.get("x-spectre-row-count", "0")))
    assert counts == [2, 1], f"row counts = {counts}, want [2, 1]"

    # Both requests carry x-spectre-batch-size=2.
    for req in receiver.requests:
        headers = {k.lower(): v for k, v in req["headers"].items()}
        assert headers.get("x-spectre-batch-size") == "2", (
            f"X-Spectre-Batch-Size = {headers.get('x-spectre-batch-size')!r}, want '2'"
        )

    # First request has 2 newline-separated rows.
    first_body = receiver.requests[0]["body"]
    assert first_body.count("\n") == 2, (
        f"first batched body should have 2 newlines, got {first_body!r}"
    )
