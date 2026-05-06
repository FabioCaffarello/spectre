# DSL evolution

> **Operational companion** to
> [ADR-0035](../adr/0035-dsl-evolution-driver-abstraction.md).
> ADR-0035 commits the four-version trajectory and the
> service-vs-engine-module decision deferral; this document
> shows what each DSL version *looks like* in operational
> form, with full per-version examples and migration paths.

## §1 — v1alpha1 DSL (current; frozen per ADR-0001)

The v1alpha1 DSL ([ADR-0012](../adr/0012-engine-dsl-and-execution-pipeline.md))
mirrors the Driver Protocol's RPC set 1:1: each verb
(`navigate`, `query`, `extract`, `output`) maps to a
Driver Protocol RPC. Driver selection is **explicit** —
the user picks `playwright` / `seleniumbase` /
`curl-impersonate` per ScrapeJob.

```yaml
apiVersion: spectre.io/v1alpha2
kind: ScrapeJob
metadata:
  name: scrape-products
  namespace: tenant-a
spec:
  engineRef:
    service: { name: spectre-engine }
  driver:
    kind: playwright
  dsl:
    navigate: { url: "https://example.com/products" }
    query:
      products:
        css: ".product-card"
    extract:
      from: products
      fields:
        name:  { css: "h3" }
        price: { css: ".price" }
    output:
      sink:
        kafka:
          topic: "tenant-a.products"
```

The DSL is **flat** — no control flow, no per-step
conditionals, no transform pipeline. Multi-step workflows
(login → navigate → extract) submit multiple ScrapeJobs or
embed multiple `navigate`/`query`/`extract` blocks; both are
verbose.

This DSL is the v1alpha1 contract — frozen per
[CONTRIBUTING.md](../../CONTRIBUTING.md)'s "Architectural
commitments" #1. v1alpha2 evolution is **additive**.

## §2 — v1alpha2 DSL (this phase; engine-internal evolution)

v1alpha2 adds **five primitives** layered on the v1alpha1
verb-mirrored surface. The Driver Protocol stays frozen —
the engine's DSL parser expands the new primitives into
v1alpha1-shaped Driver RPC sequences. Driver selection
remains explicit per
[ADR-0035 §7](../adr/0035-dsl-evolution-driver-abstraction.md).

```yaml
apiVersion: spectre.io/v1alpha2
kind: ScrapeJob
metadata:
  name: scrape-products
  namespace: tenant-a
spec:
  engineRef:
    service: { name: spectre-engine }
  driver:
    kind: playwright

  dsl:
    # ── Primitive 1: Schema declaration (per ADR-0034) ────────
    schema:
      ref: spectre.io/products/v2
      validation:
        mode: STRICT             # STRICT | LENIENT | OFF
        onFailure: FAIL_ROW      # FAIL_ROW | FAIL_JOB | LOG_AND_EMIT

    # ── Primitive 2: Multi-step navigation ─────────────────────
    steps:
      - navigate: { url: "https://example.com/login" }
      - fill:     { selector: "#username", value: "${SECRETS.user}" }
      - fill:     { selector: "#password", value: "${SECRETS.pass}" }
      - click:    { selector: "button[type=submit]" }
      - waitFor:  { selector: ".dashboard", timeoutSeconds: 10 }

    # ── Primitive 3: Pagination ────────────────────────────────
    paginate:
      next:
        click: { selector: "a.next-page" }
      stopWhen:
        absent: { selector: "a.next-page" }
      onEachPage:

        # ── Primitive 4: Conditional ───────────────────────────
        condition:
          if: { selector: ".captcha-challenge", present: true }
          then:
            captcha:
              sitekey: { selector: ".g-recaptcha", attr: "data-sitekey" }
              kind: RECAPTCHA_V2
          else: skip

        query:
          products: { css: ".product-card" }

        # ── Primitive 5: Transforms ────────────────────────────
        extract:
          from: products
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
      sink:
        kafka: { topic: "tenant-a.products" }
```

### Diff from v1alpha1

