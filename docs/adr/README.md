# Architecture Decision Records

This directory captures the architectural decisions that shape Spectre.
Each record documents one decision, its context, and its consequences.

## Format

Spectre uses [MADR 4.0](https://adr.github.io/madr/) for its records.
The empty template lives at [`0000-template.md`](0000-template.md).

## Status flow

A decision moves through three states:

- **proposed** — under discussion, not yet binding.
- **accepted** — binding; the file becomes immutable except for the
  `status` field.
- **superseded** — replaced by a later ADR. The replacing ADR's number
  goes in a `superseded-by` field, and the new ADR cites the predecessor
  in its preamble.

## Index

| ID    | Title                                                                         | Status   |
|-------|-------------------------------------------------------------------------------|----------|
| 0001  | [Driver protocol as architectural primitive](0001-driver-protocol-as-architectural-primitive.md) | accepted |
| 0002  | [Polyglot language selection](0002-polyglot-language-selection.md)            | accepted (CLI row superseded by ADR-0013) |
| 0003  | [Schema-transport separation](0003-schema-transport-separation.md)            | accepted |
| 0004  | [Protocol versioning strategy](0004-protocol-versioning-strategy.md)          | accepted |
| 0005  | [Licensing (Apache 2.0)](0005-licensing.md)                                   | accepted |
| 0006  | [Build orchestration (Just)](0006-build-orchestration.md)                     | accepted |
| 0007  | [Protocol code generation](0007-protocol-code-generation.md)                  | accepted |
| 0008  | [Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md) | accepted (§1 + §3 preserved; §2 + §4 superseded by ADR-0022 / ADR-0021 §6 in R2; implementation in R2.2 / R2.3) |
| 0009  | [Navigate, session lifecycle, and the driver error mapping](0009-navigate-and-session-lifecycle.md) | accepted (session lifecycle revisited under ADR-0023 in R4) |
| 0010  | [Element lifecycle, capability granularity, and selector mapping](0010-element-lifecycle-and-capability-gating.md) | accepted |
| 0011  | [Screenshot RPC, scope mapping, and payload boundaries](0011-screenshot-rpc-and-payload-boundaries.md) | accepted |
| 0012  | [Engine DSL surface, planner architecture, and execution pipeline](0012-engine-dsl-and-execution-pipeline.md) | accepted (§§1-3, 5, 6 preserved; §4 launcher contract superseded by ADR-0021 §5 / ADR-0022 §1 in R2.3) |
| 0013  | [CLI as engine binary](0013-cli-as-engine-binary.md) (supersedes ADR-0002 CLI row) | superseded by ADR-0020 |
| 0014  | [SeleniumBase adapter and cross-language conformance](0014-seleniumbase-adapter-and-cross-language-conformance.md) | accepted |
| 0015  | [SeleniumBase element lifecycle and screenshot coverage](0015-seleniumbase-element-lifecycle-and-screenshot-coverage.md) | accepted |
| 0016  | [curl-impersonate adapter and third-runtime divergence](0016-curl-impersonate-adapter-and-third-runtime-divergence.md) | accepted |
| 0017  | [curl-impersonate extraction and final capability divergence](0017-curl-impersonate-extraction-and-final-capability-divergence.md) | accepted |
| 0018  | [Devcontainer and engine image (Phase 2.5 kickoff)](0018-devcontainer-and-engine-image.md) | accepted (revisited under ADR-0025 in R6) |
| 0019  | [Control plane architecture and ScrapeJob CRD (Phase 3 kickoff)](0019-control-plane-architecture-and-scrapejob-crd.md) | accepted (§3 subprocess-in-pod superseded by ADR-0020; §5 `JobRunner` evolved with `jobID` + `outputSinkKind` in R4.2 — abstraction preserved; §4 gains Postgres restart-recovery in R4.2) |
| 0020  | [Microservices architecture supersession](0020-microservices-architecture-supersession.md) | accepted |
| 0021  | [Service discovery](0021-service-discovery.md)                                | accepted |
| 0022  | [TCP / gRPC transport](0022-tcp-grpc-transport.md)                            | accepted (supersedes ADR-0008 §2) |
| 0023  | [Stateful services architecture](0023-stateful-services-architecture.md)      | accepted |

## When to write an ADR

Open an ADR when:

- Adding a new component, language, or transport.
- Changing or extending the Driver Protocol.
- Replacing a load-bearing tool (build system, package manager, CI).
- Resolving a non-obvious trade-off that future contributors will want
  to understand.

You do not need an ADR for code style, file organization details, or
configuration values. When in doubt, open an issue first.

## How to add one

1. Copy `0000-template.md` to `NNNN-short-title.md` using the next free
   number (zero-padded to four digits).
2. Fill out every section. Keep the file self-contained — a new
   contributor reading only the ADRs should understand the project's
   shape.
3. Open a pull request labelled `adr`. Discussion happens on the PR.
4. Once accepted, set `status: accepted` and merge.
5. Update the index above.
