"""gRPC service implementation for the SeleniumBase adapter.

Implements the v1alpha1 unary surface for this driver:
``Initialize``, ``Navigate``, ``Close``, ``Query``, ``Extract``,
and ``Screenshot`` — with strict ElementRef invalidation, runtime
capability gating on ``MODE_EVAL``, and the Screenshot read-only
contract from ADR-0011 — minus ``screenshot_full_page``, which the
SeleniumBase adapter does not declare. See ADR-0008 (handshake),
ADR-0009 (Navigate / session lifecycle), ADR-0010 (element
lifecycle and capability gating), ADR-0011 (Screenshot RPC),
ADR-0014 (SeleniumBase initial landing), and ADR-0015
(SeleniumBase-specific deviations).
"""

from __future__ import annotations

import contextlib
import io
import json
import os
import sys
import tempfile
import time
import uuid
from typing import Any
from urllib.parse import urlparse

import grpc
from google.protobuf.duration_pb2 import Duration
from selenium.common.exceptions import StaleElementReferenceException
from selenium.webdriver.common.by import By
from spectre.driver.v1alpha1 import (
    capabilities_pb2,
    driver_pb2,
    driver_pb2_grpc,
    errors_pb2,
    extraction_pb2,
)

from .capabilities import (
    CAPABILITY_NAMES,
    DRIVER_VERSION,
    SCREENSHOT_FULL_PAGE,
    missing_capability_for_mode,
)
from .errors import (
    STALE_NAVIGATE_MESSAGE,
    STALE_PAGE_STATE_CHANGE_MESSAGE,
    UNKNOWN_REF_MESSAGE,
    selenium_error_to_driver_error,
)
from .selectors import text_selector_to_xpath
from .sessions import SessionManager, UnknownSessionError, Validation

# R4.3 / ADR-0023 §5: gating every non-Initialize RPC against
# Redis. ``different_instance`` and Redis-unreachable surface as
# transport-level gRPC ``UNAVAILABLE``; ``unknown`` continues to
# return the in-band ``CODE_INVALID_ARGUMENT`` envelope so the
# behavior on a never-Initialized id stays consistent with the
# unary surface.
DIFFERENT_INSTANCE_MESSAGE = (
    "session belongs to a different adapter instance; client must re-Initialize"
)
REDIS_UNAVAILABLE_PREFIX = "redis unreachable"

DEFAULT_NAVIGATE_TIMEOUT_MS = 30_000

# JPEG quality default carried over from ADR-0011 §2. The adapter
# always renders to PNG via Selenium's native API and converts to
# JPEG via Pillow when the request asks for JPEG; ADR-0015 §4
# records the rationale.
JPEG_QUALITY_DEFAULT = 80

# ADR-0011 §3: soft-warn at 3MB so an operator sees the boundary
# before a screenshot crosses the ~4MB transport limit. The adapter
# does not fail the RPC at the warning threshold — the bytes are
# returned unchanged and the transport surfaces a
# ``RESOURCE_EXHAUSTED`` style error if the message actually
# exceeds the limit.
SCREENSHOT_PAYLOAD_WARN_BYTES = 3 * 1024 * 1024


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


def _abort_unavailable(context: grpc.ServicerContext, details: str) -> None:
    """Abort the RPC with gRPC ``UNAVAILABLE``.

    Used for the §5 restart-invalidation case (``different_instance``)
    and for Redis unreachable conditions during validation. The
    conformance test in
    ``tools/conformance/tests/test_session_restart_invalidation.py``
    asserts on ``grpc.StatusCode.UNAVAILABLE`` precisely against
    this code path.
    """
    context.abort(grpc.StatusCode.UNAVAILABLE, details)


