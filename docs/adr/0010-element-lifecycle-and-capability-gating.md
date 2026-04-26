---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# Element lifecycle, capability granularity, and selector mapping

## Context and Problem Statement

PR4 (ADR-0009) wired the first RPC that drives a real browser:
`Navigate`, with lazy Chromium launch, per-session `BrowserContext`
allocation, and a Playwright-failure → `DriverError.Code` mapping
table. The conformance suite stayed deterministic via an in-process
HTTP fixture, and the negative test for `UNIMPLEMENTED` cemented
the contract for the four RPCs that had not yet shipped: `Query`,
`Extract`, `Screenshot`, `Close`.

PR5 closes Phase 1 of the Playwright adapter by implementing three
of those four RPCs in a single coordinated change: `Close`,
`Query`, and `Extract`. The fourth, `Screenshot`, has its own
concerns (image format, full-page vs viewport, base64 vs bytes
encoding) and is intentionally deferred to a focused follow-up.

The three RPCs landing together share a single architectural
decision space — the lifecycle of `ElementRef`, the capability
declarations that describe what the driver can do, and the
mapping from the protocol's `SelectorKind` enum to Playwright's
locator API. Splitting them across PRs would mean re-litigating
those decisions or inventing transitional shapes that survive
only one PR. Bundling them here keeps the reasoning consistent
and the contract explicit.

The schema decisions are settled (ADR-0001, ADR-0003, ADR-0004),
the codegen decisions are settled (ADR-0007), the handshake is
settled (ADR-0008), and the session lifecycle / error mapping is
settled (ADR-0009). The remaining decisions are about how the
Playwright adapter exposes DOM elements over the protocol.

## Decision Drivers

- **Driver-agnostic protocol.** `ElementRef` is opaque on the
  wire (a single `string opaque_id`). Whatever scheme the
  Playwright adapter chooses must not couple the protocol to
  Playwright's internal representation. SeleniumBase and
  curl-impersonate will need their own (probably similar) ref
  schemes, and the engine treats every driver's refs identically.
- **Correctness over convenience.** A reference returned by
  `Query` describes "the matched element on the page as it was
  at query time". After a navigation, the page is a different
  page; silently re-resolving the selector against the new page
  would return data from different DOM nodes that happen to
  match the same selector — a quiet correctness footgun. The
  protocol must make this kind of mistake loud.
- **Capability declarations are a planning surface, not just
  marketing.** The handshake's `Capabilities.names` list is what
  a future engine reads to decide whether a job will succeed
  against this driver. The list must be honest, granular enough
  to be useful, and coherent (no contradictions between names
  that imply each other).
- **The v1alpha1 enum is frozen** (ADR-0004). Whatever we decide
  about element lifecycle and capability gating must fit the
  existing `DriverError.Code` taxonomy. `CODE_INVALID_ARGUMENT`
  and `CODE_CAPABILITY_MISSING` are the load-bearing codes here.

## Considered Options

The decisions cluster into five axes (Section 4 of the PR5
master prompt):

1. **`ElementRef` lifecycle and invalidation.**
2. **`ElementRef` storage scheme.**
3. **Capability granularity and runtime gating.**
4. **Zero-matches semantics in `Query`.**
5. **`SelectorKind` → Playwright locator mapping.**

Each axis is decided below with its own option set.

## Decision Outcome

### 1. `ElementRef` lifecycle — strict invalidation

Chosen: **strict, per-session generation counter**. An
`ElementRef` is invalidated on:

- `Navigate` — any new navigation in the same session
  invalidates every prior ref, regardless of whether the new
  URL is the same as the old one.
- `Close` — session destruction invalidates everything in that
  session.

`Extract` against an invalidated ref returns
`CODE_INVALID_ARGUMENT` with one of two precise messages:

- `"element reference is stale; query was performed before a
  navigation"` (post-Navigate case).
- `"element reference is stale; session was closed"` (post-Close
  case; in practice the session lookup itself fails first, but
  the message is reserved for cases where a stale ref outlives
  the registry entry by a tick).

