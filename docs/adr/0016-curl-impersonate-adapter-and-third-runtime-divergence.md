---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# curl-impersonate adapter and third-runtime divergence

## Context and Problem Statement

Phase 2's first two PRs (PR9 and PR10) demonstrated that the
v1alpha1 Driver Protocol generalises across two browser-based
runtimes: Playwright (TypeScript, 13 declared capabilities) and
SeleniumBase (Python, 12 declared capabilities). The divergence
between the two — `screenshot_full_page` declared by Playwright
and *omitted* by SeleniumBase — was the first concrete evidence
that capability declaration is substantive, not decorative
(ADR-0015 §5).

PR11 introduces the project's third reference adapter:
**curl-impersonate**, written in **Go**, talking to a
subprocess-invoked `curl_chrome116` binary. This runtime is
fundamentally different from the two browser drivers:

- No browser, no DOM, no JavaScript engine.
- No screenshot capability — there is nothing visual to
  capture.
- No `MODE_EVAL` capability — there is no JavaScript engine.
- Pure HTTP request/response semantics.

The architectural payoff: after PR12, the same `spectre run`
command will work against three runtimes spanning three
languages, with each runtime declaring an honestly different
capability set. PR11 closes the same `Initialize` + `Navigate`
floor PR9 closed for SeleniumBase, deferring the rest of the
unary RPC surface to PR12. Capability progression stays
honest: declared = tested.

The schema decisions are settled (ADR-0001, ADR-0003,
ADR-0004), the codegen is settled (ADR-0007), the handshake is
settled (ADR-0008), the Navigate lifecycle is settled
(ADR-0009), the capability gating model is settled (ADR-0010),
the engine binary is settled (ADR-0013), the cross-language
conformance pattern is settled (ADR-0014), and the
SeleniumBase-specific deviations are settled (ADR-0015). The
remaining decisions are about how a Go-based, HTTP-only
adapter participates in those contracts and what the third
capability divergence means for the protocol's positioning.

## Decision Drivers

- **Architectural symmetry over micro-optimisation.** Adapters
  are subprocesses speaking gRPC over UDS (ADR-0008). The
  curl-impersonate runtime is also a subprocess. Holding to
  one model at every layer beats a one-off integration mode
  that would buy no architectural clarity.
- **CI tractability and cross-platform robustness.** The
  adapter must run unattended on a Linux runner and a
  developer's macOS laptop. Static binaries from the
  curl-impersonate release page are install-and-go on both;
  cgo cross-compilation is platform-specific work the project
  has not earned.
- **Declared = tested**, the contract from ADR-0014 §1. PR11
  declares only `navigation` because PR11 only ships
  `Initialize` + `Navigate` and a conformance test for
  Navigate. PR12 will grow the list when its tests land.
- **Honest absence over dishonest presence.** The
  curl-impersonate runtime cannot honour `js_execution`,
  `extract_eval`, or any `screenshot_*` name in any future PR
  without inventing a JavaScript engine or a renderer. The
  capability list will end at six entries, not catch up to
  twelve. ADR-0015 §5 framed this for SeleniumBase against
  Playwright; PR11 takes the framing one level deeper.
- **The protocol's planning surface scales by subtraction.**
  An engine reading three manifests sees Playwright with 13
  names, SeleniumBase with 12, and (after PR12) curl-impersonate
  with 6. A job's required capabilities reject drivers
  *before* any of them launch. Three concrete divergence
  points validate the protocol design at every level the
  engine cares about.

## Considered Options

The decisions cluster into five orthogonal axes:

1. **Integration model: subprocess invocation versus cgo.**
2. **`WaitCondition` semantics for an HTTP-only adapter.**
3. **Default curl variant selection.**
4. **Session and cookie-jar architecture.**
5. **Capability divergence — the third level.**

Each axis is decided below.

## Decision Outcome

### 1. Integration model — subprocess invocation, not cgo

