"""Live ``Initialize`` handshake conformance test.

This test launches the Playwright adapter as a subprocess on a
per-test Unix domain socket, dials it as a gRPC client, sends an
``InitializeRequest``, and validates the ``InitializeResponse``.

Capability declaration: the response's ``capabilities.names`` must
match the list in the adapter's ``driver.yaml`` byte-for-byte.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc

from spectre_conformance.harness import DriverHarness

PROTOCOL_VERSION_TARGET: str = str(driver_pb2.DESCRIPTOR.package)
PER_TEST_DEADLINE_S = 30.0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_initialize_returns_a_session(
    playwright_adapter: DriverHarness,
    playwright_manifest: dict[str, object],
) -> None:
    stub = driver_pb2_grpc.DriverStub(playwright_adapter.dial())

    request = driver_pb2.InitializeRequest(
        protocol_version=PROTOCOL_VERSION_TARGET,
        session=driver_pb2.SessionConfig(),
        requested_capabilities=[],
    )
    response = stub.Initialize(request, timeout=10.0)

    assert response.session_id, "session_id must be populated"
    assert response.HasField("capabilities"), (
        "capabilities envelope must be set even when names is empty"
    )
    assert not response.HasField("error"), f"unexpected error: {response.error.message!r}"

    declared_raw = playwright_manifest.get("capabilities") or []
    assert isinstance(declared_raw, list), (
        f"driver.yaml `capabilities` must be a list, got {type(declared_raw).__name__}"
    )
    declared: list[str] = [str(name) for name in declared_raw]
    assert list(response.capabilities.names) == declared, (
        "Capabilities returned by Initialize must match driver.yaml. "
        f"Returned: {list(response.capabilities.names)}, declared: {declared}"
    )

    assert response.capabilities.driver_version, "driver_version must be populated"
    assert response.capabilities.runtime_version, "runtime_version must be populated"


def test_protocol_version_target_shape() -> None:
    """The static target string keeps the v1alpha1 package shape."""

    parts = PROTOCOL_VERSION_TARGET.split(".")
    assert len(parts) == 3, "expected three dot-separated components"
    assert parts[0] == "spectre"
    assert parts[1] == "driver"
    assert parts[2].startswith("v"), "version segment must start with 'v'"
