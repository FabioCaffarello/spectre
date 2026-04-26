---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Navigate, session lifecycle, and the driver error mapping

## Context and Problem Statement

PR3 (ADR-0008) wired the first end-to-end RPC: the Playwright adapter
answers `Initialize` over gRPC on a Unix domain socket, the Python
conformance harness dials it, and the other five RPCs respond
`UNIMPLEMENTED`. PR4 lights up the first RPC that actually does
something: `Navigate`. That single addition forces decisions about
session lifecycle (when does Chromium launch, who owns it, how long
does it live), error taxonomy (how does Playwright's failure surface
collapse onto `DriverError.Code`), and how the conformance suite
stays deterministic without depending on the public internet.

The schema decisions are settled (ADR-0001, ADR-0003, ADR-0004), the
codegen decisions are settled (ADR-0007), and the handshake decisions
are settled (ADR-0008). The remaining decisions are about the
runtime semantics of `Navigate` and the conformance fixture pattern
that makes them testable.

## Decision Drivers

- The handshake must remain cheap. `Initialize` is a metadata
  exchange; it cannot pay for a Chromium launch on every call.
- The `DriverError` enum in `errors.proto` is frozen at v1alpha1
  (ADR-0004). Mapping Playwright's failure surface onto the existing
  codes is an exercise in fitting reality to a fixed taxonomy.
- The conformance suite must run deterministically on developer
  laptops (Linux, macOS) and on the CI Linux runner. A flake of
  any kind costs the project credibility faster than any other
  failure mode.
- Future drivers (SeleniumBase, curl-impersonate) and future RPCs
  (`Query`, `Extract`, `Close`, `Screenshot`) inherit the lifecycle
  contract and the error mapping. The decisions made here are
  reused, not rewritten.

## Considered Options

The decisions cluster into four orthogonal axes (Section 4 of the
PR4 master prompt):

1. **Browser launch timing.**
2. **Session lifecycle and reuse semantics.**
3. **Error taxonomy mapping.**
4. **Local HTTP fixture pattern.**

Each axis is decided below with its own option set.

## Decision Outcome

### 1. Browser launch timing

Chosen: **lazy on first `Navigate`**.

`Initialize` does not touch Chromium. The session record created at
`Initialize` time is metadata only — a `session_id`, the requested
`SessionConfig`, and bookkeeping. The first `Navigate` for a
session_id is what launches Chromium, opens a `BrowserContext`,
opens a `Page`, and stores the triple in the session record.
Subsequent `Navigate` calls with the same session_id reuse the same
`Page`.

Rejected:

- **Eager launch at `Initialize`.** Surfaces a missing-Chromium
  error earlier. Pays the launch cost on every handshake, including
  short-lived inspection sessions a future engine might run for
  capability discovery. The cost is real (Chromium boot is ~1–2s on
  a warm laptop) and recurring; the benefit is debuggability that
  the error mapping already provides via a clear `CODE_INTERNAL`
  with an actionable message.

