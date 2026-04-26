"""Live ``Screenshot`` conformance tests.

Each test launches the Playwright adapter, navigates to a fixture
page, and exercises one path through the ``Screenshot`` RPC. The
suite covers the three scopes (viewport, full-page, element) and
both formats (PNG, JPEG), plus the strict ElementRef invalidation
contract on element-scoped screenshots after Navigate. See
ADR-0011.

Scenarios
---------

- ``test_screenshot_viewport_returns_png``: viewport-PNG against
  ``/elements`` returns non-empty bytes whose leading four bytes
  are the PNG magic number.
- ``test_screenshot_full_page_is_taller_than_viewport``: full-page
  PNG against ``/long-page`` returns strictly more bytes than the
  viewport-PNG of the same URL. The ``/long-page`` route serves a
  ~3000 px tall page with varied per-row text so PNG compression
  cannot collapse the difference.
- ``test_screenshot_element_returns_jpeg``: element-scoped JPEG
  against an ``/elements`` ref returns non-empty bytes whose
  leading three bytes are the JPEG magic number, and the
  declared ``content_type`` is ``image/jpeg``.
- ``test_screenshot_element_after_navigate_returns_invalid_argument``:
  an element-scoped Screenshot against a ref allocated before a
  Navigate is rejected with the stale-ref ``CODE_INVALID_ARGUMENT``
  message PR5 established for ``Extract``.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import (
    driver_pb2,
    driver_pb2_grpc,
    errors_pb2,
    extraction_pb2,
)

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 60.0
GRPC_CALL_TIMEOUT_S = 30.0

PNG_MAGIC = b"\x89PNG"
JPEG_MAGIC = b"\xff\xd8\xff"


def _open_page(
    stub: driver_pb2_grpc.DriverStub,
    url: str,
) -> str:
    init = stub.Initialize(
        driver_pb2.InitializeRequest(
            protocol_version=str(driver_pb2.DESCRIPTOR.package),
            session=driver_pb2.SessionConfig(),
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert init.session_id and not init.HasField("error")
    nav = stub.Navigate(
        driver_pb2.NavigateRequest(session_id=init.session_id, url=url),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not nav.HasField("error"), nav.error.message
    return str(init.session_id)


def _query_one(
    stub: driver_pb2_grpc.DriverStub,
    session_id: str,
    selector: str,
) -> extraction_pb2.ElementRef:
    response = stub.Query(
        extraction_pb2.QueryRequest(
            session_id=session_id,
            selector=selector,
            kind=extraction_pb2.SELECTOR_KIND_CSS,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert len(response.elements) >= 1, f"selector {selector!r} produced no matches"
    return response.elements[0]


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_screenshot_viewport_returns_png(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_page(stub, f"{local_http_server.base_url}/elements")

    response = stub.Screenshot(
        driver_pb2.ScreenshotRequest(
            session_id=session_id,
            scope=driver_pb2.SCREENSHOT_SCOPE_VIEWPORT,
            format=driver_pb2.SCREENSHOT_FORMAT_PNG,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert response.content_type == "image/png"
    assert len(response.image) > 0, "viewport screenshot bytes must be non-empty"
    assert response.image[:4] == PNG_MAGIC, (
        f"expected PNG magic bytes {PNG_MAGIC!r}, got {response.image[:4]!r}"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_screenshot_full_page_is_taller_than_viewport(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """A full-page PNG against ``/long-page`` carries more bytes than
    the viewport-PNG of the same URL. The fixture serves ~3000 px
    of varied content so PNG compression cannot collapse the
    delta. See ``http_fixture.py``."""

    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_page(stub, f"{local_http_server.base_url}/long-page")

    viewport = stub.Screenshot(
        driver_pb2.ScreenshotRequest(
            session_id=session_id,
            scope=driver_pb2.SCREENSHOT_SCOPE_VIEWPORT,
            format=driver_pb2.SCREENSHOT_FORMAT_PNG,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not viewport.HasField("error"), viewport.error.message
    assert viewport.image[:4] == PNG_MAGIC

    full_page = stub.Screenshot(
        driver_pb2.ScreenshotRequest(
            session_id=session_id,
            scope=driver_pb2.SCREENSHOT_SCOPE_FULL_PAGE,
            format=driver_pb2.SCREENSHOT_FORMAT_PNG,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not full_page.HasField("error"), full_page.error.message
    assert full_page.image[:4] == PNG_MAGIC

    assert len(full_page.image) > len(viewport.image), (
        "full-page PNG must be larger than viewport PNG against the same "
        f"long-page URL; viewport={len(viewport.image)} bytes, "
        f"full_page={len(full_page.image)} bytes"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_screenshot_element_returns_jpeg(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_page(stub, f"{local_http_server.base_url}/elements")
    element = _query_one(stub, session_id, "#badge")

    response = stub.Screenshot(
        driver_pb2.ScreenshotRequest(
            session_id=session_id,
            scope=driver_pb2.SCREENSHOT_SCOPE_ELEMENT,
            element=element,
            format=driver_pb2.SCREENSHOT_FORMAT_JPEG,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert response.content_type == "image/jpeg"
    assert len(response.image) > 0, "element screenshot bytes must be non-empty"
    assert response.image[:3] == JPEG_MAGIC, (
        f"expected JPEG magic bytes {JPEG_MAGIC!r}, got {response.image[:3]!r}"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_screenshot_element_after_navigate_returns_invalid_argument(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """An ElementRef allocated before a Navigate is invalidated;
    Screenshot inherits the same stale-ref contract Extract uses.
    See ADR-0010 decision 1 and ADR-0011 decision 1."""

    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_page(stub, f"{local_http_server.base_url}/elements")
    element = _query_one(stub, session_id, "#title")

    nav2 = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/elements-2",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not nav2.HasField("error"), nav2.error.message

    response = stub.Screenshot(
        driver_pb2.ScreenshotRequest(
            session_id=session_id,
            scope=driver_pb2.SCREENSHOT_SCOPE_ELEMENT,
            element=element,
            format=driver_pb2.SCREENSHOT_FORMAT_PNG,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert response.HasField("error")
    assert response.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
    assert "stale" in response.error.message.lower()
    assert len(response.image) == 0, "failure response must carry empty bytes"
    assert response.content_type == ""
