---
status: accepted
date: 2026-05-06
deciders: [Fabio Caffarello]
---

# DSL evolution and driver abstraction

## §1 — Context and Problem Statement

The v1alpha1 ScrapeJob DSL ([ADR-0012](0012-engine-dsl-and-execution-pipeline.md))
is a **driver-RPC-mirrored execution surface**: each DSL
verb (`navigate`, `query`, `extract`, `output`) maps
1:1 to a Driver Protocol RPC; the engine's job-execution
loop is essentially "parse DSL, dispatch each verb to the
adapter via gRPC, emit rows". The DSL is **driver-explicit**
— the user picks the driver (`engineRef.driver:
playwright | seleniumbase | curl-impersonate`) and writes
DSL that targets that driver's capability surface.

The shape was the right call for the v1alpha1 production-
installable surface — the byte-for-byte capability
divergence chain ([ADR-0017 §1](0017-curl-impersonate-extraction-and-final-capability-divergence.md))
is the project's most architecturally consequential
narrative artefact, and the DSL surfacing the divergence
explicitly preserves it.

The shape is **insufficient** as the platform grows toward
v1beta1+. Two pressures:

- **Workflow primitives are missing.** Real scraping flows
  need pagination, conditional execution
  (`if logged_in then extract else navigate to login`),
  multi-step navigation, in-job transforms (parsePrice,
  geocode, classify). v1alpha1 handles each via DSL
  verbosity (10+ steps per workflow); v1alpha2 should add
  primitives.
- **Driver-explicit selection scales poorly.** Per-target
  driver selection is a real concern — some sites need
  Playwright's full browser; some need SeleniumBase's
  stealth posture; some need curl-impersonate's
  fingerprint precision. Asking every user to know which
  driver per target is operationally untenable as the
  catalogue of supported targets grows. v1beta1 needs
  **driver routing intelligence** — capability matching
  per target; cost-aware selection; fallback chains —
  abstracted behind the DSL.

This ADR commits the v1alpha2 platform to a **two-step
DSL evolution**:

1. **v1alpha2 DSL adds workflow primitives** (pagination,
   conditional, multi-step nav, schema declaration per
   ADR-0034, transforms) while **staying driver-explicit**.
   Users still select drivers; the new primitives reduce
   verbosity for common patterns. The Driver Protocol
   ([ADR-0001](0001-driver-protocol-as-architectural-primitive.md))
   stays frozen — additions land via DSL evolution, not
   protocol amendments.
2. **v1beta1 DSL transitions to intent-declarative** with a
   `driver-router` service handling per-target selection,
   capability matching, fallback chains. Driver explicitness
   becomes opt-in (`driverHint`) rather than default.

ADR-0035 is one of four subsystem ADRs in R9.4; it carries
**the most substantial architectural commitment** of the
four because it defines the platform's long-term DSL
trajectory and surfaces the driver-router service-vs-engine-
module decision explicitly.

### §1.1 — What this ADR does not yet land

No DSL parser changes, no engine planner changes, no
proto file, no driver-router service materialise in R9.4.
This ADR is contract-only:

- v1alpha2 DSL primitives (§4) materialise across **Wave 7
  build PRs** alongside `rate-limit-broker` and
  `fingerprint-broker` per ADR-0036's wave assignment —
  the DSL primitives are engine-internal evolution, not
  service consumers.
- The driver-router decision (§6) **surfaces to the
  maintainer at Wave 10**; this ADR records both options
  with full trade-off analysis.
- The v1beta1 DSL trajectory (§3.3) is **illustrative
  far-future** — sketched here for forward record, not
  committed in v1alpha2.

## §2 — Decision summary

R9.4 commits the DSL evolution to:

- **v1alpha2 DSL primitives** — five new DSL constructs
  (pagination, conditional, multi-step nav, schema, transform)
  layered on the v1alpha1 verb-mirrored surface. The
  primitives are **engine-internal** — the Driver Protocol
  stays frozen; no adapter changes required. Per ADR-0001
  §1's freeze commitment.
- **v1alpha2 DSL stays driver-explicit** per the framework
  v3 D9 option α decision (driver abstraction in v1beta1
  territory) — users still pick the driver; the primitives
  reduce verbosity for common patterns without abstracting
  the driver away.
