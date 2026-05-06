# Driver Protocol — design and semantics

This document explains the Driver Protocol from the perspective of an
engineer either implementing a driver or reasoning about engine
behaviour. For the canonical schema, read the `.proto` files in
[`proto/spectre/driver/v1alpha1/`](../../proto/spectre/driver/v1alpha1).

> **Status:** the protocol is `v1alpha1` and unstable. Breaking changes
> are expected until the three reference adapters all pass the
> conformance suite. See
> [ADR-0004](../adr/0004-protocol-versioning-strategy.md).

## Goals

1. **Driver agnostic.** The engine never references Playwright,
   Selenium, or any specific automation library. It only knows the
   protocol.
2. **Capability negotiated.** Drivers declare what they can do at
   handshake. The engine refuses to run jobs that require missing
   capabilities, at compile time, with a clear error.
3. **Schema-transport separation.** The schema is canonical
   (protobuf); the transport is gRPC over TCP, established at
   handshake (ADR-0022). Earlier ADRs (notably
   [ADR-0008](../adr/0008-driver-handshake-and-conformance-harness.md))
   considered UDS and a JSON-RPC over stdio fallback; both were
   retired in Phase R2 (R2.1 → R2.3) when the engine and adapters
   moved to gRPC over TCP exclusively. The schema-transport
   separation principle (ADR-0003) survives; "transport pluggable"
   does not.
4. **Versionable.** Path-based versioning lets new versions land
   alongside old ones; existing drivers do not break.

## RPC surface (`v1alpha1`)

The minimal-viable protocol exposes six RPCs. Streaming RPCs (network
event watches, DOM mutation streams) and advanced features (challenge
handling, browser fingerprint configuration extensions, proxy
rotation) are deferred to later versions and added as their use cases
crystallise.

```protobuf
service Driver {
  rpc Initialize(InitializeRequest) returns (InitializeResponse);
  rpc Navigate(NavigateRequest) returns (NavigateResponse);
  rpc Query(QueryRequest) returns (QueryResponse);
  rpc Extract(ExtractRequest) returns (ExtractResponse);
  rpc Screenshot(ScreenshotRequest) returns (ScreenshotResponse);
  rpc Close(CloseRequest) returns (CloseResponse);
}
```

### Initialize

Handshake. The engine sends the protocol version it speaks, the
session configuration (viewport, locale, timezone, user-agent hint,
proxy URI, headers), and any capability hints. The driver replies with
its `Capabilities` message and an opaque `session_id`.

`Capabilities` is the contract surface. The engine only schedules
operations that match the union of capabilities declared here. Driver
authors should be conservative: declare only capabilities you have
actually implemented and tested, even if the underlying library
nominally supports more.

### Navigate

Move the browser context to a URL. Returns the resolved URL after any
redirects, the HTTP status code, and timing information. The engine
uses the timing data for retry budgets and observability.

### Query

Locate elements. Takes a selector (CSS by default; XPath, text, and
attribute selectors are capabilities). Returns a list of element
references that subsequent `Extract` calls operate on.

The engine treats element references as opaque. Drivers may use any
internal scheme (DOM node IDs, locator handles) as long as the
references survive across calls within a session.

### Extract

Read fields from elements. Each field declares a name, an extraction
mode (`textContent`, `innerText`, `attr:href`, `eval:expression`,
etc.), and an optional transformer. The driver applies the extraction
and returns a structured value.

`eval:` mode requires the `js_execution` capability and is rejected at
compile time if the driver does not declare it.

### Screenshot

Capture a screenshot. Supports full-page, viewport-only, and
element-scoped captures. Returns the image bytes and a content-type
hint.

### Close

Tear down the session. Idempotent. The engine guarantees one final
`Close` for every successful `Initialize`.

## Capability negotiation

`Capabilities` is a flat list of strings, not a deeply structured
message. This keeps the contract simple to extend and easy to inspect:

```protobuf
message Capabilities {
  repeated string names = 1;
}
```

Initial capability strings (subject to evolution before `v1`):

- `navigation`
- `js_execution`
- `network_intercept`
- `screenshot_full_page`
- `cookies_persist`
- `header_overrides`
- `proxy_per_session`
- `cdp_passthrough`
- `multipage_concurrent`

Drivers may declare new capabilities by namespacing them
(`pw.cdp.session_attach`, `sb.uc_mode_v2`). Namespaced capabilities are
opaque to the engine; jobs that depend on them must request them
explicitly.

## Errors

A single canonical error envelope (`DriverError`) carries a structured
code, a human-readable message, and optional retry hints:

