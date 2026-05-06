# Helm chart

The Helm chart at [`build/helm/spectre/`](../../build/helm/spectre/)
packages Spectre's v1alpha1 stack for installation into any
conformant Kubernetes 1.27+ cluster. It is the production-deployment
counterpart to the Compose stack documented in
[development-environment.md](development-environment.md): the same
runtime topology, the same per-service env vars, exposed through
Kubernetes primitives instead of Compose primitives. The
architectural decisions live in
[ADR-0030](../adr/0030-helm-chart-structure.md); this page is the
operator-facing companion.

## §1 — Purpose

R7.1 ships the chart so that any contributor with a v1alpha1
target cluster can `helm install spectre` and submit a
`ScrapeJob` end-to-end without writing Kubernetes manifests by
hand. The chart bundles:

- The five project services — engine, control-plane operator,
  Playwright adapter, SeleniumBase adapter, curl-impersonate
  adapter.
- The four stateful upstream services — Postgres, Redis, Kafka,
  MinIO — via pinned Bitnami subcharts.
- The v1alpha2 `ScrapeJob` CRD via Helm 3's `crds/` directory.
- ServiceAccount + RBAC for the operator translated from the
  kubebuilder scaffolds.

The chart is the **deployment artifact** for v1alpha1. R7.2
added an end-to-end CI gate (`production-smoke`) that installs
it against a real `kind` cluster and submits sample `ScrapeJob`s
to `Completed` against three sinks (kafka, s3, webhook); R7.1's
structural CI gate (`helm lint`, `helm template` + `kubeval`,
and `helm install --dry-run`) runs alongside.

## §2 — Structure

```
build/helm/spectre/
├── .gitignore                  charts/ — auto-populated subchart cache
├── .helmignore                 packaging exclusions
├── Chart.yaml                  chart metadata + 4 Bitnami deps pinned
├── Chart.lock                  resolved subchart digests (committed)
├── README.md                   contributor-facing chart docs
├── values.yaml                 default values, fully commented
├── values.schema.json          JSON Schema draft-07
├── crds/
│   └── scrapejob.yaml          v1alpha2 CRD (mirror of operator's source)
└── templates/
    ├── _helpers.tpl            named templates (labels, image, envs)
    ├── NOTES.txt               post-install message
    ├── engine.yaml             engine Deployment + Service
    ├── control-plane.yaml      operator Deployment + Service
    ├── rbac.yaml               operator ServiceAccount + Cluster/Role
    ├── playwright-adapter.yaml      adapter Deployment + Service
    ├── seleniumbase-adapter.yaml    adapter Deployment + Service
    └── curl-impersonate-adapter.yaml adapter Deployment + Service
```

The `charts/` cache is gitignored (large, reproducible from
`Chart.lock` via `helm dep update`). The chart's source files —
everything else — are source-controlled.

## §3 — Compose-to-Helm correspondence

The chart mirrors the Compose service topology so a contributor
who knows the Compose layout finds the chart predictable. The
mapping:

| Compose service | Helm template file | Values key | Container port |
|-----------------|--------------------|------------|----------------|
| `engine` | `templates/engine.yaml` | `engine.*` | 8090 (gRPC) |
| `control-plane` | `templates/control-plane.yaml` + `rbac.yaml` | `controlPlane.*` | 8081 (HTTP probes) |
| `playwright-adapter` | `templates/playwright-adapter.yaml` | `playwrightAdapter.*` | 8091 (gRPC) |
| `seleniumbase-adapter` | `templates/seleniumbase-adapter.yaml` | `seleniumbaseAdapter.*` | 8092 (gRPC) |
| `curl-impersonate-adapter` | `templates/curl-impersonate-adapter.yaml` | `curlImpersonateAdapter.*` | 8093 (gRPC) |
| `postgres` | Bitnami subchart | `postgresql.*` | 5432 |
| `redis` | Bitnami subchart | `redis.*` | 6379 |
| `kafka` | Bitnami subchart | `kafka.*` | 9092 |
| `minio` | Bitnami subchart | `minio.*` | 9000 |

The `_helpers.tpl` `spectre.commonEnv` named template synthesises
the connection envs (`SPECTRE_POSTGRES_URL`, `SPECTRE_REDIS_URL`,
`SPECTRE_KAFKA_BROKERS`, `SPECTRE_S3_*`) from the Bitnami
subchart service-name conventions plus the release name. When a
subchart is disabled (`<name>.enabled: false`), the corresponding
env is omitted; the user supplies it via `<service>.extraEnv`.

## §4 — Stateful dependencies

Four Bitnami subcharts pinned via `Chart.yaml`'s `dependencies`:

| Subchart | Version | App version |
|----------|---------|-------------|
| `postgresql` | `16.0.0` | Postgres 17.0.0 |
| `redis` | `19.6.0` | Redis 7.2.5 |
| `kafka` | `30.0.0` | Kafka 3.8.0 |
| `minio` | `14.7.0` | MinIO 2024.8.3 |

`Chart.lock` is committed alongside `Chart.yaml`; reproducible
materialisation requires both. Subchart bumps require an
amendment to ADR-0030 §4 — Bitnami's yank cadence is faster
than the project's release cadence, and the discipline defends
against silent dependency drift.

Each subchart can be disabled via `<name>.enabled: false` to
connect to an externally-managed instance instead. Disabling is
an opt-in escape hatch, not the default.

## §5 — Image references

Defaults render as `<image.registry>/<service>.image.repository:<tag>`
where `tag` falls back to `.Chart.AppVersion` when empty:

```
docker.io/fabiocaffarello/spectre-engine:0.1.0-alpha.0
docker.io/fabiocaffarello/spectre-control-plane:0.1.0-alpha.0
docker.io/fabiocaffarello/spectre-playwright:0.1.0-alpha.0
docker.io/fabiocaffarello/spectre-seleniumbase:0.1.0-alpha.0
docker.io/fabiocaffarello/spectre-curl-impersonate:0.1.0-alpha.0
```

The `spectre.image` named template produces these references; no
template hand-rolls image-string concatenation. Override paths
(local builds, private registries, per-service tag pinning) are
documented in `values.yaml` and the chart README.

R6.5.3 wired the publish workflow as `workflow_dispatch` only;
R7.1 marks the **first real publish** of these tags to Docker
Hub (recorded in [releases.md](releases.md)).

## §6 — Multi-arch handling

Two of five images publish multi-arch (manifest list with
`linux/amd64` + `linux/arm64`); three are amd64-only. The chart
honours the asymmetry honestly via `nodeSelector`:

| Service | Multi-arch | Default `nodeSelector` |
|---------|------------|-----------------------|
| `spectre-control-plane` | ✅ | none |
| `spectre-playwright` | ✅ | none |
| `spectre-engine` | ❌ amd64 only | `kubernetes.io/arch: amd64` |
| `spectre-seleniumbase` | ❌ amd64 only | `kubernetes.io/arch: amd64` |
| `spectre-curl-impersonate` | ❌ amd64 only | `kubernetes.io/arch: amd64` |

The fallback case (no `nodeSelector` constraint) on
amd64-only services would let Kubernetes schedule the Pod
anywhere, then fail with `ImagePullBackOff` on arm64 nodes.
`nodeSelector` surfaces the constraint upfront with an
actionable scheduler message. Each amd64-only service's unblock
path is recorded in [ADR-0018 §5 R6.5.3
update](../adr/0018-devcontainer-and-engine-image.md); when the
unblock lands, the corresponding `nodeSelector` line is deleted
from `values.yaml` and ADR-0030 §6's table updates.

## §7 — Probes

[ADR-0025 §3](../adr/0025-compose-stack.md) committed the engine
+ adapters to use Kubernetes-native `grpc:` probes when R7.1
landed (Kubernetes 1.27+ stable; no in-image
`grpc_health_probe` binary required). The chart honours that
commitment:

- **Engine, three adapters** — `grpc:` readinessProbe +
  livenessProbe.
- **Control plane** — `httpGet` against `/readyz` (readiness)
  and `/healthz` (liveness) on the manager's
  `--health-probe-bind-address` port. controller-runtime serves
  these natively.

`initialDelaySeconds` reflects realistic warm-up:
- Engine, control-plane, curl-impersonate: short (5–15 s).
- Playwright, SeleniumBase: longer (15 / 60 s) — browser
  launch is slow.

## §8 — CRD lifecycle

The chart ships `crds/scrapejob.yaml` as a byte-for-byte copy of
`operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml`,
the controller-gen-generated source of truth. Helm 3 installs
files under `crds/` once at install time and does not template
them.

The trade-off: `helm upgrade` does **not** update CRDs. v1alpha1
ships a stable CRD; the upgrade story for a future shape change
is documented as a manual `kubectl apply` step in the chart
README.

The `chart-check-crd-sync` justfile recipe (Cluster G) and a
matching CI job prevent drift between the chart's copy and the
operator's source: a PR that touches the CRD shape but forgets
to resync the chart fails fast.

## §9 — CI integration

The chart is exercised by two CI gates with complementary
surfaces.

### §9.1 — `helm-lint` (R7.1, `ci.yml`)

