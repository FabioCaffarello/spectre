"""Live ``Query`` conformance tests.

Each test launches the Playwright adapter, navigates to the
``/elements`` fixture page, and issues one or more ``Query`` RPCs
to verify the ``SelectorKind`` mapping (CSS, XPath, Text,
Attribute) and the zero-matches contract. See ADR-0010 decisions 4
and 5.

Scenarios
---------

- ``test_query_css_returns_matches``: ``li.item`` returns three
  ``ElementRef``s.
- ``test_query_xpath_returns_matches``: ``//li[@class='item']``
  returns three ``ElementRef``s.
- ``test_query_text_returns_matches``: a substring text query
  returns at least one ``ElementRef``.
- ``test_query_attribute_returns_matches``: ``data-test=primary``
  returns one ``ElementRef``.
- ``test_query_zero_matches_returns_empty_list``: a selector that
  does not match anything returns an empty ``elements`` list and
  no ``DriverError``.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, extraction_pb2

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 60.0
GRPC_CALL_TIMEOUT_S = 30.0


def _open_elements_page(
    stub: driver_pb2_grpc.DriverStub,
    base_url: str,
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
        driver_pb2.NavigateRequest(
            session_id=init.session_id,
            url=f"{base_url}/elements",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not nav.HasField("error"), nav.error.message
    return str(init.session_id)


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_query_css_returns_matches(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)

    response = stub.Query(
        extraction_pb2.QueryRequest(
            session_id=session_id,
            selector="li.item",
            kind=extraction_pb2.SELECTOR_KIND_CSS,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert len(response.elements) == 3, (
        f"expected three matches for li.item, got {len(response.elements)}"
    )
    for element in response.elements:
        assert element.opaque_id, "every ElementRef must carry an opaque_id"


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_query_xpath_returns_matches(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)

    response = stub.Query(
        extraction_pb2.QueryRequest(
            session_id=session_id,
            selector="//li[@class='item']",
            kind=extraction_pb2.SELECTOR_KIND_XPATH,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert len(response.elements) == 3


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_query_text_returns_matches(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)

    response = stub.Query(
        extraction_pb2.QueryRequest(
            session_id=session_id,
            selector="Primary",
            kind=extraction_pb2.SELECTOR_KIND_TEXT,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert len(response.elements) >= 1, (
        "substring text 'Primary' matches at least one element on the page"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_query_attribute_returns_matches(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)

    response = stub.Query(
        extraction_pb2.QueryRequest(
            session_id=session_id,
            selector="data-test=primary",
            kind=extraction_pb2.SELECTOR_KIND_ATTRIBUTE,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert len(response.elements) == 1


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_query_zero_matches_returns_empty_list(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """Zero matches is success with an empty list, not a DriverError.
    See ADR-0010, decision 4."""

    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)

    response = stub.Query(
        extraction_pb2.QueryRequest(
            session_id=session_id,
            selector=".no-such-class",
            kind=extraction_pb2.SELECTOR_KIND_CSS,
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), (
        f"zero matches must not produce a DriverError; got {response.error.message!r}"
    )
    assert list(response.elements) == []