Chosen: **the adapter invokes `curl_chrome116` as a
subprocess per request via `os/exec.CommandContext`.** cgo
linking against `libcurl-impersonate.so` is rejected for
v1alpha1.

Rationale, in order of weight:

1. **Architectural symmetry.** The Driver Protocol's tese
   (ADR-0001) is *protocol-first*. ADR-0008 chose subprocess
   adapters over in-process plugins. Wrapping a subprocess
   inside a subprocess is the model holding consistently —
   a cgo integration would be the project's first
   architectural exception.
2. **CI simplicity.** Linux installs a static
   curl-impersonate release tarball into `/usr/local/bin`.
   macOS installs via Homebrew (when available) or a manual
   download. Both are install-and-go. cgo would require
   `libcurl-impersonate-dev`, dynamic linkage, platform-
   specific shared-object handling, and a per-OS build
   matrix.
3. **Cross-platform robustness.** Static binaries from the
   curl-impersonate releases work on any modern x86_64
   Linux. cgo cross-compilation between Linux and macOS has
   well-known platform pain that the project has not earned.
4. **Performance is not the bottleneck for v1alpha1.** Each
   `Navigate` spawns one process. Process-spawn overhead on
   Linux is ~5-15ms. For Phase 2's smoke-test scope, this is
   invisible relative to the network round-trip the curl
   actually performs. If v1alpha2 ever surfaces real
   throughput requirements, the long-running-curl
   optimisation (single subprocess pool with stdin/stdout
   pipes) is a backwards-compatible replacement.

Implementation surface:

- `internal/curlx/curlx.go` exposes a `Fetch(ctx, opts)
  (Response, error)` function. `Options` carries URL,
  Headers, CookieJar, Timeout, and Variant; `Response`
  carries StatusCode, FinalURL, Body, and ElapsedMs.
- The command line uses `-w '%{http_code} %{url_effective}\n'`
  to emit the status code and final URL after redirects to
  stdout, separated from the body by a sentinel marker. The
  adapter parses both pieces from the output.
- `os/exec.CommandContext` propagates SIGTERM cancellation
  to the curl subprocess; the gRPC server's shutdown handler
  cancels the context and the in-flight curl exits.

Rejected:

- **cgo direct against `libcurl-impersonate.so`.** Cross-
  platform fragility, async-boundary problems with the Go
  runtime under heavy use, distribution complexity. cgo
  remains a v1alpha2 candidate if a real throughput
  requirement surfaces.
- **Long-running curl-impersonate via stdin/stdout pipes.**
  A possible future optimisation; deferred until evidence
  demands it. The protocol contract does not change.
- **Pure Go HTTP client with manual fingerprint
  manipulation.** Dramatic complexity for marginal benefit;
  abandons the curl-impersonate maintenance burden the
  upstream community has already absorbed.

### 2. `WaitCondition` is an honest no-op for this adapter

Chosen: **the four `WaitCondition` enum values are accepted
without rejection and have no observable effect.**

`WaitCondition` exists in v1alpha1 because browsers have
loading semantics:

| Enum value                            | Browser meaning                              | curl-impersonate meaning |
|---------------------------------------|----------------------------------------------|--------------------------|
| `WAIT_CONDITION_UNSPECIFIED`          | implementation default                       | no-op (response is the wait) |
| `WAIT_CONDITION_LOAD`                 | wait for the `load` event                    | no-op (response = load)  |
| `WAIT_CONDITION_DOM_CONTENT_LOADED`   | wait for `DOMContentLoaded`                  | no-op (no DOM)           |
| `WAIT_CONDITION_NETWORK_IDLE`         | wait for the network to quiesce              | no-op (no follow-up traffic) |

The adapter accepts every `WaitCondition` value without
rejection because the protocol contract is "wait at least
until this condition holds". For a bare HTTP request the
condition is satisfied the moment the response is received;
all four are trivially true. Documenting this as honest
no-op contracts is the cheapest correct answer.

Rejected:

