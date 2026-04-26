---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# Engine DSL surface, planner architecture, and execution pipeline

## Context and Problem Statement

PR2 (ADR-0007) wired the protobuf schema into every consumer.
PR3-PR6 (ADR-0008 through ADR-0011) closed the Playwright reference
adapter at the v1alpha1 unary surface: the adapter speaks every RPC,
the conformance suite covers them with deterministic tests, and the
declared capability list matches runtime byte-for-byte across
thirteen names.

The engine crate at `core/engine/` has been a placeholder since
PR2. It imports the generated bindings, exposes
`PROTOCOL_VERSION`, and does nothing else. PR7 transforms it into
a working orchestrator that takes a YAML job, plans an execution,
launches the Playwright adapter as a subprocess, dials it over a
Unix domain socket, runs the
`Initialize → Navigate → Query → Extract → Close` sequence against
a real page, and emits JSON Lines.

The schema decisions are settled. The handshake (ADR-0008),
session lifecycle (ADR-0009), element lifecycle and capability
gating (ADR-0010), and the read-only `Screenshot` contract
(ADR-0011) are settled. The remaining decisions are about the
shape of the engine that consumes them: what the DSL looks like,
how a job is validated and translated into a sequence of RPCs,
how a driver subprocess is launched and dialled, and how results
are streamed to disk.

## Decision Drivers

- **Driver-agnostic execution.** The engine cannot know about
  Playwright. The decisions in this ADR must remain as true for a
  future SeleniumBase adapter or an HTTP-only adapter as they are
  for the Playwright reference today.
- **The engine is the first user-visible artifact.** Phase 1's exit
  criterion is "a contributor runs the example and sees JSONL
  output." Every decision here is judged against whether it makes
  that user experience cleaner, not whether it is technically
  elegant in isolation.
- **Capability declarations are a planning surface, not just
  metadata.** ADR-0010 established that the granular capability
  names exist so an engine can plan against a driver's declared
  list. PR7 makes the engine actually use that surface.
- **The subprocess lifecycle established in PR3 is the model.**
  The Python conformance harness at
  `tools/conformance/src/spectre_conformance/harness.py` is the
  reference shape; the Rust launcher mirrors it.
- **v1alpha1 protocol is frozen** (ADR-0004). The DSL must compile
  to RPCs that already exist; nothing here forces a schema change.
- **Recoverability under interruption.** PR7 is large. Each
  substantive piece is committed independently so a content-policy
  block, a tonic version mismatch, or a developer interruption
  costs at most one step's worth of work.

## Considered Options

The decisions cluster into six axes (Section 4 of the PR7 master
prompt):

1. **DSL philosophy — high-level over protocol.**
2. **Validation approach — manual over derive-based.**
3. **Planner architecture — explicit Plan struct.**
4. **Driver launcher contract.**
5. **JSONL output streaming.**
6. **Field-spec sugar mapping.**

Each is decided below with its own option set.

## Decision Outcome

### 1. DSL philosophy — high-level over protocol

Chosen: **the DSL is deliberately higher-level than the protocol**.
A single `extract` step in YAML compiles into multiple protocol
RPCs (`Query` + per-element `Extract`). The user describes *what*
to extract; the planner decides *how* to walk the protocol.

The hello-hackernews job's two YAML steps —

```yaml
steps:
  - navigate: https://news.ycombinator.com
  - extract:
      selector: .titleline > a
      fields:
        title: textContent
        url: href
```

compile to the following protocol-level steps:

```
Initialize { config: SessionConfig::default() }
Navigate   { url: "https://news.ycombinator.com",
             wait_until: WAIT_CONDITION_LOAD }
Query      { selector: ".titleline > a",
             kind: SELECTOR_KIND_CSS,
             limit: 0 }
ExtractEach {
  fields: [
    Field { name: "title", mode: MODE_TEXT_CONTENT, arg: "" },
    Field { name: "url",   mode: MODE_ATTR,         arg: "href" },
  ]
}
Close
```

The trade-off: the user cannot read the protocol-level
implementation from the YAML. The compensating affordance is
`cargo run --example hello-hackernews -- --verbose`, which prints
the compiled `Plan` to stderr before execution. A user
investigating extraction behaviour can see exactly what the engine
will run without reading the engine source.

