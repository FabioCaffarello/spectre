"""W3.2 Cluster B tests for ``spectre_seleniumbase.telemetry``."""

from __future__ import annotations

import pytest
from prometheus_client import CollectorRegistry
from prometheus_client.exposition import generate_latest

from spectre_seleniumbase.telemetry import (
    KIND,
    SERVICE_NAME,
    TRACER_NAME,
    _strip_scheme,
    init,
    register_metrics,
)


def test_init_succeeds_without_otlp_endpoint(monkeypatch: pytest.MonkeyPatch) -> None:
    """``Init`` without ``OTEL_EXPORTER_OTLP_ENDPOINT`` registers a
    tracer provider that drops spans silently — the same optional-
    exporter pattern as the engine + operator (ADR-0023 §6 alignment).
    """
    monkeypatch.delenv("OTEL_EXPORTER_OTLP_ENDPOINT", raising=False)
    provider = init("test-version")
    assert provider is not None
    provider.shutdown()


def test_strip_scheme_removes_known_prefixes() -> None:
    """``OTLPSpanExporter(endpoint=...)`` expects ``host:port``; the
    helper defangs the most common misconfiguration (URL prefix)."""
    assert _strip_scheme("http://collector:4317") == "collector:4317"
    assert _strip_scheme("https://collector:4317") == "collector:4317"
    assert _strip_scheme("collector:4317") == "collector:4317"
    assert _strip_scheme("") == ""


def test_register_metrics_stamps_kind_label() -> None:
    """Every ``spectre_adapter_*`` series carries ``kind="seleniumbase"``
    as a label (the const-label equivalent in ``prometheus_client``)."""
    registry = CollectorRegistry()
    metrics = register_metrics(registry)
    metrics.sessions_active.labels(kind="seleniumbase").inc()
    metrics.initialize_duration.labels(kind="seleniumbase").observe(0.01)
    metrics.navigate_duration.labels(kind="seleniumbase", result="success").observe(0.02)
    metrics.extract_duration.labels(kind="seleniumbase", result="failure").observe(0.03)
    metrics.capability_violations_total.labels(
        kind="seleniumbase",
        capability="screenshot_full_page",
    ).inc()

    output = generate_latest(registry).decode("utf-8")
    for needle in (
        'spectre_adapter_sessions_active{kind="seleniumbase"} 1.0',
        'spectre_adapter_initialize_duration_seconds_count{kind="seleniumbase"} 1.0',
        'spectre_adapter_navigate_duration_seconds_count{kind="seleniumbase",result="success"} 1.0',
        'spectre_adapter_extract_duration_seconds_count{kind="seleniumbase",result="failure"} 1.0',
        "spectre_adapter_capability_violations_total"
        '{capability="screenshot_full_page",kind="seleniumbase"} 1.0',
    ):
        assert needle in output, f"missing {needle!r} in:\n{output}"


def test_constants_match_canonical_values() -> None:
    """``KIND`` is snake_case per ADR-0031 §3.4. ``SERVICE_NAME``
    matches the chart's service-discovery convention.
    """
    assert KIND == "seleniumbase"
    assert SERVICE_NAME == "spectre-seleniumbase"
    assert TRACER_NAME == "spectre-seleniumbase"
