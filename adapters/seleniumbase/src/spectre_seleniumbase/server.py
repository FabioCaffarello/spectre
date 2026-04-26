"""gRPC service implementation for the SeleniumBase adapter.

PR9 implements ``Initialize`` and ``Navigate``. The remaining four
RPCs (``Close``, ``Query``, ``Extract``, ``Screenshot``) respond
``UNIMPLEMENTED`` and will be filled in by PR10/PR11. See ADR-0008
(handshake), ADR-0009 (Navigate / session lifecycle), and ADR-0014
(this PR — SeleniumBase progression and Selenium-error mapping).
"""

from __future__ import annotations

import contextlib
import sys
import time
import uuid
from typing import Any
from urllib.parse import urlparse

import grpc
from google.protobuf.duration_pb2 import Duration
from spectre.driver.v1alpha1 import (
    capabilities_pb2,
    driver_pb2,
    driver_pb2_grpc,
    errors_pb2,
)

from .capabilities import CAPABILITY_NAMES, DRIVER_VERSION
from .errors import selenium_error_to_driver_error
from .sessions import SessionManager, UnknownSessionError

DEFAULT_NAVIGATE_TIMEOUT_MS = 30_000

UNIMPLEMENTED_DETAIL = "RPC not yet implemented in PR9; lands in PR10/PR11"


def _ms_to_duration(ms: int) -> Duration:
    """Convert an integer millisecond count to ``google.protobuf.Duration``."""
    duration = Duration()
    duration.seconds = ms // 1000
    duration.nanos = (ms % 1000) * 1_000_000
    return duration


def _duration_to_ms(duration: Duration | None) -> int | None:
    """Return milliseconds from a ``Duration`` or ``None`` when unset.

    A zero duration is treated as "unset" because proto3 has no way to
    distinguish a populated-but-zero ``Duration`` from an absent one.
    """
    if duration is None:
        return None
    seconds = int(duration.seconds)
    nanos = int(duration.nanos)
    if seconds == 0 and nanos == 0:
        return None
    return seconds * 1000 + nanos // 1_000_000


def _is_valid_navigation_url(url: str) -> bool:
    """Return ``True`` if ``url`` is a parseable absolute http(s) URL."""
    try:
        parsed = urlparse(url)
    except (ValueError, TypeError):
        return False
    return parsed.scheme in ("http", "https") and bool(parsed.netloc)


def _navigate_error(code: int, message: str) -> driver_pb2.NavigateResponse:
    return driver_pb2.NavigateResponse(error=errors_pb2.DriverError(code=code, message=message))


def _runtime_version_string() -> str:
    """Render a ``runtime_version`` for the ``Capabilities`` response.

    Free-form per capabilities.proto. We surface the SeleniumBase
    package version when available (with a Python tag) so the engine
    has something useful to log on a version mismatch report.
    """
    try:
        import seleniumbase

        sb_version = getattr(seleniumbase, "__version__", "unknown")
    except Exception:  # noqa: BLE001 - never fail Initialize on a metadata call
        sb_version = "unknown"
    py_version = ".".join(str(v) for v in sys.version_info[:3])
    return f"seleniumbase@{sb_version} python@{py_version}"


def _default_driver_factory() -> Any:
    """Default WebDriver factory: a SeleniumBase ``Driver`` in headless mode.

    Imported lazily so the adapter can ``Initialize`` on a host that
    has SeleniumBase installed but not Chrome — the failure surfaces
    on the first ``Navigate`` (ADR-0009 §1, ADR-0014 §2).
    """
    from seleniumbase import Driver

    return Driver(
        browser="chrome",
        headless=True,
        # uc=False keeps the driver in standard Selenium mode rather
        # than SeleniumBase's UC (undetected) variant. UC mode is a
        # v1alpha2 capability candidate (see ADR-0014 §2 out of scope).
        uc=False,
    )


