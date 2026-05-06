# Roadmap

This roadmap is a forecast, not a commitment. Dates are absent on
purpose — milestones move with real progress, not a schedule a
prompt forced into existence. Per-PR refactor history lives in the
frozen [`refactor-audit.md`](refactor-audit.md) (R1 → R8.1);
per-PR v1alpha2 phase history lives in
[`v1alpha2-audit.md`](v1alpha2-audit.md) (Phase R9+);
per-release detail lives in [`../CHANGELOG.md`](../CHANGELOG.md).

> **Last updated:** 2026-05-06 (R9.7 — roadmap §4 substantially
> rewritten to reflect the v1alpha2 trajectory codified in R9.1
> – R9.6 ADRs and architecture docs. The microservices refactor
> (R1 → R8.1) closed at `0.1.0-alpha.0`; Phase R9 (v1alpha2
> architectural foundation) is in progress; v1alpha2
> implementation begins with Wave 1 after R9.8 closes Phase R9.)

## §1 — Where we are (post-R8.1, mid-R9)

The microservices refactor closed at R8.1
(`0.1.0-alpha.0`, 2026-05-03). The platform delivers
v1alpha1 in production-installable shape: a polyglot,
gRPC-over-TCP, Helm-deployable web scraping stack with a
frozen Driver Protocol, three reference adapters, four
output sinks, three stateful tiers, and a green
production-smoke gate.

The microservices runtime ([ADR-0020](adr/0020-microservices-architecture-supersession.md))
shipped end-to-end across Phases R1–R6.5:

- **Transport (R2.1 + R2.2 + R2.3).** UDS retired; gRPC over TCP
  is the only supported transport.
- **Control plane (R3.1 + R3.2).** Operator submits jobs to the
  engine via gRPC; ScrapeJob CRD at `spectre.io/v1alpha2`.
- **Stateful services (R4.2 + R4.3 + R4.4).** PostgreSQL persists
  job state; Kafka receives streamed row events; Redis caches
  per-session metadata.
- **Output sinks (R5.1).** S3 and HTTP webhook complete the
  four-sink set.
- **Devcontainer + image story (R6.1 + R6.2 + R6.3).** Compose
  stack runs end-to-end inside the devcontainer with
  Docker-in-Docker + kind external network.
- **Quality and hardening (R6.5.1 + R6.5.2 + R6.5.3 + R6.5.4).**
  CI matrix `images` + `full-stack` gating; multi-arch publish to
  Docker Hub; shared `buf-base` codegen base.
- **Platform Maturation (R6.6).** Four ADRs (0026 – 0029) committed
  the platform taxonomy, SDK strategy, infra-services catalog,
  and data-platform model.

