"""SeleniumBase driver adapter — entry point.

Resolves a TCP port from the ``SPECTRE_ADAPTER_GRPC_PORT`` env var
(ADR-0021 §4 — production default 9092 reserved for SeleniumBase,
harness allocates a free port at test time), starts the gRPC service
on ``0.0.0.0:<port>``, registers the gRPC standard health check
(ADR-0021 §6), and shuts down cleanly on SIGTERM or SIGINT. R2.2
retired the prior Unix-domain-socket transport; readiness is now
signalled by ``Health.Check`` returning ``SERVING``. The wire-level
driver protocol contract is unchanged — see ADR-0008 for the original
handshake design and ADR-0022 for the TCP transport contract.
"""

from __future__ import annotations

import os
import signal
import sys
import threading
from concurrent import futures
from typing import Any

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from spectre.driver.v1alpha1 import driver_pb2_grpc

from spectre_seleniumbase import PROTOCOL_VERSION, __version__
from spectre_seleniumbase.server import DriverServicer, _default_driver_factory
from spectre_seleniumbase.sessions import SessionManager

SHUTDOWN_DEADLINE_S = 5.0
MAX_WORKERS = 4

PORT_ENV_VAR = "SPECTRE_ADAPTER_GRPC_PORT"


def identity() -> str:
    """Return the adapter's build identity string."""
    return f"spectre-seleniumbase {__version__} (driver protocol {PROTOCOL_VERSION})"


def resolve_port(env: dict[str, str]) -> int:
    """Resolve the bind port from ``SPECTRE_ADAPTER_GRPC_PORT``.

    The env var is required and must parse to an integer in the
    valid TCP port range. The conformance harness sets it to a
    free ephemeral port at start time; production deployments use
    the canonical 9092 reserved by ADR-0021 §4.
    """
    raw = env.get(PORT_ENV_VAR, "")
    if not raw:
        raise SystemExit(
            f"{PORT_ENV_VAR} is required: set it to the TCP port the adapter should bind"
        )
    try:
        port = int(raw)
    except ValueError as err:
        raise SystemExit(f"{PORT_ENV_VAR} must be a port number, got {raw!r}") from err
    if not 0 <= port <= 65535:
        raise SystemExit(f"{PORT_ENV_VAR} must be between 0 and 65535, got {port}")
    return port


def _create_server(
    sessions: SessionManager,
    port: int,
) -> grpc.Server:
    """Build a gRPC server bound to ``0.0.0.0:<port>``.

    Registers the v1alpha1 ``Driver`` service and the gRPC standard
    health check. The health servicer reports ``SERVING`` for the
    overall service ('') from process startup; the conformance
    harness polls until that response arrives, and production
    health probes consume the same endpoint.
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    driver_pb2_grpc.add_DriverServicer_to_server(DriverServicer(sessions), server)

    health_servicer = health.HealthServicer()
    health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    server.add_insecure_port(f"0.0.0.0:{port}")
    return server


def serve(
    port: int,
    factory: Any | None = None,
) -> None:
    """Start the gRPC server, register health, and serve until SIGTERM/SIGINT.

    ``factory`` is the WebDriver factory the session manager calls
    lazily on first ``Navigate``. The default starts a SeleniumBase
    Chrome driver in headless mode; tests inject a fake.
    """
    sessions = SessionManager(factory=factory or _default_driver_factory)
    server = _create_server(sessions, port)
    server.start()

    sys.stderr.write(f"{identity()} listening on 0.0.0.0:{port}\n")
    sys.stderr.flush()

    stop_event = threading.Event()

    def _shutdown(signum: int, _frame: Any) -> None:
        if stop_event.is_set():
            return
        stop_event.set()
        sys.stderr.write(f"received signal {signum}, shutting down\n")
        sys.stderr.flush()

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)

    stop_event.wait()

    server.stop(SHUTDOWN_DEADLINE_S).wait()
    sessions.close_all()


def main(argv: list[str] | None = None) -> None:
    """CLI entry point. Resolves the port and serves.

    ``argv`` is accepted but ignored — the adapter reads its bind
    port from the environment, not from CLI flags. The parameter is
    retained so existing entry-point wrappers and tests do not need
    a coupled signature change.
    """
    del argv
    port = resolve_port(dict(os.environ))
    serve(port)


if __name__ == "__main__":
    main()
