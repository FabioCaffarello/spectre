# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`CONTRIBUTING.md` "v1alpha2 process rigor matrix"** (R9.0):
  codifies the differentiated process rigor that governs all
  subsequent v1alpha2 PRs (transformational change / single
  architectural decision / incremental change / doc-only). Opens
  Phase R9 — v1alpha2 architectural foundation.

## [0.1.0-alpha.0] - 2026-05-03

This release marks the close of Spectre's microservices refactor
(R1 → R8.1). The platform delivers v1alpha1 in production-installable
shape: a polyglot, gRPC-over-TCP, Helm-deployable web scraping stack.

### Refactor narrative

**What v1alpha1 is.** A driver-agnostic web scraping platform built
around a frozen wire protocol with three reference adapters
(Playwright, SeleniumBase, curl-impersonate), a Rust execution
engine, a Go control-plane operator, four output sinks (stdout,
Kafka, S3, HTTP webhook), three stateful dependencies (PostgreSQL,
Redis, Kafka), and a Helm chart for production deployment. Local
development is `docker compose up`; production deployment is
`helm install`.

**The protocol.** gRPC over TCP per
[ADR-0001](docs/adr/0001-driver-protocol-as-architectural-primitive.md)
+ [ADR-0022](docs/adr/0022-tcp-grpc-transport.md). The wire contract
in `proto/spectre/driver/v1alpha1/` was treated as read-only across
every refactor PR. Capability surface 13 / 12 / 6 (Playwright /
SeleniumBase / curl-impersonate) is preserved byte-for-byte from
the project's earliest reference adapters; the conformance test
`test_<adapter>_initialize::test_capabilities_match_manifest_byte_for_byte`
gates regressions. ADR-0008's UDS choice was superseded by
ADR-0022; ADR-0021 settled service discovery; the strict-subset
chain documented in
[ADR-0017 §1](docs/adr/0017-curl-impersonate-extraction-strategy.md)
is the project's most architecturally consequential narrative
artifact.

**The runtime.** Per-service Dockerfiles for the engine
(`engines/engine/Dockerfile`), the operator
(`operators/control-plane/Dockerfile`), and the three adapters
(`adapters/{playwright,seleniumbase,curl-impersonate}/Dockerfile`)
build five service images orchestrated by `docker-bake.hcl`. Images
publish to Docker Hub at
`docker.io/fabiocaffarello/spectre-<name>:<tag>`; the first real
publish landed at `0.1.0-alpha.0` in R7.1. Multi-arch posture per
[ADR-0018 §5 R6.5.3 update](docs/adr/0018-devcontainer-and-engine-image.md):
`playwright-adapter` and `control-plane` ship `linux/amd64 +
linux/arm64`; `engine`, `seleniumbase-adapter`, and
`curl-impersonate-adapter` ship `linux/amd64`-only with documented
unblock criteria per image.

**The Helm chart.** `build/helm/spectre/` (R7.1, ADR-0030) installs
the v1alpha1 stack — engine, three adapters, control-plane operator,
and stateful dependencies (PostgreSQL, Redis, Kafka, MinIO) — into
any conformant Kubernetes 1.27+ cluster. Bitnami subcharts pinned
(postgresql 16.0.0 / redis 19.6.0 / kafka 30.0.0 / minio 14.7.0)
with `Chart.lock` committed; bumps require ADR amendment.
Kubernetes-native `grpc:` probes for engine + 3 adapters; HTTP probe
for the operator. CRDs ship under `crds/` with a chart-CRD-sync
drift invariant gating CI.

**The development environment.** `docker compose up` brings up
eleven services per
[ADR-0025](docs/adr/0025-compose-stack.md) (engine + 3 adapters +
control-plane + Postgres + Redis + Kafka + Redpanda Console +
MinIO + MinIO Console). The Devcontainer ships with
Docker-in-Docker enabled per
[ADR-0018 §3a](docs/adr/0018-devcontainer-and-engine-image.md) so
the Compose stack and a local `kind` cluster live inside a single
Reopen-in-Container session (R6.3). Helm-chart development against
a real Kubernetes cluster works inside the same devcontainer.

**The platform taxonomy.** Eight production-code categories per
[ADR-0026](docs/adr/0026-platform-taxonomy.md). Four are inhabited
at v1alpha1: `proto/`, `engines/`, `operators/`, `adapters/`. Four
are reserved for v1alpha2 growth, each with its governing ADR and
a placeholder README pointing at it: `infra-services/` per
[ADR-0028](docs/adr/0028-ancillary-infra-services-catalog.md),
`sdks/` per
[ADR-0027](docs/adr/0027-sdk-strategy.md),
`data-platform/` (with three stage subdirectories `parse/`,
`transform/`, `aggregate/`) per
[ADR-0029](docs/adr/0029-data-platform-and-lake-dsls.md), and
`shared-libs/` per ADR-0026 §3.8's organic-admission contract.
R6.6 dissolved the pre-refactor `core/` umbrella; `engines/engine/`
and `operators/control-plane/` are top-level peers of `adapters/`.

