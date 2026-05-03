# `infra-services/`

Ancillary infrastructure services that the engine and adapters
consume to solve cross-cutting concerns: proxy management, CAPTCHA
solving, fingerprint rotation, session persistence, and rate-limit
coordination.

This directory is intentionally empty at v1alpha1 (Phase R6.6
reservation; refactor closed at R8.1). The category is
defined by **[ADR-0026 §3.5](../docs/adr/0026-platform-taxonomy.md)**;
the catalog of named slots and the admission criteria for materialising
each one live in **[ADR-0028](../docs/adr/0028-ancillary-infra-services-catalog.md)**.

Reserved slots (per ADR-0028 §4):

- `proxy-broker/` — proxy IP management (high conviction)
- `captcha-solver/` — CAPTCHA-solving routing (high conviction)
- `fingerprint-broker/` — browser-fingerprint rotation (probable)
- `session-store/` — cross-restart session persistence (probable)
- `rate-limit-broker/` — cross-tenant rate budgets (probable)

A slot becomes built code when (a) at least one consumer in
`engines/`, `adapters/`, `data-platform/`, or another `infra-services/`
has a concrete need, (b) at least two provider implementations are
designed, (c) the service's protocol lands in `proto/` in the same PR,
(d) per-language SDK clients land per [ADR-0027](../docs/adr/0027-sdk-strategy.md),
and (e) a deployment posture (Compose service, Helm placeholder) is
recorded in the same PR. See ADR-0028 §5 for the full gate.