This decision is the most consequential architectural choice in
PR7. v1alpha2 DSL proposals will inevitably suggest bringing the
DSL closer to the protocol (one DSL step per RPC), and that
suggestion has to be argued against on its merits each time. The
load-bearing reason to resist it: "user-facing intent" and
"protocol-level mechanics" are different concerns, and conflating
them leaks the protocol's internal lifecycle (ElementRefs,
generation invalidation, capability gating) into the user's job
file. Users should not have to know about ElementRef lifetimes to
write a working extraction.

A future capability (e.g. `loop`, `paginate`, `join`) lives at the
DSL layer, not the protocol layer. The protocol stays a primitive;
the DSL is where ergonomics accumulate.

Rejected:

- **1:1 DSL-to-protocol mapping.** The user writes `query` and
  `extract` separately, managing ElementRefs explicitly across
  steps. Good for transparency, fatal for ergonomics. Most
  realistic jobs are "select N, extract M fields from each";
  forcing the user to express that as three protocol-level steps
  is friction without offsetting benefit.
- **A pure imperative DSL (Lua-like, with control flow).** Pushes
  loops, conditionals, and JS-like evaluation into v1alpha1.
  Massive scope expansion; unrelated to the Phase 1 exit criterion.

### 2. Validation approach — manual over derive-based

Chosen: **hand-rolled validation**. YAML is parsed by `serde_yaml_ng`
into intermediate `RawJob` / `RawStep` structures whose only job is
deserialisation. A second pass converts those into the validated
`Job` / `Step` types and emits structured `JobError` values for
every failure mode.

Rejected:

- **`validator` crate (or `garde`) with derive macros.** Couples
  error format to the library's display impl. The error surface
  PR7 needs is small (six or seven cases), and each case has a
  hand-tuned message that includes a field path the user can
  navigate to. Derive-based validation produces messages like
  "validation failed at field 'selector'", which forces a future
  reader of an error to map back to the YAML structure
  themselves.

The validation surface is intentionally narrow:

| Case                                | `JobError` variant       | Path example                    |
|-------------------------------------|--------------------------|---------------------------------|
| Unknown `spectre` version           | `UnknownProtocol`        | (top-level)                     |
| Unknown `driver` name               | `UnknownDriver`          | `driver`                        |
| Empty `steps` list                  | `Invalid`                | `steps`                         |
| Malformed step (missing keys)       | `Invalid`                | `steps[N]`                      |
| Empty `selector`                    | `Invalid`                | `steps[N].extract.selector`     |
| Empty `fields` map                  | `Invalid`                | `steps[N].extract.fields`       |
| Unknown field-spec sugar            | `UnknownFieldSpec`       | `steps[N].extract.fields.<key>` |
| Malformed URL in `navigate`         | `InvalidUrl`             | `steps[N].navigate`             |
| Unknown output `format`             | `Invalid`                | `output.format`                 |
| YAML syntax error                   | `Yaml`                   | (line/col from `serde_yaml_ng`) |

`JobError`'s `Display` impl renders `<path>: <message>` so a
terminal user gets one self-contained line per error. The
`Yaml` variant carries the parser's line/column where available.

Adding a new validation rule (e.g. URL scheme must be `http` or
`https`) is a one-line match arm — no attribute dance, no
container-derive ceremony.

### 3. Planner architecture — explicit `Plan` struct

Chosen: **`plan(job: &Job, capabilities: &[String]) -> Result<Plan>`,
returning a data structure**.

The `Plan` is data, not behaviour. It is a `Vec<PlanStep>`, an
`OutputSink` configuration, the chosen driver name, and the
`HashSet<String>` of required capabilities.

```rust
pub enum PlanStep {
    Initialize { config: SessionConfig },
    Navigate   { url: String, wait_until: WaitCondition },
    Query      { selector: String, kind: SelectorKind, limit: u32 },
    ExtractEach { fields: Vec<Field> },
    Close,
}
```

`ExtractEach` is the planner-level abstraction that captures the
"for each ElementRef from the prior Query, run Extract with these
fields" pattern. It is not a protocol RPC; the executor unrolls it
into one Extract per ElementRef at runtime. Naming it explicitly
keeps the data structure pattern-matchable and lets a future
parallel executor dispatch the per-element calls concurrently
without re-parsing the DSL.

