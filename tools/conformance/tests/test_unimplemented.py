"""Negative-path conformance: unimplemented RPCs must report cleanly.

PR4 implements ``Initialize`` and ``Navigate``. The four remaining
unary RPCs (``Query``, ``Extract``, ``Screenshot``, ``Close``) must
respond with the gRPC ``UNIMPLEMENTED`` status rather than hanging,
crashing the adapter, or returning an empty success response. This
test cements that contract so future PRs cannot silently regress it
when the corresponding RPC lands. ``Query`` is the canary chosen for
PR4; PR5 will rotate it to a different unimplemented RPC once
``Query`` itself is implemented.
"""

from __future__ import annotations

import grpc
import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, extraction_pb2

from spectre_conformance.harness import DriverHarness

PER_TEST_DEADLINE_S = 30.0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_query_returns_unimplemented(
    playwright_adapter: DriverHarness,
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())

    request = extraction_pb2.QueryRequest(
        session_id="conformance",
        selector="body",
        kind=extraction_pb2.SELECTOR_KIND_CSS,
    )

    with pytest.raises(grpc.RpcError) as excinfo:
        stub.Query(request, timeout=5.0)

    rpc_error = excinfo.value
    assert isinstance(rpc_error, grpc.Call)
    assert rpc_error.code() == grpc.StatusCode.UNIMPLEMENTED


# Keep a reference to driver_pb2 so the import stays meaningful for
# tooling that scans the file for protocol coverage.
_ = driver_pb2.DESCRIPTOR
