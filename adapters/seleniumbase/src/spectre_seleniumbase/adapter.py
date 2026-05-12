"""SeleniumBase driver adapter — entry point.

Resolves a TCP port from the ``SPECTRE_ADAPTER_GRPC_PORT`` env var
(ADR-0021 §4 / ADR-0025 §7 — production default 8092), a Redis URL from
``SPECTRE_REDIS_URL`` (ADR-0023 §4 + §6), and (optionally — for
the conformance suite only) ``SPECTRE_ADAPTER_INSTANCE_ID``
(ADR-0023 §5 R4.3 addendum). PINGs Redis at startup and exits
non-zero on failure so ``docker compose
--depends_on.condition: service_healthy`` and equivalent Helm
readiness gates surface the dependency cleanly. Then starts the
gRPC service on ``0.0.0.0:<port>``, registers the gRPC standard
health check (ADR-0021 §6), and shuts down on SIGTERM / SIGINT.

The wire-level driver protocol contract is unchanged — see
ADR-0008 for the original handshake design, ADR-0022 for the TCP
transport contract, and ADR-0023 §4/§5 for the Redis keyspace and
restart-invalidation mechanism this adapter participates in.
"""

from __future__ import annotations

import os
import signal
import threading
import uuid
from concurrent import futures
from typing import Any

import grpc
import structlog
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from opentelemetry.instrumentation.grpc import server_interceptor
from prometheus_client import CollectorRegistry, start_http_server
from spectre.driver.v1alpha1 import driver_pb2_grpc

from spectre_seleniumbase import PROTOCOL_VERSION, __version__
from spectre_seleniumbase.logging import configure as configure_logging
from spectre_seleniumbase.redis_client import RedisClient
from spectre_seleniumbase.server import DriverServicer, _default_driver_factory
from spectre_seleniumbase.sessions import SessionManager
from spectre_seleniumbase import tls
from spectre_seleniumbase.telemetry import init as init_telemetry
from spectre_seleniumbase.telemetry import register_metrics

SHUTDOWN_DEADLINE_S = 5.0
MAX_WORKERS = 4

PORT_ENV_VAR = "SPECTRE_ADAPTER_GRPC_PORT"
REDIS_URL_ENV_VAR = "SPECTRE_REDIS_URL"
INSTANCE_ID_ENV_VAR = "SPECTRE_ADAPTER_INSTANCE_ID"
METRICS_PORT_ENV_VAR = "SPECTRE_METRICS_PORT"

# Local-dev default; ADR-0023 §9 (Compose) and ``.env.example``
# both surface this URL. Production deployments must set the env
# var explicitly.
DEFAULT_REDIS_URL = "redis://127.0.0.1:6379/0"

# ADR-0031 §3.3 uniform metrics port honoured when
# ``SPECTRE_METRICS_PORT`` is unset (local-dev convenience).
DEFAULT_METRICS_PORT = 9090


def identity() -> str:
    """Return the adapter's build identity string."""
    return f"spectre-seleniumbase {__version__} (driver protocol {PROTOCOL_VERSION})"


