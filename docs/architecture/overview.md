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

1. A client (the control plane in production, `grpcurl` in
   transitional local-dev workflows) sends a `RunJob` request to
   the engine's gRPC service. The request carries the inline DSL
   document; the engine parses it into a validated `Job`.
2. The engine compiles the job: parser → type checker → planner.
   The capability matcher runs here; missing capabilities fail
   with a clear error referencing the job line and the capability
   name.
3. The engine resolves `driver: <name>` against an
   `AdapterRegistry` populated from per-driver environment
   variables (ADR-0021 §5) and dials the resulting TCP endpoint
   over gRPC (ADR-0022). Adapters are long-running services with
   a `grpc.health.v1.Health` readiness check (ADR-0021 §6) — the
   engine no longer spawns them as subprocesses.
4. The engine dispatches the plan to the driver as a sequence of
   RPCs (`Initialize`, `Navigate`, `Query`, `Extract`,
   `Screenshot`, `Close`).
5. The driver replies with results. Each `Extract` response
   becomes a `RunJobResponse.Row` event on the streaming response;
   a terminal `Completed { rows_extracted }` follows on success or
   `Failed { error_code, error_message }` on failure.
6. The client consumes the stream — the control plane writes
   each row to its configured sink (Kafka in R4.4, S3 / webhook
   in R5); a `grpcurl`-driven local run prints the JSON-encoded
   events to its own stdout.

In a distributed run, the control plane orchestrates the
`RunJob` per `ScrapeJob` resource and persists the resulting
rows.

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