Mechanism: each session carries a monotonic `generation` counter
starting at zero. Each `ElementRef.opaque_id` is paired in the
in-memory registry with the generation at which it was created.
`Navigate` increments the counter for that session and clears
the registry's entry for prior generations. `Extract` looks up
the ref, verifies its stored generation matches the session's
current generation, and rejects mismatches.

The locator itself is *not* the source of truth for staleness.
Playwright's `Locator` re-resolves on each operation against
whatever page the context currently holds — so even after a
navigation the locator might still resolve. The generation
check is what enforces the strict contract regardless of
whether Playwright would accept the locator on the new page.

Rejected:

- **Tolerant re-resolution (silently re-evaluate the selector
  on the new page).** This is the convenient option for naive
  clients but a correctness footgun: a query for `.product` on
  page A returning ten refs, followed by a navigation to page B
  with different products, followed by an `Extract` on the
  third ref, would return the third `.product` on page B —
  silently, with no signal that the engine misused the
  protocol. The cost of strictness is that clients must
  re-`Query` after `Navigate`; that is the correct contract.
- **Mark refs as stale but allow `Extract` to re-resolve on
  request.** Adds an opt-in flag to the protocol and pushes the
  judgment call onto each client. The protocol stays simpler if
  the choice is the maintainer's, and "fail loud" is the
  correct default for a v1alpha protocol that has not yet
  earned tolerant defaults.

#### Why fail-loud is the load-bearing choice

This decision is the most important contract this PR
establishes. It is what makes the protocol trustworthy when
multiple drivers implement it. A driver that silently
re-resolves stale refs and a driver that strictly invalidates
them are not interchangeable at the protocol layer; jobs that
work against one will silently produce different data against
the other. The "client plans, server enforces" model in this
project pushes hard towards strict server enforcement so that
clients can rely on consistent semantics across drivers.

A future capability (e.g. `extract_resilient_refs`) could
declare a tolerant variant per-driver. That door stays open;
the default does not.

### 2. `ElementRef` storage — UUIDv4 in an in-memory map

Chosen: **per-session `Map<UUID, { locator, generation }>`,
keyed by sessionId at the outer level**.

The `opaque_id` is a UUIDv4 generated per match at `Query`
time using Node's `crypto.randomUUID()`. The registry stores:

```ts
Map<sessionId, Map<UUID, { locator: Locator; generation: number }>>
```

Cleared on `Navigate` (per session, the generation bumps and
the inner map is emptied) and on `Close` (per session, the
outer entry is removed).

The session manager owns the registry. The `Query` handler
allocates UUIDs by calling into the registry; the `Extract`
handler resolves UUIDs back to locators by calling into the
same registry. No `ElementRef` ever crosses the session
boundary.

Rejected:

- **Deterministic hash of `selector + position`.** Stateless
  and free of registry bookkeeping. Fragile on similar DOMs:
  two queries on different pages whose nth match has the same
  selector would produce colliding refs. A determinism that
  feels nice but breaks under realistic conditions is worse
  than a UUID.
- **Serialised Playwright `Locator`.** Couples the wire
  protocol to Playwright's internal representation. The
  protocol is meant to be driver-agnostic; embedding a vendor's
  locator string in the opaque id makes the protocol leak
  Playwright. SeleniumBase or curl-impersonate would have to
  invent a separate scheme anyway, and the engine would then
  have to know which scheme each driver uses.
- **Auto-incrementing integer per session.** Cheap and
  predictable. Conflicts with the protocol's "opaque" framing —
  a numeric id invites clients to construct or guess refs,
  which the protocol explicitly forbids. UUIDs make
  construction visibly non-trivial.

### 3. Capability granularity and runtime gating

Chosen: **eleven capability names, alphabetical order, with
runtime gating implemented for `MODE_EVAL` only and a startup
coherence invariant binding `extract_eval` to `js_execution`**.

The Playwright adapter declares:

```
extract_attribute
extract_eval
extract_html
extract_text
js_execution
navigation
query_attribute
query_css
query_text
query_xpath
```

(Alphabetical order is the rule for both `driver.yaml` and
`capabilities.ts`. The byte-for-byte equality assertion in the
conformance suite compares lists, not sets, so an order is
required; alphabetical was chosen because it has a single
canonical form and survives editor reordering without churn.)

The granular names break into two roles:

- **Descriptive declarations.** `query_css`, `query_xpath`,
  `query_text`, `query_attribute`, `extract_text`,
  `extract_html`, `extract_attribute`, `extract_eval`,
  `navigation`. These describe what the adapter can do. A
  client uses them to plan whether a job will succeed against
  this driver. They do not gate behaviour at runtime — the
  Playwright adapter implements all of them, so there is no
  runtime branch. A future driver that supports only CSS
  selectors (e.g. an HTTP-only adapter) would declare
  `query_css` and not `query_xpath`; clients that need XPath
  would plan around that absence.
- **Gating capabilities.** `js_execution` is the only one that
  gates behaviour at runtime in PR5. When `Extract` is called
  with a `Field` whose `mode` is `MODE_EVAL`, the adapter
  checks the declared capability list at the start of the
  request. If `js_execution` is absent, the RPC returns
  `CODE_CAPABILITY_MISSING` with message `"MODE_EVAL requires
  the js_execution capability"`. The whole request fails (not
  just that field) — partial-success semantics are explicitly
  out of v1alpha1 scope.

#### Coherence invariant

At adapter startup, `assertCapabilityCoherence(names)` is
called with the declared list. It enforces the rule:

> If `extract_eval` is in the declared list, `js_execution`
> must also be in the declared list.

Violation throws synchronously at module load. The
Playwright adapter's declared list satisfies the invariant
trivially; the assertion exists so a future maintainer who
removes `js_execution` while `extract_eval` is still
declared sees the contradiction immediately, not as a
confusing runtime error to a client.

The unit test for this assertion is small but load-bearing:
it is the project's first runtime invariant beyond
compilation correctness. It signals that the capability
mechanism is genuine, not decorative — an adapter cannot
declare a contradictory capability set and still start.

The invariant is extensible: future capabilities that imply
each other (e.g. `screenshot_full_page` implying
`screenshot`) get a row added to the same assertion.

Rejected:

- **A single `extract` capability instead of four
  `extract_*` names.** Saves three lines in the manifest;
  loses the ability for a future driver to declare partial
  extract support. The cost of granularity is one frozenset;
  the benefit is the planning surface.
- **Runtime gating on every granular capability (`query_css`
  presence required to handle CSS selectors).** Dual
  enforcement is appealing but the Playwright adapter
  implements all of them — the runtime gate would fire only
  on a deliberately misconfigured manifest. The startup
  coherence assertion catches the same misconfiguration
  earlier, more clearly, and at a single source of truth.
- **No coherence invariant.** A misconfigured driver that
  declares `extract_eval` but not `js_execution` would
  surface as `CAPABILITY_MISSING` to the first MODE_EVAL
  caller — a confusing inversion ("you declared the thing,
  why is it missing?"). The invariant prevents that class
  of operator confusion.

### 4. Zero matches in `Query` — success with empty list

Chosen: **`Query` returns success with `elements: []` when the
selector resolves to zero elements**. No `DriverError`. Clients
decide whether zero matches is acceptable for their use case.

Rejected:

- **`CODE_NOT_FOUND` for zero matches.** Forces every client
  to wrap `Query` in try/except for what is a normal outcome
  (a selector that simply does not match anything). Worse, it
  conflates two distinct conditions: "selector matched
  nothing" (a fact about the page) and "the adapter cannot
  answer" (a fact about the driver). The first is data; the
  second is failure. The protocol distinguishes them clearly
  by leaving `error` unset on zero matches.
- **`CODE_NOT_FOUND` only when `limit > 0` and matches are
  fewer than `limit`.** Adds a conditional that does not pull
  its weight; clients still have to handle empty-list
  separately when `limit == 0`.

`CODE_NOT_FOUND` retains its original meaning — the requested
*resource* is missing (a future RPC that fetches by id, for
instance). Selector-matched-nothing is not a missing resource;
the page exists, the selector ran, the result was empty.

### 5. `SelectorKind` → Playwright locator mapping

Chosen baseline:

| `SelectorKind`                | Playwright invocation                         | Notes                                                      |
|-------------------------------|-----------------------------------------------|------------------------------------------------------------|
| `SELECTOR_KIND_CSS`           | `page.locator(selector)`                      | Default Playwright behaviour.                              |
| `SELECTOR_KIND_XPATH`         | `page.locator('xpath=' + selector)`           | Explicit prefix; avoids ambiguity with CSS-shaped queries. |
| `SELECTOR_KIND_TEXT`          | `page.getByText(selector, { exact: false })`  | Substring match by default.                                |
| `SELECTOR_KIND_ATTRIBUTE`     | `page.locator('[' + selector + ']')`          | Selector format: `name=value` or just `name`.              |
| `SELECTOR_KIND_UNSPECIFIED`   | rejected with `CODE_INVALID_ARGUMENT`         | Force clients to be explicit.                              |

#### Why substring for `SELECTOR_KIND_TEXT`

Playwright's `getByText` defaults to substring matching, and
that is the choice this ADR ratifies. Exact match is rare in
practice — the dominant use case is "find the element whose
visible text contains 'Sign in'", not "find the element whose
text is precisely 'Sign in' with no surrounding whitespace or
sibling text". A future capability (`query_text_exact` or a
schema field on `QueryRequest`) could expose the exact-match
variant if real workloads surface it; the v1alpha1 default
optimises for the common case.

