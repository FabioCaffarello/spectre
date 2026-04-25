# Architecture overview

This document is the entry point for understanding how Spectre is put
together. It complements the
[ADRs](../adr/README.md) (which capture *why* each decision was made)
by describing *what* the resulting system looks like.

## Layered model

```
┌────────────────────────────────────────────────────────────────┐
│                      User-authored job                         │
│                  (DSL document, YAML or DSL)                   │
├────────────────────────────────────────────────────────────────┤
│                  Spectre Engine  (Rust)                        │
│   - Parser, type-checker, planner                              │
│   - Capability matcher (job vs. driver capabilities)           │
│   - Execution scheduler (single-host)                          │
├────────────────────────────────────────────────────────────────┤
│              Driver Protocol  (protobuf, vNalphaM)             │
│            Transport: gRPC over UDS or TCP/TLS                 │
│                    or JSON-RPC over stdio                      │
├──────────────┬───────────────────┬─────────────────────────────┤
│  Playwright  │   SeleniumBase    │      curl-impersonate       │
│ adapter (TS) │  adapter (Python) │      adapter (Go/cgo)       │
├──────────────┴───────────────────┴─────────────────────────────┤
│        Browser runtimes / HTTP libraries (third-party)         │
└────────────────────────────────────────────────────────────────┘

         ┌────────────────────────────────────────────┐
         │            Control Plane (Go)              │
         │  Kubernetes-native scheduler, queues,      │
         │  multi-tenant routing, retries, quotas     │
         └────────────────────────────────────────────┘
```

The engine and the control plane are independent processes. Single-host
runs (CLI, local dev) talk to the engine directly. Distributed runs
(K8s, fleets) go through the control plane, which dispatches jobs to
engine workers.

## Components

### Engine (`core/engine`, Rust)

Owns the DSL — lexer, parser, type checker, planner, scheduler.
Statically validates a job against the connected driver's declared
capabilities and refuses to run if a required capability is missing.
The engine emits *plans* (sequences of protocol RPCs) and feeds them to
the driver over the chosen transport.

Why Rust: parsing and validation are CPU-bound and benefit from no-GC
tail-latency behaviour. The engine is also the embedding target for
SDKs in other languages (N-API for Node, PyO3 for Python). See
[ADR-0002](../adr/0002-polyglot-language-selection.md).

### Control plane (`core/control-plane`, Go)

Kubernetes-native scheduler. Receives job specs, materialises pods or
in-cluster workers, tracks state, applies retry and quota policies,
and exposes a control API for SDKs and CLIs.

Why Go: first-class Kubernetes ecosystem, mature gRPC tooling, static
binary deployment, goroutines for concurrent scheduling.

### Driver Protocol (`proto/spectre/driver/v1*`)

Language-neutral contract between the engine and any browser-automation
runtime. Defined in protobuf; transport-pluggable (gRPC, JSON-RPC).
Drivers declare their capabilities at handshake time. See
[ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md)
and [ADR-0003](../adr/0003-schema-transport-separation.md).

### Reference adapters

Three reference adapters ship with the project. They serve two
purposes: they make Spectre useful out of the box, and they exercise
the protocol against real automation libraries before `v1` is declared
stable (see ADR-0004).

- **Playwright** (`adapters/playwright`, TypeScript). Wraps the
  Playwright library. Targets modern Chromium, Firefox, and WebKit.
- **SeleniumBase** (`adapters/seleniumbase`, Python). Wraps
  SeleniumBase, including its UC Mode (CDP-driven). Useful where the
  Python ecosystem is the team's primary tooling.
- **curl-impersonate** (`adapters/curl-impersonate`, Go via cgo).
  Wraps the curl-impersonate C library. Useful for HTTP-only flows
  where a full browser is overkill but the request fingerprint must
  match a real browser's TLS and HTTP/2 profile.

### Intelligence layer (`core/intelligence`, Python — deferred to Phase 2)

Optional. Provides selector self-healing (LLM-assisted), visual diff
for layout regressions, and a small computer-vision pipeline for
extraction tasks where DOM selectors are insufficient.

### CLI (`cmd/spectre`, Go — deferred to Phase 1)

Single static binary. The user-facing entry point for local jobs and
control-plane interactions.

### SDKs (`sdks/`, multiple languages — deferred to Phase 2)

Thin wrappers around the control-plane API for users who want to
integrate Spectre programmatically rather than through the CLI.

## Data flow: a single job

1. User authors a job document and runs `spectre run job.yaml`.
2. CLI loads the job and selects a driver (per `driver:` field or
   per `driver_selector:` policy).
3. CLI starts the chosen driver process (or connects to a pre-started
   instance) and reads its `Capabilities` over the handshake RPC.
4. Engine compiles the job: parser → type checker → planner. The
   capability matcher runs here; missing capabilities fail with a
   clear error referencing the job line and the capability name.
5. Engine dispatches the plan to the driver as a sequence of RPCs
   (`Initialize`, `Navigate`, `Query`, `Extract`, `Screenshot`,
   `Close`).
6. Driver replies with results. The engine writes outputs in the
   format the job requested (JSONL, CSV, Parquet, etc.).
7. CLI exits with status 0 on success, non-zero with a structured
   error otherwise.

In a distributed run, steps 2–6 happen inside an engine worker
scheduled by the control plane.

## Boundaries and what is intentionally outside scope

- Spectre does not provide hosted browser infrastructure. Adopters run
  it on their own clusters or developer machines.
- Spectre does not bundle proxy services. The driver manifest can
  declare a proxy capability; configuration is the operator's
  responsibility.
- Spectre does not decide what targets are appropriate to scrape — see
  the [responsible use guide](../guides/responsible-use.md).

## See also

- [Driver Protocol deep dive](driver-protocol.md)
- [Writing a driver](../guides/writing-a-driver.md)
- [Roadmap](../roadmap.md)
- [Architecture Decision Records](../adr/README.md)