This separation lets the planner be tested without a runtime, and
lets `--verbose` print a human-readable plan via `Debug` derives.
The executor consumes the `Plan` and produces JSONL.

#### Capability resolution

The planner computes `required_capabilities` as follows:

- Always: `"navigation"`.
- For each `Field` whose `mode` is `MODE_EVAL`: add
  `"js_execution"`.

That is the entire mapping in PR7. The granular `extract_*` and
`query_*` names declared in the Playwright manifest (ADR-0010 §3)
are intentionally **not** required by the engine planner in
v1alpha1 — they are descriptive declarations that gate at runtime
nowhere except for `MODE_EVAL` (which uses `js_execution`).

This is a deliberate scoping choice. A v1alpha2 driver that
implements only a subset of the granular declarations (e.g. an
HTTP-only adapter that supports `query_css` but not `query_xpath`,
or a static-HTML driver that supports `extract_text` but not
`extract_html`) will require the planner to expand its mapping —
adding `query_xpath` to required capabilities when the DSL uses
XPath, etc. The PR7 scope is the Playwright adapter, which
declares all of them; expanding the planner's mapping today would
be code without exercise.

The PR7 master prompt's Section 5 Step 5 example
("e.g., `MODE_TEXT_CONTENT` requires `extract_text`") was an
illustration of the mapping shape, not a binding contract; the
acceptance test in Section 6 item 3 asserts the actual scope
(`{"navigation"}` for hello-hackernews). This ADR ratifies the
test as authoritative and treats the example as illustrative.

#### Capability validation

`validate_capabilities(plan: &Plan, declared: &[String]) -> Result<()>`
returns `EngineError::CapabilityMissing { missing }` when the
plan's required set is not a subset of the declared set. This
runs after `Initialize` (so the engine has the driver's actual
declared list) and before any other RPC. A capability mismatch
fails the job with a clear diagnostic naming the missing
capabilities.

Rejected:

- **Closure-based planner** (`fn plan(job) -> impl Fn(Client)
  -> Future`). Hard to test, hard to debug, hard to print. Hides
  the structure that makes the planner valuable as a separation
  of concerns.
- **AST-walking interpreter** (parse and execute in one pass).
  Mixes parsing concerns with execution. A future "validate
  without running" path would have nowhere to plug in.
- **Per-mode capability gating in v1alpha1.** Doable, but premature
  — every PR7 mode is supported by the only existing driver, so
  the planner's gate would never fire. v1alpha2 picks this up when
  a second driver lands with a partial capability set.

### 4. Driver launcher contract

Chosen: **subprocess launcher in `core/engine/src/launcher.rs`,
mirroring the Python harness from PR3** (ADR-0008).

The contract:

- **Manifest source.** `<adapters_path>/<driver>/driver.yaml`,
  parsed with `serde_yaml_ng`. The first transport whose `kind`
  is `grpc-uds` provides the launch command.
- **Adapter path resolution.** Defaults to a workspace-relative
  `../../adapters/` from the engine crate. Override via the
  `SPECTRE_ADAPTERS_PATH` environment variable. PR7 does not
  introduce a registry; that is a v1alpha2 concern when the third
  driver is on the horizon.
- **UDS path generation.** `format!("/tmp/spectre-engine-{}.sock",
  Uuid::new_v4())`. Anchoring under `/tmp` keeps the path under
  macOS' 104-character AF\_UNIX limit. Same constraint as ADR-0008.
- **Spawn.** `tokio::process::Command` with stdout piped, stderr
  piped, `SPECTRE_DRIVER_SOCKET` set to the chosen path,
  `--socket=<path>` appended to argv. The cwd is the driver's
  directory so relative `dist/index.js` resolves.
- **Readiness.** A line-pumping task reads the child's stdout
  line-by-line. The first line matching `^ready unix:(\S+)$` (the
  exact contract from ADR-0008) signals readiness. A 10-second
  overall timeout escalates to failure. Failure surfaces a
  `LauncherError` whose message includes the tail of captured
  stderr, mirroring the Python harness's diagnostic.
- **Shutdown.** `DriverHandle::shutdown()` sends SIGTERM via
  `nix::sys::signal::kill`, waits up to 10 seconds, escalates to
  SIGKILL on timeout, and unlinks the socket file. The 10-second
  graceful window is twice the conformance harness's 5-second
  default; a real-world driver may be holding a `BrowserContext`
  or two and benefit from the slightly looser deadline.
