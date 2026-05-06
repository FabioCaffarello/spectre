# Architecture overview

This document is the entry point for understanding how Spectre is
put together at the end of the v1alpha1 refactor (R1 → R8.1,
closed 2026-05-03). It complements the
[ADRs](../adr/README.md) — which capture *why* each decision was
made — by describing *what* the resulting system looks like today.

The platform is a set of cooperating services with explicit
categories, a dependency DAG, and a small set of runtime
topologies (devcontainer, Compose stack, kind cluster, Helm
chart). Older ADRs cite paths under `core/engine/` and
`core/control-plane/`; Phase R6.6 renamed those to
`engines/engine/` and `operators/control-plane/`. The breadcrumb
in [`docs/adr/README.md`](../adr/README.md) records the
translation.

## §1 — Platform taxonomy

[ADR-0026](../adr/0026-platform-taxonomy.md) committed eight
production-code categories and four out-of-band categories. Every
module the platform ships fits into exactly one category; new
categories require a new ADR.

| Category           | Path                  | Inhabitants today                                                         |
|--------------------|-----------------------|---------------------------------------------------------------------------|
| Protocol           | `proto/`              | `spectre/driver/v1alpha1/`, `spectre/engine/v1alpha1/` (frozen schemas)   |
| Engines            | `engines/`            | `engines/engine/` (Rust)                                                  |
| Operators          | `operators/`          | `operators/control-plane/` (Go, kubebuilder)                              |
| Adapters           | `adapters/`           | `playwright/` (TS), `seleniumbase/` (Python), `curl-impersonate/` (Go)    |
| Infra services     | `infra-services/`     | reserved (catalog: ADR-0028 — `proxy-broker`, `captcha-solver`, …)        |
| SDKs               | `sdks/`               | reserved (per-language workspaces — ADR-0027)                             |
| Data platform      | `data-platform/`      | reserved (`parse/`, `transform/`, `aggregate/` — ADR-0029)                |
| Shared libs        | `shared-libs/`        | reserved (organic admission per ADR-0026 §3.8)                            |

Out-of-band (do not run in production): `tools/` (build-time and
test-time scripts; the conformance harness lives here),
`build/` (Dockerfile fragments + `versions.env`), `docs/` (this
document set + ADRs), `examples/` (sample DSL and CRs).

The four reserved production categories carry placeholder
READMEs that point to their governing ADRs. First inhabitants
land per the admission criteria recorded in
ADR-0026 §6 and the per-category ADRs (0027 / 0028 / 0029).

## §2 — Dependency DAG

The DAG below is normative
([ADR-0026](../adr/0026-platform-taxonomy.md) §5). Categories at
the top depend only on `proto/`; categories at the bottom may
depend on any category above them. Forbidden edges (e.g.,
`proto/` → `engines/`, an adapter → an operator) are review-time
violations.

```
                          proto/
                            │
              ┌────────────┴────────────┐
              ▼                         ▼
          engines/                  adapters/
              │                         │
              ├────────► sdks/ ◄────────┤   (codegen consumers)
              │                         │
              └─────────► infra-services/   (engine consumes brokers)
                            │
                            ▼
                       operators/         (operators submit to engines,
                            │              consume infra brokers)
                            ▼
                      data-platform/      (downstream of jobs:
                                            parse → transform → aggregate)
                            │
                            ▼
                       shared-libs/       (cross-cutting, dependency-free)
```

Read the DAG top-down for "who depends on whom". The two control
edges that matter at runtime: an operator submits a `RunJob` to
an engine over gRPC; an engine dials adapter endpoints over gRPC.
Everything else is build-time consumption (codegen, library use)
or post-hoc consumption (data-platform reads sink output).

## §3 — Today's inhabitants

### Protocol — `proto/spectre/`

Two protobuf packages, both frozen at `v1alpha1`:

- `driver/v1alpha1/driver.proto` — the Driver Protocol
  ([ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md));
  six unary RPCs (Initialize / Navigate / Query / Extract /
  Screenshot / Close); capability negotiation; canonical
  `DriverError` envelope.
- `engine/v1alpha1/engine.proto` — the Engine RPC consumed by
  the operator (`RunJob`, streaming `RunJobResponse` rows); see
  [ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md).