- **v1beta1 DSL trajectory sketch** — intent-declarative
  surface with capability hints replacing driver-explicit
  selection. Sketched in §3.3 for forward record;
  illustrative.
- **driver-router service-vs-engine-module decision
  deferred to Wave 10** — both options surfaced with full
  trade-off analysis; final decision lands when
  v1alpha2 evidence base is sufficient.

## §3 — DSL evolution trajectory

The DSL evolves across four versions: v1alpha1 (current,
frozen), v1alpha2 (this ADR), v1beta1 (sketched),
v1 (illustrative). Each version is a **superset**
(additive) of its predecessor for the verbs they share;
breaking changes happen at major-version boundaries
(v1alpha2 → v1beta1; v1beta1 → v1).

### §3.1 — v1alpha1 DSL (current, frozen)

The v1alpha1 DSL per
[ADR-0012](0012-engine-dsl-and-execution-pipeline.md):

```yaml
spec:
  engineRef:
    service:
      name: spectre-engine
  driver:
    kind: playwright          # explicit driver selection
  dsl:
    navigate: { url: "https://example.com/products" }
    query:
      products:
        css: ".product-card"
    extract:
      from: "products"
      fields:
        name:  { css: "h3" }
        price: { css: ".price" }
    output:
      sink: { kafka: { topic: "products" } }
```

The verb set is the Driver Protocol RPC set
mirrored: `navigate` ↔ `Driver.Navigate`; `query` ↔
`Driver.Query`; `extract` ↔ `Driver.Extract`; `output` ↔
`Engine.Emit` (engine-side, not driver). The DSL is
**flat** — no control flow, no per-step conditionals, no
transform pipeline.

This DSL is **the v1alpha1 contract** — frozen per
[CONTRIBUTING.md](../../CONTRIBUTING.md)'s "Architectural
commitments" #1; v1alpha2 evolution is additive.

### §3.2 — v1alpha2 DSL (this ADR's commitment)

v1alpha2 adds five primitives:

```yaml
spec:
  engineRef:
    service:
      name: spectre-engine
  driver:
    kind: playwright          # still explicit per §7

  dsl:
    # Schema declaration per ADR-0034
    schema:
      ref: spectre.io/products/v2
      validation:
        mode: STRICT
        onFailure: FAIL_ROW

    # Multi-step navigation (§4.3)
    steps:
      - navigate: { url: "https://example.com/login" }
      - fill:     { selector: "#username", value: "${SECRETS.user}" }
      - fill:     { selector: "#password", value: "${SECRETS.pass}" }
      - click:    { selector: "button[type=submit]" }
      - waitFor:  { selector: ".dashboard", timeoutSeconds: 10 }

    # Pagination (§4.1) — loops until exhausted
    paginate:
      next:
        click:    { selector: "a.next-page" }
      stopWhen:
        absent:   { selector: "a.next-page" }
        # OR: maxPages: 100
      onEachPage:

        # Conditional (§4.2)
        condition:
          if: { selector: ".captcha-challenge", present: true }
          then:
            captcha:        # invokes captcha-solver per ADR-0036 §3.1
              sitekey: { selector: ".g-recaptcha", attr: "data-sitekey" }
              kind: RECAPTCHA_V2
          else: skip

        # Standard query + extract per v1alpha1
        query:
          products:
            css: ".product-card"

        # Extract with transforms (§4.5)
        extract:
          from: "products"
          fields:
            name:  { css: "h3" }
            price:
              css: ".price"
              transform:
                - parseDecimal: { thousandsSeparator: "," }
                - currency:     { fromText: ".price-currency" }
            in_stock:
              css: ".availability"
              transform:
                - matches: { pattern: "in stock", caseSensitive: false }

    output:
      sink: { kafka: { topic: "products" } }
```