- **Drop safety.** `DriverHandle::Drop` invokes `shutdown()`
  through a synchronous Tokio handle path, so a panic between
  `Initialize` and `Close` does not orphan a Chromium subprocess
  on the developer's machine. The trade-off is documented in
  comments at the call site: a panic during `Drop` (e.g. a poisoned
  Tokio runtime) can still leak; PR7 accepts that as a known limit
  consistent with stdlib `Drop` semantics.

The launcher is independent of `tonic`. The returned
`DriverHandle` exposes the chosen socket path and a `dial()` helper
that builds a tonic `Channel`; the channel construction lives in
`client.rs` and is a separate concern.

Rejected:

- **`tempfile::NamedTempFile`-based UDS path.** On macOS this
  resolves under `/var/folders/...`, which routinely exceeds the
  104-character AF\_UNIX limit. The conformance harness made the
  same call in ADR-0008; the engine does too.
- **Shell out for SIGTERM.** Portable across the Tokio API
  surface, but `nix::sys::signal::kill` is one dependency the
  engine already needs for clean process control on Unix and
  costs nothing in clarity.
- **Watching the socket file instead of the readiness line.** A
  driver that writes the readiness line but fails to actually
  bind (regression class) would slip through. The line-oriented
  signal plus the underlying socket file presence (the Python
  harness uses both; the Rust launcher uses the line as primary
  and the socket connection as the implicit fallback when tonic
  dials it) gives both signals.

### 5. JSONL output streaming

Chosen: **streaming output, one row at a time, flushed per row**.

The `OutputSink` trait exposes `write_row(&mut self, row:
serde_json::Value) -> Result<()>`. `JsonlFileSink` wraps a
`BufWriter<File>` and calls `flush()` after every row. `StdoutSink`
wraps `io::stdout().lock()` and flushes per row.

Consequences:

- A long-running job that processes ten thousand elements writes
  ten thousand visible lines during execution, not at the end.
- A panic between rows preserves all prior rows on disk.
- A user piping `cargo run --example hello-hackernews` into a
  downstream process (jq, less, a database loader) sees rows as
  they are produced.

The cost is one `flush()` syscall per row. For a Phase 1 job
extracting 30 stories from HN that is invisible; for a Phase 3
control-plane job extracting millions of rows it would be a real
overhead, and the API will need a "bulk" variant. PR7 is not
Phase 3.

Rejected:

- **Buffer in memory, flush at end.** Loses partial results on
  panic; user sees nothing during a long job.
- **Wrap in a JSON array.** Not streamable: requires writing the
  closing bracket at the end, which means partial outputs are
  syntactically invalid.
- **Newline-delimited JSON without per-row flush.** The
  `BufWriter` defaults would buffer up to 8KB before any output
  is visible, which is enough to swallow the entire HN front page
  without flushing.

The output `path` from the YAML is resolved relative to the
**job file's directory**, not the engine's CWD. This matches
user intuition: "I wrote `./stories.jsonl` in my YAML, the file
is next to my YAML, regardless of where I ran the engine from."
A path of `-` writes to stdout regardless of resolution.

### 6. Field-spec sugar mapping

Chosen mapping table, frozen at v1alpha1:

| YAML field-spec | proto `Field.Mode` | proto `Field.arg` |
|-----------------|--------------------|-------------------|
| `textContent`   | `MODE_TEXT_CONTENT`| (empty)           |
| `innerText`     | `MODE_INNER_TEXT`  | (empty)           |
| `innerHTML`     | `MODE_INNER_HTML`  | (empty)           |
| `outerHTML`     | `MODE_OUTER_HTML`  | (empty)           |
| `href`          | `MODE_ATTR`        | `href`            |
| `src`           | `MODE_ATTR`        | `src`             |
| `attr:<name>`   | `MODE_ATTR`        | `<name>`          |
| anything else   | rejected with `JobError::UnknownFieldSpec`            |

The `href` and `src` shortcuts exist because they are the two
attributes that account for the overwhelming majority of
extraction tasks (links and images). Extending the shortcut list
is a deliberate v1alpha2 concern; doing it ad-hoc in PR7 would
seed a friction point for every future maintainer who wants to
add a shortcut.

