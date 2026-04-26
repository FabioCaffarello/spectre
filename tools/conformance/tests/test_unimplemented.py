"""Negative-path conformance: unimplemented RPCs must report cleanly.

PR4 implemented ``Initialize`` and ``Navigate``; PR5 added
``Close``, ``Query``, and ``Extract``. ``Screenshot`` is the only
remaining unary RPC in v1alpha1 that has not been implemented;
this test cements that it returns ``UNIMPLEMENTED`` rather than
hanging, crashing the adapter, or returning an empty success
response. Future PR6 cannot silently regress the contract when
``Screenshot`` lands — the test rotates again at that point.
"""

from __future__ import annotations

import grpc
import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc

from spectre_conformance.harness import DriverHarness

PER_TEST_DEADLINE_S = 30.0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_screenshot_returns_unimplemented(
    playwright_adapter: DriverHarness,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())

    request = driver_pb2.ScreenshotRequest(
        session_id="conformance",
        scope=driver_pb2.SCREENSHOT_SCOPE_VIEWPORT,
        format=driver_pb2.SCREENSHOT_FORMAT_PNG,
    )

    with pytest.raises(grpc.RpcError) as excinfo:
        stub.Screenshot(request, timeout=5.0)

    rpc_error = excinfo.value
    assert isinstance(rpc_error, grpc.Call)
    assert rpc_error.code() == grpc.StatusCode.UNIMPLEMENTED
