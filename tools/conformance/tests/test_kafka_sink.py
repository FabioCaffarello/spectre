"""Kafka sink conformance test (R4.4 / ADR-0023 §3 R4.4 addendum).

Validates the engine's `OutputSink.Kafka` path: jobs targeting
the kafka sink publish JSONL rows to the configured topic with
correct partition keys (job UUID) and headers (job_id, row_index,
driver, timestamp).

Pattern (engine-level, not adapter-level):

  1. Compose stack must have Kafka + Postgres running (skipif if
     not).
  2. Spawn the Playwright adapter via ``DriverHarness`` so the
     engine has a real upstream driver.
  3. Spawn the engine binary as a subprocess with
     ``SPECTRE_KAFKA_BROKERS`` and ``SPECTRE_PLAYWRIGHT_ENDPOINT``
     pointing at the running Kafka broker and the Playwright
     adapter's ephemeral port. Wait for the engine's gRPC health
     check to report SERVING.
  4. Submit a ``RunJob`` over the engine's gRPC port with
     ``output_sink_kind="kafka"`` and a unique test topic name.
     The DSL extracts three ``<li class="item">`` rows from the
     local HTTP fixture's ``/elements`` route.
  5. Subscribe to the topic with ``confluent_kafka.Consumer`` and
     drain messages.
  6. Assert: row count matches engine's ``Completed`` event,
     partition keys are the job UUID, headers contain
     job_id + row_index + driver + timestamp.

The test runs against Playwright only — the upstream driver is
incidental to the kafka-sink-being-exercised. SeleniumBase and
curl-impersonate's kafka behaviour is identical because the
engine sits between adapter and kafka; the kafka path is engine
behaviour, not driver-protocol capability.

ADR references:
  - ADR-0023 §3 (Kafka topic / partitioning / headers)
  - ADR-0023 §3 R4.4 addendum (KRaft + Console + admission gating)
  - ADR-0023 §6 (Kafka admission-gated)
"""

from __future__ import annotations

import os
import socket
import subprocess
import time
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

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

DEFAULT_KAFKA_BROKERS = "localhost:9092"
DEFAULT_POSTGRES_URL = "postgres://spectre:spectre_dev_password@localhost:5432/spectre"

ENGINE_READY_TIMEOUT_S = 30.0
PER_TEST_DEADLINE_S = 120.0
KAFKA_DRAIN_DEADLINE_S = 20.0


def _kafka_brokers() -> str:
    """The Kafka brokers address the conformance run dials.

    Honours ``SPECTRE_KAFKA_BROKERS`` so the test runs against the
    same Kafka the engine uses; falls back to the local-dev
    default (matches ``.env.example``).
    """
    return os.environ.get("SPECTRE_KAFKA_BROKERS") or DEFAULT_KAFKA_BROKERS


def _postgres_url() -> str:
    return os.environ.get("SPECTRE_POSTGRES_URL") or DEFAULT_POSTGRES_URL


def _kafka_reachable() -> bool:
    """Best-effort: is the configured Kafka broker available?"""
    from confluent_kafka.admin import AdminClient

    try:
        admin = AdminClient({"bootstrap.servers": _kafka_brokers(), "socket.timeout.ms": "2000"})
        admin.list_topics(timeout=2.0)
    except Exception:
        return False
    return True


