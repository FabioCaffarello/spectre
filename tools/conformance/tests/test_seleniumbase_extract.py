"""Live ``Extract`` conformance tests for the SeleniumBase adapter.

Each test launches the SeleniumBase adapter, navigates to the
``/elements`` fixture page, queries for at least one element, and
issues an ``Extract`` against the resulting ``ElementRef``. The
suite covers the extract field modes (text content, attribute,
inner HTML), the capability gating contract, and the strict
ElementRef invalidation on Navigate. ADR-0010 fixes the contract
for both drivers; ADR-0015 §2 / §4 record the SeleniumBase
specifics (textContent via get_attribute synthetic-attribute,
two stale messages, MODE_EVAL via execute_script).

Scenarios
---------

- ``test_seleniumbase_extract_text_content``: read the heading's
  ``MODE_TEXT_CONTENT`` and verify the JSON-encoded value.
- ``test_seleniumbase_extract_attribute``: read the link's
  ``href`` attribute via ``MODE_ATTR``.
- ``test_seleniumbase_extract_inner_html``: read the list's
  ``MODE_INNER_HTML`` and assert the markup contains the three
  list items.
- ``test_seleniumbase_extract_eval_with_js_execution``:
  ``MODE_EVAL`` succeeds because ``js_execution`` is declared.
- ``test_seleniumbase_extract_after_navigate_returns_invalid_argument``:
  an ``ElementRef`` allocated before a navigation is rejected
  with ``CODE_INVALID_ARGUMENT`` and the documented stale message.
"""

from __future__ import annotations

import json

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, errors_pb2, extraction_pb2

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 90.0
GRPC_CALL_TIMEOUT_S = 60.0


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


def _query_one(
    stub: driver_pb2_grpc.DriverStub,
    session_id: str,
    selector: str,
    kind: int = extraction_pb2.SELECTOR_KIND_CSS,
) -> extraction_pb2.ElementRef:
    response = stub.Query(
        extraction_pb2.QueryRequest(session_id=session_id, selector=selector, kind=kind),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    assert len(response.elements) >= 1, f"selector {selector!r} produced no matches"
    return response.elements[0]


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_extract_text_content(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)
    element = _query_one(stub, session_id, "#title")

    response = stub.Extract(
        extraction_pb2.ExtractRequest(
            session_id=session_id,
            element=element,
            fields=[
                extraction_pb2.Field(name="title", mode=extraction_pb2.Field.MODE_TEXT_CONTENT),
            ],
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    fields = list(response.values.fields)
    assert len(fields) == 1
    assert fields[0].name == "title"
    assert json.loads(fields[0].json_value) == "Elements Page"


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_extract_attribute(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)
    element = _query_one(stub, session_id, "#link")

    response = stub.Extract(
        extraction_pb2.ExtractRequest(
            session_id=session_id,
            element=element,
            fields=[
                extraction_pb2.Field(name="href", mode=extraction_pb2.Field.MODE_ATTR, arg="href"),
            ],
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    fields = list(response.values.fields)
    assert len(fields) == 1
    assert json.loads(fields[0].json_value) == "https://example.com/target"


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_extract_inner_html(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)
    element = _query_one(stub, session_id, "#items")

    response = stub.Extract(
        extraction_pb2.ExtractRequest(
            session_id=session_id,
            element=element,
            fields=[
                extraction_pb2.Field(name="markup", mode=extraction_pb2.Field.MODE_INNER_HTML),
            ],
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    fields = list(response.values.fields)
    inner = json.loads(fields[0].json_value)
    assert "<li" in inner and "first" in inner and "second" in inner and "third" in inner


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_extract_eval_with_js_execution(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """``MODE_EVAL`` succeeds against the SeleniumBase adapter
    because its declared capability list includes ``js_execution``.
    The negative case — gate fires when ``js_execution`` is absent
    — is covered as a unit test on ``missing_capability_for_mode``
    in ``test_capabilities.py``."""

    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)
    element = _query_one(stub, session_id, "#title")

    response = stub.Extract(
        extraction_pb2.ExtractRequest(
            session_id=session_id,
            element=element,
            fields=[
                extraction_pb2.Field(
                    name="upper",
                    mode=extraction_pb2.Field.MODE_EVAL,
                    arg="arguments[0].textContent.toUpperCase()",
                ),
            ],
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not response.HasField("error"), response.error.message
    fields = list(response.values.fields)
    assert json.loads(fields[0].json_value) == "ELEMENTS PAGE"


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_extract_after_navigate_returns_invalid_argument(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """A ref allocated before a Navigate is invalidated by the
    navigation, regardless of whether the new page would also
    match the original selector. ADR-0010 §1, ADR-0015 §1 — the
    post-Navigate stale message lives on the registry's pre-flight
    check, not on the Selenium ``StaleElementReferenceException``
    path (which uses a different message; see ADR-0015 §2)."""

    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _open_elements_page(stub, local_http_server.base_url)
    element = _query_one(stub, session_id, "#title")

    nav2 = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/elements-2",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not nav2.HasField("error"), nav2.error.message

    response = stub.Extract(
        extraction_pb2.ExtractRequest(
            session_id=session_id,
            element=element,
            fields=[
                extraction_pb2.Field(name="title", mode=extraction_pb2.Field.MODE_TEXT_CONTENT),
            ],
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert response.HasField("error")
    assert response.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
    # Post-Navigate stale message — distinct from the
    # page-state-change message (ADR-0015 §2).
    assert "stale" in response.error.message.lower()
    assert "before a navigation" in response.error.message.lower()