class DriverServicer(driver_pb2_grpc.DriverServicer):  # type: ignore[misc]
    """gRPC service implementing the v1alpha1 Driver protocol."""

    def __init__(self, sessions: SessionManager) -> None:
        self._sessions = sessions

    # ------------------------------------------------------------------
    # Initialize

    def Initialize(  # noqa: N802 - gRPC method casing
        self,
        request: driver_pb2.InitializeRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.InitializeResponse:
        del request  # PR9 ignores the request payload — see ADR-0009 §1.
        del context
        session_id = str(uuid.uuid4())
        self._sessions.register(session_id)
        capabilities = capabilities_pb2.Capabilities(
            names=list(CAPABILITY_NAMES),
            driver_version=DRIVER_VERSION,
            runtime_version=_runtime_version_string(),
        )
        return driver_pb2.InitializeResponse(
            session_id=session_id,
            capabilities=capabilities,
        )

    # ------------------------------------------------------------------
    # Navigate

    def Navigate(  # noqa: N802
        self,
        request: driver_pb2.NavigateRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.NavigateResponse:
        del context

        if not request.session_id:
            return _navigate_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "session_id is required",
            )
        if not self._sessions.has(request.session_id):
            return _navigate_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                f"unknown session_id {request.session_id!r}; call Initialize first",
            )
        if not request.url:
            return _navigate_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "url is required",
            )
        if not _is_valid_navigation_url(request.url):
            return _navigate_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                f"url must be an absolute http(s) URL, got {request.url!r}",
            )

        timeout_ms = _duration_to_ms(request.timeout) or DEFAULT_NAVIGATE_TIMEOUT_MS

        try:
            driver = self._sessions.get_or_create_driver(request.session_id)
        except UnknownSessionError as exc:
            return _navigate_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                str(exc),
            )
        except Exception as exc:  # noqa: BLE001 - any browser-launch failure is a DriverError
            mapped = selenium_error_to_driver_error(exc)
            return _navigate_error(mapped.code, mapped.message)

        # Selenium accepts page-load timeouts in seconds, as a float.
        # Best-effort: not every WebDriver flavour supports the call.
        with contextlib.suppress(Exception):
            driver.set_page_load_timeout(timeout_ms / 1000.0)

        start = time.monotonic()
        try:
            driver.get(request.url)
        except Exception as exc:  # noqa: BLE001 - mapped via the error table
            mapped = selenium_error_to_driver_error(exc)
            elapsed = _ms_to_duration(int((time.monotonic() - start) * 1000))
            return driver_pb2.NavigateResponse(
                elapsed=elapsed,
                error=errors_pb2.DriverError(code=mapped.code, message=mapped.message),
            )

        elapsed = _ms_to_duration(int((time.monotonic() - start) * 1000))
        final_url = str(getattr(driver, "current_url", "") or request.url)
        status_code = _read_status_code(driver)
        return driver_pb2.NavigateResponse(
            final_url=final_url,
            status_code=status_code,
            elapsed=elapsed,
        )

    # ------------------------------------------------------------------
    # Unimplemented RPCs

    def Query(  # noqa: N802
        self,
        request: driver_pb2.QueryRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.QueryResponse:
        del request
        context.abort(grpc.StatusCode.UNIMPLEMENTED, UNIMPLEMENTED_DETAIL)
        # `abort` raises; the return is unreachable but required by
        # mypy because the generated stub annotates a return type.
        return driver_pb2.QueryResponse()

    def Extract(  # noqa: N802
        self,
        request: driver_pb2.ExtractRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.ExtractResponse:
        del request
        context.abort(grpc.StatusCode.UNIMPLEMENTED, UNIMPLEMENTED_DETAIL)
        return driver_pb2.ExtractResponse()

    def Screenshot(  # noqa: N802
        self,
        request: driver_pb2.ScreenshotRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.ScreenshotResponse:
        del request
        context.abort(grpc.StatusCode.UNIMPLEMENTED, UNIMPLEMENTED_DETAIL)
        return driver_pb2.ScreenshotResponse()

    def Close(  # noqa: N802
        self,
        request: driver_pb2.CloseRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.CloseResponse:
        # Close is implemented in PR9 even though Query/Extract/Screenshot
        # stay UNIMPLEMENTED. Reason: the spectre engine's executor always
        # calls Close at the end of a plan, so a navigate-only job (the
        # PR9 example at examples/seleniumbase-navigate/) cannot complete
        # without it. Close has no protocol-level capability gate (it is
        # a baseline session-lifecycle RPC like Initialize), so wiring
        # it does not violate the "declared = tested" rule of ADR-0014 §1.
        # The richer Close conformance tests (closing an unknown id,
        # closing twice) land in PR10 alongside Query/Extract.
        del context
        if not request.session_id:
            return driver_pb2.CloseResponse(
                error=errors_pb2.DriverError(
                    code=errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                    message="session_id is required",
                ),
            )
        closed = self._sessions.close_session(request.session_id)
        if not closed:
            return driver_pb2.CloseResponse(
                error=errors_pb2.DriverError(
                    code=errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                    message=f"unknown session_id {request.session_id!r}",
                ),
            )
        return driver_pb2.CloseResponse()


def _read_status_code(driver: Any) -> int:
    """Read the most recent navigation's HTTP status code.

    Selenium does not expose the response status directly. We probe a
    ``performance.getEntriesByType('navigation')[0].responseStatus``
    in the page; if the page disallows JS or the timing API is absent
    (for example a ``data:`` or ``about:blank`` URL), we return 0.
    Returning 0 is a deliberate "unknown" signal — the conformance
    suite's ``test_navigate_local_fixture_ok`` explicitly tolerates
    either 0 or the real status code (PR10 will exercise the rich
    behaviour once Extract gives the suite a way to introspect the
    page beyond ``Navigate``).
    """
    try:
        result = driver.execute_script(
            "var entries = performance.getEntriesByType('navigation');"
            "return (entries && entries[0] && entries[0].responseStatus) || 0;"
        )
        return int(result) if isinstance(result, (int, float)) else 0
    except Exception:  # noqa: BLE001 - the field is informational, not load-bearing
        return 0
