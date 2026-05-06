# v1alpha2 audit

> Per-PR / per-cluster historical record of the v1alpha2 phase.
> Companion to the frozen
> [refactor-audit.md](refactor-audit.md) which records
> R1 → R8.1 (the microservices refactor closed at
> `0.1.0-alpha.0`, 2026-05-03).
>
> **This document is forward-tracking** — unlike
> `refactor-audit.md` (frozen), this file is updated per-PR
> by future v1alpha2 work. The pattern mirrors the
> refactor-audit precedent: high-level Phase table at top;
> per-Phase per-PR detail entries below.
>
> Per-release notes live in [`../CHANGELOG.md`](../CHANGELOG.md).
> Architectural decisions live in the [ADRs](adr/).
> Process and contribution rules live in
> [`../CONTRIBUTING.md`](../CONTRIBUTING.md). The Wave 1 – 12
> trajectory lives in [`roadmap.md`](roadmap.md) §4.

---

## Phase R9 — v1alpha2 architectural foundation (CLOSED — 2026-05-06)

Phase R9 was the **documentation-only phase** that
crystallised every post-refactor architectural commitment
into canonical artefacts before any v1alpha2 implementation
PR opened. Nine sequential PRs (R9.0 → R9.8) over two
calendar days. After R9.8 closes, every v1alpha2
implementation PR (Wave 1 onwards per `roadmap.md` §4) has
firm architectural backing.