def _gate_session(
    sessions: SessionManager,
    session_id: str,
    context: grpc.ServicerContext,
) -> Validation | None:
    """Validate ``session_id`` against Redis.

    Returns the :class:`Validation` for the caller to inspect (it
    will be ``ok`` or ``unknown``) or ``None`` if the call has
    already aborted with ``UNAVAILABLE`` (``different_instance`` or
    Redis-unreachable). The caller must check for ``None`` and
    return early when this helper has aborted.
    """
    try:
        result = sessions.validate(session_id)
    except Exception as exc:  # noqa: BLE001 — any redis failure
        _abort_unavailable(context, f"{REDIS_UNAVAILABLE_PREFIX}: {exc}")
        return None
    if result.kind == "different_instance":
        _abort_unavailable(context, DIFFERENT_INSTANCE_MESSAGE)
        return None
    return result


def _navigate_error(code: int, message: str) -> driver_pb2.NavigateResponse:
    return driver_pb2.NavigateResponse(error=errors_pb2.DriverError(code=code, message=message))


def _query_error(code: int, message: str) -> extraction_pb2.QueryResponse:
    return extraction_pb2.QueryResponse(
        error=errors_pb2.DriverError(code=code, message=message),
    )


def _extract_error(code: int, message: str) -> extraction_pb2.ExtractResponse:
    return extraction_pb2.ExtractResponse(
        error=errors_pb2.DriverError(code=code, message=message),
    )


def _screenshot_error(code: int, message: str) -> driver_pb2.ScreenshotResponse:
    return driver_pb2.ScreenshotResponse(
        error=errors_pb2.DriverError(code=code, message=message),
    )


def _selector_kind_to_by(kind: int, selector: str) -> tuple[str, str] | None:
    """Map a ``SelectorKind`` onto a ``(By, expression)`` tuple.

    Returns ``None`` when the kind is ``UNSPECIFIED`` or unknown so
    the caller can return ``CODE_INVALID_ARGUMENT``. ADR-0015 §3
    records the table.
    """
    if kind == extraction_pb2.SELECTOR_KIND_UNSPECIFIED:
        return None
    if kind == extraction_pb2.SELECTOR_KIND_CSS:
        return By.CSS_SELECTOR, selector
    if kind == extraction_pb2.SELECTOR_KIND_XPATH:
        return By.XPATH, selector
    if kind == extraction_pb2.SELECTOR_KIND_TEXT:
        return By.XPATH, text_selector_to_xpath(selector)
    if kind == extraction_pb2.SELECTOR_KIND_ATTRIBUTE:
        # CSS bracketed form: ``data-test=primary`` becomes
        # ``[data-test=primary]``; bare ``data-test`` becomes
        # ``[data-test]`` (presence-only). Mirrors ADR-0010 §5.
        return By.CSS_SELECTOR, f"[{selector}]"
    return None


def _read_field_value(driver: Any, element: Any, mode: int, arg: str) -> Any:
    """Read one ``Field``'s value off a ``WebElement``.

    Raises ``StaleElementReferenceException`` (or any other
    Selenium exception) on failure — the caller catches and maps
    via ``errors.py``. ADR-0015 §4 records the mode-by-mode
    mapping decision.
    """
    if mode == extraction_pb2.Field.MODE_TEXT_CONTENT:
        return element.get_attribute("textContent")
    if mode == extraction_pb2.Field.MODE_INNER_TEXT:
        return element.text
    if mode == extraction_pb2.Field.MODE_INNER_HTML:
        return element.get_attribute("innerHTML")
    if mode == extraction_pb2.Field.MODE_OUTER_HTML:
        return element.get_attribute("outerHTML")
    if mode == extraction_pb2.Field.MODE_ATTR:
        # ``get_attribute`` returns ``None`` for missing attributes;
        # the JSON encoder emits ``null`` and clients can ``?? ""``.
        return element.get_attribute(arg)
    if mode == extraction_pb2.Field.MODE_EVAL:
        # Per ADR-0015 §4: ``execute_script`` with the element as
        # the first positional arg, addressable as ``arguments[0]``
        # in the script body. The script string is whatever the
        # client provided in ``Field.arg``.
        script = f"return ({arg});" if "return " not in arg else arg
        return driver.execute_script(script, element)
    raise _ProtocolError(
        errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
        f"field mode {mode} is unspecified or unknown",
    )


