"""Live ``Initialize`` handshake conformance test for the SeleniumBase adapter.

Mirrors ``test_initialize.py`` (which targets the Playwright adapter)
but asserts against SeleniumBase's narrower v1alpha1 capability list.
ADR-0014 §1 records the deliberate "declared = tested" rule that
keeps PR9's manifest at ``["navigation"]``.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc

from spectre_conformance.harness import DriverHarness

PROTOCOL_VERSION_TARGET: str = str(driver_pb2.DESCRIPTOR.package)
PER_TEST_DEADLINE_S = 30.0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_initialize_returns_a_session(
    seleniumbase_adapter: DriverHarness,
    seleniumbase_manifest: dict[str, object],
) -> None:
    stub = driver_pb2_grpc.DriverStub(seleniumbase_adapter.dial())

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

    declared_raw = seleniumbase_manifest.get("capabilities") or []
    assert isinstance(declared_raw, list), (
        f"driver.yaml `capabilities` must be a list, got {type(declared_raw).__name__}"
    )
    declared: list[str] = [str(name) for name in declared_raw]
    # ADR-0014 §1: PR9 declares ["navigation"] only. Future PRs
    # extend this list as their RPCs and conformance tests land.
    assert declared == ["navigation"], (
        f"PR9's seleniumbase driver.yaml must declare exactly ['navigation']; got {declared}"
    )
    assert list(response.capabilities.names) == declared, (
        "Capabilities returned by Initialize must match driver.yaml byte-for-byte. "
        f"Returned: {list(response.capabilities.names)}, declared: {declared}"
    )

    assert response.capabilities.driver_version, "driver_version must be populated"
    assert response.capabilities.runtime_version, "runtime_version must be populated"
    assert "seleniumbase" in response.capabilities.runtime_version, (
        "runtime_version should identify the SeleniumBase runtime; "
        f"got {response.capabilities.runtime_version!r}"
    )