- **Reject `DOM_CONTENT_LOADED` and `NETWORK_IDLE` with
  `CODE_INVALID_ARGUMENT` because they have no
  HTTP-equivalent.** Tighter, but the v1alpha1 protocol
  specifies wait conditions as advisory rather than
  prescriptive — a strict rejection would force engines to
  encode driver-specific knowledge of which values each
  adapter accepts. The whole point of the protocol is that
  the engine does not need that knowledge.
- **Implement `NETWORK_IDLE` as a follow-up sniffer that
  watches for further connections.** Out of scope and
  technically unsound for an HTTP-only adapter; subsequent
  connections are explicit Navigate calls, not implicit
  page-loaded traffic.

A future JavaScript-rendering driver (e.g. a Splash-style
proxy, or a future CDP-direct adapter) might honour these
conditions. v1alpha1 imposes no strictness here.

### 3. Default curl variant — `curl_chrome116`, env-overridable

Chosen: **the adapter invokes `curl_chrome116` by default,
with an environment variable `SPECTRE_CURL_VARIANT` that
overrides the binary name at session-creation time.**

Rationale:

1. **Chrome 116+ is a recent, widely-deployed browser
   fingerprint.** A request that looks like Chrome works
   with the broadest range of public web servers without
   special configuration.
2. **The `curl_chrome116` build is reliably maintained
   across curl-impersonate releases.** Older or rarer
   variants (`curl_chrome99_android`, `curl_safari15_3`)
   ship intermittently; defaulting to a variant whose
   release tarball always contains it avoids "default works
   on Linux x86_64 but not on macOS arm64" footguns.
3. **The override is scoped to the session.** The
   environment variable is read when the adapter starts; a
   running adapter binds to one variant for the lifetime of
   the process. Per-Navigate variant selection is out of
   v1alpha1 scope (the protocol has no field for it) and a
   v1alpha2 capability candidate if real workloads need it.

Documented in `driver.yaml` (with a comment block listing
known-working variants without overcommitting to support
every one) and in the adapter README.

Rejected:

- **No default; force the operator to set
  `SPECTRE_CURL_VARIANT`.** Hostile to first-time runners
  whose only goal is to exercise the protocol; the cost of
  an opinionated default is essentially zero.
- **Per-Navigate variant selection via `SessionConfig`
  extensions.** Out of v1alpha1 scope; adding fields to a
  frozen wire contract is the single thing ADR-0004
  forbids.
- **Pin to `curl_chrome116` with no override at all.**
  Loses the operator escape hatch when a target server
  rejects Chrome 116's fingerprint specifically. The env
  var is the cheapest way to keep the door open without
  expanding the protocol surface.

### 4. Session and cookie-jar architecture

Chosen: **each session allocates a temporary cookie-jar
file path of the form `/tmp/spectre-curl-<session_id>.cookies`;
`Navigate` invokes curl with `--cookie-jar <path>` and
`--cookie <path>` so cookies persist across multiple
Navigates within the same session.**

The session struct holds:

- `id` — UUIDv4, returned by `Initialize`.
- `cookieJarPath` — absolute filesystem path under
  `/tmp` (≤ 60 chars total under the macOS 104-char path
  budget that ADR-0008 documented for UDS sockets).
- `created` — `time.Time` of allocation; informational.

Lifecycle:

- `Initialize` allocates the session record and the empty
  cookie-jar path. No subprocess runs.
- `Navigate` invokes the curl subprocess with the jar path
  on both `--cookie-jar` (write) and `--cookie` (read)
  flags. On the first Navigate the jar is empty; subsequent
  Navigates inherit any cookies set by the prior response.
- `Close` (PR12) will quit the session, delete the jar
  file, and forget the session record. PR11 ships only a
  process-level shutdown handler that walks the active
  session set on SIGTERM/SIGINT and unlinks each jar before
  exit.
- An additional startup sweep removes stale jar files
  matching the `spectre-curl-*.cookies` glob — the safety
  net for sessions whose adapter crashed mid-Navigate.