Code generation is per-language and per-consumer at v1alpha1
(every consuming Dockerfile re-runs `buf generate`); the
[shared `buf-base` codegen base image](container-images.md) cuts
the duplicated apt+npm install down to one layer per
multi-platform shard.
[ADR-0027](../adr/0027-sdk-strategy.md) commits the future
trajectory: per-language SDK packages under
`sdks/<lang>/driver/v<version>/` will own codegen once the first
SDK migration lands.

### Engine — `engines/engine/` (Rust)

Owns the DSL pipeline (lexer → parser → type checker → planner)
and the engine-side runtime: capability matcher, RPC scheduler,
adapter dialer, sink writers. Speaks `engine.v1alpha1` as a
gRPC server on `:9090` by default
([ADR-0021](../adr/0021-service-discovery.md) §5).

Persists job state in PostgreSQL via a compile-time-checked
`sqlx` query layer
([ADR-0023](../adr/0023-stateful-services-architecture.md) +
the Phase R4.2 update). Produces row events to four output
sinks: stdout (default), Kafka (R4.4), S3 (R5.1), HTTP webhook
(R5.1 — see [ADR-0024](../adr/0024-output-sinks.md)).

Built as a single static musl binary on `distroless/static:nonroot`;
the published image lives at `fabiocaffarello/spectre-engine`.

### Operator — `operators/control-plane/` (Go, kubebuilder)

Reconciles the `ScrapeJob` CRD (v1alpha2 only — v1alpha1 retired
in R3.2; see
[ADR-0019](../adr/0019-control-plane-architecture-and-scrapejob-crd.md)).
Submits each Pending job to an engine via gRPC `RunJob` and
streams row events to Kubernetes status + Postgres reads through
`pgx/v5`. The Go module path is
`github.com/FabioCaffarello/spectre/operators/control-plane`.

Built as a static Go binary on `gcr.io/distroless/static:nonroot`;
the published image lives at `fabiocaffarello/spectre-control-plane`.

### Adapters — `adapters/` (three reference adapters)

