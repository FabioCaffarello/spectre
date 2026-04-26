---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# Screenshot RPC, scope mapping, and payload boundaries

## Context and Problem Statement

PR4 (ADR-0009) wired `Initialize` and `Navigate`. PR5 (ADR-0010)
landed `Close`, `Query`, and `Extract` with a strict ElementRef
invalidation contract and the runtime gate on `MODE_EVAL`. After
PR5 the Playwright adapter speaks every v1alpha1 unary RPC except
`Screenshot`. PR6 closes that final gap.

`Screenshot` looks superficially smaller than the PR5 surface —
one RPC, three scopes, two formats — but its decisions sit on top
of two contracts that did not exist before:

1. **The first read-only RPC.** Every prior RPC mutates session
   state (Navigate bumps the generation; Query allocates refs;
   Extract evaluates fields; Close evicts the session). Screenshot
   does none of that. It also reuses the ElementRef registry from
   ADR-0010 without invalidating refs. The contract for read-only
   RPCs is set here.
2. **The first protocol payload that can blow past the transport
   limit.** Connect over HTTP/2 caps incoming and outgoing
   messages at ~4MB by default; full-page screenshots of long
   pages can exceed that. v1alpha1 has no streaming variant and
   no chunking, so PR6 must decide what the adapter does at the
   boundary.

The schema decisions for `ScreenshotRequest`/`ScreenshotResponse`,
`ScreenshotScope`, and `ScreenshotFormat` are settled (ADR-0001,
ADR-0003, ADR-0004). The capability mechanism is settled
(ADR-0010). The remaining decisions are about how the Playwright
adapter renders the protocol surface against Playwright's
`page.screenshot()` and `locator.screenshot()` APIs, and how it
handles the payload-size boundary honestly.

## Decision Drivers

- **Driver-agnostic protocol.** A future SeleniumBase adapter
  must be able to implement `Screenshot` without referencing
  Playwright internals. The scope/format mapping must be the
  *protocol* mapping, not Playwright-specific.
- **Read-only contract is a precedent.** v1alpha2 will likely
  add other read-only RPCs (`GetCookies`, `GetUrl`, etc.). The
  semantics this PR establishes — no generation bump, refs
  remain valid across the call — set the default for that
  family.
- **Payload-size boundary is real.** A full-page screenshot of a
  page with many images can comfortably exceed 4MB. v1alpha1 has
  no streaming and no chunking. The adapter cannot prevent the
  failure; it can only document the boundary, warn the operator,
  and let the transport surface the failure cleanly.
- **The v1alpha1 schema is frozen** (ADR-0004). No quality field,
  no clipping rectangle, no streaming variant. Whatever we
  decide must fit `ScreenshotRequest` and `ScreenshotResponse`
  as they exist today.
- **Failure-shape consistency with PR4/PR5.** Every prior RPC
  carries `error: DriverError` in its response and signals
  success/failure via the populated message. Screenshot must
  follow the same pattern; the empty-bytes-with-populated-error
  shape is the wire contract.

## Considered Options

The decisions cluster into five axes:

1. **Scope-to-Playwright mapping.**
2. **JPEG quality default and `_UNSPECIFIED` format default.**
3. **Payload-size boundary handling.**
4. **Read-only contract (does Screenshot bump the generation?).**
5. **Failure response shape.**

Each axis is decided below.

## Decision Outcome

### 1. Scope-to-Playwright mapping

Chosen:

| `ScreenshotScope`               | Playwright invocation                                  |
|---------------------------------|--------------------------------------------------------|
| `SCREENSHOT_SCOPE_VIEWPORT`     | `page.screenshot({ fullPage: false, ... })`            |
| `SCREENSHOT_SCOPE_FULL_PAGE`    | `page.screenshot({ fullPage: true, ... })`             |
| `SCREENSHOT_SCOPE_ELEMENT`      | `locator.screenshot({ ... })` after registry lookup    |
| `SCREENSHOT_SCOPE_UNSPECIFIED`  | rejected with `CODE_INVALID_ARGUMENT`                  |

For `SCREENSHOT_SCOPE_ELEMENT`, `request.element` must be
populated with a non-empty `opaque_id`. If `scope == ELEMENT` and
`element` is missing or has an empty `opaque_id`, the adapter
returns `CODE_INVALID_ARGUMENT` with the message:

```
element is required when scope is SCREENSHOT_SCOPE_ELEMENT
```

The ElementRef is resolved against the per-session
`ElementRegistry` introduced in ADR-0010. A stale ref (allocated
before a Navigate) or an unknown ref returns `CODE_INVALID_ARGUMENT`
with the same messages PR5 established for `Extract` — the contract
is one wire-level shape per failure class, not one per RPC.