#### Why `name=value` (no brackets) for `SELECTOR_KIND_ATTRIBUTE`

The protocol's selector field is a single string. For
attribute selection, the cleanest delegation makes the
adapter responsible for the bracket syntax: clients pass
`data-test=primary` and the adapter constructs
`[data-test=primary]`. Bare `data-test` (presence-only
match) is also accepted and produces `[data-test]`.

The alternative — expecting the client to pass
`[data-test=primary]` already bracketed — was rejected
because it leaks CSS attribute-selector syntax into a
protocol that is meant to abstract over selector dialects.
A SeleniumBase adapter that maps `SELECTOR_KIND_ATTRIBUTE`
to a different underlying mechanism (XPath, by-attribute
helper) needs to parse `name=value`, not unwrap brackets.

Validation: the adapter does not enforce that `selector`
matches a specific regex shape for any kind. Malformed
selectors surface as Playwright errors at locator-resolution
time and are mapped via the existing error table. The cost
of validation in the adapter (regex per kind, divergent
between drivers) outweighs the benefit (one extra
round-trip with a friendlier message in the malformed case).

Rejected:

- **Auto-detection (let the client omit `kind` and infer from
  the selector shape).** Magical, fragile, and language-leaky:
  a CSS selector like `//div` (technically valid CSS for a
  custom element) would be misread as XPath. The protocol's
  explicitness here is a feature.
- **Exact-match default for `SELECTOR_KIND_TEXT`.** See above
  — substring is the dominant case.
- **Pass `kind` through unchecked, including `UNSPECIFIED`.**
  Rejected because `UNSPECIFIED` is the proto3 default for
  unset enum fields. A client that forgets to set `kind`
  would silently get whatever default the adapter chose. The
  explicit rejection forces clients to opt in.

## Confirmation

