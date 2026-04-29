# spectre-driver-playwright

Spectre's reference Playwright driver adapter.

> **Status:** v0.1.0-alpha.0 — implements every v1alpha1 unary
> RPC (`Initialize`, `Navigate`, `Query`, `Extract`, `Screenshot`,
> `Close`) over gRPC on a TCP listener. Streaming RPCs
> (`WatchEvents`) remain v1alpha2 territory. The R2.2 refactor
> retired the original Unix-domain-socket transport in favour of
> TCP + the gRPC standard health check (ADR-0021, ADR-0022). See
> the [roadmap](../../docs/roadmap.md).

## Build

From the repository root:

```bash
just pw-bootstrap          # pnpm install --frozen-lockfile
just pw-install-browsers   # pnpm exec playwright install chromium (idempotent)
just pw-typecheck          # tsc --noEmit
just pw-test               # vitest run
just pw-build              # tsc -> dist/
just pw-lint               # prettier --check .
```

`pw-install-browsers` is required before any RPC that drives a
browser (currently `Navigate`). The recipe is idempotent and skips
when the binary at the resolved Playwright version is already
present. On Linux it adds `--with-deps`; on macOS that flag is
omitted (Linux-only).

Or directly:

```bash
pnpm install --frozen-lockfile
pnpm typecheck
pnpm test
pnpm build
```

## Run the adapter locally

The conformance harness is the canonical way to exercise the
adapter end-to-end: it spawns the binary, allocates a free TCP
port, polls the gRPC health check until SERVING, and then drives
the adapter through the v1alpha1 RPC surface.

```bash
# Run the adapter against the conformance suite (recommended).
just conf-test
```

For ad-hoc manual runs, the canonical path post-R6.2 is the
Compose stack: `just images && just compose-up` brings the
adapter up on `127.0.0.1:8091` (ADR-0021 §4 / ADR-0025 §7
reserve `8091` for Playwright). The native-binary `pw-run`
recipe was retired in R6.2 (ADR-0025); for live-coding flows
that need a native binary, build with `just pw-build` and
launch directly via `node dist/index.js` after exporting
`SPECTRE_ADAPTER_GRPC_PORT` and `SPECTRE_REDIS_URL`.

```bash
# Bring up the Compose stack (engine + adapters + stateful deps).
just images
just compose-up

# Probe the gRPC health check from the host.
grpc_health_probe -addr=127.0.0.1:8091   # → status: SERVING
```

The adapter logs a single `listening on 0.0.0.0:<port>` line on
stderr when ready. Readiness is signalled exclusively by the gRPC
standard health check (`grpc.health.v1.Health/Check`) returning
`SERVING`; there is no readiness banner on stdout. Send `SIGTERM`
(or press Ctrl-C) to drain in-flight RPCs, tear down browser
sessions, and exit zero.

> The R6.2 Compose stack will replace `just pw-run` as the canonical
> local-dev path. Until then this recipe survives as a convenience.

### Constraints

- **Port is set via `SPECTRE_ADAPTER_GRPC_PORT`.** The env var is
  required; the resolver rejects empty, non-integer, and
  out-of-range values. The conformance harness allocates a free
  ephemeral port at start time and injects it via env. Production
  deployments use the canonical port reserved by ADR-0021 §4.
- **Redis is required (R4.3).** The adapter externalises session
  metadata to Redis under the `session:playwright:<session_id>`
  key (ADR-0023 §4). Set `SPECTRE_REDIS_URL` (default
  `redis://127.0.0.1:6379/0`) and bring Redis up before the
  adapter — `docker compose up -d redis` is the canonical local
  path. The adapter PINGs Redis on startup and exits non-zero on
  failure, in line with the
  `depends_on.condition: service_healthy` contract.
- **Restart invalidation via `adapter_instance_id`.** The adapter
  generates a fresh UUID at process startup and stamps it on every
  session metadata document. Every non-Initialize RPC re-reads the
  metadata and compares the stored id; on mismatch the RPC fails
  with gRPC `UNAVAILABLE` and the message _"session belongs to a
  different adapter instance; client must re-Initialize"_. The
  `SPECTRE_ADAPTER_INSTANCE_ID` env var lets the conformance suite
  pin the value for deterministic testing — production deployments
  must leave it unset. See
  [ADR-0023 §5](../../docs/adr/0023-stateful-services-architecture.md)
  for the full mechanism.
