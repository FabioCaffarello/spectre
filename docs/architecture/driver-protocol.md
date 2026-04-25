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
3. **Transport pluggable.** Schema is canonical (protobuf); transport
   is not (gRPC and JSON-RPC are both first-class).
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

Both transports carry the same protobuf messages. The differences are
operational, not semantic.

### gRPC over Unix domain socket (local)

Used when the driver runs as a child process of the engine. The engine
spawns the driver, the driver writes its socket path to stdout, the
engine connects.

### gRPC over TCP/TLS (distributed)

Used when the driver runs in a separate pod or on a separate host.
Mutual TLS is required. Certificate provisioning is the operator's
concern.

### JSON-RPC over stdio (fallback)

Used when the driver lives in an ecosystem without strong protobuf
tooling. The driver reads JSON-RPC requests from stdin and writes
responses to stdout. The payloads carry the same protobuf messages,
encoded via the canonical [protobuf JSON
mapping](https://protobuf.dev/programming-guides/json/).

The driver manifest declares which transports it speaks. The engine
picks the first matching transport.

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