```protobuf
message DriverError {
  enum Code {
    CODE_UNSPECIFIED = 0;
    CODE_INTERNAL = 1;            // driver bug, not retryable
    CODE_TARGET_UNREACHABLE = 2;  // network or DNS failure
    CODE_TIMEOUT = 3;             // operation exceeded budget
    CODE_BLOCKED = 4;             // remote target rejected the request
    CODE_NOT_FOUND = 5;           // selector did not match
    CODE_INVALID_ARGUMENT = 6;    // bad input from engine
    CODE_CAPABILITY_MISSING = 7;  // engine asked for unsupported feature
    CODE_PROTOCOL_VIOLATION = 8;  // driver returned malformed response
  }

  Code code = 1;
  string message = 2;
  google.protobuf.Duration retry_after = 3;
  map<string, string> details = 4;
}
```

Errors carry no internal stack traces or library-specific identifiers.
Drivers that want to expose richer diagnostics use the `details` map.

## Transport semantics

The transport is gRPC over TCP. Adapters run as long-running
services (in a Compose service, a Kubernetes Pod, or a developer's
local process) and expose a TCP listener; the engine resolves a
`host:port` endpoint per adapter and dials it via tonic's TCP path.
Adapter readiness is observable through the standard
`grpc.health.v1.Health` service.

See [ADR-0022](../adr/0022-tcp-grpc-transport.md) for the transport
decision and [ADR-0021](../adr/0021-service-discovery.md) §5 / §6
for endpoint resolution and the health contract.

Earlier transports (gRPC over a Unix domain socket spawned per
job; a hypothetical JSON-RPC-over-stdio fallback discussed in
[ADR-0008](../adr/0008-driver-handshake-and-conformance-harness.md))
were retired in Phase R2 alongside the subprocess execution model
they assumed; their record is preserved in the supersession
notes on those ADRs.

## Versioning

See [ADR-0004](../adr/0004-protocol-versioning-strategy.md). Short
version:

- The current path is `proto/spectre/driver/v1alpha1/`.
- Within `v1alpha1`, breaking changes are expected and not gated.
- Once stable (`v1`), additions are append-only; breakages get a new
  path (`v2`).

Drivers pin to a version path in their manifest:

```yaml
protocol_version: spectre.driver.v1alpha1
```

The engine refuses to load a driver whose declared version it does not
speak.

## Conformance

A conformance suite at [`tools/conformance`](../../tools/conformance)
exercises the protocol and is the gating check before the protocol is
declared stable. Driver authors run the suite against their adapter
to confirm it satisfies the contract before submitting it for
ecosystem inclusion. See
[Writing a driver](../guides/writing-a-driver.md).

## v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe the
> Driver Protocol as it exists at refactor close — frozen
> per [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md);
> capability divergence (Playwright 13 ⊃ SeleniumBase 12 ⊃
> curl-impersonate 6) preserved byte-for-byte through every
> refactor PR. Phase R9 commits to keeping that freeze
> through every v1alpha2 PR; this subsection records the
> forward-looking evolution that **layers on top of the
> protocol**, not into it.*

The Driver Protocol stays **frozen**. v1alpha2 evolution is
**engine-internal** — the engine's DSL parser gains five new
primitives (pagination, conditional, multi-step navigation,
schema declaration, transforms) per
[ADR-0035 §4](../adr/0035-dsl-evolution-driver-abstraction.md);
the parser expands them into v1alpha1-shaped Driver Protocol
calls. Adapters need no changes for the v1alpha2 DSL to
work.

The strict-subset capability chain is the rule backbone for
v1beta1's driver routing intelligence per
[ADR-0035 §5](../adr/0035-dsl-evolution-driver-abstraction.md)
and [ADR-0017 §1](../adr/0017-curl-impersonate-extraction-and-final-capability-divergence.md).
v1beta1 transitions the DSL to **intent-declarative** with
capability hints replacing explicit `driver.kind`; the
driver-router (slot 14; service-vs-engine-module decision
deferred to Wave 10 per
[ADR-0035 §6](../adr/0035-dsl-evolution-driver-abstraction.md))
consults the chain to match each target's needs to the
cheapest qualifying driver.

For the operational walkthrough of DSL evolution + driver
routing intelligence, see
[`dsl-evolution.md`](dsl-evolution.md).

The Wave 5+ build PRs that materialise the engine's
orchestrator pattern per
[ADR-0037](../adr/0037-engine-as-orchestrator.md) consume
the Driver Protocol unchanged — the engine's adapter client
shape, the per-call timeouts, the ADR-0022 transport — all
preserved.
