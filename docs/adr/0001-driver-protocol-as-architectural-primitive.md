---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Driver protocol as architectural primitive

## Context and Problem Statement

Existing browser-automation frameworks (Playwright, Selenium,
Puppeteer, SeleniumBase) wrap a single underlying runtime. Code written
against one is not portable: switching runtimes means rewriting
selectors, session handling, deployment recipes, and any extension
logic. The browser-automation landscape moves quickly — new runtimes
appear (Patchright, nodriver, Rebrowser-patches, native CDP libraries),
and adopters cannot move with the ecosystem without rewriting their
extraction codebase.

A framework that wraps yet another runtime would inherit the same
trap. We need an architectural primitive that decouples user intent
(what to extract, where, with which configuration) from the runtime
that executes the work.

## Decision Drivers

- Extraction logic written today should survive a runtime change
  tomorrow.
- New runtimes should join the ecosystem without forking a framework.
- The project must be implementable across multiple languages (the
  best runtime for a job is rarely written in the same language as
  the orchestrator).
- Capability differences across runtimes must be visible at compile
  time, not discovered at runtime.

## Considered Options

- **Option A — Yet another framework**: build a fully featured browser
  automation library that wraps one chosen runtime (e.g. Playwright)
  and adds a DSL on top.
- **Option B — Open driver protocol**: define a language-agnostic
  protocol that any runtime can implement, plus a thin engine that
  speaks the protocol.
- **Option C — Plugin SDK**: provide a plugin API in one language
  (e.g. TypeScript) and ask other languages to call out via FFI.

## Decision Outcome

Chosen option: **Option B — Open driver protocol**.

The architectural insight applied here is the same one that produced
the Kubernetes Container Runtime Interface (separating Kubernetes
from a specific container runtime) and the OpenTelemetry collector
(separating signal collection from vendor SDKs). In each case, the
*right* primitive turned out to be a protocol with multiple
implementations, not a featureful framework.

For Spectre, this means:

- The core engine, control plane, and DSL runtime know nothing about
  Playwright, Selenium, or curl-impersonate.
- Each runtime ships an adapter that implements the **Driver Protocol**.
- Capabilities are declared by drivers at handshake time and checked
  against jobs at compile time.

### Consequences

- Good, because the user's extraction logic outlives any single
  runtime; switching driver is a configuration change.
- Good, because new runtimes can be added by the community without a
  fork or core change.
- Good, because the protocol becomes the unit of governance — clear
  reviews, clear versioning.
- Bad, because protocol design is upfront work: getting the schema
  wrong is expensive once drivers depend on it. Mitigated by the
  `v1alpha1` → `v1beta1` → `v1` progression and the three-reference-
  driver dogfooding rule (see ADR-0004).
- Bad, because some runtime-specific power (e.g. raw CDP access) is
  harder to expose without leaking implementation details. Mitigated
  by typed, opt-in capability extensions.

### Confirmation

The decision is working when:

1. The three reference adapters (Playwright/TS, SeleniumBase/Python,
   curl-impersonate/Go) all pass the conformance suite.
2. A user can switch `driver: playwright` to `driver: seleniumbase`
   in a job spec and have it execute, modulo declared capability
   differences.
3. A community driver can be written without modifying core code.

## Pros and Cons of the Options

### Option A — Yet another framework

- Good, because the easiest path to a runnable demo.
- Bad, because it inherits the same lock-in problem the project sets
  out to solve.
- Bad, because it gives no architectural reason to prefer Spectre over
  the wrapped library.

### Option C — Plugin SDK

- Good, because it can ship faster than a multi-language protocol.
- Bad, because it forces every adapter to live in the SDK's host
  language (or pay an FFI tax that constrains capability surface).
- Bad, because plugin APIs evolve with the host language's release
  cadence — protocol versioning is harder when the API surface is
  language-shaped.

## More Information

- Kubernetes CRI: <https://kubernetes.io/blog/2016/12/container-runtime-interface-cri-in-kubernetes/>
- OpenTelemetry collector: <https://opentelemetry.io/docs/collector/>
- Related: [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md).
