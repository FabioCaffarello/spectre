---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# curl-impersonate extraction and final capability divergence

## Context and Problem Statement

PR11 (ADR-0016) shipped the curl-impersonate adapter at the
`Initialize` + `Navigate` floor. PR12 closes the v1alpha1 unary
surface for this driver: `Close` becomes the full contract,
`Query` and `Extract` ship together, and the declared capability
list grows from one entry (`navigation`) to **six**:
`extract_attribute`, `extract_html`, `extract_text`, `navigation`,
`query_css`, `query_xpath`. After PR12, the third reference
adapter participates in real extraction workflows against the
conformance suite, and Phase 2's exit criterion — *the same
`job.yaml` runs unchanged across all three adapters where their
capabilities allow* — is met.

The architectural contracts this RPC surface depends on are
already settled. ADR-0010 fixed the strict-invalidation element
lifecycle, the UUID-keyed registry, the capability roles
(descriptive vs gating), and the `SelectorKind` mapping for the
Playwright adapter. ADR-0014 §1 settled the *declared = tested*
progression. ADR-0015 §1 carried strict invalidation forward to
SeleniumBase; ADR-0015 §5 introduced the first capability
divergence (`screenshot_full_page` omitted). ADR-0016 settled
the curl-impersonate adapter's runtime model (subprocess
invocation, WaitCondition no-op, cookie-jar architecture) and
sketched the post-PR12 capability list with `query_text` and
`query_attribute` flagged TBD.

PR12 inherits those contracts and resolves the TBD. The
decisions worth recording in their own ADR are five
curl-impersonate-specific axes — most importantly the contract
that capability declaration is about *semantic equivalence*
across drivers, not implementation feasibility within a single
driver.

## Decision Drivers

- **Capability declarations are a planning surface, not a
  marketing surface.** A capability declared by two adapters
  must mean the same thing to a job that depends on it. If
  driver A's `query_text` is case-insensitive substring matching
  on rendered text and driver B's `query_text` is case-sensitive
  substring matching on DOM text, jobs that work against one
  silently produce different matches against the other. The
  protocol's value depends on *cross-driver semantic equivalence*,
  not just that "the adapter can answer the call".
- **Strict ElementRef invalidation, even where the runtime makes
  some forms of staleness impossible.** ADR-0010 §1's contract is
  cross-driver. curl-impersonate's parsed document is immutable
  within a generation, so the SPA-mutation staleness from
  ADR-0015 §2 cannot occur — but the *contract* stays the same.
  The adapter implements the registry shape, the generation
  counter, and the post-Navigate / post-Close invalidation paths
  byte-for-byte with the browser drivers; the unimplementable
  failure mode is documented as "structurally a no-op", not
  papered over.
- **Honest absence over dishonest presence.** ADR-0015 §5
  introduced the framing for `screenshot_full_page`; PR12
  extends it to two more capabilities (`query_text`,
  `query_attribute`) that goquery technically supports. Capability
  declaration is an evidence-based decision, not a feasibility
  decision.
- **The v1alpha1 wire format is frozen** (ADR-0004). New
  diagnostic content goes in the message, not in new wire fields.
- **MODE_EVAL gating becomes a tested path for the first time.**
  Playwright and SeleniumBase both declare `js_execution`, so the
  gate from ADR-0010 §3 has lived as a unit-tested invariant
  rather than a conformance-tested behaviour. curl-impersonate is
  the first adapter that *omits* `js_execution`; the gate
  finally has a test surface where it actually fires against a
  real adapter at the conformance layer.

## Considered Options

The decisions cluster into five orthogonal axes. Each axis sits
on top of an inherited contract; the option set is the
curl-impersonate-specific deviation, not a re-litigation of the
underlying contract.

1. **`query_text` and `query_attribute` membership in the
   declared list.**
2. **HTML parsing library.**
3. **`ElementRef` lifecycle for an immutable-document driver.**
4. **`SelectorKind` mapping under goquery.**
5. **`Field.Mode` mapping for static HTML.**

Each axis is decided below.

## Decision Outcome

