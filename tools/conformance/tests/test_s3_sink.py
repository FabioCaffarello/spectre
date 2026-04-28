"""S3 sink conformance test (R5.1 / ADR-0024 §3).

Validates the engine's ``OutputSink.S3`` path: jobs targeting
the s3 sink upload JSONL row aggregates to the configured
bucket / key with correct content type and key templating.

Pattern (engine-level, not adapter-level):

  1. Compose stack must have MinIO + Postgres running (skipif
     if not).
  2. Spawn the Playwright adapter via ``DriverHarness`` so the
     engine has a real upstream driver.
  3. Spawn the engine binary as a subprocess with
     ``SPECTRE_S3_*`` and ``SPECTRE_PLAYWRIGHT_ENDPOINT``
     pointing at the running MinIO and the Playwright
     adapter's ephemeral port. Wait for the engine's gRPC
     health check to report SERVING.
  4. Submit a ``RunJob`` over the engine's gRPC port with
     ``output_sink_kind="s3"`` and a unique key containing
     ``{{.JobID}}`` (engine-side template substitution).
  5. Fetch the resulting object via boto3, parse JSONL,
     assert row count + content match the engine's
     ``Completed`` event.

The test runs against Playwright only — the upstream driver
is incidental to the s3-sink-being-exercised. SeleniumBase and
curl-impersonate's s3 behaviour is identical because the
engine sits between adapter and S3; the s3 path is engine
behaviour, not driver-protocol capability.

ADR references:
  - ADR-0024 §3 (S3 buffering, key templating, content type)
  - ADR-0024 §5 (admission gating asymmetry)
  - ADR-0023 §6 (S3 admission-gated; engine-level state)
"""

from __future__ import annotations

import os
import socket
import subprocess
import time
import uuid
from collections.abc import Iterator
from pathlib import Path

import grpc
import pytest
from grpc_health.v1 import health_pb2, health_pb2_grpc
from spectre.engine.v1alpha1 import engine_pb2, engine_pb2_grpc

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

REPO_ROOT = Path(__file__).resolve().parents[3]
PLAYWRIGHT_DIR = REPO_ROOT / "adapters" / "playwright"
PLAYWRIGHT_DIST = PLAYWRIGHT_DIR / "dist" / "index.js"
ENGINE_BIN = REPO_ROOT / "core" / "engine" / "target" / "release" / "spectre"

DEFAULT_S3_ENDPOINT = "http://localhost:9000"
DEFAULT_S3_ACCESS_KEY = "spectre_dev_access"
DEFAULT_S3_SECRET_KEY = "spectre_dev_secret_key"
DEFAULT_S3_REGION = "us-east-1"
DEFAULT_S3_BUCKET = "spectre-rows"
DEFAULT_POSTGRES_URL = "postgres://spectre:spectre_dev_password@localhost:5432/spectre"

ENGINE_READY_TIMEOUT_S = 30.0
PER_TEST_DEADLINE_S = 120.0


def _s3_endpoint() -> str:
    return os.environ.get("SPECTRE_S3_ENDPOINT") or DEFAULT_S3_ENDPOINT


def _s3_access_key() -> str:
    return os.environ.get("SPECTRE_S3_ACCESS_KEY_ID") or DEFAULT_S3_ACCESS_KEY


def _s3_secret_key() -> str:
    return os.environ.get("SPECTRE_S3_SECRET_ACCESS_KEY") or DEFAULT_S3_SECRET_KEY


def _s3_region() -> str:
    return os.environ.get("SPECTRE_S3_REGION") or DEFAULT_S3_REGION


def _postgres_url() -> str:
    return os.environ.get("SPECTRE_POSTGRES_URL") or DEFAULT_POSTGRES_URL


def _s3_reachable() -> bool:
    """Best-effort: is the configured MinIO / S3 endpoint available?"""
    import boto3
    from botocore.exceptions import EndpointConnectionError

    try:
        client = boto3.client(
            "s3",
            endpoint_url=_s3_endpoint(),
            aws_access_key_id=_s3_access_key(),
            aws_secret_access_key=_s3_secret_key(),
            region_name=_s3_region(),
        )
        client.head_bucket(Bucket=DEFAULT_S3_BUCKET)
    except EndpointConnectionError:
        return False
    except Exception:
        return False
    return True


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