The `helm-lint` job in `.github/workflows/ci.yml` runs on
every PR that touches:

- `build/helm/**`
- `operators/control-plane/config/crd/bases/**` (CRD-sync
  invariant)
- `.github/workflows/ci.yml`

The job runs `helm dependency update`, `helm lint --strict`,
and `helm template` + `kubeval` against the default values
set. It is **structural** validation only — fast, runs on
every relevant PR.

(`helm install --dry-run` is deliberately omitted: even with
`--dry-run=client` Helm 3.13's install path still probes the
configured kubeconfig's apiserver for admission validations,
and CI's runner has no cluster. Maintainer-side verification
that `helm install` works end-to-end goes through the
`just chart-install-smoke` recipe against a local kind
cluster.)

### §9.2 — `production-smoke` (R7.2, standalone workflow)

The `production-smoke` workflow at
`.github/workflows/production-smoke.yml` installs the chart
into a real kind cluster, applies three reference ScrapeJobs
(kafka, s3, webhook sinks), and asserts row events arrive at
each sink. End-to-end correctness through the sink boundary.

Three triggers: `workflow_dispatch` (manual),
`pull_request` (paths-filtered to source + chart + test
fixtures + the workflow itself), and a daily 06:00 UTC
`schedule` for drift detection.

Standalone file (not part of `ci.yml`) because the three
triggers have different semantics from `ci.yml`'s per-PR
aggregation; keeping them separate avoids cluttering
`ci-summary` with R7.2's slow gate.

Local reproduction:

```bash
just chart-smoke-up
just chart-smoke-test
just chart-smoke-down
```

See [docs/architecture/production-smoke.md](production-smoke.md)
for the full flow, debugging guide, and list of what the
gate intentionally does and doesn't assert.

## §10 — Out of scope for R7.1

R7.1 ships chart packaging. The following are deliberately
deferred:

- **Production smoke** (Helm install in CI + sample ScrapeJobs
  to `Completed`) — R7.2.
- **Helm chart publish to an OCI registry**
  (`oci://docker.io/fabiocaffarello/charts/spectre`) —
  post-refactor.
- **Per-infra-service Helm chart fragments** (proxy-broker,
  captcha-solver, etc.) — each lands with its own slot's build
  PR per [ADR-0028 §6](../adr/0028-ancillary-infra-services-catalog.md).
- **Multi-arch unblocks** for engine / seleniumbase /
  curl-impersonate — separate per-image PRs.
- **Migration scripts for v1alpha2 → vNext CRD upgrade** —
  documented as a future concern in ADR-0030 §8.

## §11 — v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe the
> R7.1-shipped Helm chart at `build/helm/spectre/` packaging
> the v1alpha1 stack. Phase R9 commits to extending the
> chart with per-service fragments + a Mongo subchart; this
> subsection forwards readers to the v1alpha2 surface.*

The chart's **single-chart-with-named-templates** posture
per §3 is **preserved** through v1alpha2 — first-party
services land as **chart fragments** under
`build/helm/spectre/templates/`, not as subcharts. Each
new infra-service contributes:

- `templates/<slot>.yaml` + `<slot>-rbac.yaml` +
  `<slot>-config.yaml` + `<slot>-cert.yaml`
- A `<slotCamelCase>:` block in `values.yaml` matching the
  existing `engine:` / `controlPlane:` shape

Per
[ADR-0036 §5.2](../adr/0036-microservices-catalog-expansion.md)
+ [`service-shape.md` §3](service-shape.md). The
`_helpers.tpl` named-template helpers extend across
fragments.

What v1alpha2 adds at the subchart layer:

- **Mongo subchart** — Bitnami `mongodb` pinned per
  ADR-0023 §14 + ADR-0030's pinning policy. Lands in
  Wave 6 with the first Mongo-backed services
  (schema-registry + input-broker).
- **`cert-manager.enabled` flag** — default-off; flag-on
  enables service-to-service mTLS uniformly per
  [ADR-0032 §2.4](../adr/0032-service-to-service-mtls.md).
  The `_helpers.tpl` certificate template (§3.4 of
  ADR-0032) materialises in Wave 3.
- **OpenTelemetry collector subchart reference** —
  optional; deployments without an existing collector wire
  one in. Lands in Wave 3 per ADR-0031 §2.2.

The CRD lifecycle per §8 + ADR-0030 §8 extends to
ScrapeBatch (added in Wave 6 per ADR-0033). The R8.1 CRD
upgrade procedure carries forward unchanged.

For the operational walkthrough see
[`platform-architecture.md`](platform-architecture.md).