**Architectural artifacts.** Thirty ADRs (0001–0030) record every
non-trivial decision; ADR text is immutable once accepted, with
in-place evolution notes the only allowed amendment (ADR-0007's
R6.6 evolution notes, ADR-0018's R6.3 / R6.5.4 updates,
ADR-0020 §5's living-audit-table). Architecture docs at
[`docs/architecture/`](docs/architecture/) cover the engine,
control plane, driver protocol, development environment, container
images, releases, Helm chart, production smoke, output sinks, and
each stateful dependency. The frozen
[`docs/refactor-audit.md`](docs/refactor-audit.md) preserves
per-PR / per-cluster decision rationale; CHANGELOG entries are the
user-facing record;
[CONTRIBUTING.md](CONTRIBUTING.md)'s "Architectural commitments"
section captures the seven non-negotiable principles for v1alpha2
contributors.

**What's deferred to v1alpha2.** First SDK migration (engine
first, adapters second per ADR-0027 §3); first infra-service
(`proxy-broker` is the high-conviction first slot per ADR-0028);
first data-platform module (likely a `parse/pdf/` or `parse/xlsx/`
worker per ADR-0029); first shared-lib admission (organic per
ADR-0026 §3.8's three-consumer-duplication threshold); multi-arch
unblocks for the three amd64-only images per
[ADR-0018 §5 R6.5.3 update](docs/adr/0018-devcontainer-and-engine-image.md);
Helm chart OCI registry publish (post-refactor). Each lands under
its own focused PR; none of the v1alpha2 work restructures the
post-R6.6 taxonomy.

### Added

- **Phase R8.1 — Documentation refresh + narrative closing
  (closes the microservices refactor).** Docs-only PR, no source
  changes, no new ADR (per ADR-0020 §5 R8 row). Nine cohesive
  clusters: (A) [CONTRIBUTING.md](CONTRIBUTING.md) gains an
  "Architectural commitments" section preserving the seven
  non-negotiable principles in plain language for v1alpha2
  contributors; (B) [`docs/refactor-audit.md`](docs/refactor-audit.md)
  frozen with a top-of-file FROZEN header (body verbatim);
  (C) `docs/refactoring-status.md` deleted entirely (operational
  session-bookkeeping artifact with zero post-refactor archaeology
  value); (D) CHANGELOG `[Unreleased]` promoted to
  `[0.1.0-alpha.0] - 2026-05-03` with the narrative summary above
  + a fresh empty `[Unreleased]` for v1alpha2 work; (E)
  `docs/MASTER_STRATEGY_REFACTOR.md` deleted entirely (its
  principles preserved in CONTRIBUTING.md per Cluster A; its
  phase-decomposition content preserved in ADR-0020 §5); (F)
  cross-references repaired in `README.md`, `docs/roadmap.md`,
  and CHANGELOG entries that pointed at the deleted docs;
  (G) final consistency pass across `docs/architecture/` and
  `docs/guides/` plus the four placeholder READMEs and the three
  data-platform stage READMEs; (H) final scan of `README.md`
  including a v1alpha1-production-ready note; (I) ADR-0020 §5
  Implementation phases R8 row updated to `0 (closed)`, "ADR
  status changes" section gains a closing entry recording the
  refactor's terminal state, and §5/§6 narrative-prose passages
  rewritten in past tense for R8.1. After R8.1 merges, the
  microservices refactor (R1 → R8.1) is **done**; v1alpha2 begins
  next, against the post-R6.6 taxonomy without further structural
  restructuring.

- **Phase R7.2 — Production smoke (closes Phase R7).** The
  v1alpha1 production-posture phase closes with a CI gate that
  exercises R7.1's chart end-to-end through three sink
  integrations against a real Kubernetes cluster. Pure CI /
  verification PR — no source touched, no chart restructure,
  no new ADR (ADR-0030 §9 already deferred R7.2's territory).
  - **Mock webhook receiver.**
    `build/helm/test/mock-webhook-receiver.yaml`: single-Pod
    Deployment + ClusterIP Service running
    `mendhak/http-https-echo:31`, pinned to its multi-arch
    manifest list digest. The receiver logs every received
    HTTP request to stdout; `verify-webhook-sink.sh` greps the
    pod logs for the engine's expected POST shape.
  - **CI values overrides.** `build/helm/test/values-ci.yaml`:
    `pullPolicy=Never` (kind-loaded images at
    `docker.io/fabiocaffarello/spectre-*:0.1.0-alpha.0`);
    ephemeral persistence; redis architecture=standalone;
    kafka single-controller; `minio.defaultBuckets=spectre-rows`
    to match the s3 sample's bucket. Resource sizing dialled
    down for the GitHub Actions runner without changing the
    semantics R7.2 verifies.
  - **CI samples + drift-detection invariant.** Three CI
    samples at `build/helm/test/samples/{kafka,s3,webhook}.yaml`
    derived from the source samples at
    `operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_*.yaml`
    via `tools/test/sync-ci-samples.sh`. kafka byte-identical
    (KafkaSink.Brokers informational); s3 endpoint flipped to
    the chart-installed MinIO service; webhook URL flipped to
    the in-cluster mock receiver. `tools/test/check-ci-samples-sync.sh`
    is the drift-detection invariant — fails fast in CI before
    the heavy install path.
  - **Sink verification scripts.** Three idempotent verifiers
    at `tools/test/verify-{kafka,s3,webhook}-sink.sh`. kafka:
    `kubectl exec` + `kafka-console-consumer.sh --max-messages 1`
    against `spectre.rows.default`; assert JSON with `title` +
    `url`. s3: `kubectl exec` + `mc ls --recursive scrapes/`;
    assert ≥1 non-empty `.jsonl` object. webhook: `kubectl logs`
    the mock receiver; assert ≥1 POST with engine's expected
    ADR-0024 §4 header schema and a JSONL body. All three:
    bounded assertions (no exact counts/keys/offsets); 60s
    internal timeouts; shellcheck-clean.
  - **Production-smoke CI workflow.**
    `.github/workflows/production-smoke.yml`: standalone
    workflow file with three triggers (`workflow_dispatch`,
    `pull_request` paths-filtered to changes that could
    plausibly regress the smoke, `schedule` daily 06:00 UTC for
    drift detection). Pipeline: setup → sample sync invariant
    → digest pin verify (warning-only) → buf generate → bake
    build → kind cluster `spectre-r72` → kind load images →
    helm dep update → deploy mock receiver → helm install with
    values-ci.yaml → apply 3 ScrapeJobs → wait `Completed`
    (5min each) → 3 sink verifiers → always-run debug logs.
    Not folded into `ci.yml` — different trigger semantics keep
    `ci-summary` clean.
  - **Justfile recipes.** Five recipes:
    `chart-smoke-sync-samples`, `chart-smoke-check-samples`,
    `chart-smoke-up`, `chart-smoke-test`, `chart-smoke-down`.
    `just check` extends to gate the CI sample drift invariant
    alongside the existing chart-CRD sync invariant.
  - **Documentation.** New
    `docs/architecture/production-smoke.md` (~316 lines)
    covering purpose, architecture, trigger model, CI/source
    sample relationship + drift invariant, mock receiver
    rationale + digest pinning, per-sink verification
    mechanisms, debugging guide, local reproduction, and
    intentional out-of-scope items. `docs/architecture/helm-chart.md`
    §9 split into §9.1 (helm-lint) + §9.2
    (production-smoke). Chart `README.md` Verification section
    added.

  After R7.2 merges, **Phase R7 closes**; R8.1 (documentation
  refresh + narrative closing) is the refactor's final PR.

- **Phase R7.1 — Helm chart packaging.** Phase R7 opens with the
  first production-deployment artifact for Spectre's v1alpha1
  stack. One new ADR + a complete chart at
  [`build/helm/spectre/`](build/helm/spectre/) + a CI structural
  gate.
  - **[ADR-0030](docs/adr/0030-helm-chart-structure.md) — Helm
    chart structure.** Eight decisions: chart at
    `build/helm/spectre/` (out-of-band per ADR-0026 §3.9); single
    chart with named-template helpers; Bitnami subcharts pinned
    (postgresql 16.0.0, redis 19.6.0, kafka 30.0.0, minio 14.7.0)
    with `Chart.lock` committed and bumps requiring ADR
    amendment; image references default to
    `docker.io/fabiocaffarello/spectre-<name>:<chart appVersion>`;
    multi-arch via `nodeSelector: kubernetes.io/arch: amd64` on
    the three amd64-only services per ADR-0018 §5 R6.5.3 update;
    Kubernetes-native `grpc:` probes for engine + 3 adapters
    (1.27+ stable per ADR-0025 §3 forward commitment); HTTP
    probes for the control-plane operator; CRD shipped via Helm 3
    `crds/` directory with the documented upgrade caveat; chart
    `version` + `appVersion` pinned to the repository's `VERSION`.
  - **Chart contents.** Five service templates (engine,
    control-plane + RBAC, three adapters), shared `_helpers.tpl`,
    NOTES.txt with a sample ScrapeJob heredoc, `values.yaml`
    (~325 lines, fully commented), `values.schema.json` (JSON
    Schema draft-07 validating overrides at install time),
    `crds/scrapejob.yaml` (byte-for-byte copy of the operator's
    controller-gen source).
  - **First real Docker Hub publish.** R6.5.3 wired
    `publish.yml` as `workflow_dispatch` only; R7.1 dispatched
    it for the first time, producing
    `fabiocaffarello/spectre-<name>:0.1.0-alpha.0` for all five
    images. Closes the R6.5.3 deferred-trigger gap.
  - **CI helm-lint job.** Runs on every PR that touches
    `build/helm/**` or the operator's CRD source. Runs `helm
    dependency update`, `helm lint --strict`, `helm template` +
    `kubeval`, and the `chart-check-crd-sync` invariant against
    `operators/control-plane/config/crd/bases/`. (`helm install
    --dry-run` is deliberately omitted; Helm 3.13's dry-run
    still probes the apiserver and CI has no cluster — local
    smoke goes through `just chart-install-smoke`.)
  - **Justfile recipes.** `chart-sync-crds`,
    `chart-check-crd-sync`, `chart-deps`, `chart-lint`,
    `chart-install-smoke`. `just check` extends to gate the CRD
    drift invariant.
  - **Documentation.** New `docs/architecture/helm-chart.md`
    (~250 lines) — Compose-to-Helm correspondence table,
    multi-arch table, probe policy, CRD lifecycle, CI
    integration, out-of-scope list. Top-level `README.md` gains
    a "Deploying to Kubernetes" section.
    `docs/architecture/releases.md` gains a "R7.1 — Helm chart
    as a release artifact" section.

### Fixed

- **Latent R6.5.3 publish-workflow gap.**
  `.github/workflows/publish.yml` now declares `environment: ci`
  so `secrets.DOCKERHUB_TOKEN` resolves at job time. The token +
  matching `DOCKERHUB_USERNAME` live in the `ci` environment
  (the same one CI's image-matrix and full-stack jobs draw
  from); without the declaration the first real dispatch failed
  with `Password required`. R6.5.3 shipped the workflow without
  the line and went unnoticed because no one had triggered it
  before R7.1.

### Removed

- **Deprecated R6.2 justfile aliases.** `op-install-crds` and
  `op-uninstall-crds` removed per the inline "Removed in R7.1"
  commitment and ADR-0025 §6's R6.3 update one-cycle deprecation
  window. Use `crds-install` / `crds-uninstall`.

### Added (continued — Phase R6.6)

- **Phase R6.6 — Platform Maturation: four ADRs + repository
  restructure.** Phase R6.6 inserts a structural-maturation phase
  between R6.5 (closed at R6.5.4) and the originally-planned R7.1
  (Helm packaging). Four new accepted ADRs define the platform's
  forward-looking taxonomy:
  - **[ADR-0026](docs/adr/0026-platform-taxonomy.md) — Platform
    taxonomy and module categories.** Eight production-code
    categories (`proto/`, `engines/`, `operators/`, `adapters/`,
    `infra-services/`, `sdks/`, `data-platform/`, `shared-libs/`)
    plus four out-of-band (`tools/`, `build/`, `docs/`,
    `examples/`). DAG of dependency direction made normative.
    `core/` dissolved.
  - **[ADR-0027](docs/adr/0027-sdk-strategy.md) — Multi-language
    SDK strategy.** Per-language workspace (`sdks/<lang>/`) with
    per-protocol-version packages
    (`<protocol>/<version>/`). Codegen ownership moves into each
    SDK package; ADR-0007 §1/§4 preserved, §2/§3 evolved without
    supersession. Generated bindings remain gitignored.
  - **[ADR-0028](docs/adr/0028-ancillary-infra-services-catalog.md)
    — Ancillary infra services catalog.** Five named slots:
    `proxy-broker`, `captcha-solver` (high conviction);
    `fingerprint-broker`, `session-store`, `rate-limit-broker`
    (probable). Canonical shape (proto + N providers + per-language
    SDKs + Compose/Helm posture). Admission gate: ≥1 consumer +
    ≥2 providers + proto + SDK + deployment.
  - **[ADR-0029](docs/adr/0029-data-platform-and-lake-dsls.md) —
    Data platform and lake DSLs.** Medallion lake model
    (L0 raw / L1 bronze / L2 silver / L3 gold), three stages
    (`parse/`, `transform/`, `aggregate/`), up to three layer-
    transition DSLs governed by criteria for "extend vs. new".
    Engine job DSL (ADR-0012) preserved as L0-entry DSL.
- **Restructure (closes Phase R6.6).** Repository layout flips to
  match the new taxonomy:
  - `core/engine/` → `engines/engine/` (Cargo crate path; package
    name `spectre-engine` unchanged).
  - `core/control-plane/` → `operators/control-plane/`. Go module
    path `github.com/FabioCaffarello/spectre/core/control-plane` →
    `...spectre/operators/control-plane`; 10 Go files updated
    (cmd/main.go + internal/* + test/e2e/* + go.mod + PROJECT).
  - `core/` directory removed entirely.
  - Four new top-level placeholder directories with READMEs:
    `infra-services/`, `sdks/`, `data-platform/` (with `parse/`,
    `transform/`, `aggregate/` sub-stages), `shared-libs/`. Each
    `README.md` references the governing ADR (0026/0027/0028/0029).
  - Path references updated repository-wide: `docker-bake.hcl`,
    `docker-compose.yml`, `justfile`, `.devcontainer/`,
    `.github/workflows/ci.yml`, `.github/dependabot.yml`,
    `.github/CODEOWNERS`, `.github/ISSUE_TEMPLATE/`, `.gitignore`,
    `.dockerignore` (`!core/` → `!engines/` + `!operators/`),
    `.pre-commit-config.yaml`, `proto/buf.gen*.yaml`,
    `proto/README.md`, `build/docker/` (Dockerfile + README +
    versions.env), top-level `README.md`, `CONTRIBUTING.md`,
    every `docs/architecture/*.md`, `docs/MASTER_PROMPT.md`,
    `docs/roadmap.md`, `docs/refactor-audit.md`, the per-component
    `README.md` files in `engines/engine/` and
    `operators/control-plane/`.
  - Accepted ADRs (0001–0025) intentionally NOT edited (immutable
    per the status flow). A breadcrumb note added to
    `docs/adr/README.md` records the path rename for future
    readers reaching ADR text that cites the old paths.
  - Image names, Compose service names, Kubernetes API group, and
    proto package names unchanged. The taxonomy is about source
    paths only; runtime identities are stable.
- **In-place ADR amendments (R6.6).** Two accepted ADRs receive
  the precedent-shaped in-place amendments (per ADR-0018 §3a /
  §5 R6.5.x pattern):
  - **[ADR-0007](docs/adr/0007-protocol-code-generation.md)
    frontmatter** flipped from `accepted` to
    `accepted (partially evolved by ADR-0027 — see status notes
    in §2 and §3)`. Brief "ADR-0027 evolution (Phase R6.6)"
    subsections appended to §2 (output locations) and §3
    (bootstrap order) recording the trajectory. ADR-0007 §1
    (per-language generators) and §4 (CI shape) carry forward
    unchanged per ADR-0027 §2's preservation stance.
  - **[ADR-0013](docs/adr/0013-cli-as-engine-binary.md)
    frontmatter** refreshed from `superseded by ADR-0020` to
    `superseded by ADR-0019 (control-plane architecture) +
    ADR-0020 (microservices architecture supersession) — see
    status note in §1`. New "Supersession (R3.1)" subsection at
    the head of §1 records that `spectre run` was retired in
    R3.1; the engine binary's CLI surface narrowed to a service
    entry point.
  - `docs/adr/README.md` index updated for both rows; the
    breadcrumb note about pre-R6.6 vs post-R6.6 path citation
    sits below ADR-0029 in the index.
- **Documentation refresh (R6.6).** The doc surface that drifted
  across the long refactor is brought into post-R6.6 reality:
  - **`docs/architecture/overview.md`** rewritten end-to-end
    (~400 lines, 10 sections — taxonomy, dependency DAG,
    today's inhabitants, Compose runtime topology, build/image
    story, CI shape, stateful services, forward-looking
    categories, what stays, references). Pre-R2 transport
    language ("gRPC over UDS or TCP/TLS or JSON-RPC over stdio")
    and pre-R3 subprocess-spawn language removed.
  - **`docs/roadmap.md`** rewritten as the post-R6.6 platform
    roadmap (~270 lines), organised by category rather than by
    pre-refactor PR-numbered phase. Old `Phase 0/1/2/2.5/3/4/5`
    headings and `PR1–PR18` current-state references gone;
    historical past-tense recap allowed in §1's "where we are"
    section.
  - **`docs/architecture/driver-protocol.md`** — the JSON-RPC
    over stdio fallback section deleted; transport semantics
    rewritten as gRPC-over-TCP only with a historical pointer
    to ADR-0008's retired UDS / stdio considerations.
  - **`docs/guides/writing-a-driver.md`** — Step 4 (transport)
    rewritten for the long-running gRPC service shape;
    `kind: jsonrpc-stdio` and the UDS-socket TS skeleton
    removed; Step 2 (`buf generate`) gains an ADR-0027
    forward-reference so future contributors know
    `sdks/<lang>/driver/v<version>/` will replace the
    per-Dockerfile pattern once the first SDK migration lands.
  - **`CONTRIBUTING.md`** Driver Path snippet flipped from
    "gRPC or JSON-RPC over stdio" to "gRPC over TCP" with an
    ADR-0022 cross-reference.
  - Subprocess language preserved where it describes current
    behaviour: the conformance harness (ADR-0025 §5), the
    curl-impersonate `os/exec` cgo replacement (ADR-0016 §1),
    and R-series past-tense supersession notes.
- **Master strategy amendment (R6.6).** (Historical reference;
  the strategy prompt was deleted in R8.1, principles preserved
  in CONTRIBUTING.md "Architectural commitments".) The
  then-`docs/MASTER_STRATEGY_REFACTOR.md` §9 gained a
  `Post-R6.6 amendment` subsection (~30 lines) recording that
  R6.6 inserted a Platform Maturation phase between R6.5
  (closed) and R7.1 (next); criterion #5's ADR set expanded to
  "0020–0029 plus future production-phase ADRs".
- **Bookkeeping closure (R6.6).**
  [`docs/refactor-audit.md`](docs/refactor-audit.md) gains a
  new "Phase R6.6 — Platform Maturation (CLOSED)" subsection
  with one row that summarises all eight commit clusters and
  cross-references every relevant ADR. (Historical reference;
  the then-`docs/refactoring-status.md` was deleted in R8.1.)
  Its pointers advanced to "R6.6 closed; R7.1 next"; the phase
  list ticked R6.6 in a new "Phase R6.6 — Platform Maturation"
  block; the per-PR checklist replaced R6.5.4's with R6.6's
  eleven steps; "Surfaced decisions" refreshed with R6.6's
  eight decisions and the two pre-R7.1 reservations.

### Removed

- **Fossil sweep (R6.6).** The bootstrap-era scaffolding that
  survived 26 PRs of refactor without removal is gone:
  - `docs/MASTER_PROMPT.md` (706 lines) — the PR1 / Phase 0
    bootstrap prompt that authored the empty repo. Predates
    the R-series entirely; describes mechanics that no longer
    match reality. Deleted from version control via `git rm`.
  - `/MEMORY.md` (repo root) and `/memory/spectre_pr_scope.md`
    + `/memory/spectre_pr2_tooling.md` — PR1/PR2-era Claude
    memory scaffolding describing codegen at
    `core/engine/build.rs` (a path that no longer exists).
    Already gitignored via the existing `.gitignore` block;
    the on-disk files are removed to honour the no-legacy
    commitment.
  - `/.claude/scheduled_tasks.lock` (and the empty `/.claude/`
    directory) — Claude-tooling runtime artifact. Already
    gitignored; the on-disk file is removed.
  - `.gitignore` already covers `.claude/`, `.ruff_cache/`,
    `MEMORY.md`, and `memory/` — preserved unchanged. The
    existing patterns prevent re-introduction.
  - The fossil sweep is the largest application of master
    strategy §2.2 ("no legacy paths survive") the refactor has
    produced. Per the maintainer's R6.6 instruction:
    *"é importante manter apenas documentos que vão dar boas
    diretrizes para o futuro da nossa plataforma como um todo
    e não devemos manter legado ou qualquer arquivo que possa
    impactar negativamente na construção da plataforma"*.

### Changed

- **Dockerfile deduplication via shared codegen base
  (R6.5.4 — closes Phase R6.5).** Four of the five Spectre
  images previously carried a near-identical ~10-line `buf`
  install RUN block (`apt-get install curl ca-certificates &&
  curl ... buf-Linux-${BUF_ARCH} && chmod +x`). R6.5.4
  extracts the install logic into a single shared stage at
  `build/docker/buf-base.Dockerfile` (~25 lines on
  `debian:12-slim`), built by a new bake `buf-base` target
  with `output = ["type=cacheonly"]` and consumed by the four
  buf-using image targets via:
  ```hcl
  contexts = {
    buf-base = "target:buf-base"
  }
  ```
  Each consumer Dockerfile (`core/control-plane`,
  `adapters/curl-impersonate`, `adapters/playwright`,
  `adapters/seleniumbase`) loses its ~10-line install block
  and gains one
  `COPY --from=buf-base /usr/local/bin/buf /usr/local/bin/buf`
  line. The engine target intentionally stays out — Rust
  bindings come from `prost-build` inside the Cargo build
  (`core/engine/build.rs`), not via `buf generate`.
  `ARG BUF_VERSION` is preserved in each consumer Dockerfile
  with a cache-key comment so bake's per-target `args:`
  invalidates the codegen stage when the version bumps. As a
  side benefit, three of the four consumer Dockerfiles
  previously echoed `aarch_64` (with underscore) for arm64 —
  the buf release server only serves `aarch64` (no
  underscore), so those three carried a latent 404 footgun;
  consolidating into one source removes it. The playwright
  Dockerfile retains a minimal
  `apt-get install ca-certificates` because the
  `node:<version>-bookworm-slim` base ships without a trust
  store and `buf generate` reaches the Buf Schema Registry
  over TLS for remote plugins. The seleniumbase Dockerfile
  splits its previously interleaved buf+uv RUN, preserves the
  `apt-get install curl unzip ca-certificates` (curl +
  ca-certificates feed the uv install script and BSR TLS;
  unzip carries forward per the original layering), and keeps
  the uv install verbatim. The coherence script
  (`tools/build/check-versions-coherent.sh`) gains a
  `dockerfile_arg_declared()` helper plus an
  `ARG_DECLARED_CHECKS` list covering
  `build/docker/buf-base.Dockerfile`'s `ARG BUF_VERSION`
  (declared without a default — bake's `args:` supplies the
  value); the existing `ARG_CHECKS` flow is unchanged.
  `build/docker/README.md` gains a "Shared codegen base
  (R6.5.4)" section documenting the multi-arch propagation
  and the `BUF_VERSION` bump procedure. ADR-0018 §3 gains an
  "R6.5.4 update — shared codegen base" subsection;
  ADR-0020 §4's R6.5 row already covered all four sub-phase
  PRs (no new audit-trail row needed). Image sizes are
  unchanged — buf lives only in the codegen stage and never
  reaches runtime. Capability invariant 13/12/6 holds
  byte-for-byte (zero source changes; packaging-only PR).
  **No new ADR introduced** — Phase R6.5 hygiene posture
  preserved. **Phase R6.5 CLOSES with this PR.**

### Added

- **Docker Hub registry wiring + multi-arch readiness
  (R6.5.3).** The publish flow goes operational: a new
  `.github/workflows/publish.yml` workflow (`workflow_dispatch`
  only — the repo has no tags yet; auto-trigger before tags
  exist is theoretical, deferred to R7.x) builds and pushes
  the five Spectre images to Docker Hub under the
  `fabiocaffarello` flat namespace
  (`fabiocaffarello/spectre-<name>:<tag>`), gated on a
  Docker Hub Personal Access Token in the `DOCKERHUB_TOKEN`
  repo secret (operator action prerequisite, added separately
  by the maintainer before the first manual dispatch). Three
  workflow inputs: `tag` defaults to the VERSION file content
  (`0.1.0-alpha.0` as of R6.5.3 merge) with explicit override
  available; `targets` defaults to bake's `default` group (all
  five); `multi_arch` defaults to true. The bake invocation
  matches `just images` byte-for-byte except for `--push` and
  per-target platform overrides for the two multi-arch-ready
  images. Multi-arch posture (the honest story): of the five
  images, only **control-plane** (pure Go cross-compile) and
  **playwright** (Microsoft runtime base ships multi-arch
  manifest list) can practically ship `linux/amd64,linux/arm64`
  today; **engine** (hardcoded `MUSL_TARGET=
  x86_64-unknown-linux-musl` + amd64-specific cross-compile
  setup), **seleniumbase** (Google Chrome stable for Linux is
  amd64-only as of R6.5.3 — Chromium has arm64 builds, Chrome
  doesn't), and **curl-impersonate** (runtime base
  `lwthiker/curl-impersonate:0.6-chrome` is amd64-only on
  Docker Hub) are deferred with explicit per-image unblock
  criteria. Forward-readiness changes in the three deferred
  Dockerfiles: `ARG TARGETPLATFORM` / `ARG TARGETARCH`
  declarations, plus `R7.x: multi-arch unblock` comment blocks
  above each blocker referencing ADR-0018 §5 R6.5.3 update.
  control-plane and playwright Dockerfiles get a
  `# Multi-arch ready (R6.5.3): linux/amd64 + linux/arm64`
  comment marker near the top. Per-target platform set is
  declared at publish time (`--set <target>.platform=...`),
  not in `docker-bake.hcl` — bake stays minimal-by-default and
  CI's verify-only matrix doesn't need overrides (§4.3 of
  ADR-0018 §5 R6.5.3 update). A new **`publish-dry-run` CI
  job** validates the multi-arch publish path on every relevant
  change without pushing — control-plane + playwright build
  linux/amd64,linux/arm64 manifest lists, the other three build
  linux/amd64 only. The `changes` job grows a `publish_dry_run`
  filter; `ci-summary`'s `needs:` and report block extend with
  `publish-dry-run`. **ADR-0018 §5 amended in place** with a
  status note at the heading and a new "R6.5.3 update — Docker
  Hub registry + multi-arch reality" subsection (~120 lines)
  covering the two pivots (Docker Hub over ghcr.io; multi-arch
  where achievable today), the 5-image multi-arch table, the
  per-image unblock criteria, the per-target-platform-set-at-
  publish-time decision, the `workflow_dispatch`-only posture,
  the deliverables list, and the maintainer DOCKERHUB_TOKEN
  prerequisite. New `docs/architecture/releases.md` (~250
  lines) is the operator-facing runbook. `container-images.md`
  updated with a Multi-arch status subsection mirroring the
  table; `docker-bake.hcl` comment block rewritten for Docker
  Hub. `git grep -n 'ghcr\.io' docker-bake.hcl` is empty
  (acceptance criterion 4); the ghcr.io references in
  `.devcontainer` (upstream `docker-in-docker` feature image)
  and ADR-0025 §6 R6.3 update audit text stay — those are
  upstream feature consumption, not our publish target. README
  quick-start gets a one-line publish reference. Capability
  invariant 13/12/6 holds byte-for-byte (zero source changes;
  packaging-only PR). **No new ADR introduced** — Phase R6.5
  hygiene posture preserved; ADR-0018 §5 amendment is in-place.
  The publish workflow is **shipped but not run** from this PR;
  the maintainer triggers the first actual publish manually
  after merging, with `DOCKERHUB_TOKEN` configured. Phase R6.5
  is **3 of 4 PRs done** post-merge; R6.5.4 (Dockerfile
  deduplication via shared codegen base image stage) is next.
- **CI hardening — image-build matrix + bake unification +
  full-stack gate (R6.5.2).** R6.5.2 routes every CI image build
  through the canonical `docker buildx bake` orchestrator that
  `just images` runs locally; CI and local now share one build
  path byte-for-byte. The two ad-hoc `engine-image`
  (`docker buildx build`) and `operator-image` (`docker build`)
  jobs are replaced by a single matrix `images` job whose five
  entries (engine, control-plane, curl-impersonate, playwright,
  seleniumbase) each call
  `set -a; source build/docker/versions.env; set +a;
  docker buildx bake --load <target>`. CI sets `VCS_REF`,
  `BUILD_DATE`, and `TAG=ci` so the OCI labels are populated and
  CI artefacts stay distinct from local `:dev` builds. Each
  matrix entry's smoke step calls a parameterized just recipe
  (`just <recipe> TAG=ci`); the five smoke / run recipes
  (`engine-image-run`, `op-image-smoke`, `curl-imp-image-smoke`,
  `pw-image-smoke`, `sb-image-smoke`) gain a positional
  `TAG='dev'` argument so local invocations are unchanged. The
  `changes` job filter set is rebuilt: the legacy `engine_image`
  output is removed; six new outputs are added (`image_engine`,
  `image_control_plane`, `image_curl_impersonate`,
  `image_playwright`, `image_seleniumbase`, `full_stack`). Each
  `image_<name>` filter rebuilds when its source, the proto
  schema, the per-Dockerfile `.dockerignore`, `docker-bake.hcl`,
  `build/docker/**`, or the workflow changes; selectivity is
  preserved so adapter-specific edits don't trigger unrelated
  rebuilds. A new **`full-stack` job** (gated by the
  `full_stack` filter) bake-builds the five images at `TAG=dev`,
  brings up the eleven-service Compose stack with
  `--profile full`, creates a kind cluster on the runner's
  Docker daemon via `helm/kind-action@v1`
  (`cluster_name: spectre-ci`, `config: build/kind/cluster.yaml`),
  regenerates `build/kind/kubeconfig` with `--internal` so the
  operator container can dial `spectre-ci-control-plane:6443`,
  applies the v1alpha2 CRD via `make install`, applies
  `spectre_v1alpha2_scrapejob_hello-hackernews.yaml`, and polls
  `kubectl get scrapejob hello-hackernews -o
  jsonpath={.status.phase}` until `Completed` (5-minute
  timeout). Always-run debug steps tail operator + engine +
  adapter logs; `compose down -v` cleans up unconditionally. The
  full-stack gate verifies master strategy §2.5 ("Compose is the
  development environment") in CI on every relevant change —
  the strongest possible signal that the post-R6.3 unified flow
  holds. `ci-summary`'s `needs:` and report block drop the
  legacy job names and gain `images` + `full-stack`.
  `docs/architecture/container-images.md` grows a "CI shape"
  subsection (matrix table, full-stack gate steps, filter map,
  per-job firing/cost table). `build/docker/README.md`'s pin
  inventory gains a footer noting CI consumes the same pins via
  the same bake invocation. Capability invariant 13/12/6 holds
  byte-for-byte (zero source changes; CI-only PR). **No new ADR
  introduced** — Phase R6.5 is hygiene work; ADR-0020 §4's
  refactor table records R6.5.2 alongside R6.5.1. R6.5.3 (Docker
  Hub registry wiring + multi-arch readiness) is next.
- **Phase R6.5 opens — stale-references sweep + R6.1 leftovers
  (R6.5.1).** R6.5 is a four-PR sub-phase inserted between R6.3
  (Phase R6 close) and R7.1 (Helm chart) to clear monorepo-health
  drift before the chart consumes a clean foundation. R6.5.1
  removes ~125 stale `PR<N>` references from live code (ADRs,
  CHANGELOG, and strategy docs intentionally retain theirs as
  audit trail) using three rewrite patterns documented in the
  phase prompt: Pattern A (delete temporal context — the dominant
  rewrite), Pattern B (replace with the canonical ADR anchor),
  Pattern C (replace with the R-tag for refactor-evolution
  context). One Go test renamed
  `TestNamesReturnsPR12List → TestNamesMatchesCapabilityManifest`
  (the new name describes what the test asserts, not which
  historical PR introduced it). R6.1's two leftover deliverables
  ship: `build/docker/README.md` documenting the versions.env
  contract, the bake/Dockerfile-ARG split, the bump procedure,
  and the pin inventory; and `tools/build/check-versions-coherent.sh`
  enforcing versions.env ↔ docker-bake.hcl ↔ adapter Dockerfile
  ARG defaults coherence + sanity-checking the bake `labels()`
  schema. The script is wired into `just check` (via a new
  top-level `check-versions` recipe; `check` chain becomes
  `check-versions lint test`) and CI's `proto` job (first step
  after checkout). `core/control-plane/config/manager/manager.yaml`
  PR# refs are removed; the resource limits / `runAsUser` /
  `terminationGracePeriodSeconds` (sized for the pre-R3.1 bundled
  adapter Pod that R3.1 retired) are tagged with explicit
  `# R7.1: revisit ...` annotations rather than retuned — the
  Helm chart owns the manager-only Pod sizing. Capability
  invariant 13/12/6 unchanged byte-for-byte; pytest collection
  count holds at 64; engine cargo test --lib 82/82; Playwright
  88/88; SeleniumBase pytest green; curl-impersonate go test
  green. **No new ADR introduced** — Phase R6.5 is hygiene work,
  recorded in ADR-0020 §4's refactor table as a four-PR
  insertion. R6.5.2 (CI hardening) is next.
- **Devcontainer with Docker-in-Docker; kind cluster managed by
  `kind-up` / `kind-down`; control-plane operator added as a
  Compose service; closes Phase R6 (R6.3).** R6.3 places the
  operator inside the unified Compose stack alongside a local
  `spectre-dev` kind Kubernetes cluster running in the
  devcontainer's Docker-in-Docker daemon (ADR-0025 §6 R6.3
  update + ADR-0018 §3a R6.3 evolution). The
  `.devcontainer/devcontainer.json` adds the official
  `ghcr.io/devcontainers/features/docker-in-docker:2` feature
  (Moby variant + Compose v2), populates `forwardPorts` for
  eleven application + stateful service ports, attaches human
  labels via `portsAttributes`, and adds
  `ms-azuretools.vscode-docker`. The Dockerfile pins
  `KIND_VERSION=0.24.0` and harmonises `BUF_VERSION` 1.45.0 →
  1.55.1 with `build/docker/versions.env`; the post-create
  script grows from five to eight numbered steps (kind cluster
  creation + CRD install + version-print sanity precede the
  ready banner). New `build/kind/cluster.yaml` (single-node
  `spectre-dev` config) + `build/kind/.gitignore`; the
  `.gitignore` carve-out is extended to track `/build/kind/`
  alongside `/build/docker/`. `docker-compose.yml` gains a
  `control-plane` service (image `spectre-control-plane:dev` +
  `pull_policy: never`; depends on engine + postgres-healthy;
  joins both the Compose default network and the external
  `kind` Docker network; mounts `build/kind/kubeconfig` read-only
  at `/home/nonroot/.kube/config`; passes
  `--engine-endpoint=engine:8090 --health-probe-bind-address=:8081
  --metrics-bind-address=0 --leader-elect=false`; profiles
  `app`, `full`); top-level `networks:` block declares `kind` as
  `external: true name: kind`. New justfile recipes:
  `kind-up` (idempotent — writes `kind get kubeconfig --internal`
  to `build/kind/kubeconfig` with server URL
  `https://spectre-dev-control-plane:6443`), `kind-down`,
  `kind-status`, `crds-install`, `crds-uninstall`. The
  R6.2 host-process `op-run` recipe is **deleted** entirely (no
  legacy paths per master strategy §2.2);
  `op-install-crds` / `op-uninstall-crds` are renamed
  `crds-install` / `crds-uninstall` and repointed at
  `build/kind/kubeconfig`, with one-cycle deprecation aliases
  preserving the old names (removed in R7.1). Sample-manifest
  endpoints updated: `_endpoint.yaml` flips
  `127.0.0.1:8090` → `engine:8090` (the Compose-internal
  hostname the operator container resolves);
  `_hello-hackernews.yaml` comment corrected (Helm provisions
  the in-cluster Service; Compose dev uses the Endpoint
  sample). ADR-0018 frontmatter status flipped to
  "partially superseded; see status notes in §3, §4 and §5";
  new §3a "R6.3 evolution: Docker-in-Docker for the
  devcontainer" subsection records the audit trail (citing
  ADR-0020 §85–91 master commitment, the two-kubeconfig dance,
  the shared `kind` Docker network, and BUF version
  harmonisation). ADR-0025 §6 gains "R6.3 update — resolution"
  subsection; §9 deferrals each marked "(resolved in R6.3)".
  `docs/architecture/development-environment.md` rewritten for
  the post-R6.3 unified flow (Reopen-in-Container as the
  supported path; new "Kubernetes-in-Docker (kind)" + "DinD
  model" subsections; toolchain prerequisites slimmed to "Docker
  on the host"). `docs/architecture/control-plane.md` Phase 3
  status table flips the R6.3 row to shipped; deployment-shapes
  table widened with R6.3 marked current; the host-operator
  subsection replaced by the post-R6.3 "Operator-as-Compose-
  service against a kind API server" walkthrough. README
  quick-start rewritten. **Phase R6 CLOSED** with this PR's
  merge — the master-strategy §2.5 promise ("what runs in
  development equals what runs in production") holds for
  application services and their direct dependencies; v1alpha1
  deferrals (mTLS, multi-arch images, Helm chart) are Phase R7
  work. **No new ADR** — ADR-0020 §4 refactor table locks Phase
  R6 to ADR-0025 only. **No source-code changes** beyond
  sample-manifest endpoint updates (topology). Conformance suite
  unchanged per ADR-0025 §5. Capability invariant 13/12/6 holds
  byte-for-byte.
- **Unified Compose stack with application services; profile-based
  topology; ADR-0025 introduced (R6.2).** `docker-compose.yml`
  gains four application services — engine, playwright-adapter,
  seleniumbase-adapter, curl-impersonate-adapter — alongside the
  six stateful services from R4.x and R5.1, on a single Compose
  network. Services consume locally-built images via
  `image: spectre-<name>:dev` + `pull_policy: never` (no `build:`
  directives — bake from R6.1 is the canonical build path,
  ADR-0025 §8). Five profiles (`infra`, `core`, `adapters`,
  `app`, `full`) cover the common subset use cases; the
  documented default is `--profile full` (aliased as
  `just compose-up`). The application port range moves from
  9090–9093 to **8090–8093** (ADR-0021 §4 implementation note —
  the plan was right since R2.1; the implementation was lazy)
  to free `localhost:9092` for Kafka under the unified Compose
  stack. ADR-0025 records the topology, profile design, port
  migration, conformance subprocess-harness rationale, and
  operator-outside-Compose deferral. **Healthcheck strategy is
  asymmetric per runtime base** (ADR-0025 §3): engine has none
  (distroless static ships no shell or probe binary); Playwright
  uses bash `/dev/tcp` redirect; SeleniumBase uses Python
  `socket`; curl-impersonate uses busybox `nc -z`. SeleniumBase
  service sets `shm_size: 1gb` for Chrome's `/dev/shm` need.
  **Operator stays a host process for R6.2** (ADR-0025 §6) —
  `just op-run` continues to dial the Compose-running engine via
  the host-port mapping `127.0.0.1:8090`. R6.3 (Devcontainer
  with Docker-in-Docker) brings the operator into the unified
  shape alongside a Compose-managed `kind` cluster. **Phase R6
  remains open**; R6.3 is next.
- **Per-service container images for the engine, control plane,
  and three reference adapters; `docker buildx bake` orchestration;
  `build/docker/versions.env` single-source-of-truth for toolchain
  pins (R6.1).** Three new Dockerfiles
  (`adapters/{curl-impersonate,playwright,seleniumbase}/Dockerfile`)
  bring the service-per-image story to all five components. The
  curl-impersonate runtime base is the upstream
  `lwthiker/curl-impersonate:0.6-chrome` Alpine image used directly
  (the variant binaries are POSIX shell wrappers and the R6.1 §4.3
  sketch's distroless base ships no shell — see
  [container-images.md](docs/architecture/container-images.md));
  the Playwright runtime is the canonical
  `mcr.microsoft.com/playwright:v1.49.0-noble` (Microsoft-maintained,
  Chromium pre-baked, version locked in step with the npm dep);
  the SeleniumBase runtime is `python:3.12-slim-bookworm` with
  Chrome stable + ChromeDriver provisioned at image build time
  and `SPECTRE_SELENIUMBASE_CONTAINER=1` baked as a default ENV.
  `docker-bake.hcl` at the repo root declares five targets, three
  groups (default / core / adapters), and two functions (`image()`
  for registry-aware naming, `labels()` for the OCI annotation
  schema injected uniformly across every image).
  `build/docker/versions.env` consolidates `RUST_VERSION` (1.85
  → 1.88; aws-sdk-sts 1.94 transitive dep MSRV), `GO_VERSION`,
  `NODE_VERSION`, `PYTHON_VERSION`, `PROTOC_VERSION`,
  `BUF_VERSION`, `UV_VERSION`, `PLAYWRIGHT_VERSION`,
  `CURL_IMPERSONATE_IMAGE`, and `CHROME_VERSION` into one POSIX
  shell-sourceable file every consumer reads. Existing engine +
  control-plane Dockerfiles refactored to drop inline `ARG`
  defaults and `LABEL` directives (bake supplies both); engine
  Dockerfile gains apt installs for `cmake`, `g++`,
  `libcurl4-openssl-dev` plus a `x86_64-linux-musl-g++` symlink
  and a curl-headers copy into musl's sysroot so librdkafka 2.12
  builds under the musl target. `.dockerignore` consolidated to
  deny-by-default + negate-include shape so the build context
  shrinks below 50 MB. ADR-0018 status frontmatter updated to
  "accepted (partially superseded)"; §4 (per-adapter Dockerfile
  deferral) **retired**; §5 (single-arch + no-publish)
  **reaffirmed for R6.1; revisited in R7.1**. New justfile
  umbrella recipes: `images`, `images-smoke`, `images-clean`,
  `images-list`; per-adapter `*-image` + `*-image-smoke`
  recipes; `engine-image` + `op-build-image` refactored to wrap
  `docker buildx bake`. **Phase R6 opens with this PR** (R6.2 wires
  the images into Compose; R6.3 revisits the Devcontainer; R7.1
  adds release-engineering — multi-arch matrix, ghcr.io
  publishing, signing). Capability invariant 13/12/6 holds
  byte-for-byte; conformance suite count unchanged at 50/14
  (no behavioural tests added).
- **S3 + webhook output sinks; `OutputSink.S3` + `OutputSink.Webhook`
  unblocked; ADR-0024 introduced (R5.1).** ADR-0024 documents
  the engine's `aws-sdk-s3` 1.x uploader (rustls features,
  custom-endpoint support for MinIO/R2/Wasabi,
  `behavior-version-latest` pinning) and `reqwest` 0.12 webhook
  client (rustls-tls-native-roots aligned with sqlx 0.23). The
  S3 sink buffers extracted rows in memory as JSON Lines and
  uploads as a single PutObject at job completion (multipart
  streaming deferred to v1alpha2); the object key supports
  `{{.JobID}}` template substitution; empty-result jobs upload
  zero-byte objects so the presence-or-absence of the key
  remains a reliable post-job signal; content type is
  `application/x-ndjson`. The webhook sink POSTs (or PUTs) rows
  to the configured URL with bounded exponential-backoff retry
  on transient errors (3 attempts, 200/400/800 ms with jitter,
  retryable on connection-refused / 5xx / 429, fatal on first
  attempt for other 4xx); per-row when `BatchSize=0` (CRD
  default) or batched at N-row threshold otherwise. Every
  request carries the `User-Agent: spectre-engine/<version>`,
  `X-Spectre-Job-Id`, `X-Spectre-Driver`, `X-Spectre-Row-Count`
  header schema (auth deferred to v1alpha2). **Admission gating
  asymmetry** (ADR-0024 §5): Kafka and S3 hold engine-level
  state validated at startup (S3's env-unset arm logs INFO, not
  WARN — BYO-credentials mode covers IAM-role / SSO / profile,
  the production-typical shape); Webhook has no global state
  and gates per-job at runtime. Engine-side errors:
  `S3_UNAVAILABLE` / `S3_FIELD_REQUIRED` / `S3_UPLOAD_FAILED` /
  `WEBHOOK_FIELD_REQUIRED` / `WEBHOOK_POST_FAILED`. `engine.proto`
  evolves non-breakingly with nested `S3SinkConfig` (field 5,
  bucket/key/endpoint/region) and `WebhookSinkConfig` (field 6,
  url/method/batchSize) messages; `kafka_topic` (field 4) stays
  as a flat string for R4.4 wire compat. The reconciler's
  `validateOutputSink` unblocks both variants (defence-in-depth
  on per-variant required fields); new helpers
  `outputSinkS3Config` / `outputSinkWebhookConfig` parallel
  R4.4's `outputSinkKafkaTopic`. The Compose stack adds **MinIO**
  at `localhost:9000` (S3 API) + `localhost:9001` (web console)
  plus a one-shot bucket-bootstrap container that pre-creates
  `spectre-rows`. `.env.example` carries the `SPECTRE_S3_*`
  block. Justfile recipes: `engine-s3-test`,
  `engine-webhook-test`, `minio-console`, `minio-ls`. Two new
  sample manifests (`spectre_v1alpha2_scrapejob_s3.yaml`,
  `..._webhook.yaml`). The conformance suite gains
  `test_s3_sink.py` (one test against Compose MinIO via boto3) +
  `test_webhook_sink.py` (per-row + batched against an
  in-process aiohttp server, no Compose dep) — full suite at
  50 passed, 14 skipped (vs R4.4's 47 / 14 — the +3 are the
  new tests). The 13 / 12 / 6 capability invariant holds
  byte-for-byte. **Phase R5 closes with this PR — every
  v1alpha2 `OutputSink` variant is behaviourally implemented.**

### Changed

- **Devcontainer toolchain `BUF_VERSION` harmonised 1.45.0 →
  1.55.1 (R6.3 — `build/docker/versions.env` is the single
  source of truth).** ADR-0018 §3a records the harmonisation;
  Go / Node / Python / protoc were already aligned.
- **`spectre_v1alpha2_scrapejob_endpoint.yaml` sample (R6.3).**
  The `engineRef.endpoint` field flips
  `127.0.0.1:8090` → `engine:8090` to reflect the post-R6.3
  topology where the operator runs as a Compose service and
  resolves engine via the Compose default network's DNS. The
  comment block describes the unified `just kind-up && just
  compose-up` flow that replaces the R6.2 multi-terminal
  `op-run` walkthrough. The `_hello-hackernews.yaml`
  Service-form sample's comment block is corrected: Helm (R7.1)
  provisions the in-cluster Service; the post-R6.3 dev flow
  uses the Endpoint sample.
- **Application port range migrated from 9090–9093 to 8090–8093
  (R6.2 — ADR-0021 §4 / ADR-0025 §7).** The engine's bind port
  default flips 9090 → 8090; adapter Dockerfile `EXPOSE` /
  `ENV SPECTRE_ADAPTER_GRPC_PORT` defaults flip 9091/9092/9093
  → 8091/8092/8093; `core/control-plane/cmd/main.go`'s
  `defaultEngineEndpoint` flips to `127.0.0.1:8090`; the
  v1alpha2 `EngineServiceRef.Port` kubebuilder default flips to
  8090 (regenerated CRD updated); `.env.example`, every sample
  ScrapeJob manifest, the architecture docs, every example
  README, and the conformance demo CLI help text follow. Kafka's
  9092 broker port stays unmolested — the migration's reason
  for being. The native-binary `pw-run` / `sb-run` /
  `curl-imp-run` recipes are retired (no fallback —
  master-strategy §2.2 forbids "temporary" legacy fallbacks);
  `just engine-run` is renamed `just engine-run-native` and
  preserved as a debugging escape hatch with a comment block
  pointing at `compose-up`. `op-build-image` is renamed
  `op-image` for naming consistency with the other
  `<service>-image` recipes.
- **`JobRunner.Run` signature evolves to seven parameters
  (R5.1).** `s3Config *enginev1alpha1.S3SinkConfig` and
  `webhookConfig *enginev1alpha1.WebhookSinkConfig` join the
  R4.2 / R4.4 parameters. ADR-0019 §5 R5.1 addendum documents
  the trade-off: a `RunRequest` struct refactor is the right
  v1alpha2 shape but doing it inside an R5.1 PR that already
  adds two new sinks would double the reviewable surface area;
  the refactor lands as its own PR in v1alpha2.

### Removed

- **`just op-run` recipe (R6.3 — ADR-0025 §6 R6.3 update).** The
  R6.2 host-process operator flow is gone; the operator runs as
  the `control-plane` Compose service. No fallback path
  survives — master strategy §2.2 forbids "temporary" legacy
  fallbacks during refactor. `op-install-crds` /
  `op-uninstall-crds` are renamed `crds-install` /
  `crds-uninstall`; the old names are kept as one-cycle
  deprecation aliases (removed in R7.1).
- **`TestFailedOnUnsupportedSink` deleted (R5.1).** Every
  v1alpha2 `OutputSink` variant is now wired; the test's input
  set (a sink the reconciler rejects) has gone to zero.
  Preserving it would require fabricating an invalid sink
  (a fifth variant), which is itself a schema violation. The
  defence-in-depth `RejectsEmpty*` tests in
  `scrapejob_controller_test.go` continue to cover the
  remaining negative-path surface. ADR-0024 §1 records the
  deletion.

- **Kafka producer integration; `OutputSink.Kafka` unblocked
  (R4.4).** ADR-0023 §3 R4.4 addendum implements the engine's
  `rdkafka` producer end-to-end. The engine binary builds one
  shared `KafkaProducer` at startup via `KafkaProducer::from_env`
  and threads it through `EngineServiceImpl` as
  `Option<Arc<KafkaProducer>>`. Kafka admission gating follows
  ADR-0023 §6's optional-service pattern: an unreachable broker
  at startup logs a warning and the engine continues without
  Kafka; subsequent `RunJob`s with `output_sink_kind = "kafka"`
  fail fast at job-start time with `error_code = "KAFKA_UNAVAILABLE"`
  (or `"KAFKA_TOPIC_REQUIRED"` for an empty topic) — equivalent UX
  to admission rejection without a custom validating webhook.
  Kafka-sinked jobs publish one message per extracted row to the
  topic from `ScrapeJob.Spec.OutputSink.Kafka.Topic`,
  partition-keyed by job UUID so all rows for a job land on a
  single partition in extraction order, with headers `job_id` /
  `row_index` / `driver` / `timestamp` (ISO-8601). Producer
  config: `acks=all`, `enable.idempotence=true`,
  `compression.type=snappy`, `linger.ms=10` (tunable via
  `SPECTRE_KAFKA_LINGER_MS`). Delivery semantics:
  **at-least-once**; consumer-side idempotency on
  `(job_id, row_index)` is the documented user responsibility.
  `engine.proto` evolves non-breakingly with `kafka_topic` field
  (number 4); the control-plane reconciler unblocks the Kafka
  branch of `validateOutputSink` and forwards the topic via the
  evolved `JobRunner` interface (ADR-0019 §5 addendum). The
  `_NOT_YET_IMPLEMENTED` Kafka sample manifest is renamed to
  `spectre_v1alpha2_scrapejob_kafka.yaml` — a functional
  example. The Compose stack gains **Apache Kafka 3.7.1 in KRaft
  mode** (production parity with R7.1's Strimzi target,
  superseding the original §3 Redpanda single-binary mention)
  and **Redpanda Console** as the topic / offset / message-browser
  UI at <http://localhost:8080>. `.env.example` carries
  `SPECTRE_KAFKA_BROKERS`. Justfile recipes:
  `engine-kafka-test`, `kafka-console`, `kafka-topics`,
  `kafka-consume`. Conformance suite gains
  `tools/conformance/tests/test_kafka_sink.py` — one
  engine-level E2E test (the kafka path is engine behaviour,
  not driver-level capability) that spawns the engine binary +
  Playwright adapter, submits a `RunJob` with the kafka sink,
  drains the topic via `confluent_kafka.Consumer`, and asserts
  partition keys + headers. The 13 / 12 / 6 capability
  invariant holds byte-for-byte. **Phase R4 closes with this PR.**
  rdkafka 0.36 with `cmake-build + ssl-vendored + tokio`
  features adds 10-15 minutes to the first clean engine build
  (OpenSSL compile from source) for forward-compat with
  v1alpha2 SASL/mTLS; cached thereafter. The OpenSSL stack
  vendored with librdkafka is *deliberately* separate from
  sqlx's rustls 0.23 — the two TLS stacks coexist without
  conflict because they are different libraries (C vs
  Rust-native).
- **Redis adapter session externalization with restart
  invalidation (R4.3).** ADR-0023 §4's keyspace lands across all
  three reference adapters: each adapter writes session metadata
  to `session:<adapter>:<session_id>` at `Initialize` with a
  1-hour idle TTL refreshed on every successful non-Initialize
  RPC. Each adapter process generates a UUID at startup
  (overridable via `SPECTRE_ADAPTER_INSTANCE_ID` for the
  conformance suite only) and stamps it on the metadata
  document; non-Initialize RPCs read the metadata, compare the
  stored `adapter_instance_id` against the live process value,
  and surface foreign-instance sessions as gRPC `UNAVAILABLE`
  with the message _"session belongs to a different adapter
  instance; client must re-Initialize"_ — the §5
  restart-invalidation contract documented in the new ADR-0023
  §5 R4.3 addendum. `Initialize` awaits the Redis write before
  responding so the local registry never drifts ahead of Redis;
  `Close` validates first, then evicts locally and best-effort
  deletes the Redis key (TTL is the safety net per phase prompt
  §4.6). Per-language libraries: Playwright uses
  `ioredis` + `ioredis-mock`; SeleniumBase uses `redis>=5.0` +
  `fakeredis`; curl-impersonate uses `go-redis/v9` +
  `redismock/v9` + `miniredis/v2`. Each adapter PINGs Redis at
  startup and exits non-zero when unreachable (ADR-0023 §6 —
  Redis required). The Compose stack gains `redis:7-alpine`
  (AOF + LRU eviction); `.env.example` carries
  `SPECTRE_REDIS_URL`. Conformance suite gains
  `tools/conformance/tests/test_session_restart_invalidation.py`
  (one test per adapter) exercising the contract via parallel
  adapter instances with distinct `instance_id_overrides`. The
  13 / 12 / 6 capability invariant holds byte-for-byte. Engine
  and control plane are unchanged operationally — ADR-0023 §7
  reserves Redis access to adapters only.
- **PostgreSQL integration end-to-end (R4.2).** ADR-0023 §2's
  schema lands as a versioned, immutable migration file in
  `core/engine/migrations/`. The engine gains an `sqlx`-backed
  `db` module — connection pool, embedded migration runner, four
  typed query functions — and writes a `jobs` row at status
  `'running'` on every admitted `RunJob`, appends `job_rows`
  audit rows for stdout-sinked jobs, and persists the terminal
  `mark_completed` / `mark_failed` UPDATE. The control plane
  gains a `pgx/v5` + pgxpool wrapper and a reconciler that reads
  `jobs` by `ScrapeJob.UID` on Running-phase entry, syncing
  terminal status from Postgres without re-running.
  `engine.proto` evolves non-breakingly with a new
  `output_sink_kind` field (proto3 default empty, engine treats
  empty as `'stdout'`). The JobRunner interface (ADR-0019 §5)
  evolves to accept `jobID uuid.UUID` and
  `outputSinkKind string`; the abstraction is preserved per the
  §5 evolution rule, with the addendum recording the breakage of
  the R3.1 byte-for-byte vindication. A `docker-compose.yml` at
  the repo root brings up `postgres:16-alpine` for local dev;
  `.env.example` documents the env var set; the justfile gains
  `compose-{up,down,logs,reset}` recipes. The
  `SPECTRE_POSTGRES_URL` env var is required at startup for both
  engine and operator; ADR-0023 §6's "no Postgres-less mode"
  holds.
- Architectural commitment to a microservices refactor recorded
  in [ADR-0020](docs/adr/0020-microservices-architecture-supersession.md).
  No code changes in this release; subsequent phase PRs (R2–R8)
  delivered the implementation. (Historical reference; the
  then-`docs/refactoring-status.md` was the live phase tracker
  and was deleted in R8.1; per-PR detail lives in the now-frozen
  [`docs/refactor-audit.md`](docs/refactor-audit.md).)
- [ADR-0023](docs/adr/0023-stateful-services-architecture.md)
  records the stateful-services architecture for Phase R4
  (R4.1, documentation-only). Three services land together:
  PostgreSQL for job state and the audit `job_rows` table
  (R4.2; engine-side `sqlx` and control-plane-side `pgx/v5`),
  Kafka for the `OutputSink.Kafka` streaming surface (R4.4;
  `rdkafka` producer publishing one message per JSONL row to
  topic `spectre.rows.<workspace>`, partitioned by job UUID
  with `job_id` / `row_index` / `driver` / `timestamp`
  headers), Redis for adapter session metadata (R4.3; `ioredis`
  / `redis-py` / `go-redis/v9` per language at the
  `session:<adapter>:<session_id>` keyspace with a 1-hour idle
  TTL). The ADR commits the deployment-shape matrix (Postgres
  + Redis required everywhere, Kafka admission-gated when an
  operator runs it), the env-var configuration convention
  extending ADR-0021 §5 (`SPECTRE_POSTGRES_URL`,
  `SPECTRE_KAFKA_BROKERS`, `SPECTRE_REDIS_URL`), the
  per-service network topology, and the migration discipline
  (sqlx forward-only versioned SQL applied at engine startup).
  ADR-0023 §5 commits the *restart-invalidation* contract for
  adapter sessions: clients hold session_ids only for the
  lifetime of the adapter Pod that allocated them; sticky
  sessions and warm recovery were evaluated and rejected.
- Initial repository structure and foundational documents
- Driver Protocol skeleton at v1alpha1
- Skeleton implementations for three reference adapters
- gRPC standard health check (`grpc.health.v1.Health`) registration
  in every adapter (R2.2, ADR-0021 §6). The conformance harness
  polls `Check` until SERVING as the readiness signal; production
  deployments wire the same endpoint into Compose / Kubernetes
  readiness probes.
- `proto/grpc/health/v1/health.proto` vendored verbatim from the
  canonical gRPC source so the Playwright TS adapter can produce
  Connect-RPC bindings; the Go and Python adapters consume their
  ecosystem libraries (`google.golang.org/grpc/health` and
  `grpcio-health-checking`) directly. Buf lint exempts the
  vendored file so it stays byte-identical to the upstream source.
- `KNOWN_BREAKAGE.md` documented the R2.2 → R2.3 engine ↔ adapter
  transport mismatch. R2.3's first commit deleted the file as the
  engine-side TCP dial landed.
- Internal `spectre.engine.v1alpha1.Engine` service contract
  (R2.3) at `proto/spectre/engine/v1alpha1/engine.proto`. A
  single streaming RPC, `RunJob`, takes an inline DSL document
  and streams `Row` events followed by a terminal `Completed` or
  `Failed`. Cancellation is gRPC stream cancellation; status,
  metrics, and listing are control-plane responsibilities.
  Bindings are generated for Rust (via `core/engine/build.rs`)
  and Go (via `proto/buf.gen.engine.yaml`); Python and TS are
  intentionally not generated.
- Engine becomes a stateless gRPC service (R2.3, ADR-0020 §3).
  The binary at `core/engine/src/bin/spectre.rs` registers
  `spectre.engine.v1alpha1.Engine` and `grpc.health.v1.Health`
  on a single TCP listener (default `0.0.0.0:9090`, override
  via `SPECTRE_ENGINE_PORT`) and shuts down cleanly on
  SIGTERM/SIGINT. Adapter discovery flows through
  `AdapterRegistry`, which reads
  `SPECTRE_PLAYWRIGHT_ENDPOINT` /
  `SPECTRE_SELENIUMBASE_ENDPOINT` /
  `SPECTRE_CURL_IMPERSONATE_ENDPOINT` (defaults
  `127.0.0.1:909{1,2,3}`).
- Engine architecture document at
  `docs/architecture/engine.md` describing the service contract,
  discovery model, health-check registration, CLI-retirement
  rationale, and v1alpha1 statelessness invariant.
- Control plane is now a thin gRPC client of the engine service
  (R3.1, ADR-0020 §5). The new
  `core/control-plane/internal/runner/engine_client.go` implements
  the `JobRunner` interface by dialling
  `spectre.engine.v1alpha1.Engine.RunJob` per invocation and
  forwarding every `Row.json_line` event into the supplied
  writer. ADR-0019 §5's interface seam is vindicated at the
  second substitution: three implementations (`StubRunner`,
  `SubprocessRunner`, `EngineClientRunner`) share one signature
  and the reconciler is unaware of which is wired. The R2.3 → R3.1
  transitional window (operator broken at runtime) closes here.
- Operator startup honours `--engine-endpoint=<host:port>` and
  `SPECTRE_ENGINE_ENDPOINT` (default `127.0.0.1:9090`) — Compose
  (R6.2) and Helm (R7.1) renderings will inject the service-
  network address. Plain-text gRPC for v1alpha1 per ADR-0022 §6;
  TLS / mTLS deferred to v1alpha2.
- `ScrapeJob` CRD evolved to `spectre.io/v1alpha2` (R3.2). New
  fields:
  - `spec.engineRef` (optional, CEL-validated): per-job engine
    selection via Kubernetes Service reference (rendered as
    `<name>.<namespace>.svc.cluster.local:<port>`) or direct
    host:port endpoint. Nil falls back to the operator's
    startup-time `SPECTRE_ENGINE_ENDPOINT` configuration.
  - `spec.outputSink` (required, CEL-validated): discriminated
    union over `stdout`, `kafka`, `s3`, `webhook` variants. R3.2
    wires only `stdout` end-to-end; the other three variants are
    schema-only — the reconciler rejects them at the
    `Pending → Running` boundary with explicit "not yet
    implemented (R4.4 / R5.1)" errors. Schema-ahead-of-
    functionality is intentional and documented in
    `config/samples/spectre_v1alpha2_scrapejob_kafka_NOT_YET_IMPLEMENTED.yaml`.
  - `status.resolvedEngineEndpoint`: records the host:port the
    operator actually dialed (debug aid for `EngineRef`
    resolution).
  CEL `XValidation` rules (stable in Kubernetes 1.25+) enforce
  the discriminated-union shapes at admission, removing the
  operational overhead of custom validating webhooks.
- `core/control-plane/config/samples/spectre_v1alpha2_*.yaml`
  (R3.2): five samples covering Service `EngineRef` for the
  three reference adapters, an `EngineRef.Endpoint` variant for
  ad-hoc local testing, and one schema-only Kafka sample
  (`_kafka_NOT_YET_IMPLEMENTED.yaml`) documenting the schema-
  ahead-of-functionality gap.

### Changed

- ADR-0019 (control plane and ScrapeJob CRD) gains an "Update
  (R3.2)" addendum recording v1alpha2 as the only registered
  version: §1, §2, §4 carry forward unchanged; §3 (subprocess
  execution model) was already superseded by ADR-0020 in R1.1;
  §5 (JobRunner interface) preserved through a construction-
  site refactor (per-Reconcile runner construction via a
  `RunnerFactory` closure) — the interface signature is byte-
  for-byte unchanged; §6 (OutputSink stdout-only commitment)
  honoured at the runtime level (the discriminated union now
  carries Kafka / S3 / Webhook field shapes, but the reconciler
  rejects them at admission until R4.4 / R5.1 wire them).
- ADR-0008 (UDS transport), ADR-0009 (session lifecycle),
  ADR-0019 (subprocess-in-pod) carry "Update (R1.1, ADR-0020)"
  notes recording per-section supersession. ADR-0019 §5
  (`JobRunner` interface) gains an "Update (R3.1, vindication)"
  addendum recording the seam's stability across three
  implementations. ADR-0013 (CLI as engine binary) is superseded
  in full. ADR-0012 (engine DSL + execution pipeline) carries an
  "Update (R2.3, ADR-0020)" note recording the launcher-contract
  supersession; §§1-3, 5, 6 are preserved unchanged. The ADR
  index reflects these changes.
- Adapter transport for all three reference adapters (Playwright,
  SeleniumBase, curl-impersonate) and the conformance harness
  switched from Unix-domain-socket gRPC to TCP gRPC (R2.2,
  ADR-0021 + ADR-0022). Each adapter binds `0.0.0.0:<port>` where
  the port is read from `SPECTRE_ADAPTER_GRPC_PORT`; the
  conformance harness allocates a free localhost port and injects
  it into the subprocess env. The wire-level driver protocol
  contract is unchanged. The 13 / 12 / 6 capability lists for
  Playwright / SeleniumBase / curl-impersonate are preserved
  byte-for-byte.
- `driver.yaml` schema for every adapter retires the `transports:`
  block; the spawn directive moves into `runtime.command`
  (transitional — R6.2's Compose stack supersedes the
  harness-spawn flow entirely). The conformance harness reads
  `runtime.command` rather than `transports[0].command`.
- `just <adapter>-run` recipes take a port argument (default
  matching the canonical port from ADR-0021 §4) instead of a
  socket path. The recipes survive R2.2 as developer
  conveniences and are scheduled for retirement in R6.2.
- Engine `Client::dial` (R2.3) accepts `host:port` or
  `grpc://host:port` endpoints and connects via
  `tonic::transport::Endpoint`'s TCP path. The
  `tower::service_fn` UDS connector and the
  `:authority=localhost` Node-http2 workaround are gone.
- Engine binary (R2.3) is service-only. The `run`, `validate`,
  and standalone `version` subcommands the CLI exposed via clap
  are gone with ADR-0013's CLI surface (ADR-0020 §3). The binary
  has no flags beyond `--help`/`--version` and its single
  responsibility is starting the gRPC service.
- `justfile` and `.github/workflows/ci.yml` (R2.3) drop the
  `spectre version` / `spectre validate` smoke steps. The
  release-binary smoke becomes "the binary built / exists at the
  canonical path"; deeper start-and-probe smokes are deferred to
  the Compose stack (R6.2). The `operator-smoke-kind` CI job is
  gated `if: false` until R3.1 lands `EngineClientRunner`.
- Example READMEs (R2.3) document the manual `grpcurl` flow
  honestly. The `seleniumbase-navigate` and
  `curl-impersonate-fetch` directories are deleted; they
  existed to demonstrate the legacy CLI's "minimum viable
  adapter run" and the `*-extract` examples cover the same
  adapters with richer functionality.
- `curl-imp-lint` pinned to `GOTOOLCHAIN=go1.25.3` mirroring the
  existing `cp-lint` pattern; without the pin `golangci-lint
  v2.8.0` (built with go1.25.5) panics on the Go 1.26 stdlib
  loaded by an unconstrained toolchain.
- `proto/buf.gen.yaml` pins the python plugins to revisions that
  emit gencode compatible with the protobuf 6.x runtime
  (`grpcio-health-checking==1.80.0` caps protobuf at <7.0.0).

### Removed

- **BREAKING**: `core/control-plane/api/v1alpha1/` (R3.2) — the
  ScrapeJob CRD's first version. Per master strategy §3.3, the
  v1alpha1 → v1alpha2 migration is a breaking change without a
  conversion webhook (no production users to migrate); v1alpha1
  ScrapeJob CRs in clusters on upgrade are orphaned. Upgrade
  procedure: `kubectl delete scrapejob --all` → install v1alpha2
  CRD → apply v1alpha2 CRs (see
  `docs/architecture/control-plane.md` and the ADR-0019 R3.2
  addendum). The retired surface includes
  `api/v1alpha1/{groupversion_info.go,scrapejob_types.go,
  zz_generated.deepcopy.go}`, the
  `config/samples/spectre_v1alpha1_scrapejob_*.yaml` set, and
  the v1alpha1 entry from `core/control-plane/PROJECT`.
- The Unix-domain-socket transport across all three reference
  adapters and the conformance harness (R2.2). No fallback path
  survives — strategy prompt §2.2 forbids "temporary" legacy
  fallbacks during refactor. The retired surface includes
  `resolveSocketPath` / `resolve_socket_path` resolvers, the
  `--socket` CLI flag, the `SPECTRE_DRIVER_SOCKET` env var, the
  stale-socket unlink logic, the `ready unix:<path>` stdout
  readiness banner, and the `transports:` block in every
  `driver.yaml`.
- `core/engine/src/launcher.rs` (R2.3) — 628 lines of subprocess
  management. The engine no longer spawns adapters; it dials
  them as long-running services via `AdapterRegistry`.
  `LauncherError` and `EngineError::Launcher` go with it.
- `core/engine/tests/integration.rs` (R2.3) — required
  `PLAYWRIGHT_AVAILABLE=1` and Chromium and exercised the same
  engine → adapter loop the conformance suite already covers
  across all three adapters.
- `Engine::new(adapters_path)`, `Engine::run_job(yaml, job_dir)`,
  `Engine::run_plan(plan, job_dir)`, `Engine::validate_only`
  (R2.3). The legacy CLI-shaped API gives way to
  `Engine::from_env` / `Engine::with_registry` and a single
  `Engine::run_plan_with_sink(plan, sink)` entry point that the
  gRPC server's `RunJob` handler drives.
- Engine Cargo.toml deps no longer used (R2.3): `nix` (SIGTERM
  helper), `regex` (readiness-line matching), `tower` (UDS
  connector via `service_fn`), `hyper-util` (production —
  `TokioIo` UDS wrapper), `clap` (CLI subcommand parser),
  dev-only `hyper` and `http-body-util` (integration-test
  fixture HTTP server). `tokio`'s `process` feature is dropped.
  `tonic-health` is added in their place.
- `examples/seleniumbase-navigate/` and
  `examples/curl-impersonate-fetch/` (R2.3) — navigate-only CLI
  demos; the `*-extract` siblings cover the same adapters.
- `core/control-plane/internal/runner/subprocess.go` (R3.1) —
  shelled out to the engine's retired `spectre run` CLI surface
  and bundled the JSONL scanner. `EngineClientRunner` replaces
  it with a gRPC stream consumer; `subprocess_test.go` and the
  `testdata/fake_spectre.go` fixture binary go with it.
- The operator image's bundled engine binary
  (`/usr/local/bin/spectre`) and three adapter trees
  (`/opt/spectre/adapters/{playwright,seleniumbase,curl-impersonate}/`)
  retire with the bundled-image execution model (R3.1). The
  Microsoft Playwright runtime base, the apt overlay for Google
  Chrome + ChromeDriver, the curl-impersonate release tarball
  download, and the per-adapter builder stages are gone. The new
  image is a Go static binary on
  `gcr.io/distroless/static:nonroot` (~50 MB on disk).
- `core/control-plane/hack/smoke-kind.sh` and the gated
  `operator-smoke-kind` CI job (R3.1) — drove the bundled-image
  in-cluster smoke. The multi-service end-to-end smoke returns
  with the Compose stack (R6.2) and the Helm production smoke
  (R7.2).