| PR | Status | Summary | ADRs / docs touched |
|----|--------|---------|---------------------|
| R9.0 | CLOSED | CONTRIBUTING.md "v1alpha2 process rigor matrix" subsection (4-row matrix mapping PR scale to documentation overhead) | (none — process doc) |
| R9.1 | CLOSED | ADR-0036 (microservices catalog expansion + canonical service shape) + ADR-0037 (engine as orchestrator of platform services) | ADR-0036, ADR-0037 |
| R9.2 | CLOSED | ADR-0023 §14 amendment in-place (per R6.5.4 precedent — only permitted in-place edit to ADRs 0001 – 0030 in Phase R9) + ADR-0039 (MongoDB as third storage tier) | ADR-0023 §14, ADR-0039 |
| R9.3 | CLOSED | ADR-0031 (observability framework — OpenTelemetry + OTLP + Prometheus + structured logs) + ADR-0032 (service-to-service mTLS via cert-manager) | ADR-0031, ADR-0032 |
| R9.4 | CLOSED | ADR-0033 + ADR-0034 + ADR-0035 + ADR-0038 (four subsystem ADRs in one PR; mirrors R6.6's 4-ADR pattern) | ADR-0033, ADR-0034, ADR-0035, ADR-0038 |
| R9.5 | CLOSED | 7 new architecture docs at `docs/architecture/` (operational companions to R9.1 – R9.4 ADRs; ADRs record decisions, architecture docs record the shape that follows) | 7 docs created |
| R9.6 | CLOSED | Forward-reference subsections in 13 existing architecture docs (body content preserved verbatim; only additions) | 13 doc updates |
| R9.7 | CLOSED | docs/roadmap.md substantial rewrite (§4 expanded from 4 placeholder subsections to 13 concrete Wave subsections; §5 / §6 / §7 refined; new §9 risks section) | 1 doc rewrite |
| R9.8 | CLOSED (this PR) | Phase R9 audit (this file) + CHANGELOG closing summary + README.md Roadmap section update | 3 docs touched |

**Phase R9 totals**: 9 PRs · 9 new ADRs (0031 – 0039) + 1
in-place ADR amendment (ADR-0023 §14) · 7 new architecture
docs · 13 architecture-doc forward-reference subsections ·
1 roadmap rewrite · 1 audit (this file) · 0 source-code
changes.

After R9.8 closes, **Wave 1 (production hardening
foundation)** opens — see [`roadmap.md`](roadmap.md) §4.1
for the detailed plan.

---

## Per-PR detail (Phase R9)

### R9.0 — CONTRIBUTING.md v1alpha2 process rigor matrix

**Merged**: 2026-05-05 (PR #89, `a6bb093`).

**Scope**: a "v1alpha2 process rigor matrix" subsection
appended to CONTRIBUTING.md's "Pull request expectations"
section. The 4-row matrix maps PR scale to documentation
overhead:

- **Transformational change** → master phase prompt + new
  ADR + multi-cluster commits + exhaustive acceptance
  criteria
- **Single architectural decision** → execution checklist +
  ADR if warranted + single-commit OK + focused acceptance
- **Incremental change** → no ADR + single commit +
  CHANGELOG entry
- **Doc-only change** → no formal scaffolding required

**Lines**: +27 (CONTRIBUTING.md +19; CHANGELOG.md +8).

**Why first**: governs the cadence of every subsequent
v1alpha2 PR (R9.1+ + Wave 1+). The matrix lands before any
substantive ADR work so reviewers know which rigor level
applies per PR.

### R9.1 — ADR-0036 + ADR-0037 (architectural foundation pair)

**Merged**: 2026-05-05 (PR #90, `2622471`).

**Scope**: two foundation ADRs every subsequent v1alpha2
ADR (R9.2 – R9.4) and every Wave 5+ build PR depends on:

- **ADR-0036** (1196 lines) — microservices catalog
  expansion (15 services) + canonical service shape;
  six gates A–F generalising ADR-0028 §5.2's two-provider
  rule; selectively supersedes ADR-0028 §6's rejections of
  `secret-broker` and `scheduler` with gate-based reframes
  and clarifies `audit-log` vs log-aggregation
- **ADR-0037** (716 lines) — engine as orchestrator of
  platform services; v1alpha1 monolith → v1alpha2 conductor;
  per-step service-orchestration ASCII diagram + pseudocode;
  five latency-cost mitigation strategies; per-service
  degradation modes; circuit-breaker scaffolding

**Three commits** (Cluster A: ADR-0036; Cluster B: ADR-0037;
Cluster C: CHANGELOG).

**Lines**: 1196 + 716 + 32 = 1944 insertions.

**Surface points**: ADR-0036 came in 96 lines over the
1100-line surface threshold per the user-set surface
protocol; trimmed via option C (aggressive trim). The
ADR-0028 §6 selective supersession was the architectural
surprise during authoring — handled via explicit §1.2
table rather than in-place edit to ADR-0028 (master prompt
§16 immutability constraint preserves ADR-0028 unchanged).

### R9.2 — ADR-0023 §14 amendment + ADR-0039 (MongoDB tier)

**Merged**: 2026-05-05 (PR #91, `9300b96`).

**Scope**: MongoDB as third storage tier alongside Postgres
+ Redis. Two artefacts:

- **ADR-0023 §14 amendment in-place** (+127 lines) — the
  **only permitted in-place edit to ADRs 0001 – 0030 in
  Phase R9** per master prompt §16; precedent set by
  ADR-0018's R6.3 / R6.5.3 / R6.5.4 update notes + ADR-0007's
  R6.6 evolution notes. Section is §14, not §11 (master
  prompt's assumption was that ADR-0023 ended at §10; actual
  file ended at §13 + a `## More Information` section). §14.1
  – §14.5 substructure preserved per master prompt §7.
- **ADR-0039** (871 lines) — rigorous backend specialisation
  across the ADR-0036 catalog. §2 commits backend selection
  criteria (5); §3 evaluates each of 15 services with
  per-service evaluation matching ADR-0036 §3.9 byte-for-byte;
  §4 commits 6 anti-patterns; §5 commits Level 2 (Moderate)
  adoption (7 services on Mongo + 2 hybrid); §7 defers
  ADR-0024 / ADR-0029 amendments to v1beta1.

**Three commits** (Cluster A: ADR-0023 §14; Cluster B:
ADR-0039; Cluster C: CHANGELOG).

**Lines**: 127 + 871 + 42 = 1040 insertions.

**Verification**: ADR-0023 §1 – §13 + `## More Information` +
R8.1 evolution note byte-identical to pre-amendment baseline
(verified via diff against origin/main).

### R9.3 — ADR-0031 + ADR-0032 (cross-cutting frameworks)

**Merged**: 2026-05-05 (PR #92, `a73fb4b`).

**Scope**: two cross-cutting framework ADRs every service
in the catalog depends on for visibility (observability) and
trust (mTLS):

- **ADR-0031** (764 lines) — OpenTelemetry as umbrella
  standard for metrics + traces + structured logs. OTLP/gRPC
  primary + OTLP/HTTP fallback to deployment-local
  collector; Prometheus `/metrics` on uniform sidecar port
  9090 as resilience scrape path; structured JSON logs to
  stdout (OTel logs SDK adoption deferred to v1beta1 pending
  Rust maturity); first-class correlation IDs propagated
  end-to-end. Makes ADR-0036 §5.4 normative.
- **ADR-0032** (651 lines) — mTLS via cert-manager.
  Per-service certificates with 90-day validity / 30-day
  renewal / ECDSA P-256 preferred. Chart's
  `cert-manager.enabled` flag default-off preserves
  v1alpha1 plaintext posture; flag-on enables mTLS uniformly.
  Webhook auth deferred. Makes ADR-0036 §5.5 normative.

**Three commits**. **Lines**: 764 + 651 + 58 = 1473 insertions.

### R9.4 — ADR-0033 + ADR-0034 + ADR-0035 + ADR-0038 (four subsystem ADRs)

**Merged**: 2026-05-05 (PR #93, `9dfdd8b`).

**Scope**: the largest single PR in the R9 arc — four
subsystem ADRs in one PR mirroring R6.6's 4-ADR pattern:

- **ADR-0033** (698 lines) — input management subsystem
  (input-broker service + ScrapeBatch CRD; bulk URL
  ingestion at scraping volumes; 5 input source variants;
  Mongo backend with documented exception to anti-pattern
  §4.3)
- **ADR-0034** (618 lines) — output schema and validation
  framework (schema-registry service + DSL `schema:` block;
  JSON Schema Draft 2020-12; BACKWARD compatibility default;
  Mongo backend per ADR-0039 §3.9)
- **ADR-0035** (792 lines) — DSL evolution and driver
  abstraction; **the most architecturally substantial**
  R9.4 ADR with the **driver-router service-vs-engine-module
  decision explicitly deferred to Wave 10** with full §6
  trade-off analysis
- **ADR-0038** (727 lines) — cost tracking and per-job
  attribution (cost-tracker service; per-job ledger;
  per-tenant rollups; billing integration hooks; Postgres
  backend)

**Five commits** (Clusters A–D + CHANGELOG).

**Lines**: 2835 + 76 = 2911 insertions.

**Trim**: aggressive trim via option D from a 2928-line
baseline; landed at 2835 (saved 93 lines; estimate of
~340-line trim was optimistic — the per-service §3
evaluation in ADR-0039 / per-emission-point detail in
ADR-0038 / per-primitive examples in ADR-0035 §4 were
denser than projected).

### R9.5 — 7 new architecture docs

**Merged**: 2026-05-06 (PR #94, `889e35c`).

**Scope**: seven new architecture docs at
`docs/architecture/` — operational companions to the R9.1 –
R9.4 ADRs. Principle: ADRs record decisions ("why");
architecture docs record the shape that follows ("how"). No
content duplication.

- `platform-architecture.md` (257) — umbrella entry-point
- `service-shape.md` (250) — canonical service shape +
  14-step contributor onboarding checklist
- `dsl-evolution.md` (345) — full DSL trajectory with
  per-version YAML examples + migration paths
- `storage-tiers.md` (229) — three-tier backend matrix
- `engine-orchestrator.md` (349) — engine v1alpha2 shape +
  ASCII fan-out diagram + pseudocode
- `observability.md` (338) — OpenTelemetry operational guide
- `service-catalog.md` (289) — 15-service status reference
  (maintained per service materialisation)

**Eight commits** (one per arch doc + CHANGELOG).

**Lines**: 2057 + 37 = 2094 insertions.

**Notable**: `service-catalog.md` is the first v1alpha2
architecture doc with **PR-side update obligations** — its
§1 status table updates per service materialisation
(`planned → authoring → shipped`).

### R9.6 — Forward-reference subsections in 13 existing architecture docs

**Merged**: 2026-05-06 (PR #95, `a81a229`).

**Scope**: each of the 13 v1alpha1 architecture docs gains
a "v1alpha2 forward-look" subsection at the file tail.
Body content of all 13 existing docs preserved **verbatim**
(verified via diff against origin/main — 0 deletions,
only additions).

Three clusters per master prompt §11 grouping:

- **Cluster A** — Core docs (4 files): overview / engine /
  control-plane / driver-protocol
- **Cluster B** — Stateful + sinks (4 files): postgres /
  redis / kafka / output-sinks
- **Cluster C** — Deployment (5 files): container-images /
  helm-chart / production-smoke / releases /
  development-environment

**Four commits** (Clusters A–C + CHANGELOG).

**Lines**: 565 + 16 = 581 insertions, 0 deletions.

**Style**: subsections match each doc's existing style —
formal `## §N — Title` for overview / helm-chart /
production-smoke; informal `## v1alpha2 forward-look` for
the rest. Each subsection opens with a R9.6 evolution-note
blockquote following the R8.1 + R9.2 evolution-note pattern.

### R9.7 — docs/roadmap.md substantial rewrite

**Merged**: 2026-05-06 (PR #96, `d0dad0e`).

**Scope**: substantial rewrite of `docs/roadmap.md`. The
previous 280-line roadmap had a 4-subsection §4 at
placeholder level (sdks / infra-services / data-platform /
shared-libs); the new 680-line roadmap has a **13-subsection
§4 with concrete Wave 1 – 10 plan + new §9 risks section**.

Single atomic cluster (the rewrite is coherent as a unit).

**Roadmap structure changes**:

- §1 / §2 — updated for R7.x close + R8.1 close + R9 status
- §4 — expanded from 4 placeholder subsections to 13
  concrete subsections (§4.0 R9 status; §4.1–§4.10 Waves
  1–10; §4.11 v1beta1 deferrals; §4.12 v1alpha2 ceiling)
- §5 — extended with §5.1 four-version DSL evolution path
- §6 — refined per framework v3/v4 horizon
- §7 — references the v1alpha2 process rigor matrix from
  R9.0 + ADR-0036's six gates
- §8 — preserved + Wave 4 user pilot participation
- §9 NEW — six risks with per-risk mitigations + acceptance
  criteria

**Lines**: +626 / −208 = +418 net (from 280 → 680 lines).

### R9.8 — Phase R9 closure (this PR)

**Merged**: 2026-05-06 (this PR).

**Scope**: closes Phase R9. Three artefacts:

- **`docs/v1alpha2-audit.md`** — created (this file). Mirrors
  `refactor-audit.md` pattern but forward-tracking. Phase R9
  high-level table + per-PR detail entries for R9.0 – R9.8.
- **CHANGELOG closing summary** — `[Unreleased]` gains a
  "Phase R9 — v1alpha2 architectural foundation
  (completed 2026-05-06)" subsection summarising R9.0 → R9.8
  outputs cumulatively.
- **README.md "Project status" + "Documentation" updates**
  — acknowledges Phase R9 close + references this audit and
  the rewritten roadmap; Documentation list adds
  v1alpha2-audit.md.

**Three clusters**.

After R9.8 merges, the v1alpha2 implementation trajectory
opens — Wave 1 (production hardening foundation; ~4 – 8
weeks) per [`roadmap.md`](roadmap.md) §4.1.

---

## Counts (Phase R9 cumulative)

- **PRs merged**: 9 (R9.0 – R9.8)
- **New ADRs accepted**: 9 (ADR-0031 through ADR-0039)
- **In-place ADR amendments**: 1 (ADR-0023 §14)
- **New architecture docs**: 7 (R9.5)
- **Architecture-doc forward-reference subsections**: 13
  (R9.6)
- **Roadmap rewrites**: 1 (R9.7)
- **Audit document creations**: 1 (this file)
- **Source-code changes**: 0 (Phase R9 is documentation-only
  per master prompt §15.2)
- **CHANGELOG `[Unreleased]` entries**: 9 (one per PR)

## Pattern for future v1alpha2 phases

Future v1alpha2 phases (Wave 1+ build PRs) append rows to
this document following the Phase R9 pattern: high-level
table at the Phase boundary; per-PR detail entries for each
PR within the Phase. Wave assignments per
[`roadmap.md`](roadmap.md) §4 govern Phase boundaries:

- **Wave 1** — Phase R10 (likely; production hardening
  foundation; 5 PRs)
- **Wave 2** — Phase R11 (likely; multi-arch unblocks; 3 PRs)
- **Wave 3** — Phase R12 (likely; foundational hardening;
  4 PRs — first ADR-0031 + ADR-0032 materialisations)
- **Wave 4** — Phase R13 (likely; user pilot; parallel)
- **Wave 5** — Phase R14 (likely; first infra-services +
  engine refactor; 3 transformational PRs)
- **Wave 6+** — subsequent phases per the
  [`roadmap.md`](roadmap.md) §4 sequence

Each Phase opens with a master phase prompt for
transformational PRs (per CONTRIBUTING.md "v1alpha2 process
rigor matrix") and closes with a Phase audit entry here.
