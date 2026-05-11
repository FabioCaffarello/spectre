"""JSON-line stdout logger for the SeleniumBase adapter (ADR-0031 §3.4).

Built on ``structlog`` with a processor chain that emits the eleven
mandatory fields ADR-0031 §3.4 codifies. ``trace_id`` + ``span_id``
are read from the active OTel context at emission time via
``trace.get_current_span().get_span_context()`` — independent of
whether the surrounding span was created by
``opentelemetry-instrumentation-grpc``'s server interceptor or by
manual ``tracer.start_as_current_span(...)`` blocks.

Mirror of the curl-impersonate adapter's ``internal/logging``
package and the engine's ``src/telemetry/logs.rs`` JSON formatter
— identical field shape so cross-service log correlation works
out of the box.
"""

from __future__ import annotations

import logging as stdlib_logging
import sys
from collections.abc import MutableMapping
from typing import Any

import structlog
from opentelemetry import trace


def configure(service_version: str) -> None:
    """Configure ``structlog`` to emit one JSON line per event.

    Side effects:

    * Sets stdlib logging to write to ``sys.stdout`` (ADR-0031
      §3.4 mandates stdout; the legacy adapter emitted to
      stderr).
    * Replaces the global ``structlog`` configuration with a
      processor chain producing the eleven canonical fields.
    """
    stdlib_logging.basicConfig(
        format="%(message)s",
        stream=sys.stdout,
        level=stdlib_logging.INFO,
    )

    structlog.configure(
        processors=[
            _add_canonical_fields(service_version),
            _add_trace_context,
            structlog.processors.TimeStamper(
                fmt="%Y-%m-%dT%H:%M:%S.%fZ",
                utc=True,
                key="timestamp",
            ),
            structlog.processors.add_log_level,
            structlog.processors.CallsiteParameterAdder(
                parameters=[
                    structlog.processors.CallsiteParameter.PATHNAME,
                    structlog.processors.CallsiteParameter.LINENO,
                ],
            ),
            _normalise_caller_field,
            _normalise_level_field,
            _rename_event_to_message,
            structlog.processors.JSONRenderer(),
        ],
        wrapper_class=structlog.make_filtering_bound_logger(stdlib_logging.INFO),
        context_class=dict,
        logger_factory=structlog.PrintLoggerFactory(),
        cache_logger_on_first_use=False,
    )


def _rename_event_to_message(
    _logger: Any, _name: str, event_dict: MutableMapping[str, Any]
) -> MutableMapping[str, Any]:
    """structlog renders the positional ``msg`` argument as the
    ``event`` key by default. ADR-0031 §3.4 mandates ``message``
    — rename in the last-but-one processor so callers see the
    canonical field.
    """
    if "event" in event_dict:
        event_dict["message"] = event_dict.pop("event")
    return event_dict


def _add_canonical_fields(service_version: str) -> Any:
    """Processor that adds ``service`` + ``service_version`` +
    null-valued ``tenant_id`` / ``request_id`` / ``job_id`` to every
    event, then nulls out fields the caller did NOT supply so the
    eleven-field schema is uniform per ADR-0031 §3.4.

    ``request_id`` and ``job_id`` may be supplied per-event via
    ``logger.info("msg", request_id="…", job_id="…")``; the
    processor's job is to ensure the keys exist (with ``None``
    sentinel) so the JSON output shape is stable.
    """

    def processor(
        _logger: Any, _name: str, event_dict: MutableMapping[str, Any]
    ) -> MutableMapping[str, Any]:
        event_dict.setdefault("service", "spectre-seleniumbase")
        event_dict.setdefault("service_version", service_version)
        event_dict.setdefault("request_id", None)
        event_dict.setdefault("job_id", None)
        # ADR-0031 §3.4 — `tenant_id` is always emitted (null) for
        # forward-compat with multi-tenant deployments (v1beta1
        # scope).
        event_dict["tenant_id"] = None
        return event_dict

    return processor


def _add_trace_context(
    _logger: Any, _name: str, event_dict: MutableMapping[str, Any]
) -> MutableMapping[str, Any]:
    """Processor that injects ``trace_id`` + ``span_id`` from the
    active OTel context. With the W3C propagator + tracer provider
    registered (``telemetry.init``), the context is current
    whenever the event fires inside an RPC span (typically the
    server-kind span the gRPC instrumentation opens automatically).

    Falls back to ``None`` when no span is active (e.g. startup
    logs before the gRPC server binds).
    """
    span = trace.get_current_span()
    sc = span.get_span_context() if span else None
    if sc is not None and sc.is_valid:
        event_dict["trace_id"] = format(sc.trace_id, "032x")
        event_dict["span_id"] = format(sc.span_id, "016x")
    else:
        event_dict["trace_id"] = None
        event_dict["span_id"] = None
    return event_dict


def _normalise_caller_field(
    _logger: Any, _name: str, event_dict: MutableMapping[str, Any]
) -> MutableMapping[str, Any]:
    """Compress structlog's ``pathname`` / ``lineno`` pair into the
    single ``caller`` field ADR-0031 §3.4 mandates (``<file>:<line>``,
    basename only — matches the engine + curl-impersonate format).
    """
    pathname = event_dict.pop("pathname", None)
    lineno = event_dict.pop("lineno", None)
    if pathname is None or lineno is None:
        event_dict.setdefault("caller", None)
        return event_dict
    basename = pathname.rsplit("/", 1)[-1] if "/" in pathname else pathname
    event_dict["caller"] = f"{basename}:{lineno}"
    return event_dict


def _normalise_level_field(
    _logger: Any, _name: str, event_dict: MutableMapping[str, Any]
) -> MutableMapping[str, Any]:
    """Uppercase the level string for parity with the engine +
    curl-impersonate JSON schema (``INFO`` not ``info``).
    """
    if "level" in event_dict and isinstance(event_dict["level"], str):
        event_dict["level"] = event_dict["level"].upper()
    return event_dict
