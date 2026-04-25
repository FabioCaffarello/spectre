# Writing a driver

This guide walks through the steps to add a new driver to Spectre. It
is the most impactful contribution path: each new driver multiplies
the project's reach.

The example throughout is a hypothetical Puppeteer adapter. The same
steps apply to any browser-automation runtime in any language.

> Before starting, read the [Driver Protocol design
> document](../architecture/driver-protocol.md). The protocol shapes
> every decision below.

## Step 1 — Pick a name and language

Conventions:

- Directory: `adapters/<short-name>/`
- Package / module name: `spectre-<short-name>` (TS) or
  `spectre_<short-name>` (Python) or `github.com/.../adapters/<short-name>`
  (Go).
- The short name is used in `driver:` job fields and CLI flags.

Short names are kebab-case for filesystem paths and snake-case for
language-native identifiers.

## Step 2 — Generate or import the protocol types

Run `buf generate` (configured by the upcoming `proto/buf.gen.yaml`)
to produce typed bindings for your language. As of `v1alpha1` the
toolchain produces:

- `spectre/driver/v1alpha1/driver_pb.ts`, `*_grpc_pb.ts` (TypeScript)
- `spectre/driver/v1alpha1/driver_pb2.py`,
  `driver_pb2_grpc.py` (Python)
- `spectre/driver/v1alpha1.go.pb` (Go)
- `spectre::driver::v1alpha1` (Rust, via tonic-build)

If your language is not yet supported by `buf generate`, you can
implement the JSON-RPC transport (see Step 4) and skip code
generation; the JSON encoding of the messages follows the
[canonical mapping](https://protobuf.dev/programming-guides/json/).

## Step 3 — Implement the six RPCs

Implement the `Driver` service interface generated for your language:

```text
Initialize  (handshake, capability declaration)
Navigate    (URL change, returns final URL and status)
Query       (selector resolution, returns element references)
Extract     (read fields from queried elements)
Screenshot  (capture image bytes)
Close       (tear down session, idempotent)
```

Three rules to keep the engine happy:

1. **Declare only the capabilities you have implemented.** The
   conformance suite tests every declared capability. Lying to the
   engine produces runtime errors that the user sees as your driver's
   fault, not the engine's.
2. **Be deterministic on element references.** Element references
   returned by `Query` must remain valid for subsequent `Extract`
   calls within the same session.
3. **Surface errors as `DriverError`.** Do not panic, do not write
   raw strings to stderr in a way the engine has to parse. The error
   envelope is the contract.

A skeleton implementation in TypeScript looks like this:

```ts
// adapters/puppeteer/src/index.ts
import { DriverServer } from '@spectre/driver-v1alpha1';

const driver = new DriverServer({
  capabilities: ['navigation', 'js_execution', 'screenshot_full_page'],
});

driver.on('Initialize', async (req) => {
  // Spawn a Puppeteer browser, store session keyed by req.sessionId.
});

driver.on('Navigate', async (req) => {
  // Page.goto(url, { waitUntil: ... })
});

// ... Query, Extract, Screenshot, Close ...

driver.listen({ transport: 'grpc', socket: process.argv[2] });
```

## Step 4 — Choose a transport

The two officially supported transports are:

- **gRPC** over a Unix domain socket (local) or TCP/TLS (remote).
  Use this if your language has solid gRPC tooling.
- **JSON-RPC over stdio** for languages where gRPC is awkward.

Declare the transport in your `driver.yaml` (Step 5). The engine
selects the first match.

## Step 5 — Write the driver manifest

Each driver ships a `driver.yaml` next to its source. The engine reads
this file to know how to launch your driver and what to expect.

```yaml
name: puppeteer
version: 0.1.0
protocol_version: spectre.driver.v1alpha1

transports:
  - kind: grpc-uds
    command: ["node", "dist/index.js"]
  # alternative:
  # - kind: jsonrpc-stdio
  #   command: ["node", "dist/json-rpc.js"]

capabilities:
  - navigation
  - js_execution
  - screenshot_full_page
  - cookies_persist

runtime:
  node: ">=20"
  packages:
    - "puppeteer@>=23"

maintainers:
  - name: Your Name
    github: yourhandle
license: Apache-2.0
```

The manifest is the canonical source for runtime requirements. The
engine uses it for compatibility checks; the ecosystem registry uses
it for discovery.

## Step 6 — Run the conformance suite

```bash
just conf-bootstrap
just conf-test -- --driver=adapters/puppeteer
```

The suite exercises:

- Handshake (capability declaration, version negotiation).
- The minimal navigation-and-extract path.
- Each capability the driver declared.
- Error envelope shape (drivers must return `DriverError`, not
  language-native exceptions).

A failing conformance test is a bug in the driver, not a request to
loosen the suite. The suite is the contract.

## Step 7 — Submit for inclusion

Open a PR using the [Driver Proposal issue
template](../../.github/ISSUE_TEMPLATE/driver_proposal.yml) first to
align on the design, then submit the implementation as a follow-up
PR. Required for inclusion in the ecosystem registry:

- Conformance suite passes against the latest published protocol
  version.
- README documents the driver, its capabilities, runtime requirements,
  and any caveats (e.g. "this driver does not yet support multi-page
  concurrency").
- Driver authors maintain their own driver. The core team helps with
  protocol questions but does not own community drivers.

## Common pitfalls

- **Over-declaring capabilities.** If your driver declares
  `network_intercept` but the underlying library only supports it on
  one browser engine, decide: either declare the capability and gate
  it internally with a clear error when invoked on the unsupported
  engine, or do not declare it. Do not silently no-op.
- **Sharing state across sessions.** The protocol assumes session
  isolation. If your driver pools browser instances, isolation must
  be preserved at the cookie / storage / memory level.
- **Leaking transport details.** Element references are opaque to the
  engine. Do not include CSS selectors, library handles, or anything
  the engine could be tempted to interpret directly.
- **Hiding errors.** A driver that swallows errors produces silent
  data loss. Always surface as `DriverError` with a meaningful code.

## Where to ask questions

Open a [Driver Proposal
issue](../../.github/ISSUE_TEMPLATE/driver_proposal.yml) or a
discussion. The maintainers will help.
