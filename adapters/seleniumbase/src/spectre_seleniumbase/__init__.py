"""Spectre SeleniumBase driver adapter."""

from __future__ import annotations

from spectre.driver.v1alpha1 import driver_pb2 as _driver_pb2

# Sourced from the generated FileDescriptor's package directive
# (`spectre.driver.v1alpha1`). See ADR-0007.
PROTOCOL_VERSION: str = str(_driver_pb2.DESCRIPTOR.package)
__version__: str = "0.1.0a0"

__all__ = ["PROTOCOL_VERSION", "__version__"]
