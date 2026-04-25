"""Negative-path conformance: unimplemented RPCs must report cleanly.

PR3 implements ``Initialize`` only. The five remaining unary RPCs in
the v1alpha1 service must respond with the gRPC ``UNIMPLEMENTED``
status rather than hanging, crashing the adapter, or returning an
empty success response. This test cements that contract so PR4 cannot
silently regress it when ``Navigate`` lands.
"""

from __future__ import annotations

import grpc
import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc

from spectre_conformance.harness import DriverHarness

PER_TEST_DEADLINE_S = 30.0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_navigate_returns_unimplemented(
    playwright_adapter: DriverHarness,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())

    request = driver_pb2.NavigateRequest(
        session_id="conformance",
        url="https://example.invalid/",
    )

    with pytest.raises(grpc.RpcError) as excinfo:
        stub.Navigate(request, timeout=5.0)

    rpc_error = excinfo.value
    assert isinstance(rpc_error, grpc.Call)
    assert rpc_error.code() == grpc.StatusCode.UNIMPLEMENTED
