"""Live ``Initialize`` handshake conformance test for the curl-impersonate adapter.

PR11 declares the narrowest capability list yet — exactly
``["navigation"]``. ADR-0016 §5 records the framing as the
strongest validation of the protocol's planning surface to date:
three drivers spanning three languages, each declaring an
honestly different capability set.
"""

from __future__ import annotations

import pytest
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc

from spectre_conformance.harness import DriverHarness

PROTOCOL_VERSION_TARGET: str = str(driver_pb2.DESCRIPTOR.package)
PER_TEST_DEADLINE_S = 30.0


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_curl_impersonate_initialize_returns_a_session(
    curl_impersonate_adapter: DriverHarness,
    curl_impersonate_manifest: dict[str, object],
) -> None:
    stub = driver_pb2_grpc.DriverStub(curl_impersonate_adapter.dial())

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

    declared_raw = curl_impersonate_manifest.get("capabilities") or []
    assert isinstance(declared_raw, list), (
        f"driver.yaml `capabilities` must be a list, got {type(declared_raw).__name__}"
    )
    declared: list[str] = [str(name) for name in declared_raw]

    # ADR-0014 §1 / ADR-0016 §5: PR11 declares exactly one name.
    expected = ["navigation"]
    assert declared == expected, (
        f"PR11 curl-impersonate driver.yaml must declare exactly {expected}; got {declared}"
    )

    # Capabilities the curl-impersonate adapter will *never* declare
    # in v1alpha1 (no JS engine, no DOM, no rendering pipeline).
    forbidden = {
        "js_execution",
        "extract_eval",
        "screenshot_viewport",
        "screenshot_element",
        "screenshot_full_page",
    }
    assert not (forbidden & set(declared)), (
        "ADR-0016 §5: curl-impersonate must not declare browser-runtime capabilities; "
        f"found {forbidden & set(declared)}"
    )

    # Byte-for-byte equality between manifest and runtime envelope —
    # the same assertion ADR-0008 introduced and ADR-0014 §1 made
    # the load-bearing test for cross-driver consistency.
    assert list(response.capabilities.names) == declared, (
        "Capabilities returned by Initialize must match driver.yaml byte-for-byte. "
        f"Returned: {list(response.capabilities.names)}, declared: {declared}"
    )

    assert response.capabilities.driver_version, "driver_version must be populated"
    assert response.capabilities.runtime_version, "runtime_version must be populated"
    assert "curl-impersonate" in response.capabilities.runtime_version, (
        "runtime_version should identify the curl-impersonate runtime; "
        f"got {response.capabilities.runtime_version!r}"
    )
