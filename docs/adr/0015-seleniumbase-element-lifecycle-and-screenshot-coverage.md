---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# SeleniumBase element lifecycle and screenshot coverage

## Context and Problem Statement

PR9 (ADR-0014) began the second-language demonstration of the
Driver Protocol by porting `Initialize` and `Navigate` to
SeleniumBase, plus a thin `Close` for the engine's executor.
The capability progression contract that ADR landed —
*declared capabilities reflect tested capabilities* — left the
PR9 manifest at exactly `["navigation"]`. PR10 implements the
remaining v1alpha1 unary RPCs (`Close` complete, `Query`,
`Extract`, `Screenshot`) for SeleniumBase and grows the manifest
to twelve names, in alphabetical order, with one capability
deliberately absent.

The architectural contracts this RPC surface depends on are
already settled: ADR-0010 fixed the strict-invalidation element
lifecycle, the UUID-keyed registry, the capability roles
(descriptive vs gating), and the `SelectorKind` mapping for the
Playwright adapter. ADR-0011 fixed the Screenshot scopes/formats,
the JPEG quality default of 80, the read-only contract (no
generation bump), the failure shape (empty bytes plus populated
`error`), and the soft-warn payload boundary at 3MB.

PR10 inherits those contracts wholesale. The decisions worth
recording in their own ADR are the SeleniumBase-specific
deviations — the implementation differences forced by the
Selenium WebDriver model that the Playwright contract did not
have to confront.

## Decision Drivers

- **Driver-agnostic protocol over driver parity.** ADR-0001
  framed the protocol as a substrate that both Playwright and
  SeleniumBase implement, not a thin wrapper around any single
  runtime. PR10 is the first PR where the two drivers diverge in
  *what they can do*, not just *how they do it* — and the
  protocol's design is what makes that divergence honest.
- **The capability progression contract is enforced, not
  aspirational.** ADR-0014 §1 made declared capabilities reflect
  tested capabilities. PR10's twelve declared names each have a
  conformance test; the thirteenth Playwright name
  (`screenshot_full_page`) has no SeleniumBase test because the
  underlying behaviour is not reliably implementable in standard
  WebDriver — so the name is not declared.
- **Selenium's `WebElement` is a direct DOM handle, not a lazy
  descriptor.** Playwright's `Locator` re-resolves on each
  operation; Selenium's `WebElement` is a server-side handle
  that can be invalidated by a DOM mutation independent of any
  protocol-level navigation. The element-lifecycle contract has
  to absorb that without weakening the strict-invalidation
  promise from ADR-0010.
- **The v1alpha1 wire format is frozen** (ADR-0004). Whatever
  failure modes Selenium introduces must collapse onto the
  existing `DriverError.Code` enum. New diagnostic content goes
  in the message, not in new wire fields.
- **Honest absence over dishonest presence.** The protocol's
  capability surface is the planning surface for any future
  engine. A capability declared but not reliably implemented is
  worse than a capability omitted: it becomes a quiet failure
  for clients that planned around its presence.

## Considered Options

The decisions cluster into five axes. Each axis sits on top of
an inherited contract; the option set in each axis is the
SeleniumBase-specific deviation, not a re-litigation of the
underlying contract.

1. **Architectural inheritance from ADR-0010 and ADR-0011.**
2. **`WebElement` vs `Locator` and the staleness failure surface.**
3. **`SELECTOR_KIND_TEXT` mapping under Selenium.**
4. **`Field.Mode` mapping onto Selenium APIs.**
5. **`screenshot_full_page` capability membership.**

Each axis is decided below with its own option set.

## Decision Outcome

### 1. Architectural inheritance from ADR-0010 and ADR-0011

Chosen: **PR10 implements the contracts established by
ADR-0010 and ADR-0011 without re-litigation.** The
SeleniumBase adapter's `Query`, `Extract`, `Screenshot`, and
the promoted `Close` reproduce the wire-level contract the
Playwright adapter exposes; the conformance suite asserts the
shared contract on both drivers separately (no
parameterisation, per ADR-0014 §4).