PR11 deliberately ships the cookie-jar machinery even
though the only RPC reading from a jar today is Navigate
itself. PR12's `Query`/`Extract` RPCs need the prior
response (HTML body, response status, final URL) and may
need a re-fetch with cookies set on a prior Navigate.
Building the architecture for that now keeps PR12 a pure
RPC-implementation PR rather than an
RPC-plus-infrastructure PR.

Concurrency posture: the session map is a plain
`map[string]*Session` guarded by a `sync.Mutex`. Concurrent
`Navigate` against the *same* session is undefined
behaviour and not protected — operators should not pipeline
Navigates against a single session. v1alpha1's wire
contract is unary; clients that violate the implicit
serial-per-session expectation get whatever behaviour curl
exhibits when two processes write to the same cookie-jar
file. Documented in the adapter README, not solved.

Rejected:

- **In-memory cookie cache instead of a jar file.** Loses
  the curl-impersonate native cookie handling for the cost
  of a small architectural concession. The jar file is
  exactly what curl wants; pretending otherwise would
  re-implement RFC 6265 in Go.
- **Per-Navigate ephemeral jar file (no persistence
  across Navigates within a session).** Breaks the
  "session" abstraction the protocol promises; a multi-step
  job that depends on a login cookie set by step 1 would
  fail at step 2.
- **One cookie-jar per adapter process.** Conflates
  unrelated sessions. Would surface as cross-job state
  leakage if the engine ever multiplexes sessions over a
  single adapter (which ADR-0008's lifecycle does not
  forbid).

### 5. Capability divergence — the third level

This is the most architecturally significant decision in
PR11 because it is **the first time three drivers declare
materially different capability sets** against the same
protocol surface. ADR-0015 §5 articulated the framing for
SeleniumBase against Playwright (`screenshot_full_page`
omitted); PR11 extends it.

PR11 declares **`["navigation"]`** for the curl-impersonate
adapter — identical to SeleniumBase's PR9 floor and growing
according to the same contract as PR9→PR10:

| PR    | Adds RPC(s)                  | Adds capability names                                                                  |
|-------|------------------------------|----------------------------------------------------------------------------------------|
| PR11  | `Initialize`, `Navigate`     | `navigation`                                                                           |
| PR12  | `Close`, `Query`, `Extract`  | `query_css`, `query_xpath`, `extract_text`, `extract_html`, `extract_attribute`        |
| PR13+ | (none — see below)           | (none — this driver has no Screenshot, no MODE_EVAL)                                   |

After PR12, the curl-impersonate adapter's capability list
ends at **6 names**: `extract_attribute`, `extract_html`,
`extract_text`, `navigation`, `query_css`, `query_xpath`.
Capabilities deliberately absent forever (in v1alpha1):

- `js_execution` — no JavaScript engine.
- `extract_eval` — depends on `js_execution`; the
  coherence invariant from ADR-0010 §3 forbids declaring
  one without the other.
- `screenshot_viewport`, `screenshot_full_page`,
  `screenshot_element` — no rendering pipeline.
- `query_text`, `query_attribute` — defer the decision to
  PR12. CSS/XPath cover the same ground; whether to add
  text/attribute query kinds against an HTML-parsed DOM is
  an evidence-based call once the parser library is
  picked. PR12 may grow the list to 6 or 7.

The cumulative picture across the three Phase 2 drivers
after PR12:

| Capability                | Playwright (13) | SeleniumBase (12) | curl-impersonate (≤7) |
|---------------------------|-----------------|-------------------|-----------------------|
| `navigation`              | yes             | yes               | yes                   |
| `js_execution`            | yes             | yes               | no                    |
| `query_css`               | yes             | yes               | yes                   |
| `query_xpath`             | yes             | yes               | yes                   |
| `query_text`              | yes             | yes               | TBD (PR12)            |
| `query_attribute`         | yes             | yes               | TBD (PR12)            |
| `extract_text`            | yes             | yes               | yes                   |
| `extract_html`            | yes             | yes               | yes                   |
| `extract_attribute`       | yes             | yes               | yes                   |
| `extract_eval`            | yes             | yes               | no                    |
| `screenshot_viewport`     | yes             | yes               | no                    |
| `screenshot_element`      | yes             | yes               | no                    |
| `screenshot_full_page`    | yes             | no                | no                    |