class _ProtocolError(Exception):
    """Internal helper carrying a (code, message) pair through the
    field-reading loop without forcing each mode to plumb the
    response shape itself."""

    def __init__(self, code: int, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def _png_to_jpeg(png_bytes: bytes, quality: int = JPEG_QUALITY_DEFAULT) -> bytes:
    """Convert a PNG byte string to JPEG.

    Selenium returns PNG bytes natively; the v1alpha1 protocol also
    accepts JPEG. Pillow does the conversion and flattens any alpha
    channel to white, which is the standard JPEG-no-alpha behaviour.
    ADR-0015 §1 / §4 record the dependency rationale.
    """
    from PIL import Image  # noqa: PLC0415 — lazy import keeps cold-path PNG fast

    src = Image.open(io.BytesIO(png_bytes))
    if src.mode in ("RGBA", "LA", "P"):
        background = Image.new("RGB", src.size, (255, 255, 255))
        background.paste(src.convert("RGBA"), mask=src.convert("RGBA").split()[-1])
        flat = background
    else:
        flat = src.convert("RGB")
    out = io.BytesIO()
    flat.save(out, format="JPEG", quality=quality)
    return out.getvalue()


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

    kwargs: dict[str, Any] = {
        "browser": "chrome",
        "headless": True,
        # uc=False keeps the driver in standard Selenium mode rather
        # than SeleniumBase's UC (undetected) variant. UC mode is a
        # v1alpha2 capability candidate (see ADR-0014 §2 out of scope).
        "uc": False,
    }
    # Container deployment knob. Default behaviour is
    # unchanged for non-containerised callers: the conformance suite
    # and the developer-host workflow continue to drive Chrome with
    # its sandbox enabled. When the operator Pod sets
    # SPECTRE_SELENIUMBASE_CONTAINER=1, forward the Chrome flags
    # required to launch under the `restricted` PodSecurityStandard.
    # SeleniumBase splits ``chromium_arg`` on commas onto the Chrome
    # launch options. The flags are:
    #
    # - ``--no-sandbox`` / ``--disable-dev-shm-usage``: the Chrome
    #   zygote needs both to start without SYS_ADMIN and the
    #   default 64 MiB /dev/shm a Pod may have.
    # - ``--disable-gpu`` / ``--disable-software-rasterizer``: the
    #   container has no GPU drivers; the software-rasterizer
    #   fallback can crash on a renderless host.
    # - ``--user-data-dir=<tmp>``: when ``readOnlyRootFilesystem``
    #   is true on the Pod, Chrome cannot write its preferences
    #   directory under ``$HOME``. Pointing the data dir at an
    #   emptyDir ``/tmp`` mount (the only writable location on
    #   the Pod) keeps Chrome's per-profile state happy.
    #   ``mkdtemp`` returns a fresh path per ``Initialize``;
    #   Kubernetes reclaims it on Pod termination.
    #
    # Crashpad is *not* disabled here. Its database directory
    # resolves under ``$HOME/.local`` independently of
    # ``--user-data-dir``; the pod spec sets ``HOME=/tmp`` so
    # crashpad's writes land on the writable /tmp emptyDir.
    if os.environ.get("SPECTRE_SELENIUMBASE_CONTAINER") == "1":
        user_data_dir = tempfile.mkdtemp(prefix="spectre-chrome-")
        kwargs["chromium_arg"] = ",".join(
            [
                "--no-sandbox",
                "--disable-dev-shm-usage",
                "--disable-gpu",
                "--disable-software-rasterizer",
                f"--user-data-dir={user_data_dir}",
            ]
        )
        # W2.2 (2026-05-08, ADR-0018 §5 W2.2 update): the
        # container image installs Debian's `chromium` package at
        # /usr/bin/chromium instead of Google Chrome stable
        # (chrome-stable is amd64-only on Linux; chromium is
        # multi-arch in bookworm-main). SeleniumBase's
        # `binary_location` kwarg points the underlying selenium
        # WebDriver at the explicit binary path; without it,
        # `Driver(browser="chrome")` would search the standard
        # google-chrome-stable install paths and fail. Dev-host
        # workflow (no SPECTRE_SELENIUMBASE_CONTAINER env) is
        # unchanged: contributors with Chrome installed locally
        # continue to drive their host's Chrome via
        # SeleniumBase's default lookup.
        kwargs["binary_location"] = "/usr/bin/chromium"
    return Driver(**kwargs)


class DriverServicer(driver_pb2_grpc.DriverServicer):  # type: ignore[misc]
    """gRPC service implementing the v1alpha1 Driver protocol."""

    def __init__(
        self,
        sessions: SessionManager,
        metrics: Any | None = None,
    ) -> None:
        self._sessions = sessions
        # W3.2 Cluster B: ``metrics`` is an
        # ``spectre_seleniumbase.telemetry.AdapterMetrics`` handle;
        # ``None`` disables emission (tests pass ``None`` so the
        # existing suite is unchanged).
        self._metrics = metrics

    # --- §5.3 recorders ------------------------------------------------

    def _record_navigate_duration(self, start: float, result: str) -> None:
        if self._metrics is None:
            return
        self._metrics.navigate_duration.labels(kind="seleniumbase", result=result).observe(
            time.monotonic() - start,
        )

    def _record_extract_duration(self, start: float, result: str) -> None:
        if self._metrics is None:
            return
        self._metrics.extract_duration.labels(kind="seleniumbase", result=result).observe(
            time.monotonic() - start,
        )

    # ------------------------------------------------------------------
    # Initialize

    def Initialize(  # noqa: N802 - gRPC method casing
        self,
        request: driver_pb2.InitializeRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.InitializeResponse:
        start = time.monotonic()
        # W3.2 Cluster B: every requested capability not in the
        # adapter manifest increments
        # ``capability_violations_total`` with the offending name.
        # The Initialize still succeeds with the adapter's actual
        # manifest so the caller can negotiate gracefully; the
        # counter is the auditable signal.
        if self._metrics is not None:
            manifest = set(CAPABILITY_NAMES)
            for requested in request.requested_capabilities:
                if requested not in manifest:
                    self._metrics.capability_violations_total.labels(
                        kind="seleniumbase",
                        capability=requested,
                    ).inc()

        session_id = str(uuid.uuid4())
        # ADR-0023 §6 makes Redis required: a metadata-write failure
        # surfaces at the transport layer so the caller sees the
        # same gRPC ``UNAVAILABLE`` it would see on adapter startup.
        # The local registry is only updated after a successful
        # write — see ``SessionManager.register``.
        try:
            self._sessions.register(session_id)
        except Exception as exc:  # noqa: BLE001 — any redis failure
            _abort_unavailable(
                context,
                f"{REDIS_UNAVAILABLE_PREFIX}; cannot persist session metadata: {exc}",
            )
            return driver_pb2.InitializeResponse()

        if self._metrics is not None:
            self._metrics.initialize_duration.labels(kind="seleniumbase").observe(
                time.monotonic() - start,
            )
            self._metrics.sessions_active.labels(kind="seleniumbase").inc()

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
        # W3.2 Cluster B: wrap the existing body so the §5.3
        # duration histogram is observed once per RPC with the
        # canonical ``result`` label derived from the in-band
        # DriverError. The handler's existing structure is
        # preserved verbatim under ``_navigate_impl``.
        start = time.monotonic()
        response = self._navigate_impl(request, context)
        result = "failure" if response.HasField("error") else "success"
        self._record_navigate_duration(start, result)
        return response

    def _navigate_impl(
        self,
        request: driver_pb2.NavigateRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.NavigateResponse:
        if not request.session_id:
            return _navigate_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "session_id is required",
            )
        gate = _gate_session(self._sessions, request.session_id, context)
        if gate is None:
            return driver_pb2.NavigateResponse()
        if gate.kind == "unknown":
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

        # Strict ElementRef invalidation: every successful Navigate
        # bumps the session's generation counter so refs allocated
        # against the prior page are rejected by Extract /
        # element-scoped Screenshot. ADR-0010 §1, ADR-0015 §1.
        self._sessions.bump_generation(request.session_id)

        elapsed = _ms_to_duration(int((time.monotonic() - start) * 1000))
        final_url = str(getattr(driver, "current_url", "") or request.url)
        status_code = _read_status_code(driver)
        return driver_pb2.NavigateResponse(
            final_url=final_url,
            status_code=status_code,
            elapsed=elapsed,
        )

    # ------------------------------------------------------------------
    # Query

    def Query(  # noqa: N802
        self,
        request: extraction_pb2.QueryRequest,
        context: grpc.ServicerContext,
    ) -> extraction_pb2.QueryResponse:
        if not request.session_id:
            return _query_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "session_id is required",
            )
        gate = _gate_session(self._sessions, request.session_id, context)
        if gate is None:
            return extraction_pb2.QueryResponse()
        if gate.kind == "unknown":
            return _query_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                f"unknown session_id {request.session_id!r}; call Initialize first",
            )
        if request.kind == extraction_pb2.SELECTOR_KIND_UNSPECIFIED:
            return _query_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "selector kind is required; SELECTOR_KIND_UNSPECIFIED is not accepted",
            )
        if not request.selector:
            return _query_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "selector is required",
            )

        driver = self._sessions.driver_of(request.session_id)
        if driver is None:
            return _query_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "no page is open for this session; call Navigate first",
            )

        mapping = _selector_kind_to_by(request.kind, request.selector)
        if mapping is None:
            return _query_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                f"unsupported selector kind: {request.kind}",
            )
        by_kind, expression = mapping

        try:
            elements = driver.find_elements(by_kind, expression)
        except Exception as exc:  # noqa: BLE001 — mapped via the error table
            mapped = selenium_error_to_driver_error(exc)
            return _query_error(mapped.code, mapped.message)

        if request.limit > 0:
            elements = elements[: request.limit]

        ids = self._sessions.register_elements(request.session_id, elements)
        return extraction_pb2.QueryResponse(
            elements=[extraction_pb2.ElementRef(opaque_id=ref_id) for ref_id in ids],
        )

    # ------------------------------------------------------------------
    # Extract

    def Extract(  # noqa: N802
        self,
        request: extraction_pb2.ExtractRequest,
        context: grpc.ServicerContext,
    ) -> extraction_pb2.ExtractResponse:
        # W3.2 Cluster B: wraps ``_extract_impl`` with the §5.3
        # duration recorder. See ``Navigate`` for the rationale.
        start = time.monotonic()
        response = self._extract_impl(request, context)
        result = "failure" if response.HasField("error") else "success"
        self._record_extract_duration(start, result)
        return response

    def _extract_impl(
        self,
        request: extraction_pb2.ExtractRequest,
        context: grpc.ServicerContext,
    ) -> extraction_pb2.ExtractResponse:
        if not request.session_id:
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "session_id is required",
            )
        gate = _gate_session(self._sessions, request.session_id, context)
        if gate is None:
            return extraction_pb2.ExtractResponse()
        if gate.kind == "unknown":
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                f"unknown session_id {request.session_id!r}; call Initialize first",
            )
        opaque_id = request.element.opaque_id if request.HasField("element") else ""
        if not opaque_id:
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "element.opaque_id is required",
            )
        if len(request.fields) == 0:
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "at least one field is required",
            )

        # Capability gate: any MODE_EVAL field forces a check on the
        # declared list. The whole request fails if the gate trips —
        # partial-success semantics are out of v1alpha1 scope. See
        # ADR-0010 §3 / ADR-0015 §1.
        for field in request.fields:
            missing = missing_capability_for_mode(field.mode, CAPABILITY_NAMES)
            if missing is not None:
                return _extract_error(
                    errors_pb2.DriverError.CODE_CAPABILITY_MISSING,
                    f"MODE_EVAL requires the {missing} capability",
                )

        # Reject any unspecified mode up-front so the field-reading
        # loop only ever sees real modes.
        for field in request.fields:
            if field.mode == extraction_pb2.Field.MODE_UNSPECIFIED:
                return _extract_error(
                    errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                    f"field {field.name!r} has an unspecified mode",
                )

        lookup = self._sessions.lookup_element(request.session_id, opaque_id)
        if lookup.status == "stale":
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                STALE_NAVIGATE_MESSAGE,
            )
        if lookup.status == "unknown" or lookup.element is None:
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                UNKNOWN_REF_MESSAGE,
            )
        element = lookup.element
        driver = self._sessions.driver_of(request.session_id)

        entries: list[extraction_pb2.ExtractedValues.Entry] = []
        try:
            for field in request.fields:
                value = _read_field_value(driver, element, field.mode, field.arg)
                entries.append(
                    extraction_pb2.ExtractedValues.Entry(
                        name=field.name,
                        json_value=json.dumps(value),
                    ),
                )
        except StaleElementReferenceException:
            # SPA mutation between Query and Extract; ADR-0015 §2.
            return _extract_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                STALE_PAGE_STATE_CHANGE_MESSAGE,
            )
        except _ProtocolError as exc:
            return _extract_error(exc.code, exc.message)
        except Exception as exc:  # noqa: BLE001 — mapped via the error table
            mapped = selenium_error_to_driver_error(exc)
            return _extract_error(mapped.code, mapped.message)

        return extraction_pb2.ExtractResponse(
            values=extraction_pb2.ExtractedValues(fields=entries),
        )

    # ------------------------------------------------------------------
    # Screenshot

    def Screenshot(  # noqa: N802
        self,
        request: driver_pb2.ScreenshotRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.ScreenshotResponse:
        if not request.session_id:
            return _screenshot_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "session_id is required",
            )
        gate = _gate_session(self._sessions, request.session_id, context)
        if gate is None:
            return driver_pb2.ScreenshotResponse()
        if gate.kind == "unknown":
            return _screenshot_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                f"unknown session_id {request.session_id!r}; call Initialize first",
            )
        if request.scope == driver_pb2.SCREENSHOT_SCOPE_UNSPECIFIED:
            return _screenshot_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "scope is required; SCREENSHOT_SCOPE_UNSPECIFIED is not accepted",
            )
        # Per ADR-0015 §5, the SeleniumBase adapter does not declare
        # ``screenshot_full_page``. The engine's planner is the
        # primary line of defence against jobs that need the
        # capability; this adapter-side guard is the second.
        if request.scope == driver_pb2.SCREENSHOT_SCOPE_FULL_PAGE:
            return _screenshot_error(
                errors_pb2.DriverError.CODE_CAPABILITY_MISSING,
                f"the seleniumbase driver does not declare {SCREENSHOT_FULL_PAGE}",
            )
        if request.scope == driver_pb2.SCREENSHOT_SCOPE_ELEMENT and (
            not request.HasField("element") or not request.element.opaque_id
        ):
            return _screenshot_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "element is required when scope is SCREENSHOT_SCOPE_ELEMENT",
            )

        driver = self._sessions.driver_of(request.session_id)
        if driver is None:
            return _screenshot_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                "no page is open for this session; call Navigate first",
            )

        is_jpeg = request.format == driver_pb2.SCREENSHOT_FORMAT_JPEG
        content_type = "image/jpeg" if is_jpeg else "image/png"

        try:
            if request.scope == driver_pb2.SCREENSHOT_SCOPE_VIEWPORT:
                png_bytes = driver.get_screenshot_as_png()
            elif request.scope == driver_pb2.SCREENSHOT_SCOPE_ELEMENT:
                lookup = self._sessions.lookup_element(
                    request.session_id, request.element.opaque_id
                )
                if lookup.status == "stale":
                    return _screenshot_error(
                        errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                        STALE_NAVIGATE_MESSAGE,
                    )
                if lookup.status == "unknown" or lookup.element is None:
                    return _screenshot_error(
                        errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                        UNKNOWN_REF_MESSAGE,
                    )
                # ``screenshot_as_png`` lives on Selenium 4+
                # ``WebElement`` and auto-scrolls the element into
                # view before capture (mirrors Playwright's
                # ``locator.screenshot``).
                png_bytes = lookup.element.screenshot_as_png
            else:  # pragma: no cover — exhaustively handled above
                return _screenshot_error(
                    errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                    f"unsupported scope: {request.scope}",
                )
        except StaleElementReferenceException:
            return _screenshot_error(
                errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                STALE_PAGE_STATE_CHANGE_MESSAGE,
            )
        except Exception as exc:  # noqa: BLE001 — mapped via the error table
            mapped = selenium_error_to_driver_error(exc)
            return _screenshot_error(mapped.code, mapped.message)

        if is_jpeg:
            try:
                image = _png_to_jpeg(png_bytes)
            except Exception as exc:  # noqa: BLE001 — Pillow failures are unexpected
                return _screenshot_error(
                    errors_pb2.DriverError.CODE_INTERNAL,
                    f"failed to encode JPEG: {exc}",
                )
        else:
            image = png_bytes

        if len(image) > SCREENSHOT_PAYLOAD_WARN_BYTES:
            # ADR-0011 §3 carries forward to SeleniumBase.
            sys.stderr.write(
                f"screenshot payload {len(image)} bytes exceeds 3MB warning threshold; "
                f"v1alpha1 transport limit is ~4MB\n",
            )
            sys.stderr.flush()

        return driver_pb2.ScreenshotResponse(
            image=bytes(image),
            content_type=content_type,
        )

    def Close(  # noqa: N802
        self,
        request: driver_pb2.CloseRequest,
        context: grpc.ServicerContext,
    ) -> driver_pb2.CloseResponse:
        # Close is the full session-teardown RPC. R4.3 adds the §5
        # restart-invalidation gate so a Close from a foreign instance
        # surfaces as ``UNAVAILABLE`` rather than tearing nothing down
        # silently. The Redis-delete inside ``close_session`` is
        # best-effort (§4.6); only the validate step is gated.
        if not request.session_id:
            return driver_pb2.CloseResponse(
                error=errors_pb2.DriverError(
                    code=errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                    message="session_id is required",
                ),
            )
        gate = _gate_session(self._sessions, request.session_id, context)
        if gate is None:
            return driver_pb2.CloseResponse()
        if gate.kind == "unknown":
            return driver_pb2.CloseResponse(
                error=errors_pb2.DriverError(
                    code=errors_pb2.DriverError.CODE_INVALID_ARGUMENT,
                    message=f"unknown session_id {request.session_id!r}",
                ),
            )
        self._sessions.close_session(request.session_id)
        if self._metrics is not None:
            self._metrics.sessions_active.labels(kind="seleniumbase").dec()
        return driver_pb2.CloseResponse()


def _read_status_code(driver: Any) -> int:
    """Read the most recent navigation's HTTP status code.

    Selenium does not expose the response status directly. We probe a
    ``performance.getEntriesByType('navigation')[0].responseStatus``
    in the page; if the page disallows JS or the timing API is absent
    (for example a ``data:`` or ``about:blank`` URL), we return 0.
    Returning 0 is a deliberate "unknown" signal — the conformance
    suite's ``test_navigate_local_fixture_ok`` explicitly tolerates
    either 0 or the real status code.
    """
    try:
        result = driver.execute_script(
            "var entries = performance.getEntriesByType('navigation');"
            "return (entries && entries[0] && entries[0].responseStatus) || 0;"
        )
        return int(result) if isinstance(result, (int, float)) else 0
    except Exception:  # noqa: BLE001 - the field is informational, not load-bearing
        return 0
