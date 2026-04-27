"""Spectre Driver Protocol conformance toolkit.

Public surface:

* :class:`spectre_conformance.harness.DriverHarness` — launches a
  driver subprocess from a ``driver.yaml``, allocates a free
  localhost TCP port and injects it via ``SPECTRE_ADAPTER_GRPC_PORT``,
  polls the gRPC standard health check until ``SERVING``, exposes
  a gRPC channel, and tears the subprocess down on exit. The R2.2
  refactor swapped the harness's transport from Unix domain sockets
  to TCP (ADR-0021 + ADR-0022); the readiness contract moved from
  a stdout banner to ``grpc.health.v1.Health.Check``.
* :mod:`spectre_conformance.capabilities` — canonical capability-name
  constants drivers may declare at handshake.

See ``docs/adr/0008-driver-handshake-and-conformance-harness.md``
for the original handshake design and
``docs/adr/0022-tcp-grpc-transport.md`` for the TCP-era contract.
"""

from __future__ import annotations

from spectre_conformance.harness import DriverHarness

__all__ = ["DriverHarness"]
