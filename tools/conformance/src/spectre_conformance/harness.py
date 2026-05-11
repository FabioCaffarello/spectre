"""DriverHarness — start a driver subprocess and dial it over TCP gRPC.

Lifecycle:

1. ``start()`` allocates a free localhost TCP port via the standard
   "bind 0, read back" pattern, exports it to the driver subprocess
   as ``SPECTRE_ADAPTER_GRPC_PORT`` (ADR-0021 §4), launches the
   driver with no extra CLI flags, and polls
   ``grpc.health.v1.Health.Check`` until it responds ``SERVING``
   within ``ready_timeout_s`` seconds (ADR-0021 §6). On failure the
   harness raises with the captured stderr/stdout tail.
2. ``dial()`` returns a configured :class:`grpc.Channel` aimed at
   the driver's TCP endpoint. The channel sets
   ``grpc.default_authority=localhost`` so Node's ``http2`` server
   (the Playwright adapter) accepts the request — see ADR-0008 for
   the original rationale and ADR-0022 for the TCP-era contract.
3. ``stop()`` sends SIGTERM, waits up to ``shutdown_timeout_s``
   seconds, and falls back to SIGKILL.

The class is a context manager: ``with DriverHarness(...) as h``.

R2.2 retired the prior Unix-domain-socket transport. The
constructor signature is preserved (``DriverHarness(command=...,
cwd=...)``) so existing fixtures keep working; the
``from_driver_yaml`` constructor reads ``runtime.command`` from the
manifest now that the ``transports:`` block has been removed
(ADR-0022 §5).
"""

from __future__ import annotations

import contextlib
import os
import signal
import socket
import subprocess
import threading
from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path
from types import TracebackType
from typing import IO

import grpc
import yaml
from grpc_health.v1 import health_pb2, health_pb2_grpc

DEFAULT_READY_TIMEOUT_S: float = 10.0
DEFAULT_SHUTDOWN_TIMEOUT_S: float = 5.0
DIAGNOSTIC_TAIL_LINES = 50

# ADR-0008 §3 documents why the harness pins the default authority
# to ``localhost``: Node's HTTP/2 server requires ``:authority`` for
# every request, and Connect-Node's defaults derive from the bind
# host. ADR-0022 carries the constraint forward — the same Node
# adapter binds TCP now, but the requirement is unchanged.
DEFAULT_AUTHORITY = "localhost"

PORT_ENV_VAR = "SPECTRE_ADAPTER_GRPC_PORT"
INSTANCE_ID_ENV_VAR = "SPECTRE_ADAPTER_INSTANCE_ID"
# W3.2 Cluster E follow-up: the adapter's Prometheus `/metrics`
# sidecar (ADR-0031 §3.3) defaults to a fixed port 9090. The
# restart-invalidation conformance test spawns two adapter
# instances concurrently; both binding 9090 surfaces
# ``OSError: [Errno 98] Address already in use`` from the
# second instance. Harness allocates a per-instance ephemeral
# port and exports it so the adapters land on distinct
# sidecar ports.
METRICS_PORT_ENV_VAR = "SPECTRE_METRICS_PORT"


def _allocate_free_port() -> int:
    """Bind a kernel-assigned ephemeral port and return its number.

    The socket is closed before return; the kernel is unlikely to
    re-issue the same port immediately, but a sufficiently busy
    machine could race. The harness re-tries the bind in
    ``start()`` if the driver subprocess fails its first listen
    attempt — this helper alone does not retry.
    """

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        port: int = s.getsockname()[1]
        return port


@dataclass
class HarnessFailure(RuntimeError):
    """Raised when the driver fails to reach a usable state."""

    message: str
    stdout_tail: str = ""
    stderr_tail: str = ""

    def __str__(self) -> str:
        parts = [self.message]
        if self.stderr_tail:
            parts.append(f"--- driver stderr ---\n{self.stderr_tail.rstrip()}")
        if self.stdout_tail:
            parts.append(f"--- driver stdout ---\n{self.stdout_tail.rstrip()}")
        return "\n".join(parts)