curl-impersonate's set is **a strict subset of
SeleniumBase's**, which is itself a strict subset of
Playwright's. Three concrete divergence points; each one
validates a different facet of the protocol design:

1. Playwright vs SeleniumBase on `screenshot_full_page`
   (ADR-0015 §5) — a single capability differs because the
   underlying runtime cannot deliver it reliably.
2. SeleniumBase vs curl-impersonate on `js_execution` and
   the `screenshot_*` family — five capabilities differ
   because the underlying runtime model does not include
   the prerequisite primitive (a browser).
3. Playwright vs curl-impersonate — the cumulative gap
   (six or seven capabilities) is what an engine planning
   against three manifests must reason about.

The conformance suite stays explicit (per ADR-0014 §4 — no
parameterisation) but now has three fixtures rather than
two. The byte-for-byte assertion from ADR-0008 holds for
all three drivers.

#### Coherence invariant carries forward (and is structurally a no-op here)

The `assert_capability_coherence` rule from ADR-0010 §3 —
*if `extract_eval` is declared, `js_execution` must also
be declared* — applies to every driver. The
curl-impersonate adapter will never declare either, so the
check is structurally a no-op for this driver forever.
Implementing it in `internal/capabilities/capabilities.go`
anyway preserves symmetry with the Python and TypeScript
adapters; a future contributor reading
`AssertCoherence([]string{"extract_eval"})` against this
driver gets the same fail-fast diagnostic the other two
drivers give.

#### Engine `KNOWN_DRIVERS` grows alphabetically

`core/engine/src/dsl.rs::KNOWN_DRIVERS` becomes
`["curl-impersonate", "playwright", "seleniumbase"]` —
alphabetical. The DSL parser accepts `driver:
curl-impersonate` and the planner runs the same
`validate_capabilities` path that already gates Playwright
and SeleniumBase plans. A v1alpha1 DSL job that needs
`js_execution` against `driver: curl-impersonate` fails at
`spectre validate` time.

Rejected:

- **Declare `query_text`, `query_attribute`,
  `extract_text`, `extract_html`, `extract_attribute`
  speculatively in PR11 because they are
  obviously-implementable.** Inverts the contract
  ADR-0014 §1 set: declared = tested. PR11 has no Query
  or Extract test; therefore no Query or Extract
  capability.
- **Declare `js_execution` as a degenerate `false`
  capability and have the engine learn to reason about
  capability *values*.** Out of scope and a major
  protocol expansion. v1alpha1 lists are flat, names-only.
- **Skip the coherence check in this adapter because the
  invariant cannot be triggered.** Asymmetry across the
  three drivers' capability modules costs more than the
  ten lines of code the check costs. Reading the same
  shape three times is the cheapest way to teach the
  pattern.

## Consequences

- Good, because the protocol's third capability divergence
  is the strongest external positioning artefact the
  project will produce in Phase 2. An engineer reading the
  three manifests sees that the protocol's planning
  surface scales by *subtraction* — adding a driver makes
  the cross-driver intersection smaller, not larger — and
  the protocol absorbs it without changes.
- Good, because subprocess invocation preserves the one-
  model-at-every-layer architecture from ADR-0008. The
  precedent for future drivers (a CDP-direct adapter, a
  cloud-render adapter, a captcha-bypass shim) is set:
  subprocess + protocol, cgo only with a written
  performance argument.
- Good, because the cookie-jar architecture lets PR12
  ship Query and Extract as RPC-only work — the session
  model already carries enough state.
