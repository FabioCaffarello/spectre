"""DriverHarness — start a driver subprocess and dial it over gRPC.

Lifecycle:

1. ``start()`` chooses a per-instance Unix domain socket path under
   ``/tmp`` (short enough to fit macOS' 104-char UDS limit), unlinks
   any prior file at that path, launches the driver subprocess with
   the chosen path appended to argv as ``--socket=<path>`` and also
   exported as ``SPECTRE_DRIVER_SOCKET``, then waits up to
   ``ready_timeout_s`` seconds for the driver to write a single line
   of the form ``ready unix:<path>`` to stdout. If stdout EOFs or the
   timeout elapses, an AF_UNIX connect attempt is the fallback signal.
   On failure the harness raises with the captured stderr/stdout tail.
2. ``dial()`` returns a configured :class:`grpc.Channel` aimed at the
   driver. The channel sets ``grpc.default_authority=localhost``
   because Node's ``http2`` server, when bound to a UDS, requires the
   ``:authority`` pseudo-header to be ``localhost`` — a known Node
   constraint. See ADR-0008.
3. ``stop()`` sends SIGTERM, waits up to ``shutdown_timeout_s``
   seconds, and falls back to SIGKILL. The temporary directory
   holding the socket is removed.

The class is a context manager: ``with DriverHarness(...) as h``.

This harness is intentionally small. PR3 covers exactly one driver
(Playwright); SeleniumBase and curl-impersonate will reuse it once
their handshake landings exercise real evidence about what the
harness should generalise.
"""

from __future__ import annotations

import contextlib
import os
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path
from types import TracebackType
from typing import IO

import grpc
import yaml

DEFAULT_READY_TIMEOUT_S: float = 10.0
DEFAULT_SHUTDOWN_TIMEOUT_S: float = 5.0
READY_LINE_PREFIX = "ready unix:"
DIAGNOSTIC_TAIL_LINES = 50

DEFAULT_AUTHORITY = "localhost"


def _allocate_socket_path() -> Path:
    """Return a fresh socket path under ``/tmp``.

    macOS limits AF_UNIX paths to 104 characters. The default
    ``tempfile.gettempdir()`` on macOS resolves to a long
    ``/var/folders/...`` path that exceeds the limit when combined
    with a per-test subdirectory. Anchoring under ``/tmp`` keeps the
    path short.
    """

    tmpdir = Path(tempfile.mkdtemp(prefix="spectre-conf-", dir="/tmp"))
    return tmpdir / "d.sock"


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
    socket_path: Path = field(default_factory=_allocate_socket_path)
    ready_timeout_s: float = DEFAULT_READY_TIMEOUT_S
    shutdown_timeout_s: float = DEFAULT_SHUTDOWN_TIMEOUT_S

    _process: subprocess.Popen[str] | None = field(default=None, init=False, repr=False)
    _stdout_lines: list[str] = field(default_factory=list, init=False, repr=False)
    _stderr_lines: list[str] = field(default_factory=list, init=False, repr=False)
    _ready_event: threading.Event = field(default_factory=threading.Event, init=False, repr=False)
    _stdout_thread: threading.Thread | None = field(default=None, init=False, repr=False)
    _stderr_thread: threading.Thread | None = field(default=None, init=False, repr=False)
    _channel: grpc.Channel | None = field(default=None, init=False, repr=False)

    @classmethod
    def from_driver_yaml(
        cls,
        manifest_path: Path,
        *,
        transport_kind: str = "grpc-uds",
        **kwargs: object,
    ) -> DriverHarness:
        """Construct a harness from a ``driver.yaml`` file.

        Reads the first transport entry whose ``kind`` matches
        ``transport_kind`` and uses its ``command`` list verbatim.
        ``cwd`` defaults to the manifest's directory so any relative
        paths inside the command (e.g. ``dist/index.js``) resolve
        correctly.
        """

        manifest = yaml.safe_load(manifest_path.read_text())
        transports = manifest.get("transports", [])
        for transport in transports:
            if transport.get("kind") == transport_kind:
                command = list(transport.get("command", []))
                if not command:
                    raise ValueError(
                        f"driver.yaml at {manifest_path} declares an empty command "
                        f"for transport {transport_kind!r}"
                    )
                return cls(
                    command=command,
                    cwd=manifest_path.parent,
                    **kwargs,  # type: ignore[arg-type]
                )
        raise ValueError(
            f"driver.yaml at {manifest_path} has no transport of kind {transport_kind!r}"
        )

    # ------------------------------------------------------------------
    # Lifecycle

    def start(self) -> None:
        if self._process is not None:
            raise RuntimeError("DriverHarness.start() called twice")

        self.socket_path.parent.mkdir(parents=True, exist_ok=True)
        if self.socket_path.exists():
            self.socket_path.unlink()

        full_command = [*self.command, f"--socket={self.socket_path}"]
        env = {**os.environ, **self.extra_env, "SPECTRE_DRIVER_SOCKET": str(self.socket_path)}

        self._process = subprocess.Popen(  # noqa: S603 - command is caller-supplied
            full_command,
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
            args=(self._process.stdout, self._stdout_lines, self._on_stdout_line),
            daemon=True,
        )
        self._stderr_thread = threading.Thread(
            target=self._pump,
            args=(self._process.stderr, self._stderr_lines, None),
            daemon=True,
        )
        self._stdout_thread.start()
        self._stderr_thread.start()

        if not self._ready_event.wait(timeout=self.ready_timeout_s) and not self._socket_pingable():
            self._fail(
                f"driver did not signal readiness within {self.ready_timeout_s:.0f}s",
            )

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

        parent = self.socket_path.parent
        if parent.exists() and parent.name.startswith("spectre-conf-"):
            shutil.rmtree(parent, ignore_errors=True)

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
        if not self._ready_event.is_set():
            raise RuntimeError("dial() called before driver became ready")
        if self._channel is None:
            self._channel = grpc.insecure_channel(
                f"unix:{self.socket_path}",
                options=[("grpc.default_authority", DEFAULT_AUTHORITY)],
            )
        return self._channel

    @property
    def stderr_text(self) -> str:
        return "".join(self._stderr_lines)

    @property
    def stdout_text(self) -> str:
        return "".join(self._stdout_lines)

    # ------------------------------------------------------------------
    # Internals

    def _on_stdout_line(self, line: str) -> None:
        if self._ready_event.is_set():
            return
        if line.lstrip().startswith(READY_LINE_PREFIX):
            self._ready_event.set()

    def _pump(
        self,
        stream: IO[str],
        sink: list[str],
        on_line: object,
    ) -> None:
        for line in stream:
            sink.append(line)
            if on_line is not None:
                callback = on_line
                callback(line)  # type: ignore[operator]

    def _socket_pingable(self) -> bool:
        if not self.socket_path.exists():
            return False
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(0.5)
        try:
            sock.connect(str(self.socket_path))
            return True
        except OSError:
            return False
        finally:
            sock.close()

    def _fail(self, message: str) -> None:
        stdout_tail = "".join(self._stdout_lines[-DIAGNOSTIC_TAIL_LINES:])
        stderr_tail = "".join(self._stderr_lines[-DIAGNOSTIC_TAIL_LINES:])
        self.stop()
        raise HarnessFailure(
            message=message,
            stdout_tail=stdout_tail,
            stderr_tail=stderr_tail,
        )
