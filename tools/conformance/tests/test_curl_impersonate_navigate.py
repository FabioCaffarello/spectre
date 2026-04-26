"""Live ``Navigate`` conformance tests for the curl-impersonate adapter.

PR11 covers the happy path against the local HTTP fixture's
``/ok`` route plus a redirect against ``/redirect → /ok``.
ADR-0016 §1 records why this adapter spawns one curl subprocess
per Navigate; the conformance suite exercises that subprocess
end-to-end without mocking.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, errors_pb2

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

PER_TEST_DEADLINE_S = 30.0
GRPC_CALL_TIMEOUT_S = 20.0


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
def test_curl_impersonate_navigate_local_fixture_ok(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
    session_id = _initialize(stub)

    target = f"{local_http_server.base_url}/ok"
    response = stub.Navigate(
        driver_pb2.NavigateRequest(session_id=session_id, url=target),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert not response.HasField("error"), f"unexpected DriverError: {response.error.message!r}"
    assert response.status_code == 200, f"expected 200, got {response.status_code}"
    # No redirect; final_url should be the same as the request URL.
    assert response.final_url == target, (
        f"final_url should match the request URL on a 200, got {response.final_url!r}"
    )
    assert response.HasField("elapsed")


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_curl_impersonate_navigate_local_fixture_redirect(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """``/redirect`` returns 302 to ``/ok``; the adapter follows it
    by default (curl ``-L``) and reports the final URL.
    """

    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
    session_id = _initialize(stub)

    target = f"{local_http_server.base_url}/redirect"
    response = stub.Navigate(
        driver_pb2.NavigateRequest(session_id=session_id, url=target),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert not response.HasField("error"), f"unexpected DriverError: {response.error.message!r}"
    assert response.status_code == 200, (
        f"expected final 200 after redirect, got {response.status_code}"
    )
    assert response.final_url.endswith("/ok"), (
        f"final_url should land on /ok after redirect, got {response.final_url!r}"
    )


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_curl_impersonate_navigate_rejects_unknown_session(
    curl_impersonate_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """``Navigate`` with an id that ``Initialize`` did not produce
    returns ``CODE_INVALID_ARGUMENT``. ADR-0009 §2 / ADR-0016 (the
    strict ``session_id`` contract carries forward to every adapter).
    No curl subprocess starts in this test — the strict-id check
    fires before the fetcher is invoked.
    """

    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())
    response = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id="never-initialized",
            url=f"{local_http_server.base_url}/ok",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )
    assert response.HasField("error")
    assert response.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