### 1. `query_text` and `query_attribute` are omitted; capability declaration is a semantic-equivalence contract

This is the most architecturally significant decision in PR12,
and the most important architectural clarification the project
has produced since ADR-0001. Document it carefully.

`query_text` is technically implementable against a goquery
document via cascadia's `:contains(text)` pseudo-class.
`query_attribute` is trivially implementable via the CSS
selector `[attribute=value]`. Both produce non-empty
`*goquery.Selection` results against typical HTML. So why omit
them?

Because **capability declaration is about cross-driver semantic
equivalence, not implementation feasibility within a single
driver.** The contract, articulated for the first time here:

> A capability is declared when the adapter implements it with
> semantic equivalence to other adapters declaring the same
> capability. Implementation feasibility is necessary but not
> sufficient.

The two capabilities split as follows:

#### `query_text` — three drivers, three semantics

- **Playwright.** `getByText(value, { exact: false })` searches
  *rendered visible text*, case-insensitively, with whitespace
  normalisation. Hidden elements (CSS `display: none`,
  `visibility: hidden`) are excluded.
- **SeleniumBase.** `By.XPATH, contains(text(), 'value')`
  searches *DOM text*, case-sensitively, no normalisation, no
  visibility filtering. ADR-0015 §3 documented the
  `concat()`-based escape function for the cross-quote case.
- **goquery / cascadia.** `:contains('value')` searches DOM text,
  case-sensitively, no normalisation, no visibility filtering.
  Identical to SeleniumBase's XPath, *not* identical to
  Playwright.

Three drivers, three different semantic contracts under one
capability name. A client writing `query_text` against the
protocol cannot trust that the same selector produces the same
matches across drivers — which defeats the planning surface the
protocol exists to provide. Declaring the capability anyway, on
the basis that "all three runtimes can answer some text query",
would erode the contract that capability presence carries
meaningful planning information.

#### `query_attribute` — semantically aligned but capability-inflationary

CSS-attribute-selector form (`[attr=value]`, `[attr]`) works
identically across all three drivers. So semantic equivalence is
satisfied. Why still omit it?

Because in v1alpha1 there is no driver that meaningfully needs
`query_attribute` as a capability *separate from* `query_css`.
The CSS selector engine on every driver covers it. Declaring
`query_attribute` would inflate the manifest with a capability
whose presence carries no planning information — every driver
that declares `query_css` also implements `query_attribute` by
the same code path. The capability progression contract from
ADR-0014 §1 cuts both ways: *declared = tested*, but also
*declared = meaningful*. A capability that a planner never has
to consult is not earning its place in the manifest.

This is a *deferred*, not *rejected*, position. If a future
driver appears that supports CSS general-purpose selectors but
not attribute selectors specifically (an edge case, but not
inconceivable for an XPath-first DOM library), the v1alpha2
manifest can grow `query_attribute` retroactively. The
v1alpha1 default holds the line.

#### Growth path, recorded for future reference

`query_text` is the more interesting case for v1alpha2. The
likely shape is *sub-capabilities* with explicit semantics
rather than re-using the umbrella name:

- `query_text_substring_ci` — case-insensitive substring match.
  Playwright supports natively; others approximate with helpers
  or out-of-protocol normalisation.
- `query_text_substring_cs` — case-sensitive substring match.
  goquery and SeleniumBase support natively; Playwright requires
  an explicit `{ exact: false }` flag with case-aware comparison.
- `query_text_visible` — visible-text-only matching (excludes
  hidden DOM). Only Playwright reliably supports; SeleniumBase
  needs JS execution; curl-impersonate cannot evaluate
  visibility without a renderer.

ADR-0017 sketches these without committing. The point is not the
specific names but that the v1alpha2 design space exists and is
written down. Future capability proposals — including any
attempt to revisit `query_text` — will be measured against the
semantic-equivalence contract this section establishes.

#### Consequences for the conformance suite

The conformance suite's `test_curl_impersonate_query_text` test
*does not exist*. The Query suite covers CSS and XPath happy
paths and the zero-matches contract. A direct attempt to call
`Query` with `SELECTOR_KIND_TEXT` against curl-impersonate
returns `CODE_INVALID_ARGUMENT` with the message:

> this adapter does not declare query_text; see ADR-0017 §1

The same message shape applies to `SELECTOR_KIND_ATTRIBUTE`.
The rejection is uniform and machine-readable: `query_text` /
`query_attribute` substring in the message lets operators and
future tooling key on the absence-reason.

Rejected:

- **Declare `query_text` and approximate Playwright's
  visibility/case semantics in the curl-impersonate adapter.**
  Impossible without a renderer; would silently drift from the
  declared semantics.
- **Declare `query_text` with case-sensitive DOM-text semantics
  and document the Playwright divergence in the README.** Trades
  the planning surface for README documentation. Operators
  reading the manifest see the capability and assume
  cross-driver consistency.
- **Declare `query_attribute` because the implementation cost is
  effectively zero.** Inflates the manifest. The contract is
  *meaningful planning surface*, not *enumeration of the
  adapter's surface area*.

### 2. goquery as the HTML parsing library, htmlquery for XPath

Chosen: **`github.com/PuerkitoBio/goquery` for parsing and CSS
selection, `github.com/antchfx/htmlquery` for XPath**. goquery
wraps the standard `golang.org/x/net/html` parser and exposes a
jQuery-style CSS selector API via cascadia. htmlquery operates
on the same `*html.Node` graph and adds XPath support.

Reasons in order of weight:

1. **Industry standard for Go HTML scraping.** goquery is the
   de-facto choice; reviewers recognise it immediately and
   future contributors do not have to learn a niche library.
2. **CSS selector coverage via cascadia.** Cascadia implements
   the W3C Selectors Level 3 spec; the same selector strings
   that work in Playwright/SeleniumBase work here.
3. **Permissive HTML parsing.** `golang.org/x/net/html` (which
   goquery wraps) handles malformed HTML the way browsers do.
   Real-world targets emit malformed HTML routinely; a strict
   parser would surface failures that the browser drivers
   silently absorb, breaking cross-driver equivalence.
4. **XPath via htmlquery.** `htmlquery.QueryAll(rootNode,
   xpath)` returns `[]*html.Node`. The same `*html.Node` graph
   goquery wraps means the two libraries interoperate cleanly:
   XPath produces nodes; the adapter wraps them in a
   `*goquery.Selection` so downstream Extract handlers see one
   uniform shape.
5. **Active maintenance.** Both libraries have been stable for
   over a decade; goquery has substantial GitHub activity and
   real-world adoption.

Concurrency note: `*goquery.Document` and `*goquery.Selection`
are not thread-safe. Concurrent reads on the same selection can
race. PR12's session manager is single-threaded sequential
(carrying forward the documented limitation from ADR-0014 §4 /
ADR-0016 §4 — operators must serialise per-session calls).
Concurrency safety remains a v1alpha2 concern.

Rejected:

- **`golang.org/x/net/html` standard library only.** Too
  low-level; would force a reimplementation of CSS selectors
  inside the adapter. cascadia exists; reusing it is the
  correct call.
- **`htmlquery` as the primary library.** Smaller adoption,
  XPath-first ergonomics inappropriate when most jobs are
  CSS-shaped. Using it as a complementary helper for the XPath
  surface is the cleanest split.
- **Vendoring a custom DOM parser.** Real engineering work for
  zero protocol benefit; defeats the architectural-symmetry
  argument from ADR-0016 §1.

### 3. ElementRef lifecycle — strict registry, structurally simpler than browsers

Chosen: **the curl-impersonate registry holds
`(*goquery.Selection, generation)` per UUID**, mirroring the
Playwright `(Locator, generation)` and SeleniumBase
`(WebElement, generation)` shapes byte-for-byte. Allocation in
`Query`, lookup in `Extract`, generation bump in `Navigate`,
forget on `Close` — the contract from ADR-0010 §1 / ADR-0015 §1
carries forward unchanged.

The substantive deviation is what is *absent* from the failure
surface, not what is present:

- **No mid-generation staleness path exists.** Playwright's
  `Locator` re-resolves on each call (so DOM mutations either
  succeed or fail with "no match"); SeleniumBase's `WebElement`
  is a server-side handle that becomes stale on DOM mutation
  (so ADR-0015 §2 introduced the *page-state-change* stale
  message under `CODE_INVALID_ARGUMENT`). curl-impersonate
  parses HTML once per Navigate into an *immutable*
  `*goquery.Document`. Within a generation, the document does
  not mutate — there is no JavaScript engine to mutate it.
- **The post-Navigate stale message is the only stale message.**
  ADR-0010's `"element reference is stale; query was performed
  before a navigation"` and the unknown-ref message
  `"element reference is unknown"` are reproduced verbatim. The
  page-state-change message from ADR-0015 §2 is never reachable
  on this adapter.

This is a positive consequence of the static-HTML model:
**fewer failure modes, cleaner contract**. The session manager
and registry mirror the Selenium and Playwright structures
shape-for-shape, but the mid-generation-stale handler is
structurally a no-op. The registry implementation is symmetric
with the other two drivers anyway (the dead branch costs
nothing in code; the symmetry costs nothing in clarity).

Rejected:

- **Skip the registry entirely; treat each Query as ephemeral.**
  Breaks the strict-invalidation contract from ADR-0010 §1. The
  protocol promises that an `ElementRef` returned by `Query`
  remains valid until `Navigate` or `Close`; "ephemeral" would
  silently shorten that lifetime in a driver-specific way.
