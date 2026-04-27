# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Architectural commitment to a microservices refactor recorded
  in [ADR-0020](docs/adr/0020-microservices-architecture-supersession.md).
  No code changes in this release; subsequent phase PRs (R2–R8)
  deliver the implementation. Live progress is tracked in
  [`docs/refactoring-status.md`](docs/refactoring-status.md).
- Initial repository structure and foundational documents
- Driver Protocol skeleton at v1alpha1
- Skeleton implementations for three reference adapters
- gRPC standard health check (`grpc.health.v1.Health`) registration
  in every adapter (R2.2, ADR-0021 §6). The conformance harness
  polls `Check` until SERVING as the readiness signal; production
  deployments wire the same endpoint into Compose / Kubernetes
  readiness probes.
- `proto/grpc/health/v1/health.proto` vendored verbatim from the
  canonical gRPC source so the Playwright TS adapter can produce
  Connect-RPC bindings; the Go and Python adapters consume their
  ecosystem libraries (`google.golang.org/grpc/health` and
  `grpcio-health-checking`) directly. Buf lint exempts the
  vendored file so it stays byte-identical to the upstream source.
- `KNOWN_BREAKAGE.md` at the repo root documents the deliberate
  engine ↔ adapter transport mismatch between R2.2 and R2.3
  merges. R2.3's first commit deletes the file.

### Changed

- ADR-0008 (UDS transport), ADR-0009 (session lifecycle),
  ADR-0019 (subprocess-in-pod) carry "Update (R1.1, ADR-0020)"
  notes recording per-section supersession. ADR-0013 (CLI as
  engine binary) is superseded in full. The ADR index reflects
  these changes.
- Adapter transport for all three reference adapters (Playwright,
  SeleniumBase, curl-impersonate) and the conformance harness
  switched from Unix-domain-socket gRPC to TCP gRPC (R2.2,
  ADR-0021 + ADR-0022). Each adapter binds `0.0.0.0:<port>` where
  the port is read from `SPECTRE_ADAPTER_GRPC_PORT`; the
  conformance harness allocates a free localhost port and injects
  it into the subprocess env. The wire-level driver protocol
  contract is unchanged. The 13 / 12 / 6 capability lists for
  Playwright / SeleniumBase / curl-impersonate are preserved
  byte-for-byte.
- `driver.yaml` schema for every adapter retires the `transports:`
  block; the spawn directive moves into `runtime.command`
  (transitional — R6.2's Compose stack supersedes the
  harness-spawn flow entirely). The conformance harness reads
  `runtime.command` rather than `transports[0].command`.
- `just <adapter>-run` recipes take a port argument (default
  matching the canonical port from ADR-0021 §4) instead of a
  socket path. The recipes survive R2.2 as developer
  conveniences and are scheduled for retirement in R6.2.
- `curl-imp-lint` pinned to `GOTOOLCHAIN=go1.25.3` mirroring the
  existing `cp-lint` pattern; without the pin `golangci-lint
  v2.8.0` (built with go1.25.5) panics on the Go 1.26 stdlib
  loaded by an unconstrained toolchain.
- `proto/buf.gen.yaml` pins the python plugins to revisions that
  emit gencode compatible with the protobuf 6.x runtime
  (`grpcio-health-checking==1.80.0` caps protobuf at <7.0.0).

### Removed

- The Unix-domain-socket transport across all three reference
  adapters and the conformance harness (R2.2). No fallback path
  survives — strategy prompt §2.2 forbids "temporary" legacy
  fallbacks during refactor. The retired surface includes
  `resolveSocketPath` / `resolve_socket_path` resolvers, the
  `--socket` CLI flag, the `SPECTRE_DRIVER_SOCKET` env var, the
  stale-socket unlink logic, the `ready unix:<path>` stdout
  readiness banner, and the `transports:` block in every
  `driver.yaml`.
