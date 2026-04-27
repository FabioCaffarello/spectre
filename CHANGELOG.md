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

### Changed

- ADR-0008 (UDS transport), ADR-0009 (session lifecycle),
  ADR-0019 (subprocess-in-pod) carry "Update (R1.1, ADR-0020)"
  notes recording per-section supersession. ADR-0013 (CLI as
  engine binary) is superseded in full. The ADR index reflects
  these changes.