- **Skip the generation counter; rely on the post-Navigate
  document replacement to invalidate refs.** Loses the *unknown*
  vs *stale* distinction (ADR-0010 §1's two-message contract).
  Costs ten lines of code for a real operator-facing signal.

### 4. `SelectorKind` mapping — CSS and XPath only

Chosen mapping:

| `SelectorKind`              | Implementation                              | Notes                                                        |
|-----------------------------|---------------------------------------------|--------------------------------------------------------------|
| `SELECTOR_KIND_CSS`         | `doc.Find(selector)` via goquery / cascadia | Default Selectors-3 behaviour.                              |
| `SELECTOR_KIND_XPATH`       | `htmlquery.QueryAll(root, selector)` then wrap nodes in a `*goquery.Selection` | Uniform downstream shape: Extract reads `*goquery.Selection` regardless of selector kind. |
| `SELECTOR_KIND_TEXT`        | rejected with `CODE_INVALID_ARGUMENT`       | `query_text` not declared (decision 4.1).                    |
| `SELECTOR_KIND_ATTRIBUTE`   | rejected with `CODE_INVALID_ARGUMENT`       | `query_attribute` not declared (decision 4.1).               |
| `SELECTOR_KIND_UNSPECIFIED` | rejected with `CODE_INVALID_ARGUMENT`       | Mirrors ADR-0010 §5 / ADR-0015 §3.                          |

The TEXT and ATTRIBUTE rejection messages reference ADR-0017 §1
explicitly so an operator hitting the rejection at runtime has a
direct path to the rationale.

cascadia is case-sensitive on attribute values by default. This
matches both SeleniumBase's CSS-selector path and Playwright's
default. Documented here for completeness even though
`query_attribute` is not declared.

Rejected:

- **Implement TEXT via `:contains()` since cascadia supports
  it.** See decision 4.1 — semantic mismatch with Playwright.
- **Implement ATTRIBUTE via `[name=value]` since the syntax is
  trivial.** See decision 4.1 — capability inflation.
- **Auto-detect `kind` from selector shape.** ADR-0010 §5
  rejected this for Playwright on the same grounds: explicit
  `kind` is a feature.

### 5. `Field.Mode` mapping for static HTML

Chosen mapping:

| `Field.Mode`        | Implementation                                | Selenium/Playwright deviation                                                                  |
|---------------------|-----------------------------------------------|------------------------------------------------------------------------------------------------|
| `MODE_TEXT_CONTENT` | `selection.Text()`                            | Equivalent to DOM `textContent` for static HTML. Same wire data both browser drivers produce. |
| `MODE_INNER_TEXT`   | `selection.Text()` (same as `TEXT_CONTENT`)   | **Documented semantic approximation** — see below.                                            |
| `MODE_INNER_HTML`   | `selection.Html()`                            | Returns `(string, error)` in goquery; the error becomes `CODE_INTERNAL`.                      |
| `MODE_OUTER_HTML`   | `golang.org/x/net/html.Render` against the underlying `*html.Node` | goquery does not expose outer HTML directly; the helper is in `internal/parser`. |
| `MODE_ATTR`         | `selection.Attr(field.arg)` — returns `(value, exists)` | `("", false)` encodes as JSON `null`; `(value, true)` encodes as JSON-encoded string. |
| `MODE_EVAL`         | rejected with `CODE_CAPABILITY_MISSING`       | `js_execution` not declared (decision 4.1's growth-path discussion).                          |

The `MODE_INNER_TEXT` choice is the most substantive deviation
from the browser drivers. In rendered DOM, `innerText` is
defined (loosely) to exclude hidden text — content under
`display: none`, `visibility: hidden`, elements with `aria-hidden`,
and similar visibility predicates. Computing visibility without
a layout engine is not just hard; it requires resolving CSS,
applying it to the box tree, and walking the cascade. The
curl-impersonate adapter has none of those primitives.

The chosen behaviour: **`MODE_INNER_TEXT` falls back to the same
output as `MODE_TEXT_CONTENT`**. This is a documented semantic
approximation, not a bug. Clients who need true visible-text
semantics must use a browser-based driver. The README and the
decision-table comment in `server.go` make this explicit. A
v1alpha2 capability split (per decision 4.1's growth path) could
distinguish the two — `extract_inner_text_visible` would be
declared only by adapters that can compute visibility — but
that is future work, not this PR.

The capability gate from ADR-0010 §3 finally has a real test
surface. Both Playwright and SeleniumBase declare `js_execution`,
so the gate has lived as a unit-tested invariant in their
adapters and as a positive conformance test (`MODE_EVAL`
succeeds). curl-impersonate is the first adapter that *omits*
`js_execution`. The conformance test
`test_curl_impersonate_extract_eval_returns_capability_missing`
is the first conformance test that exercises the negative path
of the runtime capability gate. The handler checks for any
`MODE_EVAL` field at the start of the request (atomic
fail-the-whole-request semantics; ADR-0010 §3) before any field
is read; the response is `CODE_CAPABILITY_MISSING` with the
message:

> MODE_EVAL requires the js_execution capability; this adapter
> does not declare it

This is the headline conformance signal of PR12.

Rejected:

- **Implement a tiny CSS-aware visibility heuristic for
  `MODE_INNER_TEXT`** (e.g. strip text under elements with
  `style="display:none"`). Trades semantic clarity for surface
  complexity that operators would have to reason about. The
  approximation is uniform; the heuristic would not be.
- **Reject `MODE_INNER_TEXT` with `CODE_INVALID_ARGUMENT`**
  ("not supported on static-HTML drivers"). Tighter, but the
  result for clients who want a static-HTML approximation of
  text content is "switch modes" rather than "your job runs
  with a documented approximation". The latter aligns with how
  cross-driver-portable jobs survive the capability matrix.

## Confirmation

- Acceptance criteria 1–12 of the PR12 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just sb-bootstrap
  && just ci-bootstrap && just check && just conf-test` succeeds
  on Linux. macOS notes for curl-impersonate availability are
  documented in the adapter README.
- Three runs of `just conf-test` in a row pass with no flakes.
  The ~9 new conformance tests (Close × 3, Query × 3,
  Extract × 3) bring the curl-impersonate fixture's suite from
  PR11's 4 tests to ~13 tests.
- The byte-for-byte capability assertion holds for the
  curl-impersonate adapter against the six declared names
  (alphabetical: `extract_attribute`, `extract_html`,
  `extract_text`, `navigation`, `query_css`, `query_xpath`).
- The coherence assertion accepts the new list. `extract_eval`
  is absent (no `js_execution`), so the rule from ADR-0010 §3
  remains structurally a no-op for this driver — the test
  still asserts the rule rejects an `extract_eval`-without-
  `js_execution` candidate.
- `test_curl_impersonate_extract_eval_returns_capability_missing`
  passes — the first conformance test in the entire suite that
  exercises the runtime capability gate's negative path.
- A unit test on the engine's planner confirms that a job using
  `js_execution` or any `screenshot_*` capability against
  `driver: curl-impersonate` fails capability validation before
  any adapter launches. (PR7 already wired the planner; PR12
  verifies the curl-impersonate row.)
- `spectre run examples/curl-impersonate-extract/job.yaml`
  produces a JSONL file with the expected rows against the
  local HTTP fixture (or a documented public-internet variant).
- A short cross-driver demo job using only the four-capability
  intersection (`navigation`, `query_css`, `extract_text`,
  `extract_attribute`) runs against `driver: playwright`,
  `driver: seleniumbase`, and `driver: curl-impersonate`,
  producing equivalent JSONL output. **This validates Phase 2's
  exit criterion.**

## Consequences

- Good, because the *semantic-equivalence contract* for
  capability declaration is now written down. Future
  capability proposals will be measured against it. The
  `query_text` / `query_attribute` omission is the first
  application of the rule; ADR-0015 §5's `screenshot_full_page`
  omission is retroactively re-classified as the same
  pattern (Selenium's window-resize approximation would have
  been semantically inequivalent to Playwright's true
  full-page capture).
- Good, because the cumulative capability picture across the
  three Phase 2 drivers is now complete and demonstrably
  load-bearing:

  | Capability             | Playwright | SeleniumBase | curl-impersonate |
  |------------------------|:----------:|:------------:|:----------------:|
  | `navigation`           |     ✓      |      ✓       |        ✓         |
  | `query_css`            |     ✓      |      ✓       |        ✓         |
  | `query_xpath`          |     ✓      |      ✓       |        ✓         |
  | `extract_text`         |     ✓      |      ✓       |        ✓         |
  | `extract_html`         |     ✓      |      ✓       |        ✓         |
  | `extract_attribute`    |     ✓      |      ✓       |        ✓         |
  | `js_execution`         |     ✓      |      ✓       |        —         |
  | `query_text`           |     ✓      |      ✓       |        —         |
  | `query_attribute`      |     ✓      |      ✓       |        —         |
  | `extract_eval`         |     ✓      |      ✓       |        —         |
  | `screenshot_viewport`  |     ✓      |      ✓       |        —         |
  | `screenshot_element`   |     ✓      |      ✓       |        —         |
  | `screenshot_full_page` |     ✓      |      —       |        —         |
  | **Total declared**     |   **13**   |    **12**    |      **6**       |

  curl-impersonate's set is a strict subset of SeleniumBase's,
  which is a strict subset of Playwright's. Three drivers,
  three honestly different capability sets, one protocol.
- Good, because MODE_EVAL gating is exercised at conformance
  for the first time. The gate's behaviour was correct since
  ADR-0010, but until PR12 every driver declared
  `js_execution` and the negative path was unit-only. The
  conformance test closes the loop.
- Good, because the `MODE_INNER_TEXT` approximation is
  documented in the ADR, the README, and the handler comment.
  An operator who hits surprising results has a clear path to
  the rationale and the v1alpha2 escape hatch.
- Good, because the registry's "no mid-generation staleness"
  property simplifies the failure matrix without weakening
  the contract. Symmetry across drivers comes for free; the
  static-HTML model just makes one branch unreachable in
  practice.
- Bad, because clients who want true visible-text semantics
  on the curl-impersonate adapter cannot get them in v1alpha1.
  The approximation is uniform but not always desirable.
  Documented escape hatches: switch driver, or wait for
  v1alpha2's capability split.
- Bad, because the protocol gains no new structural test for
  the semantic-equivalence contract. The contract lives in
  ADR-0017 §1 and in code review; a static check that flags
  capability declarations across drivers with divergent
  observable behaviour is a v1alpha2 follow-up.
- Bad, because `MODE_OUTER_HTML` requires a small
  `golang.org/x/net/html.Render` helper rather than a
  one-liner. goquery does not expose outer HTML directly.
  The helper lives in `internal/parser` so the rest of the
  adapter does not have to know about the underlying node
  graph.
- Neutral, because htmlquery is added as a runtime
  dependency. It's small, stable, well-adopted, and
  composes cleanly with goquery's underlying `*html.Node`
  graph.
- Neutral, because `selection.Attr` returns `(value, exists)`.
  The encoder distinguishes "attribute absent" (`null`) from
  "attribute present and empty" (`""`) — same contract
  Playwright and SeleniumBase honour; spelled out here so a
  future reader does not collapse them.

## Pros and Cons of the Options

### `query_text` / `query_attribute` membership

#### Omit (chosen)

- Good, because preserves cross-driver semantic equivalence as
  the load-bearing meaning of capability declaration.
- Good, because the v1alpha2 sub-capability growth path is
  written down explicitly.
- Good, because the rejection messages reference the ADR, so
  operators have a direct path to the rationale.
- Bad, because clients who want a curl-impersonate-flavoured
  `query_text` (DOM-text, case-sensitive) cannot use the
  capability-name shortcut and must use XPath
  (`//*[contains(text(), 'value')]`) instead.

#### Declare both

- Good, because the manifest is shorter to write.
- Good, because clients who already use these capabilities on
  browser drivers can switch driver without changing job YAML.
- Bad, because `query_text` would carry a different semantic
  contract under each driver name, eroding the planning surface.
- Bad, because future capability proposals lose the precedent
  that declaration is an evidence-based decision rather than a
  feasibility decision.

### HTML parsing library

#### goquery + htmlquery (chosen)

- Good, because both are industry-standard and well-maintained.
- Good, because the two libraries share the underlying
  `*html.Node` graph; CSS and XPath integrate cleanly.
- Bad, because two dependencies instead of one.

#### x/net/html only

- Good, because zero new external dependencies.
- Bad, because reimplementing CSS selectors inside the adapter
  is real work for no protocol benefit.

### `MODE_INNER_TEXT` semantics

#### Approximate as `TEXT_CONTENT` (chosen)

- Good, because the wire shape stays consistent with browser
  drivers (clients always get *something* readable).
- Good, because the deviation is documented and uniform.
- Bad, because the result is not what `innerText` means in a
  browser; clients depending on visibility filtering get wrong
  data.

#### Reject with `CODE_INVALID_ARGUMENT`

- Good, because the failure is loud rather than silent.
- Bad, because cross-driver-portable jobs that incidentally
  use `MODE_INNER_TEXT` (rather than `TEXT_CONTENT`) would
  break against this adapter for a difference that is
  semantically tiny in most static-HTML cases.

#### Compute a CSS-aware visibility heuristic

- Good, because the result approaches browser semantics for
  trivial cases.
- Bad, because the heuristic is fragile — `display: none` set
  via JS, computed styles, media queries, and pseudo-classes
  all diverge from a static check. The approximation would be
  *worse* than the uniform fallback for cases the heuristic
  partially handles.

## More Information

- goquery: <https://github.com/PuerkitoBio/goquery>
- cascadia (CSS Selectors): <https://github.com/andybalholm/cascadia>
- htmlquery (XPath over `*html.Node`): <https://github.com/antchfx/htmlquery>
- `golang.org/x/net/html.Render`:
  <https://pkg.go.dev/golang.org/x/net/html#Render>
- W3C Selectors Level 3: <https://www.w3.org/TR/selectors-3/>
- DOM `textContent` vs `innerText`:
  <https://developer.mozilla.org/en-US/docs/Web/API/Node/textContent>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0010 Element lifecycle and capability gating](0010-element-lifecycle-and-capability-gating.md),
  [ADR-0014 SeleniumBase adapter and cross-language conformance](0014-seleniumbase-adapter-and-cross-language-conformance.md),
  [ADR-0015 SeleniumBase element lifecycle and screenshot coverage](0015-seleniumbase-element-lifecycle-and-screenshot-coverage.md),
  [ADR-0016 curl-impersonate adapter and third-runtime divergence](0016-curl-impersonate-adapter-and-third-runtime-divergence.md).
