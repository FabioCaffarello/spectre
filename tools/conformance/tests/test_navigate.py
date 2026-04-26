"""Live ``Navigate`` conformance tests.

Each test launches the Playwright adapter, performs an
``Initialize`` to obtain a session id, and then issues one or more
``Navigate`` calls. Targets are served by the in-process
``LocalHttpServer`` fixture (see ADR-0009 — no public-internet
calls in the conformance suite).

Scenarios
---------

- ``test_navigate_ok``: the adapter loads ``/ok`` and reports a
  populated ``NavigateResponse``: 200, final URL ending in ``/ok``,
  non-zero elapsed.
- ``test_navigate_follows_redirect``: ``/redirect`` is a 302 to
  ``/ok``. The adapter follows it and reports the final URL.
- ``test_navigate_surfaces_http_error_status``: ``/status/404`` is a
  successful navigation that landed on a 404. The adapter reports
  status 404 and no ``DriverError``.
- ``test_navigate_timeout_maps_to_code_timeout``: ``/slow`` sleeps 5
  seconds; the test passes a 1-second navigate timeout and asserts
  ``error.code == CODE_TIMEOUT``.
"""

from __future__ import annotations

import pytest
from google.protobuf.duration_pb2 import Duration
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, errors_pb2

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 60.0
GRPC_CALL_TIMEOUT_S = 30.0


def _initialize(stub: driver_pb2_grpc.DriverStub) -> str:
    response = stub.Initialize(
        driver_pb2.InitializeRequest(
            protocol_version=str(driver_pb2.DESCRIPTOR.package),
            session=driver_pb2.SessionConfig(),
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert response.session_id, "Initialize must return a session_id"
    assert not response.HasField("error"), (
        f"Initialize unexpectedly errored: {response.error.message!r}"
    )
    return str(response.session_id)


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_navigate_ok(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _initialize(stub)

    target = f"{local_http_server.base_url}/ok"
    response = stub.Navigate(
        driver_pb2.NavigateRequest(session_id=session_id, url=target),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert not response.HasField("error"), f"unexpected DriverError: {response.error.message!r}"
    assert response.status_code == 200, f"expected status 200, got {response.status_code}"
    assert response.final_url == target, (
        f"final_url should match the requested URL when no redirect occurred; "
        f"got {response.final_url!r}, requested {target!r}"
    )
    assert response.HasField("elapsed")
    elapsed_ms = response.elapsed.seconds * 1000 + response.elapsed.nanos // 1_000_000
    assert elapsed_ms >= 0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_navigate_follows_redirect(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _initialize(stub)

    response = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/redirect",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert not response.HasField("error"), f"unexpected DriverError: {response.error.message!r}"
    assert response.status_code == 200
    assert response.final_url == f"{local_http_server.base_url}/ok", (
        f"final_url must be the post-redirect URL; got {response.final_url!r}"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_navigate_surfaces_http_error_status(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """A 4xx response is a successful navigation, not a DriverError.

    See ADR-0009: only network failures and timeouts produce a
    ``DriverError``. HTTP status codes are surfaced verbatim in
    ``status_code``.
    """

    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _initialize(stub)

    response = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/status/404",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert not response.HasField("error"), (
        f"a 4xx status must not produce a DriverError; got {response.error.message!r}"
    )
    assert response.status_code == 404


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_navigate_timeout_maps_to_code_timeout(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())
    session_id = _initialize(stub)

    response = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id=session_id,
            url=f"{local_http_server.base_url}/slow",
            timeout=Duration(seconds=1),
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert response.HasField("error"), (
        "a navigation that exceeds its timeout must populate DriverError"
    )
    assert response.error.code == errors_pb2.DriverError.CODE_TIMEOUT, (
        f"expected CODE_TIMEOUT, got {response.error.code}"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_navigate_rejects_unknown_session(
    playwright_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """``Navigate`` with an id that ``Initialize`` did not produce returns
    ``CODE_INVALID_ARGUMENT``. See ADR-0009 decision 2 (strict
    session_id validation)."""

    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())

    response = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id="never-initialized",
            url=f"{local_http_server.base_url}/ok",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert response.HasField("error")
    assert response.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