| What's new | Effect |
|---|---|
| `schema:` block | Engine validates per row against schema-registry-stored schema |
| `steps:` sequence | Replaces verbose multi-`navigate` patterns with one ScrapeJob |
| `paginate:` block | Loops until stop condition; replaces "submit ScrapeJob per page" |
| `condition:` block | Per-step branching; predicate evaluation engine-side |
| `transform:` chain | Per-field transform pipeline (built-in + service-backed) |

The v1alpha1 verb set (`navigate`, `query`, `extract`,
`output`) is preserved verbatim — v1alpha2 ScrapeJobs that
don't use the new primitives are byte-identical to v1alpha1
ScrapeJobs.

## §3 — v1beta1 DSL (sketch; illustrative)

v1beta1 transitions to **intent-declarative** with capability
hints replacing explicit driver selection. The driver-router
service (or engine module per ADR-0035 §6's deferred
decision) handles per-target driver picking, capability
matching, fallback chains.

```yaml
apiVersion: spectre.io/v1beta1
kind: ScrapeJob
metadata:
  name: scrape-products
  namespace: tenant-a
spec:
  engineRef:
    service: { name: spectre-engine }

  # ── Capability hints replace explicit driver ────────────────
  capabilities:
    needs:    [javascript_execution, browser_fingerprinting]
    prefers:  [low_cost, low_detection_risk]
    avoids:   [rate_limited_target]

  # ── Driver routing via router (or engine module) ────────────
  routing:
    fallbackChain: [auto, auto, auto]   # 3 attempts; auto-selected
    onFailure:
      retryWithFallback: true
      maxFallbackAttempts: 3

  # ── Optional driver hint (opt-in override) ──────────────────
  driverHint:
    kind: playwright           # only applied if capabilities permit

  dsl:
    schema:
      ref: spectre.io/products/v2

    # ── Intent-driven extraction ───────────────────────────────
    extract:
      target: PRODUCT_LISTING       # well-known intent type
      schema: spectre.io/products/v2
      # The platform's per-target playbook determines per-driver selectors

    # ── v1alpha2 primitives carry forward unchanged ───────────
    paginate:
      next: { auto: true }          # router infers from page structure
      stopWhen: { auto: true }

    output:
      sink: { kafka: { topic: "tenant-a.products" } }
```

The v1beta1 surface is **a superset** of v1alpha2 — existing
v1alpha2 ScrapeJobs continue to work unchanged when the
v1beta1 engine ships; new v1beta1 ScrapeJobs use the
intent-declarative surface; `driver.kind` becomes
`driverHint` (opt-in override).

This is **illustrative** — the exact v1beta1 surface depends
on Wave 10 evidence per the driver-router decision deferral
(ADR-0035 §6).

## §4 — v1 DSL (north-star; illustrative far-future)

v1 evolves further toward **fully abstract intent**:

```yaml
apiVersion: spectre.io/v1
kind: ScrapeJob
metadata:
  name: scrape-products
  namespace: tenant-a
spec:
  intent:
    target: spectre.io/products/v2     # output schema is the identifier
    site: "example.com"                # platform's catalogue determines approach
    schedule: "0 3 * * *"              # nightly at 03:00
    sla:
      successRate: 99.5
      maxLatencyMinutes: 30
      maxCostPerRow: 0.001             # per ADR-0038 cost tracking
```

At this stage the DSL is **a declaration of business intent**,
not an execution recipe. The platform's catalogue
(driver-router + per-target playbooks + historical
performance + per-target schemas) determines the execution
plan automatically.

This is far-future — the path from v1beta1 to v1 takes years
of evidence accumulation; the sketch surfaces the
trajectory's direction without locking implementation
choices.

## §5 — Per-version migration paths

### v1alpha1 → v1alpha2

**Non-breaking**. Existing v1alpha1 ScrapeJobs continue to
work unchanged — the v1alpha2 engine accepts both. To
migrate:

1. Identify ScrapeJobs that benefit from primitives (multi-
   step workflows; per-target paginated harvests; CAPTCHA-
   triggered targets).