@dataclass
class DriverHarness:
    """Manage a driver subprocess and a gRPC channel into it."""

    command: Sequence[str]
    cwd: Path | None = None
    extra_env: dict[str, str] = field(default_factory=dict)
    port: int = field(default_factory=_allocate_free_port)
    # W3.2 Cluster E follow-up: distinct sidecar port per harness
    # instance so two concurrent adapters don't fight over 9090.
    metrics_port: int = field(default_factory=_allocate_free_port)
    ready_timeout_s: float = DEFAULT_READY_TIMEOUT_S
    shutdown_timeout_s: float = DEFAULT_SHUTDOWN_TIMEOUT_S
    # R4.3 / ADR-0023 §5: setting ``instance_id_override`` exports
    # ``SPECTRE_ADAPTER_INSTANCE_ID`` into the subprocess so the
    # harness can drive the §5 restart-invalidation conformance
    # test (parallel adapter instances with distinct UUIDs). Leave
    # ``None`` for a fresh per-process UUID — the production
    # default and the right shape for every other test.
    instance_id_override: str | None = None

    _process: subprocess.Popen[str] | None = field(default=None, init=False, repr=False)
    _stdout_lines: list[str] = field(default_factory=list, init=False, repr=False)
    _stderr_lines: list[str] = field(default_factory=list, init=False, repr=False)
    _stdout_thread: threading.Thread | None = field(default=None, init=False, repr=False)
    _stderr_thread: threading.Thread | None = field(default=None, init=False, repr=False)
    _channel: grpc.Channel | None = field(default=None, init=False, repr=False)

    @classmethod
    def from_driver_yaml(
        cls,
        manifest_path: Path,
        **kwargs: object,
    ) -> DriverHarness:
        """Construct a harness from a ``driver.yaml`` file.

        Reads ``runtime.command`` from the manifest. The
        pre-R2.2 ``transports:`` block has been retired (ADR-0022
        §5); the spawn command lives in ``runtime.command`` until
        R6.2's Compose stack supersedes the harness-spawn flow
        entirely.

        ``cwd`` defaults to the manifest's directory so any
        relative paths inside the command (e.g.
        ``dist/index.js``) resolve correctly.
        """

        manifest = yaml.safe_load(manifest_path.read_text())
        runtime = manifest.get("runtime") or {}
        command = list(runtime.get("command") or [])
        if not command:
            raise ValueError(
                f"driver.yaml at {manifest_path} declares no runtime.command "
                "(R2.2 retired the transports: block; the spawn directive lives "
                "under runtime.command now)"
            )
        return cls(
            command=command,
            cwd=manifest_path.parent,
            **kwargs,  # type: ignore[arg-type]
        )

    # ------------------------------------------------------------------
    # Lifecycle

    def start(self) -> None:
        if self._process is not None:
            raise RuntimeError("DriverHarness.start() called twice")

        env = {
            **os.environ,
            **self.extra_env,
            PORT_ENV_VAR: str(self.port),
            METRICS_PORT_ENV_VAR: str(self.metrics_port),
        }
        if self.instance_id_override is not None:
            env[INSTANCE_ID_ENV_VAR] = self.instance_id_override

        self._process = subprocess.Popen(  # noqa: S603 - command is caller-supplied
            list(self.command),
            cwd=str(self.cwd) if self.cwd else None,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )

        assert self._process.stdout is not None
        assert self._process.stderr is not None
        self._stdout_thread = threading.Thread(
            target=self._pump,
            args=(self._process.stdout, self._stdout_lines),
            daemon=True,
        )
        self._stderr_thread = threading.Thread(
            target=self._pump,
            args=(self._process.stderr, self._stderr_lines),
            daemon=True,
        )
        self._stdout_thread.start()
        self._stderr_thread.start()

        self._wait_for_health_serving(self.ready_timeout_s)

    def stop(self) -> None:
        if self._channel is not None:
            self._channel.close()
            self._channel = None

        process = self._process
        if process is None:
            return

        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=self.shutdown_timeout_s)
            except subprocess.TimeoutExpired:
                process.kill()
                with contextlib.suppress(subprocess.TimeoutExpired):
                    process.wait(timeout=2.0)

        for thread in (self._stdout_thread, self._stderr_thread):
            if thread is not None:
                thread.join(timeout=1.0)

        self._process = None

    def __enter__(self) -> DriverHarness:
        try:
            self.start()
        except BaseException:
            self.stop()
            raise
        return self

    def __exit__(
        self,
        _exc_type: type[BaseException] | None,
        _exc: BaseException | None,
        _tb: TracebackType | None,
    ) -> None:
        self.stop()

    # ------------------------------------------------------------------
    # Channel

    def dial(self) -> grpc.Channel:
        if self._process is None:
            raise RuntimeError("dial() called before start()")
        if self._channel is None:
            self._channel = grpc.insecure_channel(
                f"127.0.0.1:{self.port}",
                options=[("grpc.default_authority", DEFAULT_AUTHORITY)],
            )
        return self._channel

    @property
    def stderr_text(self) -> str:
        return "".join(self._stderr_lines)

    @property
    def stdout_text(self) -> str:
        return "".join(self._stdout_lines)

    @property
    def endpoint(self) -> str:
        """Return the dial endpoint string (host:port)."""
        return f"127.0.0.1:{self.port}"

    # ------------------------------------------------------------------
    # Internals

    def _pump(self, stream: IO[str], sink: list[str]) -> None:
        for line in stream:
            sink.append(line)

    def _wait_for_health_serving(self, timeout_s: float) -> None:
        """Poll grpc.health.v1.Health.Check until SERVING or timeout.

        ADR-0021 §6 makes the health check the canonical readiness
        signal. The poll uses a fresh insecure channel — the
        production channel built by ``dial()`` is reserved for the
        test's own RPCs. The retry interval is intentionally simple
        (1 second); jitter and exponential back-off would only
        matter if startup were measured in tens of seconds.
        """

        deadline = self._monotonic() + timeout_s
        last_error: Exception | None = None
        retry_interval_s = 0.1

        with grpc.insecure_channel(
            self.endpoint,
            options=[("grpc.default_authority", DEFAULT_AUTHORITY)],
        ) as channel:
            stub = health_pb2_grpc.HealthStub(channel)
            request = health_pb2.HealthCheckRequest(service="")
            while self._monotonic() < deadline:
                if self._process is None or self._process.poll() is not None:
                    self._fail(
                        "driver subprocess exited before reporting health",
                    )
                try:
                    response = stub.Check(request, timeout=1.0)
                except grpc.RpcError as err:  # pragma: no cover - depends on race
                    last_error = err
                    self._sleep(retry_interval_s)
                    retry_interval_s = min(retry_interval_s * 2, 1.0)
                    continue
                if response.status == health_pb2.HealthCheckResponse.SERVING:
                    return
                # The driver is up but reports NOT_SERVING / UNKNOWN.
                # Wait and retry — the protocol allows transitions.
                self._sleep(retry_interval_s)

        self._fail(
            f"driver did not signal SERVING within {timeout_s:.0f}s"
            + (f" (last error: {last_error})" if last_error else "")
        )

    @staticmethod
    def _monotonic() -> float:
        # Indirected so tests can override timing without touching
        # the global ``time`` module.
        import time

        return time.monotonic()

    @staticmethod
    def _sleep(seconds: float) -> None:
        import time

        time.sleep(seconds)

    def _fail(self, message: str) -> None:
        stdout_tail = "".join(self._stdout_lines[-DIAGNOSTIC_TAIL_LINES:])
        stderr_tail = "".join(self._stderr_lines[-DIAGNOSTIC_TAIL_LINES:])
        self.stop()
        raise HarnessFailure(
            message=message,
            stdout_tail=stdout_tail,
            stderr_tail=stderr_tail,
        )