def _postgres_reachable() -> bool:
    """Best-effort: is the configured Postgres available?"""
    # The engine dies at startup if Postgres is unreachable, so a
    # quick TCP probe is enough — we don't need a Postgres client.
    url = _postgres_url()
    # Trim postgres://user:pass@ prefix and /db suffix.
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
def engine_with_kafka(local_http_server: LocalHttpServer) -> Iterator[tuple[str, str]]:
    """Yield ``(engine_endpoint, kafka_brokers)`` for an engine+adapter pair.

    Skips when prerequisites are missing (Playwright build,
    engine binary, Kafka broker, Postgres). The engine is wired
    to the supplied LocalHttpServer-backed Playwright adapter and
    the local Kafka broker. Cleanup terminates both processes on
    test exit.
    """
    if not PLAYWRIGHT_DIST.exists():
        pytest.skip(f"playwright adapter not built at {PLAYWRIGHT_DIST}; run `just pw-build` first")
    if not ENGINE_BIN.exists():
        pytest.skip(f"engine binary not built at {ENGINE_BIN}; run `just spectre-build` first")
    if not _kafka_reachable():
        pytest.skip(f"kafka not reachable at {_kafka_brokers()}; run `docker compose up -d`")
    if not _postgres_reachable():
        pytest.skip(f"postgres not reachable at {_postgres_url()}; run `docker compose up -d`")

    # Adapter on its own ephemeral port, owned by DriverHarness.
    pw = DriverHarness.from_driver_yaml(PLAYWRIGHT_DIR / "driver.yaml")
    pw.start()

    engine_port = _allocate_free_port()
    engine_endpoint = f"127.0.0.1:{engine_port}"
    env = {
        **os.environ,
        "SPECTRE_ENGINE_PORT": str(engine_port),
        "SPECTRE_KAFKA_BROKERS": _kafka_brokers(),
        "SPECTRE_POSTGRES_URL": _postgres_url(),
        "SPECTRE_PLAYWRIGHT_ENDPOINT": pw.endpoint,
        # Keep stderr quiet but visible enough on test failure.
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
        yield (engine_endpoint, _kafka_brokers())
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5.0)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=2.0)
        pw.stop()


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_kafka_sink_publishes_rows_with_headers(
    local_http_server: LocalHttpServer,
    engine_with_kafka: tuple[str, str],
) -> None:
    """Engine writes one Kafka message per row with §3 headers."""
    from confluent_kafka import Consumer

    engine_endpoint, kafka_brokers = engine_with_kafka
    topic = f"spectre-conf-{uuid.uuid4()}"
    job_id = uuid.uuid4()
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
            output_sink_kind="kafka",
            kafka_topic=topic,
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

    # Drain the topic.
    consumer = Consumer(
        {
            "bootstrap.servers": kafka_brokers,
            "group.id": f"conf-kafka-sink-{uuid.uuid4()}",
            "auto.offset.reset": "earliest",
            "enable.auto.commit": False,
        }
    )
    consumer.subscribe([topic])
    # Typed as `list[Any]` because confluent-kafka's `Message` type
    # surfaces Optional / Union shapes for its accessors (key bytes
    # vs str, header values bytes vs str-or-None, partition int vs
    # None) that mypy cannot narrow through the assertions below.
    received: list[Any] = []
    deadline = time.monotonic() + KAFKA_DRAIN_DEADLINE_S
    try:
        while time.monotonic() < deadline and len(received) < rows_seen:
            msg = consumer.poll(timeout=1.0)
            if msg is None:
                continue
            if msg.error():
                continue
            received.append(msg)
    finally:
        consumer.close()

    assert len(received) == rows_seen, (
        f"expected {rows_seen} kafka messages, drained {len(received)}"
    )

    seen_row_indices: set[str] = set()
    partitions: set[int] = set()
    for msg in received:
        # Partition key: the job UUID.
        key_bytes = msg.key()
        assert key_bytes is not None, "message must carry a partition key"
        assert key_bytes.decode() == str(job_id), f"partition key {key_bytes!r} != job_id {job_id}"

        # Headers: job_id, row_index, driver, timestamp. confluent-kafka
        # returns ``[(str, bytes), ...]`` — string keys with bytes values.
        raw_headers = msg.headers() or []
        headers: dict[str, bytes] = {key: value for key, value in raw_headers if value is not None}
        assert headers.get("job_id") == str(job_id).encode(), (
            f"job_id header missing or wrong: {headers!r}"
        )
        assert "row_index" in headers, f"row_index header missing: {headers!r}"
        seen_row_indices.add(headers["row_index"].decode())
        assert headers.get("driver") == b"playwright", (
            f"driver header should be playwright, got {headers.get('driver')!r}"
        )
        assert "timestamp" in headers, f"timestamp header missing: {headers!r}"
        partitions.add(int(msg.partition()))

    # All rows for one job must land on the same partition (§3
    # R4.4 addendum — partition key is the job UUID).
    assert len(partitions) == 1, (
        f"all rows for one job must land on the same partition, got {partitions}"
    )

    # row_index headers must cover 0..rows_seen-1.
    expected = {str(i) for i in range(rows_seen)}
    assert seen_row_indices == expected, (
        f"row_index headers should be {expected}, got {seen_row_indices}"
    )