Rejected:

- **Default `UNSPECIFIED` scope to `VIEWPORT`.** Symmetric with
  `WaitCondition_UNSPECIFIED` defaulting to `LOAD` in `Navigate`,
  but the Navigate default is well-anchored ("load" is the most
  common choice clients want). No such anchor exists for
  Screenshot — viewport is one of three plausible defaults, and
  silently picking it would mask client bugs (a forgotten
  `scope` field). Forcing the client to declare `scope` is the
  same opt-in posture ADR-0010 took for `SelectorKind`.
- **Auto-fill `element` from "the most recent Query result".**
  Magical, stateful, and brittle: which session? which Query?
  the protocol intentionally has no concept of "recent"
  results.

### 2. JPEG quality default and `_UNSPECIFIED` format default

Chosen:

- `SCREENSHOT_FORMAT_PNG` is passed to Playwright as
  `{ type: "png" }`.
- `SCREENSHOT_FORMAT_JPEG` is passed as
  `{ type: "jpeg", quality: 80 }`.
- `SCREENSHOT_FORMAT_UNSPECIFIED` defaults to PNG.

The JPEG quality value of 80 is a Playwright/web-imaging
convention: it is the typical Photoshop "high" preset and the
default in many image-processing libraries. It balances visual
fidelity against payload size for typical web content. v1alpha1
has no quality field on `ScreenshotRequest`; clients that need a
specific quality should request PNG (lossless) until v1alpha2
adds the field.

The `_UNSPECIFIED` → PNG default is a deliberate choice rather
than an oversight. PNG is lossless, alpha-aware, and the
common-case mental model for "give me the screenshot". A client
that sets neither `scope` nor `format` will see the scope
rejected (decision 1) before the format default is exercised, so
the format default is only reached when the client explicitly
sets `scope` and explicitly leaves `format` unset — at that
point, lossless is the safer landing.

Rejected:

- **Reject `_UNSPECIFIED` format.** Symmetric with the scope
  rejection but harsher than necessary: the scope choice has
  three plausible defaults and no anchor; the format choice has
  one obvious default (lossless). Symmetry for symmetry's sake
  is not load-bearing.
- **JPEG quality 90 or 95.** Higher fidelity but materially
  larger payloads, increasing the chance of crossing the
  transport boundary (decision 3). 80 is the conventional
  middle.
- **JPEG quality configurable via env var.** Operator-side
  configuration without protocol-level signalling diverges
  drivers silently. The protocol either declares the field or
  every adapter picks its own default; v1alpha1 takes the
  second path with 80 as the documented value.

### 3. Payload-size boundary

Chosen: **document, warn, do not chunk**.

`@connectrpc/connect-node` (and the underlying gRPC binary
protocol over HTTP/2) caps incoming and outgoing messages at
roughly 4MB by default. The total `ScreenshotResponse` size
includes the `image` bytes plus the small overhead of
`content_type` and any `error` envelope, so the practical bound
on the image is ~4MB minus a few hundred bytes.

The adapter behaviour at the boundary:

- **Soft warning at 3MB.** When `bytes.length > 3 * 1024 * 1024`,
  the adapter writes a single line to stderr in the form
  `screenshot payload <N> bytes exceeds 3MB warning threshold;
  v1alpha1 transport limit is ~4MB`. The 3MB threshold leaves
  ~1MB of headroom under the 4MB hard limit so operators see
  the warning before the failure.
- **No truncation, no chunking, no streaming.** The bytes are
  returned in one response unchanged. If they exceed the
  transport limit, the transport surfaces a Connect/gRPC error
  (typically `RESOURCE_EXHAUSTED` on the client side); the
  Playwright adapter does not catch that, because the failure
  happens after the handler returns.
- **No raising of the transport limit.** The default is the
  v1alpha1 default. Raising it on the server side without a
  corresponding client-side bump silently breaks any client that
  trusts the documented boundary.

v1alpha2 will likely add a streaming variant
(`StreamScreenshot returns (stream ScreenshotChunk)`) or a
chunked-response field. Until then, clients who need full-page
screenshots of long pages have two workarounds: take element
screenshots of bounded regions, or request JPEG (which is
typically 5–10× smaller than PNG for the same content).

Rejected:

- **Adapter-side truncation.** Returning a truncated image is
  worse than returning a transport error: clients receive a
  corrupt PNG/JPEG with no signal that the bytes are wrong.
  Failing loud at the transport is more honest.
