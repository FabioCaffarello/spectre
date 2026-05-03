# Roadmap

This roadmap is a forecast, not a commitment. Dates are absent on
purpose — milestones move with real progress, not a schedule a
prompt forced into existence. The roadmap is organised by
[platform category](architecture/overview.md#§1--platform-taxonomy)
rather than by chronological phase: it answers "what comes next
under each part of the platform" rather than "which PR ships
when". Per-phase execution is tracked in
[`refactoring-status.md`](refactoring-status.md); the historical
audit lives in [`refactor-audit.md`](refactor-audit.md).

> **Last updated:** 2026-05-02 (Phase R7 close — production
> posture. R7.1 shipped the Helm chart packaging at
> `build/helm/spectre/` (ADR-0030); R7.2 shipped the
> production-smoke CI gate that installs the chart into a
> kind cluster and asserts row events arrive at the three
> sinks (kafka, s3, webhook). R8.1 — documentation refresh
> + narrative closing — is the refactor's final PR.)

## §1 — Where we are (post-R6.6)

The microservices runtime ([ADR-0020](adr/0020-microservices-architecture-supersession.md))
shipped end-to-end across Phases R1–R6.5:

- **Transport (R2.1 + R2.2 + R2.3).** UDS retired; gRPC over TCP
  is the only supported transport. Adapters run as long-running
  services with `grpc.health.v1.Health` readiness.
- **Control plane (R3.1 + R3.2).** The operator submits jobs to
  the engine via gRPC `RunJob`; the `ScrapeJob` CRD is
  v1alpha2-only with discriminated-union fields and CEL
  validation. The `SubprocessRunner` and the `spectre run` CLI
  are retired.
- **Stateful services (R4.2 + R4.3 + R4.4).** PostgreSQL persists
  job state; Kafka receives streamed row events; Redis caches
  per-session metadata.
- **Output sinks (R5.1).** S3 and HTTP webhook complete the
  four-sink set (stdout / Kafka / S3 / webhook).
- **Devcontainer + image story (R6.1 + R6.2 + R6.3).** Every
  service is built by `docker-bake.hcl`; the Compose stack runs
  end-to-end inside the devcontainer with operator-as-Compose
  joining a Docker-in-Docker `kind` external network.
- **Quality and hardening (R6.5.1 + R6.5.2 + R6.5.3 + R6.5.4).**
  Stale-references swept; CI matrix `images` and `full-stack`
  gate green on every PR; multi-arch publish to Docker Hub wired
  for the two unblocked images; shared `buf-base` codegen base
  cuts build duplication.
- **Platform Maturation (R6.6).** Four ADRs (0026–0029) commit
  the platform taxonomy, SDK strategy, infra-services catalog,
  and data-platform model. Repository restructure dissolves
  `core/` into `engines/` + `operators/`; four placeholder
  categories (`infra-services/`, `sdks/`, `data-platform/`,
  `shared-libs/`) reserve home for v1alpha2 growth.

The platform is feature-complete for the v1alpha1 surface. The
remaining refactor work is production-readiness (R7.x) and a
narrative-closing documentation pass (R8.1).

## §2 — v1alpha1 production posture (R7.x)

Phase R7 ships v1alpha1 in production-installable shape. Two
phases are committed; details land in their per-phase prompts.

- **[x] R7.1 — Helm chart packaging.** *(complete 2026-05-02 —
  see [ADR-0030](adr/0030-helm-chart-structure.md) and
  [docs/architecture/helm-chart.md](architecture/helm-chart.md).)*
  Ships [`build/helm/spectre/`](../build/helm/spectre/) (location
  per ADR-0026 §3.9 — out-of-band) installing the engine + three
  adapters + control plane + stateful dependencies into a
  cluster. Configuration surface mirrors Compose: per-service
  env vars, Postgres/Redis/Kafka/MinIO connection strings, sink
  targets. The chart consumes the published images from
  [Docker Hub](architecture/releases.md); R7.1 included the
  first real publish at `0.1.0-alpha.0`. Structural CI gate
  (`helm-lint`) added; production smoke deferred to R7.2.
- **[x] R7.2 — Production smoke.** *(complete 2026-05-02 — see
  [docs/architecture/production-smoke.md](architecture/production-smoke.md)
  and `.github/workflows/production-smoke.yml`.)* New
  standalone workflow on three triggers (manual,
  paths-filtered PR, daily 06:00 UTC cron) installs
  [`build/helm/spectre/`](../build/helm/spectre/) into a kind
  cluster, applies the three reference ScrapeJobs (kafka, s3,
  webhook sinks) to `Completed`, and asserts row events arrive
  at each sink boundary. Mock webhook receiver
  (`mendhak/http-https-echo:31`, digest-pinned), CI value
  overrides, CI sample drift invariant, three idempotent sink
  verifiers, five justfile recipes for local reproduction.
  **Phase R7 closes with this PR.**

Three multi-arch unblocks are tracked outside the phase
sequencing — each has a per-image trigger documented in
[ADR-0018](adr/0018-devcontainer-and-engine-image.md) §5
(R6.5.3 update):

- **Engine** — Rust musl arm64 cross-compile path.
- **SeleniumBase** — Chromium arm64 availability or a documented
  swap to a Chromium-equivalent runtime.
- **curl-impersonate** — runtime base image with arm64 support
  and the curl-impersonate variant binaries available there.

Each unblocks under its own focused PR; none gates R7.1 / R7.2.

## §3 — Documentation refresh (R8.1)

Phase R8.1 closes the refactor's narrative. The targets:

- `docs/MASTER_STRATEGY_REFACTOR.md` — operationally useful
  through R7.x; R8.1 makes the keep-or-delete call.
- `docs/refactor-audit.md` and `docs/refactoring-status.md` —
  same lifecycle. Both retain stewardship value while the
  refactor has open phases; R8.1 decides whether to retire as
  historical record, fold the audit into the CHANGELOG, or
  freeze in place.
- `CHANGELOG.md` — promote the Unreleased section to a tagged
  v1alpha1 entry once R7.2 closes.
- Architecture docs — final consistency pass (every doc cites
  current paths; every category README is in sync with its
  governing ADR).

After R8.1 merges, the refactor is **done**. The platform's
growth into v1alpha2 happens against the post-R6.6 taxonomy
without further structural restructuring.

## §4 — Beyond v1alpha1: platform trajectory

The four reserved categories ([§8 of the architecture
overview](architecture/overview.md#§8--forward-looking-categories))
ship their first inhabitants in v1alpha2. The order below is
forecast, not commitment; admission is gated per the governing
ADRs.

### 4.1 — `sdks/` (first migration)

[ADR-0027](adr/0027-sdk-strategy.md) commits per-language
workspaces at `sdks/<lang>/` and per-protocol-version packages
at `<protocol>/<version>/`. The first migration moves the engine
and the three reference adapters off the per-Dockerfile `buf
generate` pattern and onto consuming
`sdks/<lang>/driver/v1alpha1/` directly. The migration is
per-consumer and gated; ADR-0027 §3 records the order
(engine first, adapters second) and the cutover criteria.

[ADR-0007](adr/0007-protocol-code-generation.md) §2 / §3 carry
in-place evolution notes recording the trajectory; ADR-0007 §1
(per-language generators) and §4 (CI shape) carry forward
unchanged.

### 4.2 — `infra-services/` (first slot)

[ADR-0028](adr/0028-ancillary-infra-services-catalog.md)
catalogues five named slots:

- **`proxy-broker`** (high-conviction) — every production
  scrape deployment needs proxy management. The natural first
  target.
- **`captcha-solver`** (high-conviction) — second priority;
  unblocks adversarial targets.
- **`fingerprint-broker`**, **`session-store`**,
  **`rate-limit-broker`** (probable) — admission as concrete
  consumer needs emerge.

The canonical shape per slot: proto definition + N providers
(at least two for the slot to ship — one is a non-shape) +
per-language SDKs + Compose service + Helm chart presence.
Admission gate (ADR-0028 §5): ≥1 consumer + ≥2 providers + proto
+ SDKs + deployment posture.

### 4.3 — `data-platform/` (first parser)

[ADR-0029](adr/0029-data-platform-and-lake-dsls.md) commits a
four-layer medallion lake model (L0 raw / L1 bronze / L2 silver /
L3 gold) and three stages: `parse/` (file-format extraction the
engine cannot do — PDF, XLSX, complex HTML), `transform/`
(L1 → L2 cleansing), `aggregate/` (L2 → L3 rollups). Up to
three layer-transition DSLs are reserved per criteria in
ADR-0029 §3.

The engine's existing job DSL
([ADR-0012](adr/0012-engine-dsl-and-execution-pipeline.md)) is
the L0 entry DSL — preserved unchanged. The first
data-platform module is likely a `parse/pdf/` or `parse/xlsx/`
worker that consumes the `jobs.completed` Kafka topic and
materialises L1 records.

### 4.4 — `shared-libs/` (organic admission)

ADR-0026 §3.8 records the lightweight admission contract: a
shared lib lands when cross-cutting copy-paste pressure crosses
the threshold (three concrete consumer-side duplications). No
reserved slots; first inhabitant gets reviewed against §3.8.

Likely candidates: a logging / structured-event helper consumed
by the engine + operator + adapters; a config-loading helper
shared between Go services.

## §5 — Stable protocol

The promotion path is `v1alpha1 → v1beta1 → v1`. Promotion
gates:

- **`v1alpha1 → v1beta1`** — capability surface stable for ≥6
  months across the three reference adapters; no breaking
  proto changes; conformance suite passes byte-for-byte across
  all three drivers.
- **`v1beta1 → v1`** — three drivers in production use across
  external organisations (the canonical "third-party validation"
  criterion).

The capability set itself stabilises through use. The 13/12/6
divergence at R6.6-close is the empirical baseline; a v1
declaration likely renames `screenshot_full_page` to a more
precise name, splits `js_execution` into pre/post-navigation
variants, and adds capability strings for the proxy and
fingerprint surfaces (consumer-facing manifestations of
infra-services slots).

A driver registry — a published index of community-maintained
drivers, their declared capabilities, and their conformance
status — ships alongside the v1 declaration.

## §6 — Beyond v1

Open questions, deliberately unscoped:

- **Browser-side WASM engine** — in-page extraction without an
  external driver. Plausible once the Driver Protocol stabilises
  and a WASM runtime can host the three adapter shapes
  uniformly.
- **Higher-level DSL** — joins, pagination, deduplication
  semantics above the current minimal extraction DSL. Lives in
  the `data-platform/` layer-transition DSLs (per ADR-0029) once
  patterns emerge.
- **Multi-cluster federation** — orchestrating ScrapeFleets
  across geo-distributed kind clusters with per-region proxy
  pools and routing decisions. Probably an `operators/`
  v1alpha2 evolution rather than a new category.
- **Managed-service offering** — a hosted reference deployment.
  Out of scope for the open-source project.

These move into per-category trajectory sections only after
they are concretely scoped against admission criteria.

## §7 — How phases get scheduled

[ADR-0026](adr/0026-platform-taxonomy.md) §6 commits the
admission contract:

- **New category** — requires a new ADR. The four reserved
  categories carry pre-written ADRs (0027 / 0028 / 0029 +
  shared-libs in ADR-0026 §3.8). A fifth category requires
  case-specific reasoning.
- **New module within a category** — the per-category ADR
  records admission criteria. `infra-services/` requires the
  five-element shape (proto + ≥2 providers + SDKs + Compose
  + Helm). `data-platform/` requires per-stage admission per
  ADR-0029 §7. `sdks/` requires per-language admission per
  ADR-0027 §3.

A roadmap entry — a forecast, not a commitment — follows once
the admission criteria look reachable. The roadmap is rewritten
at every phase boundary that adds or completes work; the
authoritative source for a future phase's intent is its phase
prompt, not this document.

## §8 — How to influence the roadmap

- **Open a feature request on GitHub** for concrete, scoped work.
- **Draft an ADR** for non-trivial additions, especially anything
  that crosses category boundaries or proposes a fifth category.
- **Engage on existing issues.** Maintainer attention follows
  community attention; an issue with five thoughtful comments
  outweighs five issues with one comment each.

The roadmap reflects current intent. It does not commit to a
schedule, a particular PR shape, or a particular set of
milestones. Subscribe to the CHANGELOG (and the `Unreleased`
section in particular) for granular progress.