2. Refactor those ScrapeJobs to use the new primitives;
   remove the verbose alternatives.
3. Add `schema:` block to all ScrapeJobs once their target
   schemas are registered in schema-registry (Wave 6 build
   PR adds the registry).
4. ScrapeJobs that use no v1alpha2 primitives need no
   changes.

### v1alpha2 → v1beta1

**Non-breaking, with renames**. Existing v1alpha2 ScrapeJobs
continue to work; the v1beta1 engine treats `driver.kind`
as `driverHint`. To migrate:

1. Identify ScrapeJobs where capability hints can replace
   explicit driver (most ScrapeJobs at v1beta1 maturity).
2. Replace `driver.kind: playwright` with `capabilities.needs`
   + `capabilities.prefers`.
3. Remove explicit selectors where intent-declarative
   `target:` covers them via per-target playbooks.
4. Test under v1beta1 driver-router (or engine module);
   compare success rates against v1alpha2 baseline.
5. ScrapeJobs requiring explicit driver pinning (debugging;
   capability-pinned workflows) keep `driverHint`.

### v1beta1 → v1

**Non-breaking, evidence-driven**. Path depends on v1beta1
evidence; v1 DSL is far-future and not committed in detail
yet.

### Migration tooling (deferred)

Automated DSL migration tooling (e.g., `spectre dsl migrate
--from v1alpha1 --to v1alpha2`) is deferred to v1beta1 work
per ADR-0035 §10. v1alpha2 → v1alpha2-with-primitives
migration is currently manual; the additive nature limits
the migration burden.

## §6 — Backwards compatibility commitments

Three commitments hold across the trajectory:

1. **The Driver Protocol stays frozen** per
   [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md)
   through every DSL version. v1alpha2 / v1beta1 / v1 add
   DSL primitives; the wire-level Driver RPC set is
   unchanged.

2. **DSL versions are supersets**. v1alpha2 contains
   every v1alpha1 verb; v1beta1 contains every v1alpha2
   construct (with `driver.kind` becoming `driverHint`); v1
   contains every v1beta1 surface plus intent abstraction.

3. **No silent semantic drift**. When a verb's behaviour
   evolves between versions (e.g., v1beta1's `paginate:
   { auto: true }` infers stop conditions where v1alpha2
   required explicit `stopWhen`), the change is **opt-in**
   — v1alpha2-shaped DSL continues to behave v1alpha2 way
   under the v1beta1 engine.

The byte-for-byte capability divergence chain
(Playwright 13 ⊃ SeleniumBase 12 ⊃ curl-impersonate 6) is
preserved through all DSL versions per ADR-0017 §1.
Capability surface changes require an ADR.

## §7 — Reference materials

- [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive; the freeze that allows DSL
  evolution to be engine-internal.
- [ADR-0012](../adr/0012-engine-dsl-and-execution-pipeline.md)
  — v1alpha1 DSL surface (current).
- [ADR-0017 §1](../adr/0017-curl-impersonate-extraction-and-final-capability-divergence.md)
  — strict-subset capability chain.
- [ADR-0034](../adr/0034-output-schema-validation.md) — the
  `schema:` primitive (§2 primitive 1).
- [ADR-0035](../adr/0035-dsl-evolution-driver-abstraction.md)
  — the four-version DSL trajectory + driver-router
  decision deferral.
- [ADR-0036 §3.7](../adr/0036-microservices-catalog-expansion.md)
  — catalog reservation for `driver-router` slot 14.
- [ADR-0038](../adr/0038-cost-tracking-attribution.md) — the
  cost data v1 SLA blocks consume.
- [`platform-architecture.md`](platform-architecture.md) —
  umbrella v1alpha2 architectural overview.
- [`engine-orchestrator.md`](engine-orchestrator.md) —
  engine's per-step orchestration that DSL primitives
  expand into.
- [CONTRIBUTING.md](../../CONTRIBUTING.md) — "Architectural
  commitments" #1 (Driver Protocol freeze).