- **Adapter-side chunking via repeated calls.** Would require a
  protocol-level chunk-id and reassembly contract, which
  v1alpha1 does not have. v1alpha2 territory.
- **Refuse to return bytes above 3MB with a typed error.**
  Tempting, but the soft threshold is a warning, not a
  contract: a 3.5MB screenshot is still useful and well within
  transport limits. Hard-erroring at the warning threshold
  would punish clients for the safety margin.

### 4. Read-only contract

Chosen: **Screenshot does not mutate session state**.

Specifically:

- The session's generation counter is *not* bumped.
- ElementRefs valid before a Screenshot call remain valid after.
- The per-session `Map<UUID, { locator, generation }>` is
  unchanged.
- No new ElementRefs are allocated by Screenshot, even when
  `scope == ELEMENT` (the registry is read-only on this path).

Clients can therefore interleave `Query → Screenshot → Extract`
freely: the ref returned by Query is the same ref still resolvable
by Extract after Screenshot. The unit test in `sessions.test.ts`
asserts that calling the Screenshot handler does not change
`currentGeneration(sessionId)`.

This is the first read-only RPC in the protocol, and the contract
sets a precedent. Future read-only RPCs in v1alpha2 (potential
`GetCookies`, `GetUrl`, `GetTitle`) inherit the same shape: they
do not bump the generation, they do not invalidate ElementRefs,
they do not write to the registry. The default for read-only RPCs
is "observable side-effect-free at the protocol level".

Rejected:

- **Bump the generation on Screenshot.** Would invalidate every
  ElementRef the session holds, forcing the client to re-Query
  before every Extract that follows a Screenshot. Wasteful in
  every realistic workflow and inconsistent with the verb
  ("screenshot" is observation, not mutation).
- **Allocate a new ElementRef for `scope == ELEMENT` and return
  it on the response.** Adds a wire field that does not exist in
  `ScreenshotResponse` and confuses the read-only contract; the
  ref the client passed in is still valid, so a re-allocation
  would be redundant.

### 5. Failure response shape

Chosen: success and failure both populate `ScreenshotResponse`;
the populated `error` field is the only success/failure signal.

On success:

- `image` is the screenshot bytes (`bytes`, encoded as a
  `Uint8Array` on the TypeScript side).
- `content_type` is `"image/png"` for PNG and `"image/jpeg"` for
  JPEG.
- `error` is left at its default-constructed value (empty
  `code = CODE_UNSPECIFIED`, empty `message`).

On failure:

- `image` is empty (`new Uint8Array(0)`).
- `content_type` is empty string.
- `error.code` is the populated `DriverError.Code`.
- `error.message` is the populated diagnostic message.