`MODE_EVAL` is **not** exposed in the v1alpha1 DSL. Adding it is a
v1alpha2 concern because it requires:

- A new field-spec syntax (`eval:<expression>` or similar).
- The planner gating the plan on the driver's `js_execution`
  capability — already implemented (Section 3 above), but not
  reachable from the DSL today.
- A documented stance on the security implications of arbitrary
  JS execution against scraped pages — the Playwright adapter's
  README touches this; the DSL surface needs the same care before
  exposing it.

Rejected:

- **Always require `attr:<name>` syntax.** Removes the `href` and
  `src` shortcuts. Cleaner one-rule grammar; ignores the actual
  distribution of extraction tasks.
- **Pass-through arbitrary mode names** (e.g. accept
  `MODE_INNER_TEXT` directly). Leaks proto-enum identifiers into
  user-facing YAML. The DSL is meant to be readable; protobuf
  enum names are not.

## Confirmation

- Acceptance criteria 1-13 of the PR7 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap`,
  `just pw-install-browsers`, `just check`, `just engine-run-hello`,
  and `just conf-test` succeeds on Linux and macOS.
- The unit tests cover the DSL parser, the planner, and the output
  sink. The integration test covers the full pipeline against a
  Rust HTTP fixture and the real Playwright adapter.
- The example binary `cargo run --example hello-hackernews`
  produces a JSONL file with at least ten rows, each a JSON object
  with `title` and `url` keys, against the live Hacker News front
  page (best-effort; the local fixture is the deterministic
  contract).
- Phase 1's first two roadmap checkboxes
  (`Engine parses DSL`, `Engine speaks gRPC`) tick. The third
  (`spectre run produces JSONL`) is documented as substantively
  delivered by `cargo run --example hello-hackernews`, with the
  Go CLI binary deferred to PR8.

## Consequences

- Good, because the engine is now usable end-to-end against the
  Playwright adapter. Every architectural choice from PR1-PR6
  composes cleanly behind the `Engine::run_job()` API.
- Good, because the DSL philosophy is documented before v1alpha2
  proposals start. Future proposals to bring the DSL closer to the
  protocol now have a written counter-argument they must engage
  with.
- Good, because the planner is data, not behaviour. A future
  parallel executor, a job-validation-only path, and a
  job-explain CLI mode all reuse the same `Plan` representation.
- Good, because JSONL streaming makes mid-job state visible. A
  developer running a job against an unfamiliar site sees rows
  appear in real time and can diagnose problems faster than with
  a terminal-flush at end.
- Good, because the launcher mirrors ADR-0008's Python harness.
  The two languages now share a contract for how a driver
  subprocess is spawned, signalled ready, and shut down. A future
  Go control plane will land a third launcher against the same
  contract.
- Bad, because the integration test reimplements a small HTTP
  fixture in Rust rather than reusing the Python `LocalHttpServer`
  in `tools/conformance/src/spectre_conformance/http_fixture.py`.
  The Python harness cannot be invoked from a Rust dev-dependency.
  Generalising the fixture across two languages is v1alpha2 work;
  PR7 documents the duplication honestly and accepts the cost.
- Bad, because the engine launches the Playwright adapter via a
  hardcoded driver list (`["playwright"]`). PR9+ adds the
  SeleniumBase adapter and will need at minimum a two-name list,
  with a registry pattern likely arriving when the third driver
  is in flight. PR7 is not the place for a plugin mechanism.
- Bad, because the integration test is `#[ignore]` by default. The
  Rust unit-test job stays fast (no Chromium install, no Node
  build), but the integration test only fires when explicitly
  invoked. The CI job that runs it shares browser caching with
  the Python conformance job to amortise the cost.
- Neutral, because `serde_yaml` was deprecated in late 2024 (the
  upstream maintainer stepped away). PR7 uses `serde_yaml_ng`, the
  community fork, as a drop-in replacement. If the fork situation
  changes, the swap is two lines.
- Neutral, because the engine's `EngineError` enum is large
  (parse, plan, transport, capability, driver, output, launcher).
  Future error consolidation is possible but only after a real
  user surfaces a diagnostic the current taxonomy does not
  support.

## Pros and Cons of the Options

