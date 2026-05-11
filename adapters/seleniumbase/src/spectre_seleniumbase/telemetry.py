"""OpenTelemetry observability surface for the SeleniumBase adapter.

ADR-0031 §3.5 (Python SDK choice) + §5.3 (adapter metric taxonomy)
landed here as W3.2 Cluster B. Mirror of the curl-impersonate
`internal/telemetry/` package: tracer + metrics + shutdown
helpers. ``Init`` registers the global W3C TraceContext propagator
unconditionally so cross-service trace_ids propagate even when
the OTLP exporter is unconfigured (the same optional-exporter
pattern the engine + operator use — ADR-0023 §6 alignment).

Metrics use the ``prometheus_client`` library directly rather
than the OTel meter (parity with the curl-impersonate adapter
which uses ``prometheus/client_golang``). The HTTP ``/metrics``
endpoint is hosted by ``prometheus_client.start_http_server`` on
port 9090 by default — ADR-0031 §3.3 uniform port.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.propagate import set_global_textmap
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator
from prometheus_client import CollectorRegistry, Counter, Gauge, Histogram

# ServiceName is the canonical ``service.name`` resource attribute
# shared across resource emissions and the JSON log ``service``
# field (ADR-0031 §3.4 + §3.5).
SERVICE_NAME = "spectre-seleniumbase"

# TracerName is the global tracer slot the adapter's spans
# register against.
TRACER_NAME = "spectre-seleniumbase"

# Kind is the canonical ``{kind}`` label value the adapter stamps
# on every ``spectre_adapter_*`` metric per ADR-0031 §3.4
# (``lower_snake_case``).
KIND = "seleniumbase"

# Default histogram buckets aligned with the curl-impersonate
# adapter (seconds-scale; sub-millisecond → tens of seconds).
_DEFAULT_DURATION_BUCKETS = (
    0.005,
    0.01,
    0.025,
    0.05,
    0.1,
    0.25,
    0.5,
    1.0,
    2.5,
    5.0,
    10.0,
)


@dataclass(frozen=True)
class AdapterMetrics:
    """Holds the §5.3 instruments the DriverServicer records into.

    ``kind`` is applied once at registration via ``ConstLabels``-
    equivalent (passed through ``labelvalues`` partial). Every
    observation carries it without per-call construction noise.
    """

    sessions_active: Gauge
    initialize_duration: Histogram
    navigate_duration: Histogram
    extract_duration: Histogram
    capability_violations_total: Counter


def register_metrics(registry: CollectorRegistry) -> AdapterMetrics:
    """Construct the §5.3 instruments with ``kind=KIND`` const-labelled.

    The ``prometheus_client`` library does not have a true "const
    label" abstraction; the adapter pins ``kind`` at registration
    via ``labelnames=["kind"]`` and binds it on every observation
    through the returned wrappers below. Result label values on
    Navigate / Extract land via the ``labels(kind, result)`` call
    in the recorder.
    """
    sessions_active = Gauge(
        "spectre_adapter_sessions_active",
        "Active driver sessions held by the adapter.",
        labelnames=["kind"],
        registry=registry,
    )
    initialize_duration = Histogram(
        "spectre_adapter_initialize_duration_seconds",
        "Driver.Initialize RPC duration in seconds.",
        labelnames=["kind"],
        buckets=_DEFAULT_DURATION_BUCKETS,
        registry=registry,
    )
    navigate_duration = Histogram(
        "spectre_adapter_navigate_duration_seconds",
        "Driver.Navigate RPC duration in seconds.",
        labelnames=["kind", "result"],
        buckets=_DEFAULT_DURATION_BUCKETS,
        registry=registry,
    )
    extract_duration = Histogram(
        "spectre_adapter_extract_duration_seconds",
        "Driver.Extract RPC duration in seconds.",
        labelnames=["kind", "result"],
        buckets=_DEFAULT_DURATION_BUCKETS,
        registry=registry,
    )
    capability_violations_total = Counter(
        "spectre_adapter_capability_violations_total",
        "Initialize requests for capabilities not in the adapter manifest.",
        labelnames=["kind", "capability"],
        registry=registry,
    )
    # W3.2 Cluster E follow-up: ``prometheus_client.Gauge`` (and
    # the other ``*Vec``-flavoured types) only emit sample lines
    # for label combinations the adapter has actually observed.
    # Without an Initialize RPC the gauge would surface as
    # ``# HELP`` / ``# TYPE`` declarations only, no value line —
    # invisible to a steady-state scrape assertion. Touch the
    # ``{kind=KIND}`` child once at registration so the gauge
    # exports ``spectre_adapter_sessions_active{kind="seleniumbase"} 0``
    # from process start.
    sessions_active.labels(kind=KIND)

    return AdapterMetrics(
        sessions_active=sessions_active,
        initialize_duration=initialize_duration,
        navigate_duration=navigate_duration,
        extract_duration=extract_duration,
        capability_violations_total=capability_violations_total,
    )


def init(service_version: str) -> Any:
    """Register the W3C propagator + a tracer provider.

    Returns the tracer provider so the caller can ``defer`` a
    ``shutdown()`` on the returned handle. The OTLP/gRPC exporter
    attaches only when ``OTEL_EXPORTER_OTLP_ENDPOINT`` is non-
    empty — mirror of the operator's optional-exporter pattern
    (ADR-0023 §6 alignment). Without an endpoint, spans still
    generate valid IDs and propagate downstream via
    ``traceparent``; they are dropped at end-of-span rather than
    exported.
    """
    set_global_textmap(TraceContextTextMapPropagator())

    resource = Resource.create(
        {
            "service.name": SERVICE_NAME,
            "service.version": service_version,
            "service.namespace": "spectre",
        }
    )
    provider = TracerProvider(resource=resource)

    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "")
    if endpoint:
        exporter = OTLPSpanExporter(endpoint=_strip_scheme(endpoint), insecure=True)
        provider.add_span_processor(BatchSpanProcessor(exporter))

    trace.set_tracer_provider(provider)
    return provider


def _strip_scheme(endpoint: str) -> str:
    """Strip ``http://`` / ``https://`` prefix from ``endpoint``.

    ``OTLPSpanExporter(endpoint=...)`` expects ``host:port``, not
    a URL — passing a URL is the most common configuration
    mistake. Mirror of the curl-impersonate adapter's same helper.
    """
    for prefix in ("http://", "https://"):
        if endpoint.startswith(prefix):
            return endpoint[len(prefix) :]
    return endpoint