@pytest.fixture
def engine_with_s3(local_http_server: LocalHttpServer) -> Iterator[str]:
    """Yield the engine endpoint for an engine+adapter pair wired
    to the Compose MinIO. Skips when prerequisites are missing.
    """
    if not PLAYWRIGHT_DIST.exists():
        pytest.skip(f"playwright adapter not built at {PLAYWRIGHT_DIST}; run `just pw-build` first")
    if not ENGINE_BIN.exists():
        pytest.skip(f"engine binary not built at {ENGINE_BIN}; run `just spectre-build` first")
    if not _s3_reachable():
        pytest.skip(
            f"s3 endpoint not reachable at {_s3_endpoint()} or bucket {DEFAULT_S3_BUCKET} "
            f"missing; run `docker compose up -d`",
        )
    if not _postgres_reachable():
        pytest.skip(f"postgres not reachable at {_postgres_url()}; run `docker compose up -d`")

    pw = DriverHarness.from_driver_yaml(PLAYWRIGHT_DIR / "driver.yaml")
    pw.start()

    engine_port = _allocate_free_port()
    engine_endpoint = f"127.0.0.1:{engine_port}"
    env = {
        **os.environ,
        "SPECTRE_ENGINE_PORT": str(engine_port),
        "SPECTRE_S3_ENDPOINT": _s3_endpoint(),
        "SPECTRE_S3_ACCESS_KEY_ID": _s3_access_key(),
        "SPECTRE_S3_SECRET_ACCESS_KEY": _s3_secret_key(),
        "SPECTRE_S3_REGION": _s3_region(),
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


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_s3_sink_uploads_jsonl_with_rendered_key(
    local_http_server: LocalHttpServer,
    engine_with_s3: str,
) -> None:
    """Engine writes one JSONL object per job at the rendered key."""
    import boto3

    engine_endpoint = engine_with_s3
    job_id = uuid.uuid4()
    key_template = f"conformance/{{{{.JobID}}}}/rows.jsonl"
    expected_key = f"conformance/{job_id}/rows.jsonl"
    job_dsl = (
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

    rows_seen = 0
    completed_count: int | None = None
    failure_detail: tuple[str, str] | None = None

    with grpc.insecure_channel(engine_endpoint) as ch:
        stub = engine_pb2_grpc.EngineStub(ch)
        request = engine_pb2.RunJobRequest(
            job_dsl=job_dsl,
            job_id=str(job_id),
            output_sink_kind="s3",
            s3=engine_pb2.S3SinkConfig(
                bucket=DEFAULT_S3_BUCKET,
                key=key_template,
                endpoint=_s3_endpoint(),
                region=_s3_region(),
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

    assert failure_detail is None, f"engine reported Failed: {failure_detail}"
    assert completed_count is not None, "engine stream ended without terminal event"
    assert rows_seen == 3, f"expected 3 rows from /elements li.item; engine streamed {rows_seen}"
    assert completed_count == rows_seen, (
        f"Completed.rows_extracted = {completed_count} disagrees with streamed rows = {rows_seen}"
    )

    # Fetch the uploaded object via boto3 and verify body + content type.
    s3 = boto3.client(
        "s3",
        endpoint_url=_s3_endpoint(),
        aws_access_key_id=_s3_access_key(),
        aws_secret_access_key=_s3_secret_key(),
        region_name=_s3_region(),
    )
    head = s3.head_object(Bucket=DEFAULT_S3_BUCKET, Key=expected_key)
    assert head["ContentType"] == "application/x-ndjson", (
        f"Content-Type = {head['ContentType']!r}, want application/x-ndjson"
    )

    body = s3.get_object(Bucket=DEFAULT_S3_BUCKET, Key=expected_key)["Body"].read()
    body_text = body.decode()
    lines = [line for line in body_text.split("\n") if line]
    assert len(lines) == rows_seen, (
        f"object body has {len(lines)} JSONL lines, want {rows_seen}"
    )

    # Each line should parse as JSON and contain a non-empty `text` field.
    import json

    for idx, line in enumerate(lines):
        row = json.loads(line)
        assert "text" in row, f"row {idx} missing 'text' field: {row}"
        assert row["text"].strip(), f"row {idx} text is empty: {row}"
