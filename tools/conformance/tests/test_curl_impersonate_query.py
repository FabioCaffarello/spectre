"""Live ``Query`` conformance tests for the curl-impersonate adapter.

PR12 implements Query with two SelectorKinds: CSS and XPATH.
``SELECTOR_KIND_TEXT`` and ``SELECTOR_KIND_ATTRIBUTE`` are
rejected with ``CODE_INVALID_ARGUMENT`` because the adapter does
not declare ``query_text`` / ``query_attribute`` — see ADR-0017
§1 (capability declaration is a cross-driver semantic-equivalence
contract). The dedicated unit tests in
``internal/server/server_test.go`` exercise those rejections and
their ADR-0017 reference; the conformance suite here covers the
positive paths (CSS, XPath, zero-matches).

Scenarios
---------

- ``test_curl_impersonate_query_css_returns_matches``: ``li.item``
  returns three ``ElementRef``s.
- ``test_curl_impersonate_query_xpath_returns_matches``:
  ``//li[@class='item']`` returns three ``ElementRef``s.
- ``test_curl_impersonate_query_zero_matches_returns_empty_list``:
  a selector that matches nothing returns an empty list and no
  ``DriverError`` (ADR-0010 §4 — same contract on every driver).
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, extraction_pb2

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 30.0
GRPC_CALL_TIMEOUT_S = 20.0


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
def test_curl_impersonate_query_css_returns_matches(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
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
def test_curl_impersonate_query_xpath_returns_matches(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
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
def test_curl_impersonate_query_zero_matches_returns_empty_list(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """Zero matches is success with an empty list, not a DriverError.
    ADR-0010 §4 — the contract is the same on every driver, even
    where the underlying runtime is structurally different."""

    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
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