The new primitives are **engine-internal** — the engine's
DSL parser interprets `paginate`, `condition`, `steps`,
`schema`, and `transform`; the resulting Driver Protocol
calls are unchanged from v1alpha1 (per-step `Navigate`,
`Query`, `Extract`). This preserves
[ADR-0001's frozen protocol](0001-driver-protocol-as-architectural-primitive.md)
as the architectural primitive while expanding the DSL's
expressiveness.

### §3.3 — v1beta1 DSL (sketch — illustrative)

v1beta1 transitions to **intent-declarative** with
capability hints replacing explicit driver selection:

```yaml
spec:
  capabilities:
    needs:    [javascript_execution, browser_fingerprinting]
    prefers:  [low_cost, low_detection_risk]
    avoids:   [rate_limited_target]
  routing:
    fallbackChain: [auto, auto, auto]
  dsl:
    extract:
      target: PRODUCT_LISTING        # intent type
      schema: spectre.io/products/v2
    onFailure:
      retryWithFallback: true
      maxFallbackAttempts: 3
```

This is **illustrative** — the exact v1beta1 surface
depends on Wave 10 evidence (per §6's driver-router
decision) and v1beta1's broader anti-detection learning
catalog. Progressive disclosure: v1alpha2 ScrapeJobs
continue to work in v1beta1 (superset surface); new
v1beta1 ScrapeJobs use intent-declarative;
`driver.kind` becomes `driverHint` (opt-in override).

### §3.4 — v1 DSL (north-star — illustrative far-future)

v1 evolves further toward **fully abstract intent**:

```yaml
spec:
  intent:
    target: spectre.io/products/v2     # identified by output schema
    site: "example.com"                # platform's catalogue determines approach
    schedule: "0 3 * * *"              # nightly at 03:00
    sla:
      successRate: 99.5
      maxLatencyMinutes: 30
      maxCostPerRow: 0.001             # per ADR-0038 cost tracking
```

At this stage, the DSL is **a declaration of business
intent**, not an execution recipe. The platform's
catalogue (driver-router + per-target playbooks +
historical performance data) determines the execution
plan automatically.

This is **far-future commentary**, not a v1 commitment —
the path from v1beta1 to v1 takes years of platform
evolution; the v1 sketch here surfaces the trajectory's
direction without locking implementation choices.

## §4 — v1alpha2 DSL primitives

The five v1alpha2 primitives, each with examples and
contract.

### §4.1 — Pagination

The `paginate` primitive loops until a stop condition
matches:

```yaml
paginate:
  next:                           # required: how to advance pages
    click: { selector: "a.next" }
    # OR: scroll: { until: "bottom" }
    # OR: navigate: { url: "${URL}?page=${PAGE_INDEX + 1}" }

  stopWhen:                       # required: at least one stop condition
    absent:   { selector: "a.next" }
    # OR: maxPages: 100
    # OR: pageMatches: { url: "page-1$" }   # back to start

  onEachPage: <inner-DSL>         # required: per-page work
```

The engine's planner expands `paginate` into a loop with
the per-page DSL. Page indices are tracked
(`${PAGE_INDEX}` available within the inner DSL); per-page
extraction emits to the same output sink as outer DSL.

### §4.2 — Conditional execution

The `condition` primitive branches on per-step state:

```yaml
condition:
  if:                             # required: predicate
    selector: ".captcha"
    present: true
    # OR: matches: { selector: ".user-name", textPattern: "Logged in as" }
    # OR: status: 403            # HTTP status from prior step
  then: <inner-DSL>               # required if branch taken
  else: <inner-DSL or skip>       # optional; default skip
```

The predicate evaluates against the page state at the
moment the condition runs (after preceding steps complete).
The engine implements predicate evaluation engine-side; the
driver does not need new RPCs.

### §4.3 — Multi-step navigation

The `steps` primitive ordered-sequences DSL operations:

```yaml
steps:
  - navigate: { url: "..." }
  - fill:     { selector: "...", value: "..." }
  - click:    { selector: "..." }
  - waitFor:  { selector: "...", timeoutSeconds: 10 }
  - extract:  { ... }
  - navigate: { url: "..." }      # second navigation
```

This consolidates the v1alpha1 pattern of "submit a
ScrapeJob with multiple `navigate` calls" into a single
DSL. The verbs available within `steps` are the
v1alpha2 verb set including `fill` and `click` and
`waitFor` (new in v1alpha2; engine-internal — driver
performs them via existing `Navigate` + `Query` RPCs).

### §4.4 — Schema declaration

Per ADR-0034 (this PR's Cluster B). The `schema:` block
references a schema in the schema-registry; the engine
fetches it once at job start and validates per-row.

### §4.5 — Transforms

The `transform:` block within an extracted field applies a
**chain of transforms** to the raw value:

```yaml
extract:
  fields:
    price:
      css: ".price"
      transform:
        - parseDecimal: { thousandsSeparator: "," }
        - currency:     { fromText: ".price-currency" }
        # Pipeline: raw text → decimal → typed currency object

    coords:
      css: ".address"
      transform:
        - geocode:      { provider: enricher }    # invokes enricher per ADR-0036 §3.4

    summary:
      css: ".description"
      transform:
        - classify:     { model: SENTIMENT, provider: enricher }
        - truncate:     { maxLength: 200 }
```

The transform set is **extensible**:

- **Built-in transforms** (engine-internal, no service
  call): `parseDecimal`, `parseInt`, `parseDate`,
  `currency`, `lowercase`, `uppercase`, `trim`, `truncate`,
  `regex`, `matches`, `replace`, `concat`, `parseJson`.
- **Service-backed transforms** (engine calls a service
  per row): `geocode` (enricher), `classify` (enricher),
  `embed` (enricher), `validate` (schema-registry).
- **User-defined transforms** (deferred to v1beta1) —
  custom WASM modules registered in a schema-registry-
  adjacent service.

The transform pipeline runs **per row** at the engine; per
ADR-0037 §4.3, service-backed transforms are synchronous
(the row depends on the result). v1alpha2's set is built-in
+ enricher; user-defined transforms defer.

## §5 — Driver routing intelligence

v1alpha2 DSL stays driver-explicit (§7); driver routing
intelligence is a **v1beta1 concern**. This section
sketches the routing concerns that ADR-0035 acknowledges
and that ADR-0035's deferred §6 decision will resolve at
Wave 10.

### §5.1 — Capability matching

Per-target capability matching:

- **Inputs**: the target URL's domain or pattern; the
  capabilities the DSL needs (`javascript_execution`,
  `browser_fingerprinting`, etc.); historical success
  data per (target, driver).
- **Output**: the driver to use, plus a fallback chain.

The matching logic is **rules + history**:
- Rules: hard-coded "JavaScript-required → Playwright or
  SeleniumBase, not curl-impersonate" rules per
  capability per driver.
- History: per-target driver-success ratios from prior
  scrapes; decay over time.

ADR-0017 §1's strict-subset chain (Playwright 13 ⊃
SeleniumBase 12 ⊃ curl-impersonate 6) is the **rule
backbone** — capability matching defaults to the most
capable driver and narrows by cost / detection risk.

### §5.2 — Cost-aware selection

Different drivers have different per-call costs:
- **Playwright**: highest (full browser; high CPU; slow)
- **SeleniumBase**: medium (browser with stealth overhead)
- **curl-impersonate**: lowest (no browser; fast; cheap)

The cost-tracker service (per ADR-0038, this PR's Cluster D)
records per-job costs; routing intelligence consumes the
cost data and selects the cheapest driver that meets
capability requirements + historical success threshold.

### §5.3 — Fallback chains

When the primary driver fails for a target:

```
attempt 1: curl-impersonate (cheapest)
  on bot-detection / 403 / CAPTCHA_REQUIRED:
    attempt 2: SeleniumBase (medium cost; better stealth)
      on continued failure:
        attempt 3: Playwright (highest cost; full browser)
          on continued failure:
            terminal: emit FAILURE_AFTER_FALLBACKS
```

Fallback chains are **per-target configurable** and
**learnable** — successful fallback patterns inform
default ordering for similar targets. The fallback chain
length is bounded (default 3); each attempt is recorded in
the audit-log per ADR-0036 §3.3.

## §6 — Driver-router: service vs engine module

The central architectural decision deferred from R9.1's
ADR-0036 §3.7 catalog. Driver routing intelligence
(§5 above) lives **somewhere**; the choice is:

- **(A) Separate `driver-router` service** at
  `infra-services/driver-router/`
- **(B) Engine module** within `engines/engine/`

Both options surface here with full trade-off analysis;
the **decision lands at Wave 10** when
v1alpha2 production data informs the trade-offs.

### §6.1 — Option A: Separate `driver-router` service

Pros:

- **Independent evolution.** Routing policies evolve at a
  different cadence than engine job execution. Updating
  routing rules + historical-success data does not redeploy
  the engine.
- **A/B testable routing strategies.** Two driver-router
  deployments can serve different routing strategies
  (e.g., "cost-prioritised" vs "success-rate-prioritised");
  per-tenant configuration directs traffic.
- **Cross-cutting consumption.** Multiple consumers benefit
  — engine for runtime selection; operator for ScrapeJob
  admission validation; CLI tooling for "what driver would
  this run on?" preview.
- **Independent failure surface** per ADR-0036 §2.6
  (gate F) — a driver-router outage degrades to the engine
  fallback (static rules per ADR-0017 §1) rather than
  breaking job execution.
- **Persisted history** — per-target driver-success history
  outlives engine restarts; backed by Mongo per ADR-0039
  §3.14.

Cons:

- **Latency cost.** Per-step routing adds a gRPC round-trip
  (~5 ms typical per ADR-0037 §4.6); for 100-step jobs the
  total adds ~500 ms versus an in-engine module.
- **Operational complexity.** A 16th service in the
  catalog (ADR-0036 only catalogues 15); chart fragment;
  Compose entry; per-language SDK matrix expansion.
- **Cold-start coordination.** A new ScrapeJob in a new
  cluster cannot make routing decisions until the
  driver-router has historical data; the bootstrap window
  needs handling.

### §6.2 — Option B: Engine module

Pros:

- **Lowest latency.** Routing decisions happen in-process
  with no network hop; per-step cost is sub-millisecond.
- **Simpler operational footprint.** One fewer service in
  the catalog; one fewer chart fragment; one fewer Compose
  entry.
- **Tightly coupled to job execution.** Routing decisions
  have full access to job state (DSL, plan, in-progress
  results) without serialisation overhead.
- **No cold-start problem.** Routing rules are part of the
  engine binary; bootstrap is automatic.

Cons:

- **Coupled evolution.** Routing-policy updates require
  engine redeploy. New per-target rules ship with engine
  releases, not independently.
- **No A/B routing strategies.** Engine binary's routing
  logic is monolithic per release; can't run two
  strategies side-by-side without forking the engine.
- **No persisted history (without external state).**
  Per-target success history would need to live in a
  shared store (Postgres or Mongo) the engine reads;
  effectively recreates the persistence layer of Option A.
- **Engine binary growth.** ADR-0037 §1.3 commits the
  engine to **shrink** in v1alpha2; adding a substantial
  routing-intelligence module fights that trajectory.

### §6.3 — The trade-off matrix

| Concern | Option A (service) | Option B (engine module) |
|---|---|---|
| Per-step latency | +5 ms typical | sub-ms |
| Operational footprint | +1 service | unchanged |
| Independent evolution | yes | no |
| A/B testable | yes | no |
| Persisted history | yes (Mongo per ADR-0039 §3.14) | requires external state |
| Cold-start | bootstrap needed | automatic |
| Engine binary size | unchanged | +substantial |
| ADR-0036 §3.7 alignment | aligned | requires re-justification |

### §6.4 — Decision deferral rationale

The choice depends on **v1alpha2 production data** that
does not yet exist:

- How latency-sensitive are real workloads? If per-step
  budgets are tight (interactive scraping use cases), the
  Option A latency penalty is meaningful; if not, the
  flexibility wins.
- How often do routing strategies need to evolve? If
  routing rules are stable (Option B is fine), the
  operational simplicity wins; if rules evolve frequently
  (especially per-tenant), Option A's flexibility wins.
- How much historical-success data accumulates per target?
  If volumes are large, persistent storage is essential
  (Option A leans natural); if small, in-engine memory
  caches suffice (Option B works).

By Wave 10, the platform has shipped Waves 5 – 9 with
production deployments providing this data. Deciding now
without evidence locks in the wrong choice.

### §6.5 — Surfaces to the maintainer at Wave 10

When Wave 10 opens, the maintainer reviews:

1. Aggregated v1alpha2 production-smoke + tenant-pilot
   metrics on per-step latency budgets.
2. Aggregated routing-policy evolution rate (how often did
   per-target rules change in Waves 5 – 9?).
3. Aggregated per-target historical-success volume.
4. The maintainer's preference between A and B given (1) –
   (3).

A new ADR (numbered after R9.4) records the decision and
materialises the chosen option. ADR-0035 §6 is the
contract: no Wave 10+ build PR ships routing intelligence
without the maintainer's resolution of A vs B.

## §7 — v1alpha2 DSL stays driver-explicit

The v1alpha2 DSL `driver.kind` field remains required —
users still pick the driver for each ScrapeJob. The
rationale:

- **The protocol freeze (ADR-0001) is preserved.** Driver-
  abstraction would require adapter-side changes (each
  driver advertising capability metadata; engine-side
  capability matching consuming it); v1alpha2 holds the
  protocol stable.
- **Driver routing intelligence requires evidence.** §6's
  decision deferral depends on production data that
  doesn't exist yet; shipping intelligence in v1alpha2
  without the data risks the wrong default.
- **The strict-subset chain (ADR-0017 §1) is the rule
  backbone.** Users selecting drivers explicitly carry
  forward the chain's invariants — capability divergence
  byte-for-byte preserved per CONTRIBUTING.md
  "Architectural commitments" #3.

The framework v3 D9 decision committed to **option α** —
driver abstraction in v1beta1 territory, not v1alpha2.
This ADR is the formal record.

### §7.1 — `driverHint` as future opt-in

When v1beta1's intent-declarative DSL lands per §3.3, the
v1alpha2 `driver.kind` field becomes `driverHint` — opt-in
override for ScrapeJobs that need explicit driver
selection (debugging, capability-pinned workflows). The
rename is **non-breaking** at the v1alpha2 → v1beta1
boundary: existing v1alpha2 ScrapeJobs continue to work
with their explicit `driver.kind`; new v1beta1 ScrapeJobs
omit the field and let the router decide.

## §8 — Migration sequence

R9.4's ADR-0035 is documentation-only. The materialisation:

| Wave | Scope |
|---|---|
| Wave 7 (build PRs) | v1alpha2 DSL primitives material — pagination, conditional, multi-step nav, transforms (built-in set). Engine planner extends; Driver Protocol unchanged. The DSL primitives ship across multiple PRs (one per primitive; each transformational scope per CONTRIBUTING.md cadence). The `schema:` primitive ships with ADR-0034's schema-registry materialisation in Wave 6 (one Wave earlier). |
| Wave 10 (architectural decision) | Driver-router service-vs-engine-module decision per §6 surfaces to maintainer. A new ADR (numbered TBD) records the decision. |
| Wave 10 (build PRs) | Driver-router materialises per the §6 decision. v1alpha2 → v1beta1 DSL transition begins; intent-declarative surface starts shipping behind a feature flag. |
| v1beta1 | Full v1beta1 DSL surface per §3.3. The intent-declarative surface becomes default; `driverHint` opt-in for explicit selection. |
| v1 | Far-future per §3.4 — illustrative only; not committed. |

The v1alpha2 DSL primitives are **engine-internal
evolution** — no service is required for any of them; the
engine's planner expands DSL primitives into the v1alpha1
verb stream the protocol carries. This preserves ADR-0001's
freeze.

## §9 — Confirmation (acceptance criteria)

The framework is working when the following hold:

**By the close of Wave 6** (ADR-0034 schema primitive):

- **A ScrapeJob with `extract.schema:` block** validates
  per-row against the registered schema; failures surface
  per the `onFailure` policy.

**By the close of Wave 7** (v1alpha2 primitives):

- **A ScrapeJob with `paginate:` block** loops through
  pages until the stop condition matches.
- **A ScrapeJob with `condition:` block** branches per the
  predicate evaluation.
- **A ScrapeJob with `steps:` block** sequences operations
  with intermediate `fill` / `click` / `waitFor` steps.
- **A ScrapeJob with `transform:` chain** applies
  built-in transforms per row; service-backed transforms
  (geocode, classify) invoke the enricher when it
  materialises in Wave 10.

**By the close of Wave 10**:

- **The driver-router decision (§6) is recorded** in a
  new ADR (numbered TBD) per §6.5's deferral.
- **The decided option is implemented** — Option A
  materialises driver-router as slot 14 per ADR-0036
  §3.7; Option B materialises driver-router as an engine
  module at `engines/engine/src/router/`.

**By v1beta1**:

- **v1beta1 DSL surface ships** per §3.3 — intent-
  declarative; capability hints; driver-router-driven
  selection.
- **v1alpha2 ScrapeJobs continue to work** under the
  v1beta1 engine — the v1beta1 DSL is a superset of
  v1alpha2's verb surface.

A signal that the framework needs revision: more than one
Wave 7+ user pilot reports a real workflow primitive
missing (e.g., loops over arrays of values; multi-page
extraction with per-page state; A/B-tested DSL
deployments). That's evidence the primitive set is
incomplete; the response is an ADR amendment that adds
the primitive following the pattern in §4, not a per-tenant
deviation.

## §10 — What's deferred / out of scope

R9.4 declines these deliberately. Each is a real concern;
each belongs to a later phase or to a sibling ADR.

- **Driver routing intelligence implementation.** Per §6's
  deferral, the router materialises at Wave 10 after the
  service-vs-engine-module decision lands.
- **v1beta1 DSL surface details.** §3.3 is illustrative;
  the full v1beta1 surface lands in v1beta1 work outside
  R9.4 scope.
- **User-defined transforms.** Custom WASM transforms
  registered in a schema-registry-adjacent service —
  deferred to v1beta1 per §4.5.
- **DSL macros / templates.** A `template:` primitive that
  expands a reusable DSL fragment — deferred to v1beta1.
- **DSL versioning at the per-job level.** Today every
  ScrapeJob's DSL is implicitly the API version's DSL
  (v1alpha2 ScrapeJobs use v1alpha2 DSL). Per-job DSL
  version pinning is v1beta1 territory.
- **DSL linting.** Static analysis for common mistakes
  (unused selectors; impossible conditions; deprecated
  transforms) — useful but tooling concern outside ADR
  scope.
- **DSL formatting.** Canonical YAML formatting for
  ScrapeJob CRs — tooling concern.
- **DSL diff / migration.** Tooling for migrating
  ScrapeJobs from v1alpha1 to v1alpha2 to v1beta1 —
  deferred to v1beta1 work.
- **Cross-job DSL sharing.** A `extends:` primitive that
  references another ScrapeJob's DSL as a base — v1beta1
  template work overlaps; deferred.
- **Visual DSL editors.** GUI tools for ScrapeJob
  composition — out of platform scope; ecosystem concern.

## §11 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive; v1alpha2 DSL preserves the
  freeze (no protocol changes needed for primitives).
- [ADR-0009](0009-navigate-and-session-lifecycle.md) —
  driver error mapping; `FAILURE_AFTER_FALLBACKS` (§5.3)
  extends ADR-0009's enum at Wave 10.
- [ADR-0012](0012-engine-dsl-and-execution-pipeline.md) —
  v1alpha1 DSL surface; v1alpha2 is additive (per §3.2).
- [ADR-0017](0017-curl-impersonate-extraction-and-final-capability-divergence.md)
  §1 — strict-subset capability chain; the rule backbone
  for §5.1 capability matching.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane and ScrapeJob CRD; v1alpha2 DSL
  primitives extend the existing CRD's `spec.dsl` field.
- [ADR-0024](0024-output-sinks.md) — output sinks; sinks
  emit rows per the existing contract; transforms run
  pre-emission per §4.5.
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy;
  per-language driver-router SDKs (if Option A in §6) follow
  the admission gate.
- [ADR-0036](0036-microservices-catalog-expansion.md) —
  the 15-service catalog; `driver-router` is slot 14
  pending §6's decision.
- [ADR-0037](0037-engine-as-orchestrator.md) — engine as
  orchestrator; the §3.2 pseudocode includes
  `driver_router.Pick`.
- [ADR-0038](0038-cost-tracking-attribution.md) (this PR's
  Cluster D) — cost tracking; cost-aware selection (§5.2)
  consumes ADR-0038's per-job cost ledger.
- [ADR-0039](0039-mongodb-third-storage-tier.md) —
  MongoDB tier; §3.14 evaluates driver-router's backend
  if Option A materialises.
- ADR-0033 (this PR's Cluster A) — input management;
  ScrapeBatch's `scrapeTemplate.dsl` follows the v1alpha2
  surface this ADR commits.
- ADR-0034 (this PR's Cluster B) — output schema and
  validation; the `schema:` primitive (§4.4) is ADR-0034's.
- Framework v3 D9 — driver abstraction timing decision;
  ADR-0035 records the option α (defer to v1beta1).