Specifically inherited, byte-for-byte:

- Strict ElementRef invalidation on `Navigate` and `Close`.
  The session's generation counter starts at zero, increments
  on every successful `Navigate`, and is consulted in every
  `Extract` / element-scoped `Screenshot`.
- UUID-keyed ElementRef registry, scoped per session, with
  prior-generation entries retained until the session is
  forgotten so the registry can distinguish *stale* (id was
  issued in a prior generation) from *unknown* (id was never
  issued for this session).
- `MODE_EVAL` runtime gate on the `js_execution` capability
  (ADR-0010 §3). The whole `Extract` request fails with
  `CODE_CAPABILITY_MISSING` if any field requests
  `MODE_EVAL` while `js_execution` is not declared.
- Zero matches in `Query` is success with an empty list, not
  `CODE_NOT_FOUND` (ADR-0010 §4).
- Read-only Screenshot contract (ADR-0011 §4): no generation
  bump, refs valid before remain valid after.
- JPEG quality default of 80 (ADR-0011 §2).
- Failure response shape: empty `image`, empty `content_type`,
  populated `error` (ADR-0011 §5).
- Soft-warn at 3MB on stderr; no chunking, no truncation
  (ADR-0011 §3).

The job of this ADR is not to repeat those contracts but to
record the SeleniumBase-specific *deviations* the implementation
forced. Decisions 2–5 below are the deviations.

Rejected:

- **Re-litigate the contracts on the Python side.** Tempting
  because Selenium and Playwright differ in their failure
  surfaces and their selector helpers. Rejected because the
  protocol's value depends on multiple drivers honouring the
  same wire-level contract; the burden of accommodating each
  driver's quirks belongs in the adapter, not in the protocol.
- **Extract a shared element-lifecycle contract module before
  the third driver lands.** Premature: two implementations make
  it hard to see which parts are essential and which are
  Playwright artifacts. PR12 (curl-impersonate) is the natural
  point for that decision, if it surfaces.

### 2. `WebElement` vs `Locator` — same registry, two stale messages

