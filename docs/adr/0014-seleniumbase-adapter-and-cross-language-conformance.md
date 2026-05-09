---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# SeleniumBase adapter and cross-language conformance

## Context and Problem Statement

Phase 1 closed with the Playwright reference adapter implementing
every v1alpha1 unary RPC and `spectre run` driving it end-to-end
against a real target. The protocol's central thesis — *driver-
agnostic, polyglot by responsibility* — has been claimed but not
yet demonstrated. A single TypeScript adapter is one data point;
the architectural argument turns on whether the same protocol can
be re-implemented from scratch in another language without leaking
the first implementation's choices.

PR9 begins that demonstration with SeleniumBase, a Python adapter
built on Selenium WebDriver. The scope mirrors PR3 + PR4
(`Initialize` + `Navigate`) so the reasoning that landed for
Playwright in those PRs gets re-examined against a different
runtime. PR10 will extend coverage to `Query` + `Extract` (and the
richer `Close` conformance tests); PR11 will add `Screenshot`. This
staged rollout matches the Playwright trajectory and avoids piling
twenty new conformance tests into a single PR.

`Close` itself is implemented in PR9 alongside `Initialize` and
`Navigate`. The reason is mechanical rather than scope-creep: the
engine's executor always issues `Close` at the end of a plan
(ADR-0012), so the PR9 navigate-only example cannot complete via
`spectre run` without it. `Close` carries no capability declaration
in v1alpha1 — it is a baseline session-lifecycle RPC like
`Initialize` — so wiring it does not violate the "declared =
tested" rule below. The conformance suite still defers `Close`
tests to PR10 because the interesting cases (closing an unknown
id, closing twice) belong with the broader element-lifecycle
suite that lands then.

The schema decisions are settled (ADR-0001, ADR-0003, ADR-0004),
the codegen is settled (ADR-0007), the handshake is settled
(ADR-0008), the Navigate lifecycle is settled (ADR-0009), the
capability gating model is settled (ADR-0010), and the engine
binary is settled (ADR-0013). The remaining decisions are about
how a second-language adapter participates in those contracts,
and how the conformance suite stays honest as it grows from one
driver to two.

## Decision Drivers

- **Cross-language correctness over convenience.** The Python
  adapter must arrive at the same wire contract Playwright produced
  without reading Playwright's source. The discipline of re-
  deriving the contract from the protobuf schema and the ADRs is
  what proves the protocol is genuinely driver-agnostic.
- **Capability declarations remain a planning surface, not a
  marketing surface.** ADR-0010 framed declared capabilities as
  what the engine reads to decide whether a job will run. Carrying
  that discipline forward means new capabilities appear in
  `driver.yaml` only as their RPCs and conformance tests ship —
  not earlier.
- **The conformance suite stays small enough to read.** Two
  drivers double the surface area. Premature parameterisation
  hides what each test actually exercises behind capability-aware
  skip logic. Two explicit fixtures with duplicated tests is the
  cheaper read.
- **The v1alpha1 `DriverError.Code` enum is frozen** (ADR-0004).
  Selenium's failure surface differs from Playwright's, but the
  same nine codes have to absorb both.
- **Container infrastructure is a separate concern.** Dockerfiles,
  Compose stacks, and devcontainer config are real engineering
  work but they do not validate the protocol thesis. Phase 2's
  exit criterion is *three drivers passing one conformance suite*,
  not *three drivers running in one container*. Phase 2.5 captures
  the deferral honestly.

## Considered Options

The decisions cluster into five orthogonal axes:

1. **Capability progression policy.**
2. **Browser launch timing for the SeleniumBase adapter.**
3. **Selenium-failure → `DriverError.Code` mapping.**
4. **Cross-language conformance pattern.**
5. **Container infrastructure: now or later.**

Each axis is decided below.

## Decision Outcome

### 1. Capability progression — narrowest start, declarations follow tests

Chosen: **a capability name appears in `driver.yaml` only after
the corresponding RPC and a conformance test for it have shipped
together.**