### DSL philosophy

#### High-level DSL (chosen)

- Good, because users describe intent, not protocol mechanics.
- Good, because future ergonomics (loops, joins, pagination) live
  at the DSL layer without requiring protocol changes.
- Bad, because users cannot read the protocol-level execution from
  the YAML. Mitigated by `--verbose` plan printing.

#### 1:1 DSL-to-protocol mapping

- Good, because every YAML step maps to one RPC; transparency is
  maximal.
- Bad, because users must manage ElementRefs and protocol
  lifecycle in their job files. Friction without offsetting
  benefit.

### Validation approach

#### Manual validation (chosen)

- Good, because every error message is tuned to what a YAML user
  needs to fix the problem.
- Good, because adding a new validation is one match arm.
- Neutral, because the manual surface is small (six or seven
  cases) and unlikely to grow before v1alpha2.

#### Derive-based validation

- Good, because boilerplate is minimised.
- Bad, because error messages are coupled to the chosen library
  and rarely include enough context to fix the YAML directly.

### Planner architecture

#### Explicit `Plan` struct (chosen)

- Good, because the planner is data and the executor is behaviour
  — clean separation.
- Good, because tests and `--verbose` reuse the same
  representation.
- Bad, because there is one extra type to maintain. Trivial cost.

#### Closure-based planner

- Good, because the planner returns a runnable thing directly.
- Bad, because closures are opaque to inspection — no `Debug`,
  no test that "the right RPCs would be issued in order."

### Driver launcher

#### Subprocess launcher mirroring the Python harness (chosen)

- Good, because the contract is already validated by PR3-PR6.
- Good, because two languages now implement the same lifecycle —
  a regression in either side surfaces during the integration
  test.
- Bad, because the SIGTERM path requires `nix` on Unix; Windows
  needs a different code path (out of scope for v1alpha1 per
  ADR-0008).

#### Watch-the-socket-only

- Good, because it removes the line-pumping task.
- Bad, because misconfigured drivers surface as "connection
  refused" without the captured stderr context.

### JSONL streaming

#### Stream per row (chosen)

- Good, because mid-job state is visible.
- Good, because partial results survive panics.
- Neutral, because per-row flush is a real syscall cost; below
  the noise floor for Phase 1 workloads.

#### Buffered, flush at end

- Good, because syscall count drops to near-zero.
- Bad, because nothing appears until the job completes; a panic
  loses all results.

#### Single JSON array

- Good, because the result is a single readable file.
- Bad, because the file is syntactically invalid until the
  closing bracket is written. Not streamable.

### Field-spec sugar

#### Curated table with `attr:` escape hatch (chosen)

- Good, because the common cases are short and the long tail
  has a single uniform syntax.
- Good, because adding a new shortcut requires a deliberate ADR
  amendment, not a stealth grammar drift.
- Bad, because users coming from Playwright direct will type
  `getAttribute('data-id')` and have to remember
  `attr:data-id`. Documented in the engine README.

#### Always-explicit `attr:`

- Good, because the grammar is one rule.
- Bad, because `href` and `src` are written so often that the
  noise of `attr:href` and `attr:src` accumulates.

## More Information

- Tonic UDS example:
  <https://github.com/hyperium/tonic/tree/master/examples/src/uds>
- Tokio process management:
  <https://docs.rs/tokio/latest/tokio/process/index.html>
- `serde_yaml_ng` (community fork after upstream archival):
  <https://github.com/acatton/serde-yaml-ng>
- Connect-Node interop with gRPC clients:
  <https://connectrpc.com/docs/node/server-plugins>
- JSONL spec (informal):
  <https://jsonlines.org/>
- Related ADRs:
  [ADR-0001 Driver Protocol as primitive](0001-driver-protocol-as-architectural-primitive.md),
  [ADR-0007 Protocol code generation](0007-protocol-code-generation.md),
  [ADR-0008 Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md),
  [ADR-0009 Navigate, session lifecycle, and the driver error mapping](0009-navigate-and-session-lifecycle.md),
  [ADR-0010 Element lifecycle, capability granularity, and selector mapping](0010-element-lifecycle-and-capability-gating.md),
  [ADR-0011 Screenshot RPC, scope mapping, and payload boundaries](0011-screenshot-rpc-and-payload-boundaries.md).