def resolve_port(env: dict[str, str]) -> int:
    """Resolve the bind port from ``SPECTRE_ADAPTER_GRPC_PORT``.

    The env var is required and must parse to an integer in the
    valid TCP port range. The conformance harness sets it to a
    free ephemeral port at start time; production deployments use
    the canonical 8092 reserved by ADR-0021 §4 (ADR-0025 §7).
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


def resolve_redis_url(env: dict[str, str]) -> str:
    """Resolve the Redis URL from ``SPECTRE_REDIS_URL``.

    Defaults to ``redis://127.0.0.1:6379/0`` when unset — a local-
    dev convenience. Production must set the var explicitly so a
    misconfiguration surfaces at deploy time rather than dialing
    a non-existent localhost endpoint.
    """
    raw = env.get(REDIS_URL_ENV_VAR, "")
    if not raw:
        return DEFAULT_REDIS_URL
    return raw


def resolve_metrics_port(env: dict[str, str]) -> int:
    """Resolve the Prometheus ``/metrics`` sidecar bind port.

    Defaults to 9090 per ADR-0031 §3.3 uniform port. The chart +
    Compose populate ``SPECTRE_METRICS_PORT`` from the
    ``observability.metricsPort`` value so production deployments
    can drift the port in one place.
    """
    raw = env.get(METRICS_PORT_ENV_VAR, "")
    if not raw:
        return DEFAULT_METRICS_PORT
    try:
        port = int(raw)
    except ValueError as err:
        raise SystemExit(
            f"{METRICS_PORT_ENV_VAR} must be a port number, got {raw!r}",
        ) from err
    if not 0 <= port <= 65535:
        raise SystemExit(f"{METRICS_PORT_ENV_VAR} must be between 0 and 65535, got {port}")
    return port


def resolve_instance_id(env: dict[str, str]) -> str:
    """Return the adapter's process-startup UUID.

    Honours ``SPECTRE_ADAPTER_INSTANCE_ID`` when set (test hook
    only — see ADR-0023 §5 R4.3 addendum and the phase prompt
    §4.1) and otherwise generates a fresh UUID per call. The
    generated value is the §5 restart-invalidation key: a Pod or
    container restart yields a new UUID; sessions written by the
    previous incarnation become foreign-instance and the next RPC
    against them returns gRPC ``UNAVAILABLE``.
    """
    raw = env.get(INSTANCE_ID_ENV_VAR, "")
    if raw:
        return raw
    return str(uuid.uuid4())


def _create_server(
    sessions: SessionManager,
    port: int,
    metrics: Any | None = None,
) -> grpc.Server:
    """Build a gRPC server bound to ``0.0.0.0:<port>``.

    Registers the v1alpha1 ``Driver`` service and the gRPC standard
    health check. The health servicer reports ``SERVING`` for the
    overall service ('') from process startup; the conformance
    harness polls until that response arrives, and production
    health probes consume the same endpoint.

    W3.2 Cluster B: the server installs
    ``opentelemetry.instrumentation.grpc.server_interceptor`` so
    every incoming RPC extracts the W3C ``traceparent`` from
    metadata and opens a server-kind span the per-RPC handlers
    inherit as parent. ``metrics`` is the ``AdapterMetrics`` handle
    the DriverServicer records ADR-0031 §5.3 instruments into;
    ``None`` disables emission (used by tests).
    """
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        interceptors=[server_interceptor()],  # type: ignore[no-untyped-call]
    )
    driver_pb2_grpc.add_DriverServicer_to_server(
        DriverServicer(sessions, metrics=metrics),
        server,
    )

    health_servicer = health.HealthServicer()
    health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    # W3.4 Cluster C: when SPECTRE_TLS_{CERT,KEY,CA}_PATH env vars
    # are configured, bind via add_secure_port with
    # require_client_auth=True (only the engine is an authorised
    # caller per ADR-0032 §4.2). Plaintext mode falls through to
    # add_insecure_port — the v1alpha1 dial path. Static load at
    # startup; rotation triggers Pod restart per ADR-0032 §5.1
    # (Python).
    tls_cfg = tls.detect_mode()
    tls_creds = tls.build_server_credentials(tls_cfg)
    if tls_creds is None:
        server.add_insecure_port(f"0.0.0.0:{port}")
    else:
        server.add_secure_port(f"0.0.0.0:{port}", tls_creds)
    structlog.get_logger().info("tls ready", tls_mode=tls_cfg.mode.value)
    return server


def serve(
    port: int,
    factory: Any | None = None,
    redis_url: str | None = None,
    instance_id: str | None = None,
) -> None:
    """Start the gRPC server, register health, and serve until SIGTERM/SIGINT.

    ``factory`` is the WebDriver factory the session manager calls
    lazily on first ``Navigate``. The default starts a SeleniumBase
    Chrome driver in headless mode; tests inject a fake.

    ``redis_url`` and ``instance_id`` default to env-var-driven
    values; tests override them directly. The Redis PING runs
    before the gRPC server binds so a missing Redis surfaces as a
    clean process exit rather than a partially-initialised
    server.
    """
    # W3.2 Cluster B: configure JSON stdout logging + OTel
    # foundation BEFORE any startup work so the redis-dial + sweep
    # messages emit in the canonical ADR-0031 §3.4 schema.
    configure_logging(__version__)
    log = structlog.get_logger()

    tracer_provider = init_telemetry(__version__)

    resolved_redis_url = redis_url or resolve_redis_url(dict(os.environ))
    resolved_instance_id = instance_id or resolve_instance_id(dict(os.environ))

    # Prometheus `/metrics` sidecar on `SPECTRE_METRICS_PORT`
    # (default 9090 per ADR-0031 §3.3). The metrics registry is
    # passed to the servicer so per-RPC handlers can record §5.3
    # instruments.
    metrics_registry = CollectorRegistry()
    metrics = register_metrics(metrics_registry)
    metrics_port = resolve_metrics_port(dict(os.environ))
    start_http_server(metrics_port, registry=metrics_registry)
    log.info("metrics sidecar listening", metrics_port=metrics_port)

    redis = RedisClient.from_url(resolved_redis_url)
    try:
        redis.ping()
    except Exception as exc:  # noqa: BLE001 — any redis failure → fail fast
        log.error("redis ping failed", redis_url=resolved_redis_url, error=str(exc))
        raise SystemExit(1) from exc

    log.info(
        "redis ready",
        redis_url=resolved_redis_url,
        adapter_instance_id=resolved_instance_id,
    )

    sessions = SessionManager(
        factory=factory or _default_driver_factory,
        redis=redis,
        instance_id=resolved_instance_id,
    )
    server = _create_server(sessions, port, metrics=metrics)
    server.start()

    log.info(
        "adapter listening",
        binary="spectre-seleniumbase",
        version=__version__,
        protocol=PROTOCOL_VERSION,
        grpc_port=port,
    )

    stop_event = threading.Event()

    def _shutdown(signum: int, _frame: Any) -> None:
        if stop_event.is_set():
            return
        stop_event.set()
        log.info("shutting down", signal=signum)

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)

    stop_event.wait()

    server.stop(SHUTDOWN_DEADLINE_S).wait()
    sessions.close_all()
    redis.disconnect()
    tracer_provider.shutdown()


def main(argv: list[str] | None = None) -> None:
    """CLI entry point. Resolves the port and serves.

    ``argv`` is accepted but ignored — the adapter reads its bind
    port and Redis URL from the environment, not from CLI flags.
    The parameter is retained so existing entry-point wrappers
    and tests do not need a coupled signature change.
    """
    del argv
    port = resolve_port(dict(os.environ))
    serve(port)


if __name__ == "__main__":
    main()