PR9 declares `["navigation"]` for the SeleniumBase adapter. PR10
will add `js_execution`, the four `query_*` names, and the four
`extract_*` names *if and only if* their conformance tests land
in the same PR. PR11 will add the three `screenshot_*` names
under the same rule.

This deliberately differs from how Playwright grew its capability
list. PR4 declared `["navigation", "js_execution"]` because the
master prompt's Section 2 enumerated both, even though the only
RPC PR4 implemented was `Navigate`. The `js_execution` capability
was technically honest — Playwright's runtime can evaluate JS —
but no test exercised that path until PR5 added `MODE_EVAL`. The
window where a capability was "declared but untested" was small,
but it existed.

PR9 corrects the pattern. The contract for every future driver
and every future capability addition is:

> Declared capabilities reflect *tested* capabilities, not
> *implementable* capabilities.

The grow path for SeleniumBase:

| PR    | Adds RPC(s)              | Adds capability names                                                                       |
|-------|--------------------------|---------------------------------------------------------------------------------------------|
| PR9   | `Initialize`, `Navigate` | `navigation`                                                                                |
| PR10  | `Close`, `Query`, `Extract` | `js_execution`, `query_css`, `query_xpath`, `query_text`, `query_attribute`, `extract_text`, `extract_html`, `extract_attribute`, `extract_eval` |
| PR11  | `Screenshot`             | `screenshot_viewport`, `screenshot_full_page`, `screenshot_element`                          |

The byte-for-byte conformance assertion from ADR-0008 stays
unchanged — `driver.yaml` and the runtime `Capabilities.names`
must match. The discipline this ADR adds is on the *contents* of
that list, not the assertion mechanism.

#### Coherence invariant carries forward

The `assert_capability_coherence` rule from ADR-0010 — *if
`extract_eval` is declared, `js_execution` must also be declared* —
applies to every future driver. PR9's SeleniumBase capability
list (`["navigation"]`) satisfies it trivially. The function is
implemented at PR9 anyway so the invariant is enforced from the
moment any future capability is declared, not introduced
retroactively when the first MODE_EVAL bug lands.

Rejected:

- **Declare the full `["navigation"]` plus everything the runtime
  could implement.** This is what PR4 did for Playwright. Cheap to
  write, tempting because the runtime *can* do those things, but
  it inverts the relationship between the manifest and the test
  suite — the manifest becomes aspirational rather than
  evidence-based.
- **Defer declaring `navigation` to PR10 along with everything
  else.** Symmetric and tidy, but PR9 actually implements
  `Navigate` and a conformance test against it. Declaring nothing
  while the test passes would itself be dishonest in the opposite
  direction.

### 2. Browser launch timing — lazy on first `Navigate`

Chosen: **the same lazy launch contract ADR-0009 established for
Playwright.** `Initialize` allocates a session record (id,
metadata) and returns. The first `Navigate` for a session is what
calls SeleniumBase's `Driver()` factory, opens Chrome, and stores
the WebDriver instance in the session record. Subsequent
`Navigate` calls with the same `session_id` reuse the same driver.