Clients determine success by checking `response.error.code !=
CODE_UNSPECIFIED` (or the equivalent zero-check in their
language's generated code). This mirrors the pattern PR4
established for `Navigate` and PR5 reused for `Query`/`Extract`/
`Close`. v1alpha1 has no `CODE_OK` — the "no error" state is the
default-constructed `DriverError` message, and that is the
v1alpha1 wire contract.

Rejected:

- **Throw a `ConnectError` for failures instead of populating
  `error`.** Would diverge from the rest of the v1alpha1 surface
  and force clients to write two error-handling code paths
  (one for transport-level errors, one for response-level
  errors). The single response-shape contract is more
  consistent.
- **Populate `error` and *also* return image bytes for partial
  failures.** v1alpha1 has no concept of partial success; the
  empty-bytes-on-failure rule keeps the contract crisp.

## Confirmation

- Acceptance criteria 1–11 of the PR6 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just
  pw-install-browsers && just check && just conf-test` succeeds
  on Linux and macOS.
- All 23 conformance tests (Initialize × 1, Navigate × 5,
  Close × 3, Query × 5, Extract × 5, Screenshot × 4) pass three
  times in a row with no flakes.
- The byte-for-byte capability assertion holds with the
  thirteen-name list (the ten from PR5 plus
  `screenshot_element`, `screenshot_full_page`, and
  `screenshot_viewport` in alphabetical order).
- A unit test in `sessions.test.ts` asserts that calling
  Screenshot does not change the session's generation counter.
- The viewport, full-page, and element scopes each return
  non-empty bytes with the documented `content_type`; the PNG
  magic bytes (`\x89PNG`) and JPEG magic bytes (`\xff\xd8\xff`)
  are present at the start of the respective payloads.
- A Screenshot with `SCREENSHOT_SCOPE_UNSPECIFIED` returns
  `CODE_INVALID_ARGUMENT`; a Screenshot with `scope=ELEMENT`
  and missing `element` returns `CODE_INVALID_ARGUMENT` with
  the documented message.
- An element-scoped Screenshot against a stale ref (allocated
  before a Navigate) returns `CODE_INVALID_ARGUMENT` with the
  same stale-ref message PR5 established.

## Consequences

- Good, because the read-only contract is explicit and
  testable. Future read-only RPCs inherit a documented default;
  contributors do not have to re-litigate the question.
- Good, because the scope/format mapping fits the v1alpha1
  schema as-is. No proto changes were needed to land
  Screenshot, and the same enum values translate cleanly to a
  hypothetical SeleniumBase adapter (Selenium has analogous
  page/element screenshot APIs).
- Good, because the payload-size boundary is documented rather
  than hidden. An operator who hits the 4MB wall sees the soft
  warning at 3MB and reads the README; v1alpha2 will fix the
  underlying constraint.
- Good, because the Playwright adapter now implements every
  v1alpha1 unary RPC. Phase 1's remaining work (engine DSL
  parser, gRPC client, `spectre run` CLI) no longer competes
  with adapter work for attention.
- Bad, because full-page screenshots of long pages will fail
  at the transport boundary in v1alpha1. The failure surfaces
  as a Connect/gRPC error rather than a structured
  `DriverError`, which is one less context cue than other
  failure modes provide. Documented as the boundary; v1alpha2
  closes it.
- Bad, because JPEG quality is fixed at 80. Clients with
  precise file-size targets (e.g. uploads to size-capped
  storage) cannot tune. Documented as a v1alpha2 candidate.
- Neutral, because Playwright's `locator.screenshot()`
  auto-scrolls to bring an off-screen element into view before
  capturing. The result is the element clipping, not the
  viewport-at-the-time. The adapter inherits this behaviour;
  the README documents it.
- Neutral, because JPEG is alpha-unaware: pages with
  transparent regions render those regions as white in the
  output. PNG preserves alpha. Clients pick the format with
  knowledge of their content; the README mentions the
  trade-off briefly without flagging it as an error class.
- Neutral, because removing `test_unimplemented.py` from the
  conformance suite leaves no adapter-level negative test for
  unimplemented RPCs against the Playwright adapter (it has
  none left). The contract that unimplemented RPCs return a
  structured gRPC status is now enforced by Connect runtime
  behaviour rather than by an adapter-level test. SeleniumBase
  and curl-impersonate will reintroduce equivalent negative
  tests when those adapters land.

## Pros and Cons of the Options

### Scope rejection of `_UNSPECIFIED`

#### Reject (chosen)

- Good, because the choice is explicit and forces the client
  to opt in.
- Good, because consistent with `SelectorKind_UNSPECIFIED`
  rejection in PR5.
- Bad, because clients writing minimal scripts pay one extra
  field. Negligible.

#### Default to viewport

- Good, because shorter happy path.
- Bad, because a forgotten `scope` field silently picks one of
  three plausible behaviours.

### Payload-size boundary

#### Document, warn, do not chunk (chosen)

- Good, because honest about the v1alpha1 limit.
- Good, because the soft warning gives operators a heads-up
  before the hard failure.
- Bad, because the transport error is less informative than a
  structured `DriverError`.

#### Adapter-side chunking

- Good, because lifts the practical limit on screenshot size.
- Bad, because requires protocol-level support
  (`chunk_id`, `is_final`, reassembly contract) that v1alpha1
  does not have.

### Read-only contract

#### No mutation (chosen)

- Good, because matches the verb's natural reading.
- Good, because sets a clean precedent for future read-only
  RPCs.
- Bad, because future read-only RPCs that *do* want to mutate
  (e.g. a "lazy load detection" Screenshot variant) cannot
  inherit the precedent.

#### Bump generation

- Good, because uniform with Navigate at the registry layer.
- Bad, because forces clients to re-Query after every
  Screenshot. Wasteful and surprising.

## More Information

- Connect transport message-size limits:
  <https://connectrpc.com/docs/node/server-plugins>
- Playwright `page.screenshot()`:
  <https://playwright.dev/docs/api/class-page#page-screenshot>
- Playwright `locator.screenshot()`:
  <https://playwright.dev/docs/api/class-locator#locator-screenshot>
- PNG magic bytes (`89 50 4E 47`):
  <https://en.wikipedia.org/wiki/PNG>
- JPEG magic bytes (`FF D8 FF`):
  <https://en.wikipedia.org/wiki/JPEG>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate and session lifecycle](0009-navigate-and-session-lifecycle.md),
  [ADR-0010 Element lifecycle and capability gating](0010-element-lifecycle-and-capability-gating.md).