Consequence to document explicitly: an adapter can `Initialize`
successfully on a host that has no Chromium binary installed.
`Navigate` is where that surfaces, with an actionable error
("`Chromium not installed; run pnpm exec playwright install
chromium`"). This is a deliberate trade-off favouring fast
handshakes over early validation.

### 2. Session lifecycle and reuse semantics

Chosen contract:

- **Allocation policy.** A new `BrowserContext` per `session_id`,
  all contexts sharing one `Browser` instance per adapter process.
  `BrowserContext` creation is cheap (single-digit ms) and provides
  cookie / storage isolation per session for free. A single browser
  process is enough for the workload an adapter handles.
- **Reuse on `Navigate`.** Same `session_id` reuses the same `Page`
  inside the existing context. `page.goto()` handles cleanup of
  previous DOM state. This matches the way real users interact with
  a browser: a session is a tab, not a fresh window per click.
- **Eviction.** Sessions live until adapter shutdown in PR4. There
  is no idle timeout, no max-sessions cap, no eviction policy.
  This is a deliberate gap; PR5+ will revisit eviction once `Close`
  lands and once a real client (the engine) starts hammering the
  adapter.
- **`session_id` validation.** Strict: `Navigate` rejects an
  unknown `session_id` with `CODE_INVALID_ARGUMENT`. The protocol
  contract is that `Initialize` precedes every other RPC. Implicit
  creation would mask client bugs; the strict path produces a
  diagnostic the client can act on.
- **Concurrency.** Out of scope for PR4. The session manager is not
  concurrency-safe under simultaneous `Navigate` calls for the same
  `session_id`. No real client multiplexes yet; PR5+ will revisit
  if and when the engine requires it. Documented here so the next
  reader does not assume the absence of locks is intentional
  thread-safety.

Rejected:

- **Lenient `session_id` (auto-create on first `Navigate`).** Hides
  client bugs. The cost of being strict is a single error round-trip;
  the benefit is that misconfigured clients fail loudly.
- **Persistent contexts (cookies survive `Close`).** Out of scope;
  decided against here only to set the default — sessions are
  ephemeral unless the protocol later grows an explicit persistence
  capability.

### 3. Error taxonomy mapping

The Playwright failure surface is mapped onto `DriverError.Code` by
a single function in `adapters/playwright/src/errors.ts`. The
mapping is the most reusable artifact of this PR — every future RPC
in the Playwright adapter, and every future driver authored in
TypeScript, inherits it.

| Playwright surface                                | DriverError.Code           | Tested in PR4    | Notes                                                                                          |
|---------------------------------------------------|----------------------------|------------------|------------------------------------------------------------------------------------------------|
| `TimeoutError` from `page.goto()` (any cause)     | `CODE_TIMEOUT`             | yes (`/slow`)    | Includes `wait_until` timeouts and the explicit `timeout` budget.                              |
| `net::ERR_NAME_NOT_RESOLVED`                      | `CODE_TARGET_UNREACHABLE`  | no (TODO)        | DNS failure. v1alpha1 has no `NETWORK` code; `TARGET_UNREACHABLE` is the closest fit.          |
| `net::ERR_CONNECTION_REFUSED`                     | `CODE_TARGET_UNREACHABLE`  | no (TODO)        | TCP/HTTP connection refused.                                                                   |
| `net::ERR_*` (other network errors)               | `CODE_TARGET_UNREACHABLE`  | no               | Generic network bucket — anything matching `/^net::ERR_/` in the message.                      |
| `Target page, context or browser has been closed` | `CODE_INTERNAL`            | no               | Adapter-side problem. The client should consider the session toast and call `Initialize` again.|
| Chromium binary missing / launch failed           | `CODE_INTERNAL`            | no (unit only)   | Operator action required. Message includes `pnpm exec playwright install chromium`.            |
| Invalid URL passed by client                      | `CODE_INVALID_ARGUMENT`    | no               | Validated before calling `page.goto()` to keep the diagnostic clean.                           |
| Unknown `session_id`                              | `CODE_INVALID_ARGUMENT`    | no               | See decision 2.                                                                                |
| Anything else                                     | `CODE_INTERNAL`            | no               | Catch-all. Original message preserved in `DriverError.message`.                                |

Two notes on the v1alpha1 enum constraints:

- The PR4 master prompt's draft mapping table referenced `NETWORK`,
  `UNAVAILABLE`, and `UNKNOWN` codes that **do not exist** in
  `errors.proto`. The frozen `v1alpha1` set is `UNSPECIFIED`,
  `INTERNAL`, `TARGET_UNREACHABLE`, `TIMEOUT`, `BLOCKED`,
  `NOT_FOUND`, `INVALID_ARGUMENT`, `CAPABILITY_MISSING`,
  `PROTOCOL_VIOLATION`. The mapping above resolves the absent
  names: `NETWORK` → `TARGET_UNREACHABLE`, `UNAVAILABLE` →
  `INTERNAL` (with an actionable message), `UNKNOWN` → `INTERNAL`.
- A `v1alpha2` enrichment may add finer-grained codes (a dedicated
  `UNAVAILABLE` for "operator must install something",
  `NETWORK`/`DNS`/`TLS` splits, an explicit `UNKNOWN`). Until then
  the catch-all collapses to `INTERNAL` and relies on the free-form
  `message` field for diagnostics. This is a TODO for `v1alpha2`,
  not a defect to fix in PR4.

The conformance suite asserts the timeout row directly (against the
`/slow` route). The other rows are exercised by unit tests in
`errors.test.ts`. Full conformance coverage of every row lands
incrementally as the underlying adapters surface real evidence.

#### HTTP status codes are not errors

`page.goto()` against a URL that lands on a 4xx or 5xx response is
a **successful navigation**. The `Navigate` RPC returns success
with `status_code` populated; no `DriverError` is set. Only
network-layer failures (the `net::ERR_*` family) and timeouts
produce a `DriverError`. The conformance suite asserts this
directly with the `/status/404` route.

#### `WaitCondition` mapping defaults

| `WaitCondition` enum                    | Playwright `wait_until` | Default? |
|-----------------------------------------|-------------------------|----------|
| `WAIT_CONDITION_UNSPECIFIED`            | `load`                  | yes      |
| `WAIT_CONDITION_LOAD`                   | `load`                  | —        |
| `WAIT_CONDITION_DOM_CONTENT_LOADED`     | `domcontentloaded`      | —        |
| `WAIT_CONDITION_NETWORK_IDLE`           | `networkidle`           | —        |

If the `NavigateRequest` omits `timeout`, the adapter applies a
30 000 ms budget. Both defaults are documented in the adapter
README so a client author does not need to read source.

### 4. Local HTTP fixture pattern

Chosen: **stdlib `http.server.ThreadingHTTPServer`, session-scoped
pytest fixture, four routes**.

Implementation:

- `tools/conformance/src/spectre_conformance/http_fixture.py`
  exports a small `LocalHttpServer` class. `start()` binds to
  `127.0.0.1:0` (random port chosen by the OS), `stop()` shuts
  down, `base_url` returns `http://127.0.0.1:<port>`.
- A pytest session-scoped fixture in `tests/conftest.py` yields a
  started instance. One server per pytest invocation, not per test.
- The four routes are:

  | Route             | Behaviour                                                       |
  |-------------------|-----------------------------------------------------------------|
  | `GET /ok`         | 200 with body `ok`.                                             |
  | `GET /redirect`   | 302 to `/ok`.                                                   |
  | `GET /status/<n>` | The given status code with body `<n>`. `n` parsed; 400 if not.  |
  | `GET /slow`       | Sleeps 5 s, then 200 with body `slow`. Used for timeout tests.  |

Rejected:

- **`aiohttp.web`.** Async surface forces async tests for what
  amounts to four routes. Not worth the dependency or the
  cognitive cost.
- **A separate fixture process.** Subprocess management overhead
  (start, ready-poll, teardown) for what `ThreadingHTTPServer`
  handles in-process in ~30 lines.
- **Hitting the public internet (`example.com`, `httpbin.org`).**
  Tests that touch the public internet are flaky by definition.
  The local fixture is non-negotiable for this reason.

#### Consequences

- Good, because the chosen lifecycle keeps `Initialize` cheap and
  defers Chromium cost to the moment a navigation is actually
  requested.
- Good, because the strict `session_id` validation surfaces client
  bugs as protocol errors instead of letting them succeed by
  coincidence.
- Good, because every future driver and every future RPC inherits
  the error mapping table as a written rule rather than tribal
  knowledge.
- Good, because the local HTTP fixture eliminates an entire class
  of CI flake (public-internet flakiness) at the cost of ~30 lines
  of stdlib code.
- Bad, because the v1alpha1 `DriverError.Code` enum is too coarse
  for some real failures (no dedicated `UNAVAILABLE` for missing
  binaries, no `UNKNOWN` for the unmapped tail). PR4 papers over
  the gap with `INTERNAL` and an actionable `message`. A
  `v1alpha2` ADR is the right place to revisit.
- Bad, because the session manager is not concurrency-safe and
  has no eviction policy. Documented as a deliberate gap; PR5+
  will revisit when a real client requires it.
- Neutral, because page reuse means residual state (cookies,
  localStorage) persists across `Navigate` calls in the same
  session. This is correct for a multi-step navigation but means
  conformance tests must use a fresh `session_id` per scenario.
- Neutral, because the conformance suite now requires Chromium
  binaries. CI caches them keyed on the Playwright version (see
  ci.yml); the first run after a Playwright bump is ~150 MB
  slower, every subsequent run hits the cache.

### Confirmation

- Acceptance criteria 1–10 of the PR4 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just
  pw-install-browsers && just check && just conf-test` succeeds
  on Linux and macOS.
- The four `Navigate` conformance tests
  (`tools/conformance/tests/test_navigate.py`) pass three times
  in a row with no flakes.
- The error-mapping unit tests (`adapters/playwright/src/errors.test.ts`)
  exercise at least the `TimeoutError`, `net::ERR_NAME_NOT_RESOLVED`,
  generic-Error, and missing-Chromium rows.
- The capability invariant from ADR-0008 still holds: the
  conformance suite asserts byte-for-byte equality between
  `driver.yaml`'s `capabilities:` block and the runtime
  `Capabilities.names`. PR4 adds `navigation` and `js_execution`
  to both.

## Pros and Cons of the Options

### Browser launch timing

#### Lazy on first `Navigate` (chosen)

- Good, because `Initialize` stays a metadata exchange and pays no
  Chromium cost.
- Good, because future engines that probe drivers for capability
  discovery do not pay the launch cost on every probe.
- Bad, because missing-Chromium failures surface later than ideal —
  on the first `Navigate` rather than on `Initialize`.

#### Eager at `Initialize`

- Good, because misconfigured hosts fail at the cheapest possible
  point.
- Bad, because every `Initialize` pays the launch cost, even when
  the session is short-lived or never navigates.

### `session_id` validation

#### Strict (chosen)

- Good, because misconfigured clients fail loudly with a
  protocol-level diagnostic.
- Good, because the protocol contract is documented and enforced
  identically: `Initialize` precedes every other RPC.
- Neutral, because the cost is one extra round-trip when a client
  forgets to call `Initialize`.

#### Lenient (auto-create on first use)

- Good, because the success path is shorter for naive clients.
- Bad, because client bugs (forgetting to call `Initialize`,
  reusing a stale id from a different process) succeed by accident
  instead of being caught early.

### Local HTTP fixture library

#### `http.server.ThreadingHTTPServer` (chosen)

- Good, because it is stdlib — no new dependency.
- Good, because the routes are trivial and a sync handler is more
  readable than an async equivalent.
- Bad, because thread-per-request is not production-grade. For
  four routes serving a handful of requests per test, it is fine.

#### `aiohttp.web`

- Good, because it's a more capable surface for future routes.
- Bad, because it forces async tests and adds a runtime dependency
  for what amounts to four routes.

#### A standalone fixture binary

- Good, because the fixture lifecycle is fully isolated.
- Bad, because subprocess management for a single in-process
  server is overkill.

## More Information

- Playwright `Page.goto()`:
  <https://playwright.dev/docs/api/class-page#page-goto>
- Playwright `BrowserContext`:
  <https://playwright.dev/docs/api/class-browsercontext>
- Playwright `TimeoutError`:
  <https://playwright.dev/docs/api/class-timeouterror>
- Python `http.server` stdlib:
  <https://docs.python.org/3/library/http.server.html>
- Pytest fixture scopes:
  <https://docs.pytest.org/en/stable/how-to/fixtures.html#fixture-scopes>
- GitHub Actions cache for Playwright browsers:
  <https://playwrightsolutions.com/playwright-github-action-to-cache-the-browser-binaries/>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md).
