"""SeleniumBase driver adapter — entry point.

Resolves a Unix domain socket path (CLI flag > env var > error),
starts the gRPC service, prints a single readiness line on stdout,
and shuts down cleanly on SIGTERM or SIGINT. See ADR-0008 for the
full lifecycle contract and ADR-0014 for the SeleniumBase-specific
decisions.
"""

from __future__ import annotations

import argparse
import contextlib
import os
import pathlib
import signal
import sys
import threading
from concurrent import futures
from typing import Any

import grpc
from spectre.driver.v1alpha1 import driver_pb2_grpc

from spectre_seleniumbase import PROTOCOL_VERSION, __version__
from spectre_seleniumbase.server import DriverServicer, _default_driver_factory
from spectre_seleniumbase.sessions import SessionManager

SHUTDOWN_DEADLINE_S = 5.0
MAX_WORKERS = 4


def identity() -> str:
    """Return the adapter's build identity string."""
    return f"spectre-seleniumbase {__version__} (driver protocol {PROTOCOL_VERSION})"


def resolve_socket_path(argv: list[str], env: dict[str, str]) -> pathlib.Path:
    """Resolve the socket path from argv (``--socket=...``) or env.

    CLI flag takes precedence; ``SPECTRE_DRIVER_SOCKET`` is the fallback.
    Both inputs accept absolute filesystem paths only — relative paths
    are rejected because macOS UDS paths must be ≤ 104 characters and
    the harness anchors short paths under ``/tmp`` (ADR-0008).
    """
    parser = argparse.ArgumentParser(
        prog="spectre-seleniumbase",
        description="Spectre SeleniumBase driver adapter (gRPC over UDS).",
        add_help=True,
    )
    parser.add_argument(
        "--socket",
        type=str,
        default=None,
        help="absolute path to the Unix domain socket the adapter binds to",
    )
    args = parser.parse_args(argv)
    candidate = args.socket or env.get("SPECTRE_DRIVER_SOCKET")
    if not candidate:
        raise SystemExit(
            "no socket path provided: pass --socket=<absolute-path> or set SPECTRE_DRIVER_SOCKET"
        )
    path = pathlib.Path(candidate)
    if not path.is_absolute():
        raise SystemExit(f"socket path must be absolute, got {candidate!r}")
    return path


def _create_server(
    sessions: SessionManager,
    socket_path: pathlib.Path,
) -> grpc.Server:
    """Build a gRPC server bound to the given UDS path."""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    driver_pb2_grpc.add_DriverServicer_to_server(DriverServicer(sessions), server)
    server.add_insecure_port(f"unix:{socket_path}")
    return server


def serve(
    socket_path: pathlib.Path,
    factory: Any | None = None,
) -> None:
    """Start the gRPC server, signal readiness, and serve until SIGTERM/SIGINT.

    ``factory`` is the WebDriver factory the session manager calls
    lazily on first ``Navigate``. The default starts a SeleniumBase
    Chrome driver in headless mode; tests inject a fake.
    """
    if socket_path.exists():
        socket_path.unlink()
    socket_path.parent.mkdir(parents=True, exist_ok=True)

    sessions = SessionManager(factory=factory or _default_driver_factory)
    server = _create_server(sessions, socket_path)
    server.start()

    # Readiness line on stdout, identity on stderr — matches the
    # Playwright adapter's contract (ADR-0008 §2).
    sys.stdout.write(f"ready unix:{socket_path}\n")
    sys.stdout.flush()
    sys.stderr.write(f"{identity()} listening on unix:{socket_path}\n")
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

    # Stop accepting new connections, drain in-flight RPCs up to the
    # deadline, then tear down browser sessions and unlink the socket.
    server.stop(SHUTDOWN_DEADLINE_S).wait()
    sessions.close_all()
    with contextlib.suppress(FileNotFoundError):
        socket_path.unlink()


def main(argv: list[str] | None = None) -> None:
    """CLI entry point. Parses argv, resolves the socket path, and serves."""
    if argv is None:
        argv = sys.argv[1:]
    socket_path = resolve_socket_path(argv, dict(os.environ))
    serve(socket_path)


if __name__ == "__main__":
    main()
