"""Live ``Navigate`` conformance tests for the SeleniumBase adapter.

PR9 covers the happy path against the local HTTP fixture's ``/ok``
route plus the strict ``session_id`` validation contract from
ADR-0009 §2 (carried over to SeleniumBase by ADR-0014 §2). The
richer Navigate scenarios (redirect, 4xx, timeout) land in PR10
once Extract gives the suite a way to read post-navigation state
and once the test surface justifies running multiple Chrome
launches per CI job.

Each test does its own ``Initialize`` to obtain a fresh session;
the adapter's lazy-launch contract means Chrome only boots on the
``Navigate`` call itself.
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
    assert not response.HasField("error"), (
        f"Initialize unexpectedly errored: {response.error.message!r}"
    )
    return str(response.session_id)


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_navigate_local_fixture_ok(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())
    session_id = _initialize(stub)

    target = f"{local_http_server.base_url}/ok"
    response = stub.Navigate(
        driver_pb2.NavigateRequest(session_id=session_id, url=target),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert not response.HasField("error"), f"unexpected DriverError: {response.error.message!r}"
    # Selenium does not surface HTTP status directly; the adapter
    # probes the PerformanceNavigationTiming API and returns 0 when
    # unavailable. Accept either 0 or 200 for the happy path.
    assert response.status_code in (0, 200), (
        f"expected status 0 (timing API unavailable) or 200 (timing API populated), "
        f"got {response.status_code}"
    )
    # Selenium normalises URLs (trailing slashes, etc.); compare on
    # path containment rather than exact equality.
    assert "/ok" in response.final_url, f"final_url should land on /ok; got {response.final_url!r}"
    assert response.HasField("elapsed")


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_navigate_rejects_unknown_session(
    seleniumbase_adapter: DriverHarness,
    local_http_server: LocalHttpServer,
) -> None:
    """``Navigate`` with an id that ``Initialize`` did not produce returns
    ``CODE_INVALID_ARGUMENT``. ADR-0014 §2 carries the strict
    ``session_id`` contract from ADR-0009 forward to SeleniumBase.

    No Chrome process launches in this test — the strict-id check
    fires before the lazy driver factory runs.
    """

    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())

    response = stub.Navigate(
        driver_pb2.NavigateRequest(
            session_id="never-initialized",
            url=f"{local_http_server.base_url}/ok",
        ),
        timeout=GRPC_CALL_TIMEOUT_S,
    )

    assert response.HasField("error")
    assert response.error.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