- Acceptance criteria 1–13 of the PR5 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just
  pw-install-browsers && just check && just conf-test` succeeds
  on Linux and macOS.
- All 20 conformance tests (Initialize × 1, Navigate × 5, Close
  × 3, Query × 5, Extract × 5, Unimplemented × 1) pass three
  times in a row with no flakes.
- The byte-for-byte capability assertion still holds with the
  eleven-name list.
- The coherence assertion accepts the current list and rejects
  a list with `extract_eval` but no `js_execution`.
- The Query → Navigate → Extract flow returns
  `CODE_INVALID_ARGUMENT` on the second `Extract`.
- The Query → Close → Extract flow returns
  `CODE_INVALID_ARGUMENT` on the second `Extract`.
- A `MODE_EVAL` extract with `js_execution` declared succeeds;
  unit test confirms the gate would reject the same call with
  `js_execution` removed from the runtime list.

## Consequences

- Good, because the strict invalidation contract is the same
  for every driver. A job that works against the Playwright
  adapter will not silently produce different data against a
  hypothetical driver that re-resolves selectors after
  navigation — both must reject the stale ref.
- Good, because UUID storage keeps the protocol opaque and lets
  each driver pick its own internal mapping without leaking
  Playwright shapes onto the wire.
- Good, because the eleven granular capability names give a
  future engine a real planning surface. A job that needs
  XPath knows from the manifest whether the driver supports
  it.
- Good, because the coherence invariant is the project's first
  runtime invariant beyond compilation. It demonstrates that
  the capability mechanism is enforced, not aspirational.
- Good, because zero-matches as success preserves the
  distinction between "data says no" and "driver could not
  answer". `CODE_NOT_FOUND` keeps its meaning.
- Bad, because clients that re-use refs after a `Navigate`
  pay one extra round-trip (a re-Query) compared to a
  tolerant protocol. This is the deliberate cost of the
  fail-loud contract.
- Bad, because `ExtractedValues.Entry.json_value` is a string
  carrying JSON. For text content, this means the wire payload
  for `MODE_TEXT_CONTENT` returning `"hello"` is the
  JSON-encoded string `"\"hello\""` (with literal quotes and
  escapes). Clients that expect a raw string will be
  surprised. This is a known wart documented in the adapter
  README; v1alpha2 will revisit by adding a typed `oneof`.
- Bad, because `MODE_EVAL` exposes arbitrary JS execution in
  the page context. The capability gate is the protocol-level
  safeguard; an adapter operator who declares `js_execution`
  is opting in to that exposure. The README documents this; a
  hardened-execution capability could land later.
- Neutral, because between `Query` and `Extract` within the
  same generation, Playwright's locator can still fail with
  "element not found" if the page's DOM shifted significantly
  between the two calls (e.g. a JS-driven mutation that
  removed the matched node). This is mapped to
  `CODE_INVALID_ARGUMENT` with message `"element not found in
  current DOM"` — distinct from the strict-invalidation
  message. The conformance suite does not exercise this path
  in PR5; it is documented for future readers.
- Neutral, because `Locator.all()` materialises the entire
  match set at call time. For pages with many matches and
  small `limit`, this is wasteful. The cost is real but
  small in practice; a TODO is left in the Query handler in
  case real workloads surface it.
- Neutral, because the generation counter is a JavaScript
  `number`. Practical overflow is irrelevant
  (`Number.MAX_SAFE_INTEGER ≈ 9 × 10^15`); the registry
  comment notes this so a future reader does not add a
  spurious overflow guard.

## Pros and Cons of the Options

### `ElementRef` lifecycle

#### Strict invalidation (chosen)

- Good, because the protocol is consistent across drivers and
  consistent across navigations within a session.
- Good, because the contract is explicit and the failure mode
  is loud.
- Bad, because clients must re-`Query` after every `Navigate`
  if they want to extract from the new page.

#### Tolerant re-resolution

- Good, because naive clients have a shorter happy path.
- Bad, because cross-page selector aliasing produces
  silently-wrong data; the protocol cannot be trusted to mean
  the same thing across drivers.

### Capability granularity

#### Eleven granular names with one gating capability (chosen)

- Good, because clients have a real planning surface.
- Good, because the gating logic is concentrated in one
  place (the `MODE_EVAL` check) instead of sprinkled across
  every handler.
- Bad, because the manifest is a little longer. Negligible.

#### One omnibus `extract` and one omnibus `query`

- Good, because the manifest is shorter.
- Bad, because a future driver that supports only some
  variants cannot signal that to the engine.

### Selector-kind mapping

#### Explicit `kind` enum (chosen)

- Good, because every selector has one unambiguous
  interpretation.
- Good, because the adapter can reject `UNSPECIFIED` and
  force clients to opt in.
- Bad, because the request body is one field longer than
  pure-string-with-inference would be.

#### Auto-detection from the selector shape

- Good, because the request body is shorter.
- Bad, because the inference is fragile and the protocol
  cannot give a clear error for ambiguous selectors.

## More Information

- Playwright Locator API:
  <https://playwright.dev/docs/api/class-locator>
- Playwright `getByText` semantics:
  <https://playwright.dev/docs/locators#locate-by-text>
- Playwright `evaluate` and JS contexts:
  <https://playwright.dev/docs/evaluating>
- Node `crypto.randomUUID`:
  <https://nodejs.org/api/crypto.html#cryptorandomuuidoptions>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate and session lifecycle](0009-navigate-and-session-lifecycle.md).