Three production-shaped adapters validate the Driver Protocol
across three runtimes and three languages (ADR-0004's "three
distinct ecosystems" criterion):

- **`adapters/playwright/`** (TypeScript / Node 22) — Playwright
  Chromium (with Firefox/WebKit available behind capability
  toggles); 13-capability declaration; the protocol's superset
  reference.
- **`adapters/seleniumbase/`** (Python 3.12 + Chrome 147) —
  SeleniumBase including UC Mode (CDP-driven); 12-capability
  declaration (`screenshot_full_page` deliberately absent —
  ADR-0015 §5).
- **`adapters/curl-impersonate/`** (Go via `os/exec`, no cgo —
  ADR-0016 §1) — HTTP-only, no JS execution, no screenshots;
  6-capability declaration.

Each adapter ships a `driver.yaml` manifest, a `Dockerfile`, and
a per-platform CI image build. The three published images are
`fabiocaffarello/spectre-{playwright,seleniumbase,curl-impersonate}-adapter`.

The capability divergence (13 / 12 / 6) is a load-bearing test
of the contract: capability declaration is a cross-driver
semantic-equivalence promise rather than a feasibility statement
([ADR-0017](../adr/0017-curl-impersonate-extraction-and-final-capability-divergence.md)
§1).

### Reserved categories

`infra-services/`, `sdks/`, `data-platform/` (with `parse/` /
`transform/` / `aggregate/` subdirectories), and `shared-libs/`
exist as placeholder directories with READMEs at v1alpha1.
First inhabitants are gated on the admission criteria in their
governing ADRs.

## §4 — Runtime topology (Compose stack)

Phase R6.3 unified the development environment around `docker
compose up --profile full` running the eleven-service stack
inside the devcontainer (operator-as-Compose-service joining a
Docker-in-Docker `kind` external network — see
[ADR-0025](../adr/0025-compose-stack.md) §6 + §9).

The eleven services:

| Service                       | Image                                                          | Purpose                                               |
|-------------------------------|----------------------------------------------------------------|-------------------------------------------------------|
| `engine`                      | `spectre-engine:dev`                                           | gRPC server on 9090; Driver Protocol orchestrator      |
| `playwright-adapter`          | `spectre-playwright-adapter:dev`                               | gRPC server on 9091                                    |
| `seleniumbase-adapter`        | `spectre-seleniumbase-adapter:dev`                             | gRPC server on 9092                                    |
| `curl-impersonate-adapter`    | `spectre-curl-impersonate-adapter:dev`                         | gRPC server on 9093                                    |
| `control-plane`               | `spectre-control-plane:dev` (in `--profile full`)              | reconciles ScrapeJobs in the in-Compose `kind` cluster |
| `postgres`                    | `postgres:16-alpine`                                           | engine job persistence                                 |
| `redis`                       | `redis:7-alpine`                                               | adapter session cache                                  |
| `kafka`                       | KRaft-mode Kafka                                               | row event sink (R4.4)                                  |
| `redpanda-console`            | `docker.redpanda.com/redpandadata/console`                     | Kafka observability                                    |
| `minio`                       | `minio/minio`                                                  | S3-compatible row sink (R5.1)                          |
| `webhook-debug`               | `mendhak/http-https-echo`                                      | webhook target for sink debugging                      |

Profiles partition the stack: `--profile minimal` brings up
engine + adapters; `--profile stateful` adds Postgres + Redis +
Kafka + MinIO; `--profile full` adds the control plane and the
in-Compose `kind` cluster.

The devcontainer image (`build/docker/devcontainer.Dockerfile`)
includes `docker` + `docker-compose` clients and joins the
external `kind` network so a contributor running "Reopen in
Container" gets a working stack without host-side setup. See
[`development-environment.md`](development-environment.md).

## §5 — Build and image story

The image build orchestrator is `docker-bake.hcl` at the repo
root. Bake compiles five published images via the shared
`buf-base` codegen base introduced in R6.5.4
([ADR-0018](../adr/0018-devcontainer-and-engine-image.md) §3
R6.5.4 update; see
[`build/docker/README.md`](../../build/docker/README.md)):

| Image                                   | Base                                          | Multi-arch?                              |
|-----------------------------------------|-----------------------------------------------|------------------------------------------|
| `spectre-engine`                        | musl static + `distroless/static:nonroot`     | linux/amd64 only (R6.5.3 deferral)       |
| `spectre-control-plane`                 | Go static + `distroless/static:nonroot`       | linux/amd64 + linux/arm64                |
| `spectre-playwright-adapter`            | Microsoft Playwright base (digest-pinned)     | linux/amd64 + linux/arm64                |
| `spectre-seleniumbase-adapter`          | Playwright base + uv-built Python venv        | linux/amd64 only (Chromium switch TBD)   |
| `spectre-curl-impersonate-adapter`      | distroless + curl-impersonate variant binaries| linux/amd64 only (runtime base TBD)      |

The three single-arch images carry per-image deferral notes in
ADR-0018 §5 (R6.5.3 update). Each defers for a specific reason
(engine's Rust musl cross-compile path; seleniumbase's Chromium
arm64 availability; curl-impersonate's runtime base on arm64).
Each unblocks under its own focused PR post-refactor; the
v1alpha1 Helm chart sets `nodeSelector: kubernetes.io/arch:
amd64` for the three until then per ADR-0030 §6.4.

The `versions.env` file at `build/docker/versions.env` pins
every base image and tool version; the
`tools/build/check-versions-coherent.sh` script asserts the
single source of truth across all five Dockerfiles + the
devcontainer + the `buf-base` (R6.5.4) shared base.

## §6 — CI shape

R6.5.x committed three CI invariants:

- **Matrix `images` job (R6.5.2)** — every image builds on every
  PR via the bake matrix. Catches drift between Dockerfile / pin
  / `buf-base` invariants atomically.
- **`full-stack` gate (R6.5.2)** — assembles the eleven-service
  Compose stack and asserts the operator reconciles a sample
  ScrapeJob to `Completed`. The end-to-end signal that path
  flips and dependency changes do not break the runtime.
- **`publish-dry-run` (R6.5.3)** — exercises the multi-arch
  publish path (driver-amd64 + arm64 builders → multi-arch
  manifest) without pushing. Catches multi-arch drift before
  release time.

The `proto-check` workflow guards the protobuf schemas (buf
lint + breaking-change detection); the `codeql` workflow scans
the Go and Rust source. Per-language quality (rustfmt + clippy
+ cargo test; golangci-lint + go test; ruff + pytest;
prettier + tsc + jest) runs in the language-specific job.

## §7 — Stateful services

[ADR-0023](../adr/0023-stateful-services-architecture.md)
committed Postgres + Redis + Kafka as the platform's stateful
core; [ADR-0024](../adr/0024-output-sinks.md) added S3 (MinIO in
dev) + HTTP webhook as the post-job sink targets. Each service
has a single canonical purpose:

- **PostgreSQL (`postgres:16-alpine`)** — engine job persistence
  (`jobs`, `job_rows`, `adapter_instances` tables). Migrations
  embedded in the engine binary; `sqlx::migrate!` runs on startup.
  Operator reads (`pgx/v5`) for status backfill on restart.
- **Redis (`redis:7-alpine`)** — adapter session cache
  (per-session metadata, capability hints, deduplication keys).
- **Kafka (KRaft single-broker)** — row event sink for streaming
  consumers. Redpanda Console exposes a UI on a dev-only port.
- **MinIO** — S3-compatible object storage; engine writes
  partitioned NDJSON to a configured bucket per
  ADR-0024 §3.
- **Webhook target** — `webhook-debug` echoes posted bodies; the
  engine POSTs row batches per ADR-0024 §4.

Production deployments swap MinIO for AWS S3 (or compatible),
the in-cluster Postgres for a managed instance, the in-cluster
Redis for ElastiCache or equivalent, and the single-broker Kafka
for a managed cluster. The configuration surface — per-sink env
vars, Postgres / Redis / Kafka connection strings — is identical.

## §8 — Forward-looking categories

Phase R6.6 reserved four categories with placeholder READMEs.
Each has a governing ADR and explicit admission criteria; first
inhabitants land in v1alpha2.

- **`infra-services/`** —
  [ADR-0028](../adr/0028-ancillary-infra-services-catalog.md)
  catalogues five named slots: `proxy-broker` (high-conviction —
  every production scrape needs one), `captcha-solver`
  (high-conviction); `fingerprint-broker`, `session-store`,
  `rate-limit-broker` (probable). The canonical shape is proto +
  N providers + per-language SDKs + Compose service +
  Helm chart presence. Admission gate: ≥1 consumer + ≥2
  providers + proto + SDKs + deployment posture.
- **`sdks/`** —
  [ADR-0027](../adr/0027-sdk-strategy.md) commits to per-language
  workspaces at `sdks/<lang>/`; per-protocol-version package at
  `<protocol>/<version>/`. Codegen ownership moves into each SDK
  package; consumers stop running `buf generate` per Dockerfile.
  ADR-0007 §2 / §3 carry an in-place evolution note recording
  the trajectory.
- **`data-platform/`** —
  [ADR-0029](../adr/0029-data-platform-and-lake-dsls.md)
  commits to a four-layer medallion model (L0 raw / L1 bronze /
  L2 silver / L3 gold) and three stages (`parse/`, `transform/`,
  `aggregate/`). Up to three layer-transition DSLs are reserved
  per criteria recorded in ADR-0029 §3. The engine's existing
  job DSL ([ADR-0012](../adr/0012-engine-dsl-and-execution-pipeline.md))
  is preserved as the L0 entry DSL.
- **`shared-libs/`** — lightweight admission per ADR-0026 §3.8;
  emerges organically when cross-cutting copy-paste pressure
  crosses thresholds. No reserved slots; first inhabitant gets
  reviewed against the §3.8 contract.

The placeholder READMEs are pointers, not specs: each ~15-30
line README cites the governing ADR and lists what the category
will hold. New contributors see the shape of the platform's
growth without reading every ADR.

## §9 — What stays the same

Phase R6.6 was a structural restructure, not a feature change.
The contracts the platform is built on are preserved
byte-for-byte:

- **Driver Protocol primitive** —
  [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md);
  six unary RPCs, capability negotiation, canonical `DriverError`.
- **Capability divergence** — the three reference adapters
  declare 13 / 12 / 6 capabilities; the conformance suite asserts
  each list byte-for-byte
  ([ADR-0017](../adr/0017-curl-impersonate-extraction-and-final-capability-divergence.md)
  §1).
- **Proto-as-source-of-truth** —
  [ADR-0001](../adr/0001-driver-protocol-as-architectural-primitive.md) +
  [ADR-0007](../adr/0007-protocol-code-generation.md); the
  protobuf schemas at `proto/spectre/` are the single source.
  Generated bindings remain gitignored.
- **No-legacy commitment** — the master strategy prompt's §2.2;
  R6.6's fossil sweep is the most aggressive expression of this
  principle (`docs/MASTER_PROMPT.md`, repo-root `MEMORY.md`,
  `/memory/`, and `.claude/` runtime artifacts removed).
- **Conformance is the gate** — the suite at `tools/conformance/`
  passes 49 / 14 (skipped) across the three reference adapters
  before any merge to `proto/` ([ADR-0014](../adr/0014-seleniumbase-adapter-and-cross-language-conformance.md)).

## §10 — References

- [Driver Protocol deep dive](driver-protocol.md)
- [Control plane](control-plane.md)
- [Engine](engine.md)
- [Container images](container-images.md)
- [Development environment](development-environment.md)
- [Postgres](postgres.md) / [Kafka](kafka.md) / [Redis](redis.md)
- [Output sinks](output-sinks.md)
- [Releases](releases.md)
- [Roadmap](../roadmap.md)
- [Architecture Decision Records](../adr/README.md)

Per-category ADR pointers:

- `proto/` — ADR-0001, ADR-0007 (codegen), ADR-0027 (SDK
  evolution)
- `engines/` — ADR-0012 (DSL), ADR-0023 (stateful services),
  ADR-0024 (sinks)
- `operators/` — ADR-0019, ADR-0020 (microservices supersession)
- `adapters/` — ADR-0014 (SeleniumBase), ADR-0015 (SB
  capability), ADR-0016 (curl-impersonate), ADR-0017 (capability
  divergence)
- `infra-services/` — ADR-0028
- `sdks/` — ADR-0027
- `data-platform/` — ADR-0029
- `shared-libs/` — ADR-0026 §3.8

The platform taxonomy ADR ([ADR-0026](../adr/0026-platform-taxonomy.md))
is the index — every category is governed there, and the
admission criteria for new categories live in §6.

## §11 — v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe the
> v1alpha1 platform overview as it existed at refactor close
> (R8.1, `v0.1.0-alpha.0`). Phase R9 (R9.1 – R9.8) opens the
> v1alpha2 architectural foundation; this subsection
> forwards readers to the v1alpha2 surface.*

v1alpha2 expands the platform from 4 inhabited categories
(today's `proto` + `engines` + `operators` + `adapters`) to
**all eight production-code categories** ADR-0026 reserves —
`infra-services/` materialises as 14 services across
Waves 5 – 10, `sdks/` populates per ADR-0027's admission gate
as services consume protocols in their language, and
`data-platform/` evolves per ADR-0029's lake-layer model.

The umbrella v1alpha2 architectural surface lives at
[`platform-architecture.md`](platform-architecture.md). For
the operational shape:

- **Service catalog** — [`service-catalog.md`](service-catalog.md)
  + [ADR-0036](../adr/0036-microservices-catalog-expansion.md)
- **Canonical service shape** — [`service-shape.md`](service-shape.md)
- **Engine evolution** — [`engine-orchestrator.md`](engine-orchestrator.md)
  + [ADR-0037](../adr/0037-engine-as-orchestrator.md)
- **Storage tiers** (adds MongoDB) —
  [`storage-tiers.md`](storage-tiers.md) +
  [ADR-0023 §14](../adr/0023-stateful-services-architecture.md)
  + [ADR-0039](../adr/0039-mongodb-third-storage-tier.md)
- **DSL evolution** — [`dsl-evolution.md`](dsl-evolution.md)
  + [ADR-0035](../adr/0035-dsl-evolution-driver-abstraction.md)
- **Observability** — [`observability.md`](observability.md)
  + [ADR-0031](../adr/0031-observability-framework.md)
- **mTLS** —
  [ADR-0032](../adr/0032-service-to-service-mtls.md)

The Wave 1 – 12 plan lives in
[`docs/roadmap.md`](../roadmap.md) §4 (rewritten in R9.7);
per-PR phase history in
[`docs/v1alpha2-audit.md`](../v1alpha2-audit.md) (R9.8).
