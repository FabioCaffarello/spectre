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
| 0007  | [Protocol code generation](0007-protocol-code-generation.md)                  | accepted (partially evolved by ADR-0027 — §2 / §3 carry an R6.6 evolution note) |
| 0008  | [Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md) | accepted (§1 + §3 preserved; §2 + §4 superseded by ADR-0022 / ADR-0021 §6 in R2; implementation in R2.2 / R2.3) |
| 0009  | [Navigate, session lifecycle, and the driver error mapping](0009-navigate-and-session-lifecycle.md) | accepted (session lifecycle revisited under ADR-0023 in R4) |
| 0010  | [Element lifecycle, capability granularity, and selector mapping](0010-element-lifecycle-and-capability-gating.md) | accepted |
| 0011  | [Screenshot RPC, scope mapping, and payload boundaries](0011-screenshot-rpc-and-payload-boundaries.md) | accepted |
| 0012  | [Engine DSL surface, planner architecture, and execution pipeline](0012-engine-dsl-and-execution-pipeline.md) | accepted (§§1-3, 5, 6 preserved; §4 launcher contract superseded by ADR-0021 §5 / ADR-0022 §1 in R2.3) |
| 0013  | [CLI as engine binary](0013-cli-as-engine-binary.md) (supersedes ADR-0002 CLI row) | superseded by ADR-0019 + ADR-0020 (R6.6 status refresh; CLI source deleted in R3.1) |
| 0014  | [SeleniumBase adapter and cross-language conformance](0014-seleniumbase-adapter-and-cross-language-conformance.md) | accepted |
| 0015  | [SeleniumBase element lifecycle and screenshot coverage](0015-seleniumbase-element-lifecycle-and-screenshot-coverage.md) | accepted |
| 0016  | [curl-impersonate adapter and third-runtime divergence](0016-curl-impersonate-adapter-and-third-runtime-divergence.md) | accepted |
| 0017  | [curl-impersonate extraction and final capability divergence](0017-curl-impersonate-extraction-and-final-capability-divergence.md) | accepted |
| 0018  | [Devcontainer and engine image (Phase 2.5 kickoff)](0018-devcontainer-and-engine-image.md) | accepted (partially superseded; see status notes in §3, §4 and §5 — §3a R6.3 evolution adds Docker-in-Docker) |
| 0019  | [Control plane architecture and ScrapeJob CRD (Phase 3 kickoff)](0019-control-plane-architecture-and-scrapejob-crd.md) | accepted (§3 subprocess-in-pod superseded by ADR-0020; §5 `JobRunner` evolved through R4.2 / R4.4 / R5.1 — abstraction preserved, struct-refactor deferred to v1alpha2; §4 gains Postgres restart-recovery in R4.2; §6 schema-only stub retired in R5.1) |
| 0020  | [Microservices architecture supersession](0020-microservices-architecture-supersession.md) | accepted |
| 0021  | [Service discovery](0021-service-discovery.md)                                | accepted |
| 0022  | [TCP / gRPC transport](0022-tcp-grpc-transport.md)                            | accepted (supersedes ADR-0008 §2) |
| 0023  | [Stateful services architecture](0023-stateful-services-architecture.md)      | accepted (§5 gains R4.3 addendum on `adapter_instance_id`; §6 admission-gating asymmetry refined by ADR-0024 §5 in R5.1) |
| 0024  | [Output sinks (S3 and HTTP webhook)](0024-output-sinks.md)                    | accepted |
| 0025  | [Compose stack (application services + profile-based topology)](0025-compose-stack.md) | accepted (§6 + §9 R6.3 update — operator-in-Compose deferral resolved; closes Phase R6) |
| 0026  | [Platform taxonomy and module categories](0026-platform-taxonomy.md) | accepted (Phase R6.6 — fundação) |
| 0027  | [Multi-language SDK strategy](0027-sdk-strategy.md)                  | accepted (Phase R6.6 — evolves ADR-0007 §2/§3) |
| 0028  | [Ancillary infra services catalog](0028-ancillary-infra-services-catalog.md) | accepted (Phase R6.6 — 5 named slots) |
| 0029  | [Data platform and lake DSLs](0029-data-platform-and-lake-dsls.md)   | accepted (Phase R6.6 — closes phase prologue; restructure PR follows) |

## A note on directory paths in older ADRs

Several accepted ADRs (notably 0002, 0007, 0012, 0013, 0015, 0016,
0018, 0019, 0022, 0023, 0024, 0025) reference source paths under
`core/engine/` and `core/control-plane/`. As of Phase R6.6
([ADR-0026](0026-platform-taxonomy.md) §4), `core/` was dissolved:
those modules now live at `engines/engine/` and
`operators/control-plane/`. The Go module path of the operator also
changed (`github.com/FabioCaffarello/spectre/core/control-plane` →
`...spectre/operators/control-plane`).

ADR text is immutable per the status flow above; the older paths are
a historical record of where the code lived when each decision was
recorded. Future ADRs cite the post-R6.6 paths.

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