Chosen: **the SeleniumBase registry stores
`(WebElement, generation)` per UUID** (mirrors the Playwright
registry's `(Locator, generation)`), and the runtime
`StaleElementReferenceException` is mapped onto a *distinct
message* under the same wire code as the post-Navigate stale
case.

Background. Selenium's `WebElement` is a direct DOM handle: the
WebDriver server stores a server-side reference to a specific
DOM node and the client invokes methods through that handle. If
the node is detached from the DOM — for example by a JavaScript
mutation that re-renders a SPA route, swaps a virtual list
viewport, or replaces a node the framework still considers
"the same" — the handle becomes stale and any subsequent method
call raises `StaleElementReferenceException`. Playwright's
`Locator` does not have this failure mode: it re-resolves on
each operation, so a DOM mutation between `Query` and `Extract`
either succeeds (the locator finds the element again) or fails
with a "no match" error that maps cleanly to
`CODE_INVALID_ARGUMENT`.

The strict-invalidation contract from ADR-0010 covers the
*navigation* case. The SPA-mutation case is a new failure mode
that needs to land somewhere on the wire.

Implementation:

- The registry stores `(WebElement, generation)` per UUID
  exactly like the Playwright registry.
- The Extract handler wraps the field-reading block in a
  single `try/except StaleElementReferenceException`; on
  catch, it returns `CODE_INVALID_ARGUMENT` with the message:

  ```
  element became stale during page state change
  ```

- The post-Navigate case returns the same message ADR-0010
  fixed for Playwright:

  ```
  element reference is stale; query was performed before a navigation
  ```

Both responses share the same wire code
(`CODE_INVALID_ARGUMENT`); the message is the only signal that
distinguishes them. Operator-facing tooling can grep on
"page state change" vs "before a navigation" to distinguish the
cause without forcing the protocol to grow a new code.

Why two messages and not one. The two cases motivate different
client-side responses:

- **Post-Navigate stale**: the client navigated; the contract
  required the client to re-`Query` against the new page. The
  fix is a deterministic protocol-level dance.
- **Page-state-change stale**: the page mutated independently of
  any client action. The fix is workload-specific (re-`Query`,
  poll, or wait for the SPA's idle state). The wire code is the
  same because v1alpha1 has no `CODE_RACE` or
  `CODE_PRECONDITION_FAILED` to distinguish them, but the
  message is the only contract addition this PR makes that costs
  nothing and reads operationally useful.

The conformance suite covers the post-Navigate case directly
(`test_seleniumbase_extract_after_navigate_returns_invalid_argument`).
The page-state-change case is exercised at unit level in
`test_sessions.py` because reproducing a real
`StaleElementReferenceException` deterministically requires
fragile SPA orchestration; the unit test fakes the exception
to assert the mapping.

Rejected:

- **Single message for both stale cases.** Loses operator
  signal for free. The wire code is shared either way; the
  message is the only place the distinction can live in
  v1alpha1.
- **Tighten the wire contract with a new code.** v1alpha1 is
  frozen (ADR-0004). A new code belongs in v1alpha2 if real
  workloads surface the need. For now, a documented message
  difference is the cheapest honest signal.
- **Catch `StaleElementReferenceException` and silently re-`Query`.**
  Same correctness footgun ADR-0010 rejected for the
  post-Navigate case: silent recovery from staleness produces
  data from different DOM nodes that happen to match the same
  selector. Fail-loud is the contract for v1alpha1.

### 3. `SELECTOR_KIND_TEXT` via XPath, with proper escaping

Chosen: **map `SELECTOR_KIND_TEXT` to a Selenium XPath that
substring-matches `text()`, with an escape function that
handles both quote characters via `concat()` so any printable
ASCII selector is safe.**

Background. Selenium has no native `getByText` equivalent.
Playwright's `getByText(s, { exact: false })` is a curated
substring matcher; Selenium's closest analogue is an XPath
expression of the form `//*[contains(text(), 'needle')]`. The
naïve substitution fails the moment a selector contains a
single quote, and the dual-substitution fails the moment a
selector contains both quote types.

Implementation. The adapter ships a small helper:

```python
def text_selector_to_xpath(text: str) -> str:
    """Convert a TEXT-kind selector to a Selenium-safe XPath.

    Uses XPath concat() to escape both single and double quotes
    so the selector is safe regardless of contents.
    """
    if "'" not in text:
        return f"//*[contains(text(), '{text}')]"
    if '"' not in text:
        return f'//*[contains(text(), "{text}")]'
    segments: list[str] = []
    for part in text.split('"'):
        if "'" not in part:
            segments.append(f"'{part}'")
        else:
            segments.append(f'"{part}"')
    expression = ", '\"', ".join(segments)
    return f"//*[contains(text(), concat({expression}))]"
```

Test cases recorded with the implementation:

- `"hello"` → `//*[contains(text(), 'hello')]`
- `"it's"` → `//*[contains(text(), "it's")]`
- `'say "hi"'` → uses `concat()` to splice the double quote
  literal between single-quoted segments.

The conformance suite exercises only the no-quote happy path
(matching ADR-0010 §5's substring contract). The dual-quote
case is unit-tested against the helper directly so it stays
green without standing up Chrome.

Selector-kind alignment with Playwright stays as ADR-0010 §5
defined it:

| `SelectorKind`              | Selenium invocation                                                  |
|-----------------------------|----------------------------------------------------------------------|
| `SELECTOR_KIND_CSS`         | `driver.find_elements(By.CSS_SELECTOR, selector)`                    |
| `SELECTOR_KIND_XPATH`       | `driver.find_elements(By.XPATH, selector)`                           |
| `SELECTOR_KIND_TEXT`        | `driver.find_elements(By.XPATH, text_selector_to_xpath(selector))`   |
| `SELECTOR_KIND_ATTRIBUTE`   | CSS bracketed: `driver.find_elements(By.CSS_SELECTOR, "[" + selector + "]")` |
| `SELECTOR_KIND_UNSPECIFIED` | rejected with `CODE_INVALID_ARGUMENT`                                |

Substring vs exact match is the same choice ADR-0010 §5 made
for Playwright. `contains(text(), …)` is XPath's substring
match by default; an exact-match capability remains a v1alpha2
candidate.

Rejected:

- **Validate selector and reject if it contains quotes.** Too
  restrictive for real-world use cases (e.g. matching button
  labels with apostrophes). The `concat()` escape covers every
  printable-ASCII selector; the cost is one more code path per
  selector.
- **Use a tag-name-restricted XPath like `//*[normalize-space(text())='X']`**.
  Promotes substring-match to an exact match, which would
  silently disagree with Playwright's substring default.
  Cross-driver consistency on the wire is more important than
  the convenience of a normalised match.

### 4. `Field.Mode` mapping onto Selenium

Chosen: **the table below**. The wire-level result for each
mode is the same as Playwright; the divergence is purely in
how the adapter sources the value.

| `Field.Mode`        | Selenium implementation                       | Selenium quirk                                                                       |
|---------------------|-----------------------------------------------|--------------------------------------------------------------------------------------|
| `MODE_TEXT_CONTENT` | `element.get_attribute("textContent")`        | Works despite not being a real attribute — Selenium synthesises it via JS internally. |
| `MODE_INNER_TEXT`   | `element.text`                                | Property, not attribute. Returns visible text only.                                  |
| `MODE_INNER_HTML`   | `element.get_attribute("innerHTML")`          | Same synthetic-attribute pattern as `textContent`.                                   |
| `MODE_OUTER_HTML`   | `element.get_attribute("outerHTML")`          | Same.                                                                                |
| `MODE_ATTR`         | `element.get_attribute(field.arg)`            | Returns `None` for absent attributes; encoded as JSON `null`.                        |
| `MODE_EVAL`         | `driver.execute_script(field.arg, element)`   | First positional arg to script is the element; reachable as `arguments[0]` in JS.    |

Notes that influence the implementation but not the wire:

- `get_attribute` on Selenium synthesises `textContent` /
  `innerHTML` / `outerHTML` via an internal JavaScript shim.
  These are *not* real DOM attributes. The behaviour has
  shipped since Selenium 3 and is documented in the WebDriver
  reference; the adapter relies on it for parity with
  Playwright's `Locator.textContent()` / `innerHTML()` /
  `evaluate("el => el.outerHTML")`.
- `MODE_EVAL` requires the `js_execution` capability per
  ADR-0010 §3. The capability gate runs at the start of the
  Extract handler before any field is read; if any field
  requests `MODE_EVAL` while the declared list omits
  `js_execution`, the handler returns
  `CODE_CAPABILITY_MISSING` and no field is evaluated. The
  whole-request-or-nothing contract from ADR-0010 carries
  through unchanged.
- `MODE_UNSPECIFIED` is rejected with `CODE_INVALID_ARGUMENT`
  per the ADR-0010 §5 default — the adapter forces clients to
  set `mode` explicitly.

Rejected:

- **Use `driver.execute_script("return arguments[0].textContent;", element)`
  for `MODE_TEXT_CONTENT` instead of `get_attribute`.** Same
  result, an extra round-trip per field, no readability benefit.
  The `get_attribute` path is the conventional Selenium
  idiom.
- **Treat `null` attributes as empty strings.** Loses the
  signal that an attribute is absent vs present-but-empty.
  Encoding `None` as JSON `null` preserves the distinction;
  clients that prefer empty-string can `?? ""` after parsing.

### 5. `screenshot_full_page` is omitted from the SeleniumBase capability list

This is the most architecturally significant decision in PR10
because it is the **first time a driver declares a strict
subset of another driver's capabilities**, validating the
core thesis from ADR-0001 that capability declaration is
substantive, not decorative.

Decision: SeleniumBase declares **two** screenshot
capabilities (`screenshot_viewport`, `screenshot_element`),
not three. `screenshot_full_page` is *absent* from the
manifest, absent from `CAPABILITY_NAMES`, and absent from the
runtime `Capabilities.names` returned by `Initialize`.

Rationale:

1. **Selenium WebDriver returns the viewport by default.**
   `driver.get_screenshot_as_png()` captures the visible
   viewport only. There is no API in standard WebDriver for
   full-page capture; the W3C WebDriver spec does not require
   one and most implementations follow the spec.
2. **The window-resize trick is fragile.** A common
   workaround is: read `document.body.scrollHeight`, resize
   the browser window to that height, screenshot, restore the
   original size. This works for static pages and breaks on
   real ones:
   - **Sticky headers** are rendered at every viewport
     position the resize creates, producing a stitched image
     with the header repeated down the page.
   - **Lazy-loaded images** that depend on viewport-intersection
     observers fire as the resize advances, but the screenshot
     captures placeholder content if the load did not finish
     in time.
   - **Fixed-position overlays** render at the wrong logical
     positions because the window's content-area height has
     changed underneath them.
   - **Pages whose layout responds to height** (sticky footers
     above the fold, intentionally short content areas) shift
     visibly between the resize and the capture.
   The result is a screenshot that *looks* full-page until
   someone inspects it carefully — exactly the kind of quiet
   failure ADR-0010 §1 chose strict invalidation to avoid.
3. **CDP-based capture works in Chrome but couples the
   adapter to Chrome.** The Chrome DevTools Protocol exposes
   `Page.captureScreenshot` with `captureBeyondViewport: true`,
   which produces a true full-page capture. SeleniumBase has
   `execute_cdp_cmd` to invoke it. The catch:
   - The CDP namespace and its `captureBeyondViewport` flag
     are Chromium-family specific (Chrome, Edge, Brave).
     Firefox's WebDriver BiDi has a different shape.
   - Using CDP from inside the SeleniumBase adapter bypasses
     the Selenium WebDriver abstraction the rest of the
     adapter is built on, and creates two parallel control
     paths in one driver.
   - The same CDP call has subtle behaviour differences on
     long pages with lazy-loaded content; honest support
     would require additional scrolling/waiting orchestration
     that the protocol cannot specify.
4. **The capability progression contract from ADR-0014 §1
   says declared = tested.** Without a reliable, browser-
   independent implementation, declaring the capability and
   adding a conformance test would either green only on
   Chromium hosts (silently brittle) or pass on a broken
   implementation that the test asserts is "good enough".
   The protocol prefers honest absence over dishonest
   presence.

Consequences flowing from the decision:

- A `spectre run` job whose YAML eventually requires
  `screenshot_full_page` (the v1alpha1 DSL does not, but a
  future v1alpha2 DSL would) against `driver: seleniumbase`
  will fail at engine validate-time with the same
  `validate_capabilities` mechanism PR7 already wired. The
  engine's planner sees the missing required capability and
  rejects the plan before any browser launches. The test
  `validate_capabilities_reports_seleniumbase_full_page_missing`
  in `core/engine/src/plan.rs` synthesises that scenario and
  asserts the rejection path.
- A direct caller that bypasses the engine and issues a
  `Screenshot` RPC with `scope == FULL_PAGE` against the
  SeleniumBase adapter receives `CODE_CAPABILITY_MISSING`
  with the message `"the seleniumbase driver does not
  declare screenshot_full_page"`. The adapter performs this
  defensive check at the start of the `Screenshot` handler;
  the engine's planner is the primary line of defence, the
  adapter check is the second.
- Users who need full-page capture for SeleniumBase have two
  paths in v1alpha1: switch to the Playwright driver, or
  capture a series of element screenshots of bounded
  regions. Neither is a perfect substitute. The honest
  posture is what the protocol affords today.

Path forward in v1alpha2. The likely shape is a sub-capability
rather than a re-litigation of the umbrella name. A
hypothetical `screenshot_full_page_cdp` capability could be
declared by adapters that opt into a Chromium-specific CDP
implementation, with operators aware of the dependency.
SeleniumBase's adapter could declare it once UC Mode (or a
direct CDP integration) ships, and the v1alpha2 protocol
would let the engine plan against that name explicitly. PR10
does not make that change; it ships the conservative option.

This is the single most important paragraph in the PR for the
project's external positioning: **we added a driver and the
capability surface shrank.** A driver that quietly fails to
deliver a declared capability erodes the protocol's value;
a driver that honestly omits a capability validates it.

Rejected:

- **Implement `screenshot_full_page` via window-resize anyway
  and document the caveats.** Trades correctness for
  apparent feature completeness. Future operators reading the
  README would see the capability, run a job that depended on
  it, and discover the failure modes only after their data
  was wrong. The fail-loud posture in ADR-0010 §1 applies
  here too.
- **Implement via CDP and declare `screenshot_full_page`
  unconditionally.** Couples the SeleniumBase adapter to
  Chromium and inverts the abstraction the rest of the
  adapter respects. Acceptable as an opt-in
  `screenshot_full_page_cdp` capability in v1alpha2;
  unacceptable as a silent v1alpha1 implementation.
- **Document `screenshot_full_page` as "best effort" in the
  README.** Same erosion of the planning surface — the
  manifest is the contract, not the README.

## Confirmation

- Acceptance criteria 1–15 of the PR10 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just
  sb-bootstrap && just check && just conf-test` succeeds on
  Linux and macOS.
- All 43 conformance tests (Playwright × 23, SeleniumBase ×
  20: Initialize × 1, Navigate × 2, Close × 3, Query × 5,
  Extract × 5, Screenshot × 4) pass three runs in a row with
  no flakes.
- The byte-for-byte capability assertion holds for the
  SeleniumBase adapter against the twelve declared names
  (the eleven from ADR-0010's Playwright list minus
  `screenshot_full_page`).
- The coherence assertion accepts the new list; the unit
  test confirms a list with `extract_eval` and no
  `js_execution` still raises.
- A unit test on the engine's planner asserts that a plan
  requiring `screenshot_full_page` against a declared list
  matching the SeleniumBase manifest fails
  `validate_capabilities` with `screenshot_full_page` in the
  missing names.
- The runtime gate test confirms that `MODE_EVAL` succeeds
  when `js_execution` is declared and would fail with
  `CODE_CAPABILITY_MISSING` if the runtime list omitted it.
- Two distinct stale messages exist in `errors.py` /
  `sessions.py`; conformance covers the post-Navigate
  message, unit tests cover the page-state-change message.
- A `Screenshot` with `scope == FULL_PAGE` against the
  SeleniumBase adapter returns `CODE_CAPABILITY_MISSING`
  with the documented message.

## Consequences

- Good, because the protocol's planning surface is now
  meaningfully cross-driver. A future engine reading two
  manifests sees the SeleniumBase driver declare twelve
  names and the Playwright driver declare thirteen, and
  knows which jobs can run where without launching either.
- Good, because the read-only Screenshot contract from
  ADR-0011 §4 is reproduced unchanged on the second driver,
  giving v1alpha2 evidence that read-only RPCs generalise.
- Good, because the two stale messages cost nothing on the
  wire (same code) and give operators a real signal in logs.
  The pattern — *one wire code, two messages for distinct
  failure modes* — is reusable for future RPCs that face
  similar overloading.
- Good, because the XPath escape function is a small,
  composable utility that does not leak Selenium specifics
  into the rest of the adapter. A future maintainer reads it
  once and moves on.
- Bad, because SeleniumBase clients who need full-page
  capture must either switch drivers or accept the manual
  workaround. v1alpha1 has no streaming or chunking either,
  so the gap is documented as a v1alpha2 candidate.
- Bad, because the `WebElement` storage model means the
  registry holds a server-side reference per allocated UUID.
  A long-running session that issues many `Query` calls
  without `Close`-ing accumulates handles. The adapter's
  per-`Navigate` generation bump abandons prior-generation
  entries to GC, but the entries remain in the dict until
  `forgetSession` runs. Documented in the registry comment;
  not a problem at v1alpha1 workload sizes.
- Neutral, because the `WebElement.screenshot_as_png`
  method auto-scrolls to bring an off-screen element into
  view before capturing — same behaviour as Playwright's
  `Locator.screenshot()`. The README documents both.
- Neutral, because Pillow is added as a runtime dependency
  to convert Selenium's PNG output to JPEG (Selenium's
  WebDriver does not return JPEG directly). Pillow is a
  ubiquitous Python image library; the dependency cost is
  acceptable for the parity with Playwright's JPEG output.
- Neutral, because the soft-warn-at-3MB threshold from
  ADR-0011 §3 fires on the SeleniumBase side too. The
  v1alpha1 transport boundary is shared across drivers; the
  warning is shared too.

## Pros and Cons of the Options

### `screenshot_full_page` membership

#### Omit (chosen)

- Good, because honest about Selenium's API surface.
- Good, because it validates the capability progression
  contract from ADR-0014 §1 against the first real divergence.
- Good, because the engine's `validate_capabilities` already
  rejects plans that need omitted capabilities — the
  enforcement is mechanical.
- Bad, because SeleniumBase users who want full-page capture
  pay the workaround tax until v1alpha2.

#### Declare and implement via window-resize

- Good, because parity with Playwright on the manifest.
- Bad, because the implementation produces wrong images on
  realistic pages (sticky headers, lazy images, fixed
  overlays).

#### Declare and implement via CDP

- Good, because the resulting image is correct on Chromium.
- Bad, because the adapter becomes implicitly Chromium-only
  while still claiming to be "the SeleniumBase adapter".
- Bad, because the same capability name claims Chromium and
  Firefox parity that the implementation cannot deliver.

### Stale message overloading

#### Two messages, one code (chosen)

- Good, because preserves the v1alpha1 wire contract.
- Good, because gives operator-facing signal at zero cost.
- Bad, because tools that key on message strings rather than
  codes have a more brittle dependency.

#### One message, one code

- Good, because simpler.
- Bad, because operators cannot tell page-state-change
  staleness from navigation-staleness without re-running.

#### New code in v1alpha2

- Good, because the wire contract carries the distinction.
- Bad, because requires a frozen-protocol change for a
  signal that today's operators can read off the message.

### `MODE_TEXT_CONTENT` via `get_attribute`

#### `element.get_attribute("textContent")` (chosen)

- Good, because matches the Selenium idiom.
- Good, because returns identical wire data to Playwright.
- Bad, because relies on a documented-but-non-obvious
  Selenium synthesis (textContent is not a real attribute).

#### `driver.execute_script("return arguments[0].textContent;", element)`

- Good, because explicit about the JavaScript path.
- Bad, because requires `js_execution` capability for a
  field mode that does not declare it; would force every
  driver to declare `js_execution` to support
  `MODE_TEXT_CONTENT`. Inverts the gating intent of
  ADR-0010 §3.

## More Information

- Selenium WebDriver Python API:
  <https://www.selenium.dev/documentation/webdriver/>
- SeleniumBase reference:
  <https://seleniumbase.io/help_docs/method_summary/>
- StaleElementReferenceException:
  <https://www.selenium.dev/exceptions/#staleelementreferenceexception>
- Selenium `WebElement.get_attribute` (textContent /
  innerHTML / outerHTML synthesis):
  <https://www.selenium.dev/documentation/webdriver/elements/information/#getattribute>
- Pillow (used for the JPEG conversion):
  <https://pillow.readthedocs.io/>
- XPath `concat()` escape pattern:
  <https://www.w3.org/TR/xpath-functions-31/#func-concat>
- Chrome DevTools Protocol `Page.captureScreenshot`
  (rejected for v1alpha1):
  <https://chromedevtools.github.io/devtools-protocol/tot/Page/#method-captureScreenshot>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate, session lifecycle, and the driver error mapping](0009-navigate-and-session-lifecycle.md),
  [ADR-0010 Element lifecycle, capability granularity, and selector mapping](0010-element-lifecycle-and-capability-gating.md),
  [ADR-0011 Screenshot RPC, scope mapping, and payload boundaries](0011-screenshot-rpc-and-payload-boundaries.md),
  [ADR-0014 SeleniumBase adapter and cross-language conformance](0014-seleniumbase-adapter-and-cross-language-conformance.md).
