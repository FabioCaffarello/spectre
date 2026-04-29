"""Live ``Initialize`` handshake conformance test for the curl-impersonate adapter.

The declared list has six entries — alphabetical:
``extract_attribute``, ``extract_html``, ``extract_text``,
``navigation``, ``query_css``, ``query_xpath``. The seven
capabilities the adapter does *not* declare are the canonical
artefact of the project's third capability divergence — see
ADR-0016 §5 for the framing and ADR-0017 §1 for the
``query_text`` / ``query_attribute`` omission rationale.
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

    # ADR-0014 §1 / ADR-0017 §1: declares exactly six names.
    expected = [
        "extract_attribute",
        "extract_html",
        "extract_text",
        "navigation",
        "query_css",
        "query_xpath",
    ]
    assert declared == expected, (
        f"curl-impersonate driver.yaml must declare exactly {expected}; got {declared}"
    )

    # Capabilities the curl-impersonate adapter will *never* declare
    # in v1alpha1.
    #
    #   - js_execution / extract_eval — no JavaScript engine.
    #   - screenshot_* — no rendering pipeline.
    #   - query_text / query_attribute — would violate the
    #     cross-driver semantic-equivalence contract from
    #     ADR-0017 §1 (Playwright's getByText vs goquery's
    #     :contains() are different searches under one name).
    forbidden = {
        "js_execution",
        "extract_eval",
        "screenshot_viewport",
        "screenshot_element",
        "screenshot_full_page",
        "query_text",
        "query_attribute",
    }
    assert not (forbidden & set(declared)), (
        "ADR-0016 §5 / ADR-0017 §1: curl-impersonate must not declare these capabilities; "
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
