---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Schema-transport separation

## Context and Problem Statement

The Driver Protocol (ADR-0001) needs to be implementable in many
languages, including some without first-class protobuf tooling. At the
same time, official drivers should benefit from strongly typed RPC and
binary efficiency. Coupling the schema definition to a specific
transport (e.g. "the protocol is gRPC") would either exclude the
slow-tooling ecosystems or pin the project to gRPC's particular trade-
offs (HTTP/2 framing, header semantics, stream lifecycle).

## Decision Drivers

- Schema must have one canonical definition that all generators and
  validators agree on.
- Transport must be replaceable without changing the schema layer.
- A driver author in a niche language must be able to ship a
  conforming driver without writing a protobuf compiler.
- The default transport must be efficient, well-tooled, and friendly
  to deployment in Kubernetes.

## Considered Options

- **Option A — gRPC-only**: protobuf as schema, gRPC as the only
  transport.
- **Option B — JSON-RPC-only**: JSON Schema as schema, JSON-RPC over
  stdio/HTTP as the transport.
- **Option C — Schema-transport separation**: protobuf as the
  canonical schema; multiple transports allowed.

## Decision Outcome

Chosen option: **Option C — Schema-transport separation**.

- **Schema layer**: protobuf definitions in
  `proto/spectre/driver/v1*/`. Single source of truth. Used to generate
  JSON Schema, TypeScript types, Python types (`grpc_tools.protoc` or
  `betterproto`), Rust types (`tonic-build`), and Go types
  (`protoc-gen-go`).
- **Transport layer**: pluggable. Two officially supported transports:
  - **gRPC** over Unix domain socket for local invocation, TCP/TLS for
    distributed. Used by all reference adapters with strong protobuf
    tooling.
  - **JSON-RPC over stdio** for languages where protobuf tooling is
    immature or absent. The JSON-RPC payloads carry the same protobuf
    messages, encoded as JSON via the canonical mapping.

The driver manifest (`driver.yaml`) declares which transport(s) it
speaks. The engine selects the first match.

### Consequences

- Good, because adopters can pick the transport that fits their
  language and operational profile without forking the protocol.
- Good, because the engine implements one schema-aware codec layer
  and a small transport adapter table.
- Good, because future transports (WebSocket, in-process, IPC pipes)
  drop in without protocol churn.
- Bad, because two transports means two conformance test paths. The
  conformance suite asserts schema equivalence across both transports
  to prevent drift.
- Bad, because JSON encoding loses some protobuf field semantics
  (well-known types, `int64` precision in JSON consumers). Documented
  in the protocol guide and addressed by canonical-JSON conventions.

### Confirmation

- The reference Playwright (TS) and curl-impersonate (Go) adapters use
  gRPC; SeleniumBase (Python) uses gRPC. A future minimal adapter
  (e.g. a shell-script reference for documentation purposes) uses
  JSON-RPC. Both pass the same conformance suite.
- `buf lint` and `buf breaking` enforce schema discipline in CI.

## Pros and Cons of the Options

### Option A — gRPC-only

- Good, because a single transport simplifies tooling and conformance.
- Good, because gRPC is well-understood in cloud-native operations.
- Bad, because it excludes ecosystems where protobuf or gRPC tooling
  is immature, awkward, or unsupported.
- Bad, because a future need to embed Spectre in environments without
  HTTP/2 (e.g. WASM, browser-side) becomes a protocol-level rewrite.

### Option B — JSON-RPC-only

- Good, because trivially supported in any language with JSON parsing.
- Bad, because it forfeits binary efficiency, code generation, and
  the well-developed gRPC tooling ecosystem.
- Bad, because schema drift between drivers is harder to catch
  without a typed source of truth.

## More Information

- Canonical JSON-protobuf mapping: <https://protobuf.dev/programming-guides/json/>
- Buf: <https://buf.build/docs>
- Related: [ADR-0001](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0004](0004-protocol-versioning-strategy.md).
