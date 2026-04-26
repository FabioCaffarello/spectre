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
| 0002  | [Polyglot language selection](0002-polyglot-language-selection.md)            | accepted |
| 0003  | [Schema-transport separation](0003-schema-transport-separation.md)            | accepted |
| 0004  | [Protocol versioning strategy](0004-protocol-versioning-strategy.md)          | accepted |
| 0005  | [Licensing (Apache 2.0)](0005-licensing.md)                                   | accepted |
| 0006  | [Build orchestration (Just)](0006-build-orchestration.md)                     | accepted |
| 0007  | [Protocol code generation](0007-protocol-code-generation.md)                  | accepted |
| 0008  | [Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md) | accepted |
| 0009  | [Navigate, session lifecycle, and the driver error mapping](0009-navigate-and-session-lifecycle.md) | accepted |

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
