# Changelog — proxy-broker

Per-service changelog per
[ADR-0036 §5.6](../../docs/adr/0036-microservices-catalog-expansion.md).
Mirrors the platform's `[Unreleased]` window during v1alpha2;
independent semver split is a future possibility, not a
W5.1 concern.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this service adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **First inhabitant of `infra-services/` (W5.1).** Slot 1
  of the ADR-0036 §3.1 catalog materialised. Surface per
  ADR-0028 §4.1: `Acquire` / `AcquireBatch` / `Release` /
  `ReportFailure` / `BudgetStatus` over `spectre.proxy.v1alpha1`.
  Redis-backed state per ADR-0039 §3.1 (cooldown table,
  per-(provider, domain) ban sliding window, per-proxy health
  scores, lease tracking). Two-provider design per ADR-0028
  §5 criterion #2: BrightData wired end-to-end as the real
  provider; stub as second provider satisfying the
  abstraction-proof gate (TODO(W5.1b) replaces with real
  second provider once Wave 4 pilot data informs the pick).
  mTLS via the W3.3 env-trio pattern; plaintext default
  preserves the v1alpha1 dial path. OTel + Prometheus
  `/metrics` sidecar + structured slog JSON per ADR-0031 §3.
  Multi-arch (amd64 + arm64) per ADR-0018 from the start.
