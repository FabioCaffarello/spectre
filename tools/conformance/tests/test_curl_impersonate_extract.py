"""Live ``Extract`` conformance tests for the curl-impersonate adapter.

Extract is implemented with five field modes (TEXT_CONTENT,
INNER_TEXT, INNER_HTML, OUTER_HTML, ATTR) and the runtime
capability gate from ADR-0010 §3 firing on MODE_EVAL. The
headline test of this file is
``test_curl_impersonate_extract_eval_returns_capability_missing``
— the first conformance test in the entire suite that exercises
the runtime capability gate's negative path. Playwright and
SeleniumBase both declare ``js_execution`` so the gate has lived
as a positive test on those drivers and a unit-tested invariant
on the negative side. curl-impersonate finally provides the test
surface where the gate fires against a real adapter.

Scenarios
---------

- ``test_curl_impersonate_extract_text_content``: read the
  heading's ``MODE_TEXT_CONTENT``.
- ``test_curl_impersonate_extract_attribute``: read the link's
  ``href`` via ``MODE_ATTR``.
- ``test_curl_impersonate_extract_eval_returns_capability_missing``:
  the runtime capability gate fires; entire request fails with
  ``CODE_CAPABILITY_MISSING``.

ADR-0017 §5 records the field-mode mapping and the MODE_INNER_TEXT
approximation; ADR-0017 §1 records the semantic-equivalence
contract that justifies the absent ``js_execution`` capability.
"""

from __future__ import annotations

import json

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, errors_pb2, extraction_pb2

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
def test_curl_impersonate_extract_text_content(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
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
def test_curl_impersonate_extract_attribute(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
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
def test_curl_impersonate_extract_eval_returns_capability_missing(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """Headline test for ADR-0017 §5: the curl-impersonate adapter
    omits ``js_execution``, so any ``MODE_EVAL`` field forces the
    runtime gate from ADR-0010 §3 to fire. The whole request fails
    with ``CODE_CAPABILITY_MISSING`` (atomic semantics — no fields
    are read on the way out). This is the first conformance test
    in the entire suite that exercises the gate's negative path."""

    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
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
    assert response.HasField("error"), "MODE_EVAL must surface a DriverError"
    assert response.error.code == errors_pb2.DriverError.CODE_CAPABILITY_MISSING
    assert "js_execution" in response.error.message, (
        f"rejection message must reference js_execution; got {response.error.message!r}"
    )
    # ADR-0010 §3's atomic-fail-the-whole-request contract: when
    # the gate fires, no values are returned at all.
    assert not list(response.values.fields)