Consequence to document: an adapter can `Initialize` successfully
on a host that has no Chrome installed. `Navigate` is where that
surfaces, with `Code.INTERNAL` and an actionable message ("install
Chrome and run `seleniumbase install chromedriver`"). This is
the same trade-off made for Playwright in ADR-0009 — fast
handshakes over early validation — and it is reused here for
consistency rather than re-litigated.

A consequence specific to SeleniumBase: ChromeDriver discovery is
managed by the `seleniumbase install chromedriver` step. The
sb-bootstrap recipe runs that step; CI runs it explicitly. If
ChromeDriver is missing or version-mismatched, the
`SessionNotCreatedException` path in the error mapping below
surfaces the operator action.

### 3. Selenium-failure → `DriverError.Code` mapping

The Selenium failure surface differs from Playwright's. Selenium
raises `WebDriverException` and a small family of subclasses,
typically with messages embedding Chromium's `net::ERR_*` strings
when the failure originated in the browser networking stack. The
mapping below is the SeleniumBase-side analogue of ADR-0009's
table; like that table, it is the most reusable artifact of this
PR.

| Selenium / SeleniumBase surface                    | DriverError.Code           | Tested in PR9 | Notes                                                                                |
|-----------------------------------------------------|----------------------------|---------------|---------------------------------------------------------------------------------------|
| `TimeoutException` from `driver.get(...)`           | `CODE_TIMEOUT`             | yes (`/slow`) | Includes wait timeouts and explicit page-load timeouts.                              |
| `WebDriverException("net::ERR_NAME_NOT_RESOLVED")`  | `CODE_TARGET_UNREACHABLE`  | unit only     | DNS failure.                                                                          |
| `WebDriverException("net::ERR_CONNECTION_REFUSED")` | `CODE_TARGET_UNREACHABLE`  | unit only     | TCP/HTTP connection refused.                                                          |
| `WebDriverException("net::ERR_*")` (other)          | `CODE_TARGET_UNREACHABLE`  | no            | Generic network bucket — anything matching `net::ERR_` in the message.                |
| `InvalidSessionIdException`                          | `CODE_INTERNAL`            | no            | Adapter-side problem; the client should consider the session toast and re-`Initialize`. |
| `SessionNotCreatedException` (no Chrome)            | `CODE_INTERNAL`            | unit only     | Operator action required. Message includes the `seleniumbase install chromedriver` hint. |
| Invalid URL passed by client                        | `CODE_INVALID_ARGUMENT`    | no            | Validated before calling `driver.get()` to keep the diagnostic clean.                 |
| Unknown `session_id`                                | `CODE_INVALID_ARGUMENT`    | no            | Mirrors ADR-0009 decision 2 (strict `session_id`).                                    |
| `WebDriverException` (catch-all)                    | `CODE_INTERNAL`            | no            | Original message preserved in `DriverError.message`.                                  |

The mapping is partial — only Navigate-relevant rows. PR10 adds
selector-failure rows (`NoSuchElementException`,
`StaleElementReferenceException`); PR11 adds screenshot-failure
rows.

The same v1alpha1 enum gaps ADR-0009 documented apply: there is
no dedicated `UNAVAILABLE` for missing binaries, no `NETWORK`
split, no `UNKNOWN` for the unmapped tail. The mapping collapses
to `INTERNAL` with an actionable message in those cases. A
v1alpha2 enrichment is the right place to revisit, not this ADR.

#### HTTP status codes are not errors

Same contract as Playwright (ADR-0009). Selenium's
`driver.get()` against a 4xx or 5xx response is a successful
navigation. The `NavigateResponse` carries the status code and
no `DriverError`. Selenium does not surface the HTTP status as
a structured field on `driver` itself — the adapter reads it via
Chrome DevTools Protocol if available, falling back to a
`PerformanceNavigationTiming` evaluation. PR9's conformance
suite asserts the happy path against `/ok`; the rich status-code
behaviour is exercised more thoroughly in PR10 once Extract
gives the suite a way to read element data after navigations.

### 4. Cross-language conformance pattern — explicit fixtures

Chosen: **two pytest fixtures (`playwright_adapter`,
`seleniumbase_adapter`), tests duplicated rather than
parameterised.**

The conformance suite at PR8 is built around the
`playwright_adapter` fixture. PR9 adds `seleniumbase_adapter` as
a sibling fixture and adds two new test files
(`test_seleniumbase_initialize.py`,
`test_seleniumbase_navigate.py`) that target it. The existing
Playwright tests stay where they are.

The decision is to **not** parameterise tests across both adapters
at this PR. Premature parameterisation forces capability-aware
skip logic that obscures what each test does:

```python
# Reject — too clever for two drivers
@pytest.mark.parametrize("adapter", ["playwright", "seleniumbase"])
def test_extract_text(adapter, request):
    if "extract_text" not in declared(adapter):
        pytest.skip(...)
    ...
```

The cost of duplicating two tests across two files is low. The
benefit of explicit fixtures is that a developer reading
`test_seleniumbase_navigate.py` sees what SeleniumBase is
expected to do without first parsing skip predicates.

The decision is *deferred*, not *rejected*. Once a third driver
lands (PR12, curl-impersonate) the parameterisation is forced
anyway because the matrix has three rows; the refactor is
mechanical and well-evidenced at that point. Anchoring the
pattern now would build it from speculation rather than
evidence.

Rejected:

- **Parameterise every test across both adapters from PR9 onward.**
  Discussed above. Two explicit fixtures, evidence comes later.
- **One shared test file with `@pytest.mark.parametrize` and a
  capability matrix.** Same objection — capability-aware skip
  logic is the load-bearing complexity, and v1alpha1 has not
  earned that complexity yet.

### 5. Docker / Compose / containerization — Phase 2.5, not now

Chosen: **defer all container infrastructure to a separate Phase
2.5 work block, after Phase 2's three drivers pass conformance.**

The maintainer raised the question of whether SeleniumBase
should ship with a Dockerfile, a Compose stack, or a
devcontainer config in PR9. The analysis concluded that
introducing container infrastructure before Phase 2 proves the
cross-language thesis is premature. The argument:

- The architectural primitive of this project is *the protocol*,
  not *the deployment topology*. Adding containers before three
  drivers prove the protocol distracts from the load-bearing
  question.
- Drivers in this project are subprocesses speaking gRPC over
  Unix domain sockets. ADR-0008 chose UDS specifically because
  it is the lowest-friction transport for local multi-process
  development. Wrapping each subprocess in its own container
  adds a network boundary (the socket has to traverse a volume
  mount or a named pipe) and a build cost (per-driver image)
  with no protocol benefit until a control-plane component
  consumes those images.
- The control plane is Phase 3. The point at which container
  images become *useful* (not just *possible*) is when a
  Kubernetes operator deploys them. Building the images now and
  letting them rot for two phases would invert the rule that
  every artifact has a current consumer.

ADR-0014 documents the deferral. `docs/roadmap.md` adds a new
*Phase 2.5 — Container infrastructure* section sketching the
work that *will* happen after Phase 2 closes:

- Devcontainer config so contributors can build without a local
  Python/Node/Rust toolchain.
- Per-adapter Dockerfiles producing single-binary (or
  single-virtualenv) images with the runtime browser.
- A Compose stack for local multi-service development against
  the engine + a chosen adapter.
- CI variants that build and exercise the images.

PR9 implements only the **roadmap section**, not any of the
work. Resisting the scope-creep is the point: the visible
position on Docker is held without delaying Phase 2.

Rejected:

- **Devcontainer-only in PR9 (lightest container artifact).**
  The smallest defensible scope, but it still pulls a
  not-currently-needed file into PR9 and sets a precedent that
  every PR can introduce a container-adjacent artifact.
- **Per-adapter Dockerfiles in PR9 (followed by Compose later).**
  Larger surface, same objection, plus it commits to one image
  shape (Alpine? Slim? Distroless?) before the control plane
  has had any input.
- **Skip the roadmap section entirely.** Honest deferral is part
  of the deliverable. A reader who asks "is Docker on the
  roadmap" deserves a written yes, not a silent maybe.

### 6. W2.2 amendment (2026-05-08) — Chrome → Chromium runtime swap

> **In-place evolution note** per the precedent established in
> ADR-0018 §5 (R6.3 / R6.5.3 / R6.5.4 / W1.2 / W2.1 / W2.2).
> The original ADR-0014 (PR12) referred to "Chrome" as the
> runtime browser the SeleniumBase adapter drives. W2.2 swaps
> the container runtime browser from Google Chrome stable to
> Debian's Chromium package; this section records why the
> capability invariant from §1 still holds.

**What changes.** The adapter's Docker runtime stage now
installs `chromium` + `chromium-driver` from Debian
bookworm-main instead of `google-chrome-stable` from Google's
apt repo. The adapter's `_default_driver_factory` passes
`binary_location="/usr/bin/chromium"` to SeleniumBase's
`Driver()` when `SPECTRE_SELENIUMBASE_CONTAINER=1`. Dev-host
workflow (env unset) is unchanged: contributors with Chrome
installed locally continue driving their host's Chrome.

**Why this preserves the §1 capability progression invariant.**
The 12 capabilities the adapter declares (extract_attribute,
extract_eval, extract_html, extract_text, js_execution,
navigation, query_attribute, query_css, query_text, query_xpath,
screenshot_element, screenshot_full_page, screenshot_viewport)
are the **declared = tested** set per the §1 rule. The
conformance suite's red-bar discipline does not assume Chrome
vs Chromium; it asserts protocol-level behaviour that both
browsers' V8 + Blink engines produce identically:

- DOM extraction (text / attribute / HTML) — Blink rendering,
  identical across Chrome and Chromium.
- JavaScript execution and result marshalling — V8, identical.
- Screenshots — Blink's compositor produces byte-different
  outputs across browser builds even on the same Chrome
  branch (font rendering, video codec presence in the OS
  layer); the conformance suite's screenshot tests already
  use shape / size / format assertions rather than
  byte-for-byte comparison per ADR-0014's screenshot
  capability commentary. Chrome → Chromium fits the same
  shape — no test change required.
- Navigation, queries, DSL bindings — protocol-level
  semantics governed by the SeleniumBase + Selenium WebDriver
  layers, both of which operate identically against Chrome
  and Chromium.

**What does *not* port.** Chrome's branded features that
Chromium does not ship — DRM (Widevine), proprietary
video / audio codecs (H.264, AAC), Google's sync surface — are
out of scope for the SeleniumBase adapter's capability set.
None of the 12 declared capabilities exercise these surfaces.
A future capability that legitimately needs Chrome-branded
behaviour (e.g., a video-extraction capability requiring
H.264 decode) would require a second decision; if and when
that appears, the adapter can either (a) ship a separate
`spectre-seleniumbase-chrome` image variant for amd64-only
workloads or (b) bundle codecs via the `chromium-codecs-ffmpeg`
add-on package. ADR-amendable when the use case materialises.

**ChromeDriver provenance.** Debian releases `chromium` and
`chromium-driver` from a single source package, so the
chromedriver binary at `/usr/bin/chromedriver` is
version-locked to the chromium binary at `/usr/bin/chromium`.
The R6.1 `seleniumbase install chromedriver` runtime step is
no longer required and has been removed from the Dockerfile.

**Image size.** Debian's chromium package + dependencies is
~250 MB compressed, vs ~95 MB for Google Chrome stable. The
size growth is acceptable at v1alpha2 maturity (the image is
already the largest of the five at >2 GB uncompressed due to
the Python venv + dependency tree); image-size optimisation
remains a Day-2 follow-up if it becomes painful.

**Forward path.** A future amendment may revisit this if
either (a) Google ships a Linux/arm64 stable Chrome channel
making the deferred path (a) from ADR-0018 §5 R6.5.3 update
viable, or (b) Spectre adds capabilities that exercise
Chrome-branded surfaces. Until then, Debian Chromium is the
single runtime browser.

## Consequences

- Good, because the capability progression rule (declared = tested)
  is the most important pattern this PR establishes for the rest
  of the project. Every future driver and every future RPC follows
  the same discipline; the manifest becomes a load-bearing test
  artifact rather than a marketing artifact.
- Good, because the lazy-launch contract from ADR-0009 is reused
  rather than re-derived, keeping the reasoning consistent across
  drivers.
- Good, because the Selenium-error mapping table is the substance
  of cross-language correctness for `Navigate`. PR10 and PR11 add
  rows to this same table; they do not invent a parallel one.
- Good, because the explicit-fixtures pattern keeps the conformance
  suite readable as it grows. The parameterisation deferral is
  evidence-based, not stubborn.
- Good, because Phase 2.5 honours the maintainer's Docker concern
  with a written position rather than silent absence.
- Bad, because two adapters now share none of their infrastructure
  yet. A future shared-contract module (the SessionManager pattern,
  the error-mapping shape) is plausible but premature; PR12+ will
  surface real evidence about what to extract.
- Bad, because the capability progression rule is a *discipline*,
  not a *check*. Nothing in CI prevents a future PR from declaring
  capabilities ahead of tests. ADR-0014 documents the rule; code
  review enforces it. A static check (e.g. a lint that fails when
  `driver.yaml` declares a capability with no matching test marker)
  is a follow-up.
- Bad, because Selenium's HTTP-status reporting is less direct
  than Playwright's. The PR9 conformance suite asserts only the
  `/ok` happy path. PR10 will exercise the 4xx/5xx and redirect
  paths once Extract is available to read post-navigation state.
- Neutral, because PR9's two new conformance tests bring the suite
  to 25-26 tests across two drivers. The duplication is small and
  intentional. It will resolve in PR12+ when three drivers force
  the parameterisation refactor.
- Neutral, because the Python adapter's session manager and error
  mapping are direct re-implementations of the TypeScript shapes
  rather than imports of a shared contract. The Section 2
  out-of-scope note ("cross-language extraction of the common
  shape is deferred") makes this explicit.

## Confirmation

- Acceptance criteria 1–14 of the PR9 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just sb-bootstrap
  && just check && just conf-test` succeeds on Linux and macOS.
- The two new SeleniumBase conformance tests pass three times in
  a row with no flakes.
- The byte-for-byte capability assertion holds for SeleniumBase:
  `driver.yaml` and the runtime `Capabilities.names` are both
  `["navigation"]`.
- The coherence assertion accepts `["navigation"]` and rejects
  `["extract_eval"]` (no `js_execution`). Same invariant ADR-0010
  introduced; same enforcement, now in Python.
- `KNOWN_DRIVERS` in the engine grows to two entries; an unknown
  driver still rejects with `JobError::UnknownDriver`.
- The `seleniumbase-navigate` example is runnable via `spectre run
  examples/seleniumbase-navigate/job.yaml` against a host with
  Chrome and ChromeDriver installed.
- Sending SIGTERM to a running adapter that has launched Chrome
  causes the Chrome process tree to exit cleanly within 5 seconds.

## Pros and Cons of the Options

### Capability progression policy

#### Declared = tested (chosen)

- Good, because the manifest becomes evidence rather than aspiration.
- Good, because the byte-for-byte conformance assertion gains real
  meaning — a future PR cannot silently "promote" a capability by
  editing the manifest.
- Bad, because the rule is enforced by review, not by a static
  check.

#### Declare everything implementable from PR1

- Good, because the manifest is faster to write.
- Bad, because it inverts the manifest-vs-tests relationship; the
  manifest leads instead of follows.

### Cross-language conformance pattern

#### Two explicit fixtures, duplicated tests (chosen)

- Good, because each test reads as a single behaviour against a
  single driver.
- Good, because the duplication is bounded — PR9 adds two tests,
  PR10/PR11 add a handful more.
- Bad, because the duplication will eventually become real
  duplication once three drivers exist.

#### Parameterise from PR9

- Good, because there is no later refactor.
- Bad, because the parameterisation matrix has to encode capability
  presence, which is the actual content of each test.

### Container infrastructure timing

#### Phase 2.5 deferral (chosen)

- Good, because the artifact is recorded in the roadmap rather
  than forgotten.
- Good, because Phase 2's narrow scope (three drivers, one
  protocol) is preserved.
- Bad, because contributors who would prefer a devcontainer wait
  until Phase 2.5 lands.

#### Devcontainer-only in PR9

- Good, because it offers contributors a fast onboarding path.
- Bad, because it imports an artifact that will rot until the
  Compose stack lands; nothing in PR9 currently consumes it.

## More Information

- SeleniumBase project: <https://seleniumbase.io>
- SeleniumBase `Driver` factory:
  <https://seleniumbase.io/help_docs/syntax_formats/sb_sf_07/>
- SeleniumBase `install chromedriver` recipe:
  <https://seleniumbase.io/help_docs/install/>
- Selenium WebDriver Python API:
  <https://selenium-python.readthedocs.io/>
- Selenium exception hierarchy:
  <https://www.selenium.dev/selenium/docs/api/py/common/selenium.common.exceptions.html>
- grpcio Python server:
  <https://grpc.io/docs/languages/python/basics/>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate and session lifecycle](0009-navigate-and-session-lifecycle.md),
  [ADR-0010 Element lifecycle and capability gating](0010-element-lifecycle-and-capability-gating.md),
  [ADR-0013 CLI as engine binary](0013-cli-as-engine-binary.md).
