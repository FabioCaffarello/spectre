# Roadmap

This roadmap is a forecast, not a promise. Dates are absent on
purpose — the maintainers will move milestones based on real
progress, not a schedule the prompt forced into existence. The
phases are listed in dependency order; each phase unblocks the next.

> **Last updated:** 2026-04-26.

## Phase 0 — Foundation (current)

Goal: a credible, navigable repository that signals "this person
knows what they are doing." Nothing has to *run* yet.

- [x] Repository structure and foundational documents (LICENSE,
      README, CONTRIBUTING, GOVERNANCE, SECURITY).
- [x] Editor / Git config, pre-commit, CI workflow scaffolding.
- [x] Architecture Decision Records 0001–0007.
- [x] `justfile` build orchestration.
- [x] Driver Protocol skeleton at `v1alpha1` (Step 2.6).
- [x] Three reference adapter skeletons (Playwright, SeleniumBase,
      curl-impersonate) — directory layout, manifests, README, build
      passes (Step 2.8).
- [x] Conformance suite skeleton (Step 2.9).
- [x] Protocol code-generation pipeline wired across Rust, Go,
      Python, and TypeScript (ADR-0007). Every consumer imports
      generated types instead of literal protocol strings.

Exit criterion: every component compiles to nothing useful but
compiles cleanly. CI is green. **Met.** Phase 1 work is unblocked.

## Phase 1 — Hello, World (in progress)

Goal: end-to-end execution of a trivial job through the protocol.

- [ ] Engine parses the minimal DSL (one navigation + one extract).
- [ ] Engine speaks gRPC over UDS to a single driver.
- [x] Playwright reference adapter implements every v1alpha1 unary
      RPC. The protocol surface is complete on this driver;
      remaining Phase 1 work is engine-side.
  - [x] `Initialize` — gRPC over UDS, with `Capabilities` declared
        at handshake. ADR-0008.
  - [x] `Navigate` — lazy Chromium launch, per-session
        `BrowserContext`, error-mapping table from
        `Playwright → DriverError.Code`. ADR-0009.
  - [x] `Close`, `Query`, `Extract` — strict ElementRef
        invalidation on Navigate, per-session UUID registry,
        runtime gating of `MODE_EVAL` on `js_execution`. ADR-0010.
  - [x] `Screenshot` — three scopes (viewport, full-page,
        element), two formats (PNG, JPEG with quality 80),
        read-only contract (no generation bump, refs remain
        valid), payload-size boundary documented at ~4MB.
        ADR-0011.
- [ ] `spectre run hello-hackernews/job.yaml` produces a JSONL file
      with the expected rows.
- [x] Conformance suite covers every v1alpha1 unary RPC against
      the Playwright adapter (`Initialize`, `Navigate × 5`,
      `Close × 3`, `Query × 5`, `Extract × 5`, `Screenshot × 4`).
      The Driver Protocol byte-for-byte capability assertion
      holds against the thirteen declared capability names.

Exit criterion: a new contributor can `git clone && just bootstrap &&
spectre run examples/hello-hackernews/job.yaml` and see results.

## Phase 2 — Three drivers, one protocol

Goal: validate the protocol design by running it against all three
reference runtimes.

- [ ] SeleniumBase reference adapter — `v1alpha1` minimum capability
      set. Passes conformance.
- [ ] curl-impersonate reference adapter — `v1alpha1` capability set
      tailored to HTTP-only flows (no JS execution, no screenshots).
      Passes conformance.
- [ ] Streaming RPCs added behind a feature flag in `v1alpha2`:
      `WatchEvents` for network monitoring, `WatchDom` for mutation
      observation.
- [ ] Capability extensions for cookies, header overrides, basic
      proxy.
- [ ] Driver manifest validation tooling.

Exit criterion: the same `job.yaml` runs unchanged across all three
adapters where their capabilities allow. Conformance gates merges to
`proto/`.

## Phase 3 — Distributed execution

Goal: the control plane.

- [ ] Control-plane API (job submission, status, logs).
- [ ] Kubernetes operator (CRDs for jobs, drivers, fleets).
- [ ] Worker pool with retry / quota / priority.
- [ ] Helm chart in `helm/spectre/`.
- [ ] Observability: structured logs, OpenTelemetry traces, metrics.

Exit criterion: a job submitted to the control plane runs across N
workers, with bounded retries, quota enforcement, and observable
state.

## Phase 4 — Intelligence layer

Goal: selector self-healing and computer-vision-assisted extraction
for adopters who want them.

- [ ] LLM-backed selector repair: when a `Query` returns no matches,
      the engine can request the intelligence layer suggest an
      updated selector based on the page's current DOM and the job's
      historical successful selectors.
- [ ] Visual diff for layout regressions.
- [ ] Vision-based extraction for pages where the DOM is uninformative
      (canvas-rendered tables, images of text, etc.).

Exit criterion: a job whose selector silently broke after a target
site redesign auto-heals, with clear human-readable audit logs.

## Phase 5 — Stable protocol and ecosystem

Goal: declare `v1` and grow the driver ecosystem.

- [ ] Promote `v1alpha*` → `v1beta1` after capability surface settles.
- [ ] Promote `v1beta1` → `v1` after a stabilisation period with no
      breaking changes and three drivers in production use.
- [ ] Driver registry: a published index of community-maintained
      drivers, their declared capabilities, and their conformance
      status.
- [ ] SDKs in TypeScript, Python, and Go for embedding Spectre into
      applications.
- [ ] CLI ships as a static binary across macOS, Linux, and Windows.

Exit criterion: a third party publishes a driver against `v1`, and
Spectre runs it without protocol changes.

## Beyond Phase 5

Open questions, not yet committed:

- Browser-side WASM engine for in-page extraction without an external
  driver.
- A higher-level DSL above the current minimal one (joins,
  pagination, deduplication semantics).
- A managed-service offering as a reference deployment, hosted
  separately from the open-source project.

These are tracked in issues labelled `phase:next` and will move to
this document only after they have been concretely scoped.

## How to influence the roadmap

- Open a feature request on GitHub.
- For non-trivial changes, draft an ADR.
- Engage on existing issues. Maintainer attention follows where the
  community is paying attention.
