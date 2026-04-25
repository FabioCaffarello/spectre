"""Spectre Driver Protocol conformance toolkit.

Public surface:

* :class:`spectre_conformance.harness.DriverHarness` — launches a
  driver subprocess from a ``driver.yaml``, watches for readiness,
  exposes a gRPC channel, and tears it down on exit.
* :mod:`spectre_conformance.capabilities` — canonical capability-name
  constants drivers may declare at handshake.

The harness is intentionally minimal in PR3. It is the seed the
SeleniumBase and curl-impersonate conformance tests will reuse. See
``docs/adr/0008-driver-handshake-and-conformance-harness.md``.
"""

from __future__ import annotations

from spectre_conformance.harness import DriverHarness

__all__ = ["DriverHarness"]