- **Health check is the readiness signal.** The adapter registers
  `grpc.health.v1.Health` and starts in the `SERVING` state. The
  conformance harness polls `Check` until it returns `SERVING`
  within a 10-second deadline; production deployments wire the
  same endpoint into Compose / Kubernetes readiness probes.
- **`:authority` for direct dials.** Node's `http2` server expects
  the `:authority` pseudo-header. Clients dialling the TCP listener
  with a host other than `127.0.0.1` may need to set
  `grpc.default_authority`. The conformance harness applies this
  automatically; see
  [ADR-0008](../../docs/adr/0008-driver-handshake-and-conformance-harness.md)
  and [ADR-0022](../../docs/adr/0022-tcp-grpc-transport.md).
- **No mTLS or authentication in v1alpha1.** ADR-0022 §6 defers
  transport security to v1alpha2; ADR-0023 §6 defers Redis
  AUTH/TLS to the same milestone.

## Layout

```
adapters/playwright/
├── src/
│   ├── capabilities.ts    # declared capability list (matches driver.yaml)
│   ├── index.ts           # entry point — argv + signals + lifecycle
│   ├── index.test.ts      # in-process service tests (no transport)
│   ├── server.ts          # Connect-RPC service + http2 listener
│   └── proto/             # gitignored, generated by `just proto-generate`
├── driver.yaml            # adapter manifest (capabilities, runtime)
├── package.json           # ESM, pnpm-managed, vitest + prettier + tsc
├── tsconfig.json
└── README.md
```

## What this adapter owns