- Good, because the engine's `KNOWN_DRIVERS` and
  `validate_capabilities` paths exercised twice already
  (against Playwright and SeleniumBase) absorb the third
  driver mechanically. No engine-side special casing.
- Bad, because subprocess invocation costs ~5-15ms per
  Navigate on Linux. For latency-sensitive workloads
  (which v1alpha1 explicitly is not), this is real
  overhead. Mitigation is documented as a v1alpha2
  candidate (long-running curl with stdin/stdout pipes,
  or cgo) but not implemented.
- Bad, because the `WaitCondition` no-op contract is a
  *driver* discipline, not a *protocol* enforcement. A
  future adapter author may misread the spec and reject
  conditions the protocol intends as advisory. ADR-0016
  documents the contract; code review enforces it.
- Bad, because the curl-impersonate version is a binary
  on PATH rather than a vendored library. CI pins to a
  specific release tarball; local development depends on
  the operator's install. Variant-mismatch bugs across
  versions are possible. The version pin in the
  `ci-bootstrap` recipe is the mitigation.
- Neutral, because the Go session manager and error
  mapping are direct re-implementations of the
  TypeScript/Python shapes. ADR-0014 §4 deferred the
  shared-contract extraction to "after a third driver
  lands"; PR11 *is* that third driver and the surface
  area is now visible. ADR-0014's deferral resolves to
  "no extraction yet" — PR12 may surface evidence to
  reopen, but PR11 holds the line.
- Neutral, because the cookie-jar file path collision
  risk (two adapter processes targeting the same
  socket/jar prefix) is documented and not solved. The
  startup sweep removes stale jars; concurrent adapters
  on the same host with the same SESSION_ID space would
  step on each other, which the engine's per-process
  driver lifecycle does not produce in practice.

## Confirmation

- Acceptance criteria 1–14 of the PR11 master prompt are
  the verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just
  ci-bootstrap && just check` succeeds on Linux. macOS
  may require a manual curl-impersonate install
  (documented in the adapter README).
- The two new conformance tests
  (`test_curl_impersonate_initialize`,
  `test_curl_impersonate_navigate`) pass three times in
  a row with no flakes when `curl_chrome116` is on PATH.
- The byte-for-byte capability assertion holds for the
  curl-impersonate adapter against the single declared
  name `["navigation"]`.
- The coherence assertion accepts `["navigation"]` and
  rejects `["extract_eval"]` (no `js_execution`). Same
  invariant ADR-0010 introduced; same enforcement, now
  in Go.
- `KNOWN_DRIVERS` in the engine grows to three entries;
  an unknown driver still rejects with
  `JobError::UnknownDriver`.
- A unit test confirms the `SPECTRE_CURL_VARIANT` env
  override changes the binary name passed to the curl
  subprocess.
- The `examples/curl-impersonate-fetch` example runs
  end-to-end against the local HTTP fixture (and the
  README documents a public-internet variant).
- Sending SIGTERM to a running adapter unlinks every
  active session's cookie-jar file before exit.

## More Information

- curl-impersonate releases:
  <https://github.com/lwthiker/curl-impersonate/releases>
- curl-impersonate README:
  <https://github.com/lwthiker/curl-impersonate>
- grpc-go documentation:
  <https://grpc.io/docs/languages/go/>
- Go `os/exec` documentation:
  <https://pkg.go.dev/os/exec>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0003 Schema-transport separation](0003-schema-transport-separation.md),
  [ADR-0004 Protocol versioning strategy](0004-protocol-versioning-strategy.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate and session lifecycle](0009-navigate-and-session-lifecycle.md),
  [ADR-0010 Element lifecycle and capability gating](0010-element-lifecycle-and-capability-gating.md),
  [ADR-0014 SeleniumBase adapter and cross-language conformance](0014-seleniumbase-adapter-and-cross-language-conformance.md),
  [ADR-0015 SeleniumBase element lifecycle and screenshot coverage](0015-seleniumbase-element-lifecycle-and-screenshot-coverage.md).
