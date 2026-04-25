---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Polyglot language selection

## Context and Problem Statement

Spectre is composed of components with very different responsibilities:
a parser/runtime for a DSL, a Kubernetes-native control plane, browser
adapters that wrap third-party libraries, and an intelligence layer
built on the LLM and computer-vision ecosystem. No single language is
the right tool for every component. Forcing one language onto every
component would either compromise performance, ecosystem fit, or
contributor reach.

## Decision Drivers

- Each component should run in the language whose ecosystem and
  runtime characteristics match the component's responsibilities.
- Cross-component communication uses the Driver Protocol (ADR-0001),
  so language choice does not leak across boundaries.
- The project should be approachable to contributors in each language
  community (Rust, Go, Python, TypeScript).
- Operational concerns (binary size, memory footprint, deploy
  artifacts) matter for components that run inside customer clusters.

## Considered Options

- **Option A — Single language across the stack** (e.g. all-Go or
  all-Rust).
- **Option B — Polyglot by responsibility**: each component picks the
  best-fit language; communication is via the Driver Protocol.
- **Option C — Two-language stack** (e.g. Rust core, TypeScript
  everything else).

## Decision Outcome

Chosen option: **Option B — Polyglot by responsibility**.

| Component                                  | Language          | Reasoning                                                                                                  |
|--------------------------------------------|-------------------|------------------------------------------------------------------------------------------------------------|
| DSL runtime and engine core                | Rust              | Performance-critical parsing; type safety; FFI via N-API and PyO3; WASM target for browser-side validation. |
| Control plane and orchestrator             | Go                | First-class Kubernetes ecosystem; mature gRPC; static binary; goroutines for concurrent scheduling.        |
| Playwright adapter                         | TypeScript        | Playwright's first-class language; CDP is JavaScript-native.                                               |
| SeleniumBase adapter                       | Python            | SeleniumBase is Python-only; native CDP-mode integration.                                                  |
| curl-impersonate adapter                   | Go (cgo wrapper)  | Wraps the C library; exposes a gRPC server; static deploy.                                                 |
| Intelligence layer (auto-heal, vision)     | Python            | LLM and computer-vision ecosystem unmatched in other languages.                                            |
| Compatibility core (TLS, HTTP/2 framing)   | Rust              | Bytes manipulation and FFI safety; no GC interference in hot paths.                                        |
| CLI                                        | Go                | Static cross-platform binary; trivial install.                                                             |
| SDKs                                       | TS, Python, Go    | Match the languages users actually build in.                                                               |

### Consequences

- Good, because each component leverages its ecosystem's strengths.
- Good, because contributors can work in their preferred language
  without learning the entire stack.
- Bad, because CI complexity is non-trivial: every language needs its
  own toolchain, lint, and test job. Mitigated by path-filtered CI
  jobs (only the affected languages run on a given PR).
- Bad, because release coordination across languages requires
  discipline. Mitigated by protocol versioning (ADR-0004) which gives
  each component a stable contract.
- Neutral, because adding a sixth or seventh language down the line
  (e.g. Kotlin SDK) is straightforward — the protocol absorbs the
  diversity.

### Confirmation

Each component's CI job is green and demonstrates that:

1. The component compiles cleanly with stable toolchain versions.
2. Format, lint, type-check, and test gates run in under five minutes
   per language.
3. A change to one component's source does not trigger rebuilds in
   unaffected languages.

## Pros and Cons of the Options

### Option A — Single language

- Good, because the simplest tooling story.
- Bad, because it forces compromises: a Rust-only stack gives up
  Kubernetes ergonomics; an all-Go stack gives up Rust's safety in
  the engine and Python's ML libraries in the intelligence layer.
- Bad, because it shrinks the contributor pool by language preference.

### Option C — Two languages

- Good, because it limits CI complexity.
- Bad, because the chosen pair inevitably mismatches at least one
  component (e.g. SeleniumBase has no TS or Rust port).

## More Information

- Related: [ADR-0001 Driver protocol as architectural primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md).
