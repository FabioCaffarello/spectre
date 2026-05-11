"""W3.2 Cluster B tests for ``spectre_seleniumbase.logging``."""

from __future__ import annotations

import json
from collections.abc import Iterator

import pytest
import structlog
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider

from spectre_seleniumbase.logging import configure


@pytest.fixture
def configured_logger() -> Iterator[None]:
    """Configure structlog with the production processor chain.

    ``cache_logger_on_first_use=False`` in ``configure()`` makes
    repeated ``structlog.get_logger()`` calls observe the latest
    config — important across tests that share global structlog
    state.
    """
    configure("test-version")
    yield
    structlog.reset_defaults()


def test_emits_eleven_mandatory_fields_at_info(
    configured_logger: None,
    capsys: pytest.CaptureFixture[str],
) -> None:
    log = structlog.get_logger()
    log.info("hello", request_id="req-1", job_id="job-1")

    out = capsys.readouterr().out.strip()
    obj = json.loads(out)

    for k in (
        "timestamp",
        "level",
        "service",
        "service_version",
        "caller",
        "message",
        "trace_id",
        "span_id",
        "request_id",
        "job_id",
        "tenant_id",
    ):
        assert k in obj, f"missing {k!r} in {obj}"

    assert obj["level"] == "INFO"
    assert obj["service"] == "spectre-seleniumbase"
    assert obj["service_version"] == "test-version"
    assert obj["message"] == "hello"
    assert obj["job_id"] == "job-1"
    assert obj["request_id"] == "req-1"
    assert obj["tenant_id"] is None
    assert obj["trace_id"] is None
    assert obj["span_id"] is None


def test_populates_trace_id_from_active_span_context(
    configured_logger: None,
    capsys: pytest.CaptureFixture[str],
) -> None:
    """When a real OTel span is current, the JSON line carries its
    trace_id + span_id byte-for-byte."""
    provider = TracerProvider()
    trace.set_tracer_provider(provider)
    tracer = provider.get_tracer("test")

    with tracer.start_as_current_span("test.span"):
        span = trace.get_current_span()
        sc = span.get_span_context()
        expected_trace = format(sc.trace_id, "032x")
        expected_span = format(sc.span_id, "016x")

        log = structlog.get_logger()
        log.info("inside span")

    out = capsys.readouterr().out.strip().splitlines()[-1]
    obj = json.loads(out)
    assert obj["trace_id"] == expected_trace
    assert obj["span_id"] == expected_span


def test_caller_field_is_basename_only(
    configured_logger: None,
    capsys: pytest.CaptureFixture[str],
) -> None:
    log = structlog.get_logger()
    log.info("checkpoint")
    obj = json.loads(capsys.readouterr().out.strip())
    assert "/" not in obj["caller"]
    assert ":" in obj["caller"]