The R7.x production-posture phases ([§2](#§2--v1alpha1-production-posture-r7x))
and the R8.1 documentation refresh ([§3](#§3--documentation-refresh-r81-completed-2026-05-03))
closed the refactor.

**Phase R9 — v1alpha2 architectural foundation** opened
2026-05-05 and is in progress. Eight sub-PRs (R9.0 – R9.7
shipped; R9.8 in flight): CONTRIBUTING.md cadence (R9.0);
ADR-0036 + ADR-0037 (R9.1, foundation pair); ADR-0023 §14
amendment + ADR-0039 (R9.2, MongoDB tier); ADR-0031 +
ADR-0032 (R9.3, observability + mTLS); ADR-0033 + ADR-0034
+ ADR-0035 + ADR-0038 (R9.4, four subsystem ADRs); seven
new architecture docs (R9.5); forward-reference subsections
in 13 existing architecture docs (R9.6); roadmap rewrite
(this PR — R9.7); v1alpha2-audit.md + CHANGELOG closing
(R9.8).

After R9.8 closes, **Wave 1 (production hardening
foundation)** opens — see [§4](#§4--v1alpha2-trajectory-post-r81-in-progress)
for the detailed plan.

## §2 — v1alpha1 production posture (R7.x)

**[x] Phase R7 closed 2026-05-02.** Two sub-phases shipped:

- **[x] R7.1 — Helm chart packaging.** *(complete 2026-05-02 —
  see [ADR-0030](adr/0030-helm-chart-structure.md) and
  [docs/architecture/helm-chart.md](architecture/helm-chart.md).)*
  Ships [`build/helm/spectre/`](../build/helm/spectre/) installing
  the engine + three adapters + control plane + stateful
  dependencies into any conformant Kubernetes 1.27+ cluster.
  First real publish at `0.1.0-alpha.0`.
- **[x] R7.2 — Production smoke.** *(complete 2026-05-02 — see
  [docs/architecture/production-smoke.md](architecture/production-smoke.md).)*
  New standalone workflow on three triggers (manual,
  paths-filtered PR, daily 06:00 UTC cron) installs the chart
  into a kind cluster, applies three reference ScrapeJobs, and
  asserts row events arrive at each sink boundary.

Three multi-arch unblocks remain tracked outside the phase
sequencing per
[ADR-0018 §5 R6.5.3 update](adr/0018-devcontainer-and-engine-image.md).
v1alpha2 Wave 2 covers them; see
[§4.2](#§42--wave-2-multi-arch-unblocks-6-12-weeks).

## §3 — Documentation refresh (R8.1, completed 2026-05-03)

The refactor's narrative-closing PR. Three stewardship docs
were retired: the strategy prompt
(`docs/MASTER_STRATEGY_REFACTOR.md`) deleted with its seven
non-negotiable principles preserved in
[`../CONTRIBUTING.md`](../CONTRIBUTING.md) "Architectural
commitments"; `docs/refactoring-status.md` deleted entirely;
[`refactor-audit.md`](refactor-audit.md) frozen with a top-of-file
FROZEN header. CHANGELOG promoted to
[`[0.1.0-alpha.0]`](../CHANGELOG.md). ADR-0020 §5 R8 row closed.

**The refactor is closed.** v1alpha2 growth begins against
the post-R6.6 taxonomy without further structural restructuring.

## §4 — v1alpha2 trajectory (post-R8.1, in progress)

The microservices refactor closed at R8.1; v1alpha2 begins
with R9 (architectural foundation, in progress) and continues
through Waves 1 – 10. The Wave plan is **forecast, not
commitment** — calendar ranges are rough; actual progress
depends on review depth, authoring pace, and discovery during
execution. The total v1alpha2 horizon is **roughly 18 – 30
months** from refactor close, depending on Wave-by-Wave
evidence.

### §4.0 — Phase R9: v1alpha2 architectural foundation (in progress)

R9 is the **documentation-only phase** that crystallises every
post-refactor architectural commitment into canonical
artefacts before any v1alpha2 implementation PR opens. Eight
sequential PRs (R9.0 → R9.8); see
[`v1alpha2-audit.md`](v1alpha2-audit.md) for per-PR detail.

| PR | Scope | Status |
|----|-------|--------|
| R9.0 | CONTRIBUTING.md "v1alpha2 process rigor matrix" | shipped |
| R9.1 | ADR-0036 + ADR-0037 (foundation pair) | shipped |
| R9.2 | ADR-0023 §14 amendment + ADR-0039 (MongoDB tier) | shipped |
| R9.3 | ADR-0031 (observability) + ADR-0032 (mTLS) | shipped |
| R9.4 | ADR-0033 + ADR-0034 + ADR-0035 + ADR-0038 (subsystem ADRs) | shipped |
| R9.5 | 7 new architecture docs at `docs/architecture/` | shipped |
| R9.6 | Forward-reference subsections in 13 existing arch docs | shipped |
| R9.7 | Roadmap rewrite (this PR) | in progress |
| R9.8 | `v1alpha2-audit.md` + CHANGELOG closing summary | pending |

After R9.8 closes, every v1alpha2 implementation PR has firm
architectural backing. **Wave 1 opens immediately after R9.8
merges.**

### §4.1 — Wave 1: Production hardening foundation (~4 – 8 weeks)

Five small focused PRs, parallelizable:

- **W1.1 — Tier 0 cadence** *(shipped in R9.0)*
- **W1.2 — Auto-trigger publish on tag push.** R6.5.3 §4.4
  deferred trigger materialised — `git tag v<x.y.z>` triggers
  the existing publish workflow without manual `gh workflow
  run`. Reference:
  [`docs/architecture/releases.md`](architecture/releases.md)
  v1alpha2 forward-look + R6.5.3 §4.4.
- **W1.3 — Image vulnerability scanning** (Trivy in CI). Every
  built image scans before publish; severity thresholds
  configurable per image; HIGH / CRITICAL findings fail the
  build. Standalone workflow added per
  [`docs/architecture/container-images.md`](architecture/container-images.md)
  v1alpha2 forward-look.
- **W1.4 — Image signing** (cosign keyless via GitHub OIDC).
  Every published image signed with cosign keyless attestation;
  verification documented for downstream consumers.
- **W1.5 — CRD upgrade procedure docs** ([ADR-0030](adr/0030-helm-chart-structure.md)
  §8 amendment in-place). Documents the operator's CRD
  upgrade path (v1alpha2 → vNext); Helm `pre-upgrade` hook
  pattern.

Each is **single architectural decision** scope under the
v1alpha2 process rigor matrix
([CONTRIBUTING.md](../CONTRIBUTING.md) "Pull request
expectations"). No master phase prompt; small focused PRs.

### §4.2 — Wave 2: Multi-arch unblocks (~6 – 12 weeks)

Three PRs, parallelizable. Per
[ADR-0018 §5 R6.5.3 update](adr/0018-devcontainer-and-engine-image.md):

- **W2.1 — Engine multi-arch** (Rust musl cross-compile to
  aarch64). Unblocks `linux/arm64` for the `engine` image.
- **W2.2 — SeleniumBase multi-arch** (Chromium-on-arm64
  decision + PR). Likely a small ADR amendment to ADR-0018
  first documenting the Chrome → Chromium runtime swap or the
  arm64 Chrome availability path.
- **W2.3 — curl-impersonate multi-arch** (build-from-source
  decision + PR). Either runtime base image with arm64
  curl-impersonate variants or build-from-source path.

Each is **single architectural decision** scope. After Wave 2
closes, all five published images ship `linux/amd64 +
linux/arm64` manifest lists.

### §4.3 — Wave 3: Foundational hardening (~10 – 16 weeks)

Sequential ADR-then-implementation PRs landing the cross-
cutting frameworks ADR-0031 and ADR-0032 commit:

- **W3.1 — ADR-0031 first observability PR.** Engine first
  OTel SDK integration; trace context propagation through
  DSL → driver call; Prometheus `/metrics` on engine + operator
  (the two services already deployed in v1alpha1); chart's
  `opentelemetry-collector` subchart reference.
- **W3.2 — ADR-0031 second PR.** Three adapters extend per-
  language reload plumbing; Prometheus `/metrics` on adapters;
  trace topology per ADR-0031 §4.2 reproducible end-to-end in
  the production-smoke gate.
- **W3.3 — ADR-0032 first auth PR.** Operator ↔ engine mTLS;
  chart's `_helpers.tpl` certificate template lands; chart's
  `cert-manager.enabled` flag (default off); per-language reload
  plumbing in `sdks/<lang>/common/`.
- **W3.4 — ADR-0032 second auth PR.** Engine ↔ adapter mTLS;
  three adapters extend the per-language reload plumbing.

Each is **transformational scope** (master phase prompt;
multi-cluster commits; exhaustive acceptance criteria) per the
process rigor matrix.

### §4.4 — Wave 4: User pilot (~4 – 6 weeks calendar, parallel)

Parallel to engineering. **Output**: structured per-layer
questionnaire informing Wave 5+ priorities. The pilot
consults real-deployment users (the maintainer dogfoods first
per framework v1 D4) on the seven-layer platform-vs-driver
split — which catalogue services are most needed first?
Which are nice-to-have? Which can defer to v1beta1?

The pilot's quantitative companions are the four quality
metrics ADR-0031 §8 commits — extraction completeness,
schema-validation pass rate, per-(target, driver) success
ratio, dedup collision ratio. Wave 9 lands the engine-side
emission paths; Wave 4 collects qualitative feedback against
the metric shape.

Pilot output may **reorder Waves 5 – 10** based on real
demand. The Wave plan's order is not pre-decided.

### §4.5 — Wave 5: First infra-services + engine refactor (~16 – 20 weeks)

The first **transformational PR sequence** — proxy-broker +
captcha-solver + the engine orchestrator scaffolding land
together:

- **W5.1 — `proxy-broker`** (Go) — slot 1 per
  [ADR-0036 §3.1](adr/0036-microservices-catalog-expansion.md);
  Redis backend per
  [ADR-0039 §3.1](adr/0039-mongodb-third-storage-tier.md).
  First inhabitant of `infra-services/`; the canonical service
  shape ([`docs/architecture/service-shape.md`](architecture/service-shape.md))
  lands as a reusable template. At least two providers wired
  per ADR-0028 §5 admission gate (BrightData + one other).
- **W5.2 — `captcha-solver`** (Go) — slot 2 per ADR-0036 §3.1;
  Postgres backend per ADR-0039 §3.2 (financial-record);
  follows the canonical shape established by proxy-broker.
- **W5.3 — Engine refactor per ADR-0037.** Engine consumes
  proxy-broker + captcha-solver as services; orchestrator
  pattern scaffolding lands (caching scaffold §4.2, circuit-
  breaker scaffold §5.3 per
  [ADR-0037](adr/0037-engine-as-orchestrator.md)). Old in-engine
  proxy / CAPTCHA code paths deleted in the same PR.

Each of W5.1 / W5.2 / W5.3 is a transformational PR. Multiple
master phase prompts over 4 – 5 months.

### §4.6 — Wave 6: Schema + Input management (~12 – 16 weeks)

Two **foundation services** whose contracts every subsequent
service consumes:

- **W6.1 — `schema-registry`** (Go, Mongo per ADR-0039 §3.9) —
  slot 9. ADR-0034 materialisation; DSL `schema:` block parsed
  by engine; operator admission validates schema refs.
- **W6.2 — `input-broker`** (Go, Mongo per ADR-0039 §3.12) —
  slot 12. ADR-0033 materialisation; ScrapeBatch CRD added at
  `spectre.io/v1alpha2`; operator gains ScrapeBatch reconciler.
- **W6.3 — Helm chart adds Mongo subchart.** Bitnami `mongodb`
  pinned per ADR-0023 §14.1 + ADR-0030 pinning policy. From
  Wave 6 close, MongoDB is a **required** tier per ADR-0023
  §14.2.

Wave 6 grows the chart, Compose stack, and library matrix
operationally — first time operators encounter the four-tier
stateful posture (Postgres + Redis + Kafka + MongoDB).

### §4.7 — Wave 7: Acquisition completion + DSL primitives (~10 – 14 weeks)

Five PRs across two concerns. Acquisition layer completion:

- **W7.1 — `rate-limit-broker`** (Go, Redis per ADR-0039 §3.4)
  — slot 4.
- **W7.2 — `fingerprint-broker`** (Rust, Mongo + Redis per
  ADR-0039 §3.3) — slot 3. First Rust catalog service; first
  hybrid-backend service.

Plus three DSL workflow primitives (engine-internal evolution
per ADR-0035 §4):

- **W7.3 — DSL pagination primitive.**
- **W7.4 — DSL conditional primitive.**
- **W7.5 — DSL multi-step navigation primitive.**

DSL primitives are **engine-internal** — the Driver Protocol
freeze ([ADR-0001](adr/0001-driver-protocol-as-architectural-primitive.md))
is preserved; the engine's parser expands primitives into
v1alpha1-shaped Driver Protocol calls. See
[`docs/architecture/dsl-evolution.md`](architecture/dsl-evolution.md)
for the operational detail.

### §4.8 — Wave 8: Operational + scheduling (~10 – 14 weeks)

Three operational-layer services rounding out the v1alpha2
catalogue's day-to-day surface:

- **W8.1 — `session-store`** (Go, Mongo per ADR-0039 §3.5) —
  slot 5.
- **W8.2 — `secret-broker`** (Go, Postgres or Vault per
  ADR-0039 §3.13) — slot 13. Reframes ADR-0028 §6's rejected
  `secrets-broker` per ADR-0036 §1.2.
- **W8.3 — `scheduler`** (Go, Postgres per ADR-0039 §3.6) —
  slot 6. Reframes ADR-0028 §6's rejected `scheduler` per
  ADR-0036 §1.2; operator gains schedule-driven CRD creation.

### §4.9 — Wave 9: Quality + observability concretes (~8 – 12 weeks)

Quality / observability services close the catalogue's
quality layer:

- **W9.1 — `cost-tracker`** (Go, Postgres per ADR-0039 §3.7) —
  slot 7. ADR-0038 materialisation; rollup webhooks fire for
  registered tenants.
- **W9.2 — `audit-log`** (Go, Mongo time-series per ADR-0039
  §3.8) — slot 8. Distinct from log aggregation per ADR-0036
  §1.2's clarification.
- **W9.3 — ADR-0031 quality metrics implementation.** The four
  quality metrics ADR-0031 §8 commits land at the engine's
  per-row-emission boundary.

After Wave 9 closes, the platform has **per-job cost
attribution + per-tenant rollups + per-job audit trail +
quality metrics** — the production-grade observability surface
ADR-0031 + ADR-0038 codify.

### §4.10 — Wave 10: Driver abstraction (the v1alpha2 culmination, ~12 – 16 weeks)

The **most architecturally consequential Wave** — driver
routing intelligence lands and v1alpha2 → v1beta1 DSL
transition begins:

- **W10.1 — `driver-router` decision.** A new ADR (numbered
  after R9.4) records the maintainer's resolution of
  [ADR-0035 §6](adr/0035-dsl-evolution-driver-abstraction.md)'s
  service-vs-engine-module decision based on aggregated
  v1alpha2 production-smoke + tenant-pilot evidence
  (latency budgets, routing-policy evolution rate, historical-
  success volume).
- **W10.2 — `driver-router` implementation.** Per the §6
  decision — separate service at slot 14 OR engine module at
  `engines/engine/src/router/`. Capability matching, cost-aware
  selection, fallback chains.
- **W10.3 — `enricher`** (language TBD per ADR-0036 §3.4;
  Mongo + Redis per ADR-0039 §3.10) — slot 10. First
  Python-or-Rust language decision since the catalog opened.
- **W10.4 — `dedup-service`** (Go, Redis per ADR-0039 §3.11) —
  slot 11.

Wave 10 closes the v1alpha2 implementation horizon. The full
catalogue (14 of 15 services) is shipped; template-service
(slot 15) defers to v1beta1.

### §4.11 — Wave 11+: v1beta1 territory (deferred)

After Wave 10 closes, v1beta1 work begins. Deferred items:

- `template-service` (slot 15) materialisation
- Multi-tenancy enforcement (per-tenant isolation across
  storage tiers + RBAC)
- Compliance work (PII redaction, GDPR-shaped per-field
  access control, audit retention policies)
- Anti-detection learning (per-target playbooks; ML-driven
  fingerprint rotation; behaviour-pattern variation)
- Full driver-routing intent-DSL per
  [`docs/architecture/dsl-evolution.md`](architecture/dsl-evolution.md)
  §3.3; v1alpha2 → v1beta1 DSL transition completes
- Mongo as L0 sink (ADR-0024 amendment) + Mongo as Bronze
  storage (ADR-0029 amendment) per
  [ADR-0039 §7](adr/0039-mongodb-third-storage-tier.md)
- Webhook authentication (HMAC / bearer / mTLS-for-receivers)
  per [ADR-0032 §7](adr/0032-service-to-service-mtls.md)
- Quota enforcement consuming cost-tracker data per
  [ADR-0038 §7.4](adr/0038-cost-tracking-attribution.md)
- Cost forecasting / anomaly detection
- Multi-region deployment topology
- Continuous profiling + eBPF observability
- OTel logs SDK adoption (Rust maturity dependent)
- External SDK publishing (crates.io / PyPI / npm / Go module
  proxy)
- Custom user-defined transforms (WASM modules)
- Visual DSL editors / kubectl plugins / GraphQL gateway

The v1beta1 work proceeds against v1alpha2's evidence base
— Wave-by-Wave production data informs which v1beta1 items
land first.

### §4.12 — v1alpha2 ceiling

After Wave 10 completes, the v1alpha2 surface is:

- **8 substantial catalog services shipped** (proxy-broker,
  captcha-solver, schema-registry, input-broker,
  rate-limit-broker, fingerprint-broker, session-store,
  scheduler) + 5 quality / output / driver-routing services
  (cost-tracker, audit-log, enricher, dedup-service,
  driver-router) — **13 of 15 catalog services
  materialised** (template-service deferred to v1beta1)
- **DSL workflow primitives** (pagination, conditionals,
  multi-step nav, schemas, transforms) in v1alpha2 DSL
- **Observability + auth + cost tracking** foundational —
  every per-step service call traceable; per-job cost
  attributable; service-to-service mTLS flag-on default for
  multi-tenant deployments
- **3-tier persistent storage** (Postgres + Redis + Mongo)
  + Kafka streaming operational
- **Engine refactored to orchestrator pattern** — engine is
  a conductor, not a god object
- **Multi-arch image manifests** for all 13+ services per
  Wave 2's unblock work

At v1alpha2 ceiling, the platform serves **production
multi-tenant scraping deployments** with complete cost
visibility, rigorous output schemas, observability across
the per-step service-orchestration fan-out, and the
performance characteristics of co-located services hitting
the ~5 ms / step latency budget per ADR-0037 §4.6.

v1beta1 begins next, anchored by the deferred items in
§4.11.

## §5 — Stable protocol + DSL evolution

The protocol promotion path is `v1alpha1 → v1beta1 → v1`.
The Driver Protocol stays **frozen** through every v1alpha2
PR per
[ADR-0001](adr/0001-driver-protocol-as-architectural-primitive.md);
v1alpha2 evolution is **engine-internal** (DSL primitives
expand into v1alpha1-shaped Driver Protocol calls).

Promotion gates:

- **`v1alpha1 → v1beta1`** — capability surface stable for ≥6
  months across the three reference adapters; no breaking
  proto changes; conformance suite passes byte-for-byte across
  all three drivers.
- **`v1beta1 → v1`** — three drivers in production use across
  external organisations (the canonical "third-party validation"
  criterion).

The 13/12/6 capability divergence at R6.6-close is the
empirical baseline; a v1 declaration likely renames
`screenshot_full_page` to a more precise name, splits
`js_execution` into pre/post-navigation variants, and adds
capability strings for the proxy and fingerprint surfaces
(consumer-facing manifestations of infra-services slots).

A **driver registry** — a published index of community-
maintained drivers, their declared capabilities, and their
conformance status — ships alongside the v1 declaration.

### §5.1 — DSL evolution path

The DSL evolves along a parallel four-version trajectory per
[ADR-0035 §3](adr/0035-dsl-evolution-driver-abstraction.md)
+ [`docs/architecture/dsl-evolution.md`](architecture/dsl-evolution.md):

| DSL version | Surface | Status |
|---|---|---|
| **v1alpha1** | Driver-RPC-mirrored verbs; flat | Frozen |
| **v1alpha2** | + 5 workflow primitives (pagination, conditional, multi-step nav, schema, transforms); driver-explicit | Wave 6 – 7 |
| **v1beta1** | Intent-declarative; capability hints replace `driver.kind`; driver-router-driven; `driverHint` opt-in | v1beta1 work |
| **v1** | Fully abstract intent (target schema + site + SLA → platform decides execution plan) | Far-future; illustrative |

DSL evolution is **additive** — v1alpha2 ScrapeJobs continue
to work in v1beta1 (superset surface); v1beta1 ScrapeJobs
continue to work in v1. The Driver Protocol freeze decouples
DSL evolution from adapter changes.

## §6 — Beyond v1

Open questions, deliberately unscoped:

- **Browser-side WASM engine** — in-page extraction without an
  external driver. Plausible once the Driver Protocol stabilises
  and a WASM runtime can host the three adapter shapes
  uniformly.
- **Higher-level DSL** — joins, pagination across data layers,
  semantic deduplication. Lives partially in `data-platform/`
  layer-transition DSLs per ADR-0029 once patterns emerge;
  v1beta1's intent-declarative DSL is a step in this direction.
- **Multi-cluster federation** — orchestrating ScrapeFleets
  across geo-distributed kind clusters with per-region proxy
  pools and routing decisions. Probably an `operators/`
  v1alpha2+ evolution rather than a new category.
- **Managed-service offering** — a hosted reference deployment.
  Out of scope for the open-source project.
- **AI-driven extraction** — vision models, LLM-based
  intent-following, autonomous-agent scrape composition. The
  enricher service (slot 10) is the natural platform-side
  integration point if v1beta1 evidence supports.
- **Streaming-engine variant** — continuous-collection
  deployments per [ADR-0026 §3.2](adr/0026-platform-taxonomy.md)
  reservation; second engine in `engines/` plural.

These move into per-Wave trajectory sections only after they
are concretely scoped against admission criteria.

## §7 — How phases get scheduled

The v1alpha2 process rigor matrix
([CONTRIBUTING.md](../CONTRIBUTING.md) "Pull request
expectations" — committed in R9.0) governs how each Wave's
PRs are reviewed:

- **Transformational change** (master phase prompt + new ADR
  + multi-cluster commits + exhaustive acceptance criteria) —
  W5.1 / W5.2 / W5.3 (engine refactor + first services); W6.1
  / W6.2 (foundation services); W10.1 / W10.2 (driver-router).
- **Single architectural decision** (no master prompt; new
  ADR if warranted; single commit OK; focused acceptance
  criteria) — W1.2 / W1.3 / W1.4 / W1.5; W2.1 / W2.2 / W2.3;
  most W7+ services.
- **Incremental change** (no ADR; single commit; CHANGELOG
  entry; brief acceptance) — DSL primitive PRs (W7.3 / W7.4
  / W7.5); follow-up bug fixes; documentation amendments.

[ADR-0026](adr/0026-platform-taxonomy.md) §6's category-
admission criteria still apply for new top-level categories;
[ADR-0036 §2](adr/0036-microservices-catalog-expansion.md)'s
six gates (A – F) govern service-vs-library decisions for new
infra-service candidates beyond the 15 ADR-0036 catalogues.

A roadmap entry — a forecast, not a commitment — follows once
the admission criteria look reachable. The roadmap is
rewritten at every phase boundary that adds or completes
work; the authoritative source for a future phase's intent is
its phase prompt, not this document.

## §8 — How to influence the roadmap

- **Open a feature request on GitHub** for concrete, scoped work.
- **Draft an ADR** for non-trivial additions, especially anything
  that crosses category boundaries or proposes a new catalog
  service candidate.
- **Engage on existing issues.** Maintainer attention follows
  community attention; an issue with five thoughtful comments
  outweighs five issues with one comment each.
- **Participate in Wave 4 user pilot** when it opens
  ([§4.4](#§44--wave-4-user-pilot-4-6-weeks-calendar-parallel))
  — the pilot's per-layer questionnaire directly informs
  Wave 5+ priorities.

The roadmap reflects current intent. It does not commit to a
schedule, a particular PR shape, or a particular set of
milestones. Subscribe to the CHANGELOG (and the `Unreleased`
section in particular) for granular progress.

## §9 — v1alpha2 risks and mitigations

Six risks the v1alpha2 plan acknowledges; each has a
mitigation strategy committed in the relevant ADR.

### §9.1 — Per-step orchestration latency cost

**Risk**: 9+ service calls per engine step at 5 ms each adds
45+ ms per step; a 100-step job becomes 4.5 seconds slower
than v1alpha1's monolithic engine.

**Mitigation**: Five strategies per
[ADR-0037 §4](adr/0037-engine-as-orchestrator.md) hold
typical per-step overhead in the **~5 ms range** —
batching, per-job/per-session caching, async-where-correct
emissions, tunable per-deployment service-disable flags,
service co-location via chart `nodeAffinity`.

**Acceptance**: production-smoke gate (R7.2 extended for
Wave 5) measures actual overhead; deviation >50% over budget
triggers an ADR amendment per ADR-0037 §7's signal-of-needed-
revision criteria.

### §9.2 — Catalog scope sprawl

**Risk**: 15 services × 4 languages × 7 operational patterns
= easy to drown in process. The platform collapses under its
own coordination overhead at ~5 services if patterns aren't
disciplined.

**Mitigation**: The canonical service shape per
[ADR-0036 §5](adr/0036-microservices-catalog-expansion.md) +
[`docs/architecture/service-shape.md`](architecture/service-shape.md)
14-step onboarding checklist makes adding a service mostly
*filling in templated blanks*. Per-language SDK admission
gate per [ADR-0027 §3.1](adr/0027-sdk-strategy.md) prevents
SDK sprawl. Per-service CHANGELOG / ADR trees prevent the
platform-wide ADR set from bloating.

**Acceptance**: catalogue stays at 15 services across
v1alpha2; new candidates require an ADR amendment to
ADR-0036 demonstrating §2's six gates A – F.

### §9.3 — MongoDB operational cost

**Risk**: Adding Mongo as a third storage tier is real
operational work — Helm subchart, Compose entry, backup/DR,
monitoring, library matrix, indexing discipline, cognitive
load.

**Mitigation**: Level 2 (Moderate) adoption committed per
[ADR-0039 §5.4](adr/0039-mongodb-third-storage-tier.md) — 7
services on Mongo + 2 hybrid earn the operational addition.
Level 1 (Conservative) underuses; Level 3 (Aggressive)
over-commits before evidence supports. Six anti-patterns
per ADR-0039 §4 prevent overuse.

**Acceptance**: every Wave 6+ Mongo-backed service follows
the canonical indexing discipline (ADR-0039 §4.6); slow
queries monitored in production; index size growth tracked.

### §9.4 — Driver-router decision premature locking

**Risk**: Deciding the driver-router service-vs-engine-module
shape now without v1alpha2 production evidence locks in the
wrong default. Latency-sensitive workloads suffer with
service-shape; flexibility-sensitive workloads suffer with
module-shape.

**Mitigation**: Per
[ADR-0035 §6](adr/0035-dsl-evolution-driver-abstraction.md),
the decision **defers to Wave 10** when v1alpha2 production
data exists (latency budgets per Wave 5+ smoke; routing-
policy evolution rate per Waves 5 – 9; historical-success
volume per Waves 5 – 9). Both options surface in ADR-0035
§6.1 / §6.2 with full trade-off analysis until then.

**Acceptance**: Wave 10 opens with a new ADR (numbered after
R9.4) recording the maintainer's resolution; both options
remain reserved in ADR-0036 until then.

### §9.5 — User pilot priority misalignment

**Risk**: Wave 5 – 10 ordering reflects framework v3's
analytical priorities (acquisition first; quality/observability
later). Real users may prioritise differently — schema
validation may be more urgent than CAPTCHA solving for a
particular tenant.

**Mitigation**: Wave 4 user pilot ([§4.4](#§44--wave-4-user-pilot-4-6-weeks-calendar-parallel))
runs **parallel** to Wave 1 – 3 engineering. Pilot output
may reorder Waves 5 – 10. The Wave plan's order is
**forecast, not commitment** per the roadmap header.

**Acceptance**: per-layer questionnaire collected in Wave 4;
Wave 5 priorities revised based on responses; reordering
documented as a roadmap update.

### §9.6 — DSL evolution drift

**Risk**: v1alpha2's five DSL primitives + v1beta1's intent-
declarative surface evolve faster than user adoption can
keep up; existing ScrapeJobs become deprecated faster than
tenants migrate.

**Mitigation**: DSL evolution is **additive** per
[ADR-0035 §3](adr/0035-dsl-evolution-driver-abstraction.md)
+ [`docs/architecture/dsl-evolution.md`](architecture/dsl-evolution.md)
§6 — v1alpha1 ScrapeJobs continue working in v1alpha2;
v1alpha2 ScrapeJobs continue working in v1beta1 (superset).
No silent semantic drift; `driver.kind` becomes
`driverHint` (rename) at the v1alpha2 → v1beta1 boundary.

**Acceptance**: every v1alpha2 ScrapeJob in production-smoke
continues to pass against the v1beta1 engine when v1beta1
opens; conformance test gate enforces the additive
invariant.
