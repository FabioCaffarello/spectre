"""Conformance suite smoke test.

The real conformance suite exercises a driver via the Driver Protocol
and verifies handshake, capability declarations, error-envelope
shape, and per-capability behaviour. That work lands in Phase 1 of
the project roadmap.

Until then this module contains a single self-contained smoke test
that asserts the protocol-version target string the suite expects
matches the shape the schema commits to. The target is sourced from
the generated FileDescriptor (see ADR-0007), not declared as a
literal here.
"""

from __future__ import annotations

from spectre.driver.v1alpha1 import driver_pb2 as _driver_pb2

PROTOCOL_VERSION_TARGET: str = str(_driver_pb2.DESCRIPTOR.package)


def test_protocol_version_target_shape() -> None:
    parts = PROTOCOL_VERSION_TARGET.split(".")
    assert len(parts) == 3, "expected three dot-separated components"
    assert parts[0] == "spectre"
    assert parts[1] == "driver"
    assert parts[2].startswith("v"), "version segment must start with 'v'"
