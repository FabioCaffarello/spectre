"""Live ``Close`` conformance tests for the SeleniumBase adapter.

PR10 promotes the SeleniumBase ``Close`` from the thin PR9
implementation (which existed only so the engine's executor
could finish navigate-only plans) to the full contract: strict
``session_id`` validation, idempotent rejection of unknown ids,
ElementRegistry teardown, and driver.quit() so no Chrome
process leaks past the call. See ADR-0010 §1 and ADR-0015 §1.

Scenarios
---------

- ``test_seleniumbase_close_happy_path``: Initialize → Navigate →
  Close succeeds and returns no ``DriverError``.
- ``test_seleniumbase_close_then_navigate_returns_invalid_argument``:
  a session that has been closed is rejected by subsequent
  ``Navigate`` calls with ``CODE_INVALID_ARGUMENT``.
- ``test_seleniumbase_close_unknown_session_id_returns_invalid_argument``:
  ``Close`` against an id that ``Initialize`` did not produce
  returns ``CODE_INVALID_ARGUMENT``.

Each test does its own ``Initialize`` to obtain a fresh
session; the adapter's lazy-launch contract means Chrome only
boots on the ``Navigate`` call itself, so the unknown-id Close
test does not require a browser.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, errors_pb2

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 90.0
GRPC_CALL_TIMEOUT_S = 60.0


def _initialize(stub: driver_pb2_grpc.DriverStub) -> str:
    response = stub.Initialize(
        driver_pb2.InitializeRequest(
            protocol_version=str(driver_pb2.DESCRIPTOR.package),
            session=driver_pb2.SessionConfig(),
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert response.session_id, "Initialize must return a session_id"
    assert not response.HasField("error")
    return str(response.session_id)


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_close_happy_path(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _initialize(stub)

    nav = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/ok",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not nav.HasField("error")

    close = stub.Close(
        driver_pb2.CloseRequest(session_id=session_id),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert not close.HasField("error"), (
        f"Close must succeed for an open session; got {close.error.message!r}"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_close_then_navigate_returns_invalid_argument(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _initialize(stub)

    stub.Close(
        driver_pb2.CloseRequest(session_id=session_id),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    nav = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/ok",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert nav.HasField("error"), "Navigate against a closed session must surface a DriverError"
    assert nav.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_close_unknown_session_id_returns_invalid_argument(
    seleniumbase_adapter: DriverHarness,
) -> None:
    """Close against an id that Initialize did not produce is
    rejected with CODE_INVALID_ARGUMENT — no Chrome launches because
    the strict-id check fires before any driver work."""

    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())

    response = stub.Close(
        driver_pb2.CloseRequest(session_id="never-initialized"),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert response.HasField("error")
    assert response.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