- A `Driver` server that listens on a TCP gRPC channel. Built on
  [`@connectrpc/connect-node`](https://www.npmjs.com/package/@connectrpc/connect-node);
  see ADR-0008 for the framework selection rationale and ADR-0022
  for the TCP transport contract that superseded the original UDS
  binding.
- Implementations of every unary RPC in
  `proto/spectre/driver/v1alpha1/driver.proto`. PR3 implemented
  `Initialize`; PR4 added `Navigate`; PR5 added `Close`, `Query`,
  and `Extract`; PR6 added `Screenshot`. The v1alpha1 unary
  surface is complete.
- Capability declarations in `driver.yaml`, added incrementally as
  each capability passes the conformance suite. The declared list
  must match `src/capabilities.ts` exactly — the conformance suite
  asserts this at runtime.

### Element lifecycle

- **Strict invalidation.** Every successful `Navigate` bumps the
  session's generation counter, invalidating every `ElementRef`
  the session received from a previous `Query`. An `Extract`
  against a stale ref returns `CODE_INVALID_ARGUMENT` with the
  message _"element reference is stale; query was performed before
  a navigation"_. Re-issue `Query` after each `Navigate`. See
  [ADR-0010](../../docs/adr/0010-element-lifecycle-and-capability-gating.md).
- **`Close` tears down one session.** The per-session
  `BrowserContext` and `Page` are closed; the shared `Browser`
  keeps running so other sessions are unaffected. Closing an
  unknown id returns `CODE_INVALID_ARGUMENT`.
- **Capability coherence.** The startup invariant
  `assertCapabilityCoherence` rejects a declared list with
  `extract_eval` but not `js_execution`. The Playwright adapter's
  list satisfies the rule by construction; the assertion exists
  so a future maintainer who removes a capability sees the
  contradiction at module load.
- **`MODE_EVAL` and untrusted JS.** `MODE_EVAL` runs arbitrary
  JS in the page context. Operators who run this adapter accept
  that exposure. The capability gate is the protocol-level
  safeguard; a future hardened-execution capability could
  narrow it.
- **JSON-encoded extracted values.** `ExtractedValues.Entry.json_value`
  carries a JSON-encoded string. `MODE_TEXT_CONTENT` returning
  `hello` arrives on the wire as `"\"hello\""` (literally the
  five characters `"`, `h`, `e`, `l`, `l`, `o`, `"` after
  decoding). Clients call `json.loads` (or equivalent) to
  unwrap. v1alpha2 may add a typed `oneof` to skip the wrap for
  common cases.

### Navigate semantics

- **Lazy browser launch.** Chromium is launched on the first
  `Navigate` for a given session, not at `Initialize` time. An
  adapter on a host without Chromium installed will `Initialize`
  successfully and surface the missing-browser failure on
  `Navigate`. See [ADR-0009](../../docs/adr/0009-navigate-and-session-lifecycle.md).
- **Session reuse.** Each `session_id` is backed by a dedicated
  `BrowserContext` and a single `Page` reused across navigations.
  Residual state (cookies, localStorage) persists across
  `Navigate` calls in the same session by Playwright design.
- **Strict `session_id`.** A `Navigate` with an unknown id returns
  `CODE_INVALID_ARGUMENT`. `Initialize` must precede every other
  RPC.
- **`WaitCondition` defaults.** An omitted condition maps to
  `load`. The full mapping is `LOAD → "load"`,
  `DOM_CONTENT_LOADED → "domcontentloaded"`,
  `NETWORK_IDLE → "networkidle"`.
- **Timeout default.** `30_000` ms when `timeout` is omitted.
- **HTTP status is data, not error.** A 4xx or 5xx response is a
  successful navigation that landed on an error page; the
  `NavigateResponse` carries the status without setting
  `DriverError`. Only network-layer failures and timeouts produce
  a `DriverError`.

### Screenshot semantics

- **Three scopes, two formats.** The protocol's
  `ScreenshotScope` maps to Playwright as
  `VIEWPORT → page.screenshot({ fullPage: false })`,
  `FULL_PAGE → page.screenshot({ fullPage: true })`, and
  `ELEMENT → locator.screenshot()` after the ElementRef is
  resolved against the per-session registry. `UNSPECIFIED` is
  rejected with `CODE_INVALID_ARGUMENT`. `ScreenshotFormat` maps
  `PNG → { type: "png" }` and `JPEG → { type: "jpeg", quality: 80 }`.
  `_UNSPECIFIED` defaults to PNG (lossless, alpha-aware). See
  [ADR-0011](../../docs/adr/0011-screenshot-rpc-and-payload-boundaries.md).
- **Read-only contract.** `Screenshot` does not bump the
  per-session generation counter and does not invalidate any
  ElementRef. Clients can interleave `Query → Screenshot →
Extract` without re-querying. This is the first read-only RPC
  in v1alpha1; future read-only RPCs (e.g. potential
  `GetCookies`, `GetUrl`) inherit the same invariant.
- **Element scope reuses the registry.** When `scope == ELEMENT`,
  the request must populate `element.opaque_id`. The handler
  resolves the ref against the session's `ElementRegistry`
  (ADR-0010) and returns `CODE_INVALID_ARGUMENT` with the same
  stale-ref message `Extract` uses if the ref was allocated
  before a Navigate. Playwright auto-scrolls the element into
  view before capturing, so an off-screen element still produces
  the element clipping rather than the viewport-at-the-time.
- **JPEG quality is fixed at 80** in v1alpha1; the schema has no
  quality field. Clients with precise file-size targets should
  request PNG and post-process. JPEG has no alpha channel —
  pages with transparent regions render those regions as white
  in the JPEG output. PNG preserves alpha. Pick the format with
  knowledge of the target page.
- **Payload-size boundary at ~4MB.** Connect's HTTP/2 transport
  caps message size at roughly 4MB by default. Full-page
  screenshots of long pages can cross the boundary. The adapter
  warns to stderr when the resulting payload exceeds 3MB
  (leaving ~1MB of headroom under the hard limit) but returns
  the bytes unchanged. If the message actually exceeds the
  transport limit, the failure surfaces as a Connect/gRPC error
  on the client side rather than as a structured `DriverError`.
  v1alpha2 will likely add a streaming or chunked variant.
- **Failure-response shape.** On failure, `image` is empty,
  `content_type` is empty, and `error` carries the populated
  `DriverError`. On success, `error` is the default-constructed
  message (empty `code`, empty `message`). v1alpha1 has no
  `CODE_OK`; clients distinguish success and failure by checking
  whether `error.code` is the zero value of the enum.

## Generated code

The Driver Protocol TypeScript bindings live at
`src/proto/spectre/driver/v1alpha1/` — a gitignored, generated tree
produced by `just proto-generate` via `@bufbuild/protoc-gen-es`.
`src/index.ts` imports the generated `FileDescriptor` to source
`PROTOCOL_VERSION` from one place. Run `just proto-generate` (or
`just pw-bootstrap`, which depends on it) before `pnpm typecheck`.
See [ADR-0007](../../docs/adr/0007-protocol-code-generation.md).

## References

- [Driver Protocol design](../../docs/architecture/driver-protocol.md)
- [Writing a driver](../../docs/guides/writing-a-driver.md)
- [Playwright](https://playwright.dev)
