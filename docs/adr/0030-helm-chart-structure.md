---
status: accepted
date: 2026-05-02
deciders: [Fabio Caffarello]
---

# Helm chart structure (R7.1)

## §1 — Context and Problem Statement

R6.6 closed the platform-maturation phase. The taxonomy
(ADR-0026), the SDK strategy (ADR-0027), the ancillary
infra-services catalog (ADR-0028), and the data-platform
layering (ADR-0029) all landed; `core/` dissolved into
`engines/engine/` and `operators/control-plane/`; the four
placeholder directories materialised. The repository is
structurally ready for production posture work.

R7 is the production-posture phase. R7.1 (this ADR's PR)
ships the **Helm chart** that packages the v1alpha1 stack —
the engine, three driver adapters, the control-plane
operator, and the four stateful dependencies (Postgres,
Redis, Kafka, MinIO) — into something a contributor can
`helm install` against any conformant Kubernetes 1.27+
cluster. R7.2 will follow with production smoke (Helm
install + sample ScrapeJobs to `Completed` against a real
cluster, asserted in CI). R7.1 is **packaging**; R7.2 is
**verification**.

The chart's location, structure, and per-decision rationale
are the architectural questions R7.1 must settle. ADR-0026 §1
(note on numbering) explicitly deferred those questions:

> ADR-0025 §10 reserves "ADR-0026 (when drafted)" for Helm
> chart packaging at R7.1. That reservation was implicit, not
> authoritative — no draft existed. Phase R6.6 takes 0026
> because Platform Maturation precedes Helm packaging in the
> revised phase order. Helm packaging will pick up the next
> free number when R7.1 resumes.

Two further forward commitments tie the ADR to choices made
upstream:

- **ADR-0025 §3** committed the engine and adapters to use
  Kubernetes-native `grpc:` readiness/liveness probes when
  R7.1 ships. R6.2 declined to bundle `grpc_health_probe` in
  the Compose images precisely so R7.1 could rely on
  Kubernetes 1.27+ probe semantics rather than per-image
  binary plumbing.
- **ADR-0028 §6** committed each future infra-service to
  ship its own Helm chart **fragment** (a small bundle
  alongside the service's own build PR), composing into the
  primary chart that R7.1 establishes. The chart structure
  R7.1 lands has to be additive enough to absorb those
  fragments without churn.

Plus one constraint that surfaced during R6.5.3:

- **ADR-0018 §5 R6.5.3 update** records the multi-arch
  state of the five published images: `control-plane` and
  `playwright` ship as multi-arch manifest lists
  (linux/amd64 + linux/arm64); `engine`, `seleniumbase`, and
  `curl-impersonate` are amd64-only with explicit unblock
  paths. R7.1's chart has to render the asymmetry honestly:
  amd64-only services need a scheduling constraint or they
  will pull-fail on arm64 nodes with cryptic
  `ImagePullBackOff` rather than an actionable
  `nodeSelector` mismatch.

This ADR resolves the deferred questions in eight related
decisions (§2). The decisions are tightly coupled — the
chart's location implies the layout convention; the layout
convention implies how subcharts are pinned; the pinning
implies the upgrade story; the multi-arch posture implies
the values surface; and so on. Splitting them into eight
ADRs would obscure the coupling.

R7.1 also marks the **first real publish** of the project's
container images to Docker Hub. R6.5.3 wired the publish
workflow but never triggered it (no tags existed; the repo
had no manifests on `docker.io/fabiocaffarello/spectre-*`).
R7.1's chart references those images by tag, so the publish
cannot remain hypothetical. The chart's
`fabiocaffarello/spectre-<name>:0.1.0-alpha.0` defaults are
**load-bearing** — they must point at real manifests by the
time the PR merges.

## §2 — Decision summary

R7.1 commits to:

1. **Chart location at `build/helm/spectre/`.** §3 details the
   placement rationale. The chart is production-deployment
   tooling — out-of-band relative to ADR-0026's eight
   production categories; lands inside the established
   `build/` slot.

2. **Single chart with named-template helpers.** §3 details
   the layout. The five project services (engine + three
   adapters + control-plane) are templated under
   `templates/`, with cross-cutting concerns (labels, image
   refs, common envs) factored into `_helpers.tpl`. No
   subcharts for project services. Stateful dependencies
   (Postgres, Redis, Kafka, MinIO) **are** subcharts via
   pinned upstream Bitnami releases.

3. **Subchart pinning policy.** §4 details the rule. Each
   `dependencies:` entry in `Chart.yaml` pins to an exact
   version. `Chart.lock` is committed. Bumps require an
   amendment to this ADR.

4. **Image references default to `docker.io/fabiocaffarello/spectre-<name>:<chart appVersion>`.**
   §5 details the override paths. The chart's `appVersion`
   tracks the repository's `VERSION` file; the chart's
   `image.registry` is overridable for private-registry,
   local-build (kind), and external-image deployments.

5. **Multi-arch posture via `nodeSelector` for amd64-only
   services.** §6 details the table and unblock criteria.
   `engine`, `seleniumbase-adapter`, and
   `curl-impersonate-adapter` carry
   `nodeSelector: kubernetes.io/arch: amd64`;
   `control-plane` and `playwright-adapter` ship without a
   selector and schedule on any arch the cluster offers.

6. **Native gRPC probes for engine + adapters; HTTP probes
   for the operator.** §7 details the rationale. The chart
   does not bundle `grpc_health_probe` in any image; it
   relies on Kubernetes 1.27+ `grpc:` probe stability.
   Kubernetes 1.27+ is documented as a chart prerequisite.

7. **CRD shipped via the Helm 3 `crds/` directory.** §8
   details the lifecycle. Helm 3 installs CRDs from `crds/`
   at install time only — `helm upgrade` does not update
   CRDs. The chart documents the trade-off and records the
   v1alpha2 → future-CRD upgrade procedure as a known
   future concern.

8. **Chart version pinned to the repository's `VERSION` file.**
   `Chart.yaml`'s `version:` and `appVersion:` both equal the
   `VERSION` file's content. Bumping `VERSION` bumps both. R7.1
   ships at `0.1.0-alpha.0`. A future VERSION bump (separate
   PR) updates `Chart.yaml` accordingly.

## §3 — Chart structure

### §3.1 — Location

The chart lives at `build/helm/spectre/`.

`build/` is one of ADR-0026 §3.9's four out-of-band
categories: directories that exist for contributor support,
are not part of the production dependency DAG, and are not
deployed to runtime hosts. The Helm chart fits this scope
naturally — it is the artifact a deployer applies, but it is
not itself runtime code. ADR-0026 §3.9 documents `build/`'s
contents as "container build infrastructure" but does not
preclude additional out-of-band tooling under the same slot.
The chart joins `build/docker/` and `build/kind/` as a
sibling subdirectory.

Three alternatives were considered and rejected:

- **`deploy/` as a new top-level directory.** Would require
  amending ADR-0026 §3 to add a 13th category (a 5th
  out-of-band slot). The maturity bar in ADR-0026 §6
  ("admitting a new category requires demonstrated need
  across multiple consumers") is not met for a single
  chart. Reject.
- **`charts/` as a new top-level directory.** Same
  category-admission concern as `deploy/`; further, `charts/`
  is the conventional name for a Helm chart's own subchart
  cache (auto-populated by `helm dep update`), so a top-level
  `charts/` would invite confusion. Reject.
- **`operators/control-plane/dist/chart/` per the kubebuilder
  Helm plugin convention.** Kubebuilder's plugin scaffolds
  charts that bundle **only** the operator. Spectre's chart
  bundles the operator plus the engine plus three adapters
  plus four stateful subcharts; the kubebuilder convention
  doesn't fit. Reject.

`build/helm/spectre/` it is. The directory name is
deliberately `spectre/` (the chart's name) rather than
`chart/` (a generic placeholder), so future infra-service
chart fragments per ADR-0028 §6 will land as siblings:
`build/helm/proxy-broker/`, `build/helm/captcha-solver/`,
etc. The `build/helm/` umbrella becomes the project's
chart-distribution slot.

### §3.2 — Files

```
build/helm/spectre/
  .gitignore                  # one line: charts/
  .helmignore                 # standard exclusions
  Chart.yaml                  # chart metadata + dependencies
  Chart.lock                  # subchart digest pins (committed)
  README.md                   # contributor-facing chart README
  values.yaml                 # default values, fully commented
  values.schema.json          # JSON Schema validation
  charts/                     # gitignored — populated by `helm dep update`
  crds/
    scrapejob.yaml            # copy of operators/...spectre.io_scrapejobs.yaml
  templates/
    _helpers.tpl              # named templates (labels, image refs, common envs)
    NOTES.txt                 # post-install message
    engine.yaml               # Deployment + Service for engine
    control-plane.yaml        # Deployment + Service + ServiceAccount for operator
    rbac.yaml                 # ClusterRole(Binding), Role(Binding) for operator
    playwright-adapter.yaml   # Deployment + Service
    seleniumbase-adapter.yaml # Deployment + Service
    curl-impersonate-adapter.yaml  # Deployment + Service
```

Each file's role:

- **`Chart.yaml`** — chart metadata. `apiVersion: v2`, the
  five-line `dependencies:` block (Bitnami subcharts), name
  + version + appVersion both pinned to the `VERSION` file.
- **`Chart.lock`** — generated by `helm dep update`. Pins
  each subchart's digest. **Committed.** Without it, two
  `helm dep update` runs against the same `Chart.yaml` can
  resolve different subchart artifacts when Bitnami
  republishes.
- **`charts/`** — local cache of pulled subcharts. Populated
  by `helm dep update`; deleted by `helm dep update --skip-refresh`.
  Gitignored because it is large (multiple `*.tgz`) and
  reproducible from `Chart.lock`.
- **`values.yaml`** — the chart's API surface. Every key is
  commented inline; every key has a corresponding entry in
  `values.schema.json`.
- **`values.schema.json`** — JSON Schema (draft-07) that
  validates `--set` flags and `-f overrides.yaml` content
  at install time. The schema is what makes
  `helm install --set engine.replicas=banana` fail at the
  install boundary rather than at Pod-creation time.
- **`crds/scrapejob.yaml`** — direct copy of
  `operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml`.
  Helm 3 installs files in `crds/` once at install time
  (see §8).
- **`templates/_helpers.tpl`** — named templates:
  `spectre.fullname`, `spectre.labels`,
  `spectre.selectorLabels`, `spectre.serviceAccountName`,
  per-service image-reference helpers
  (`spectre.engineImage`, `spectre.controlPlaneImage`,
  `spectre.playwrightImage`, `spectre.seleniumbaseImage`,
  `spectre.curlImpersonateImage`), and `spectre.commonEnv`
  (database / queue / cache connection envs computed from
  Bitnami subchart service names).
- **`templates/<service>.yaml`** — five service templates,
  each rendering the service's Deployment + Service when
  `<service>.enabled: true`. Each template uses the helper
  templates for cross-cutting concerns; per-service values
  govern the rest.
- **`templates/rbac.yaml`** — operator RBAC: ClusterRole +
  ClusterRoleBinding + leader-election Role + RoleBinding +
  CRD editor/viewer/admin roles. Translated from
  kubebuilder's scaffolds at
  `operators/control-plane/config/rbac/`.
- **`templates/NOTES.txt`** — printed on `helm install` and
  `helm upgrade`. Brief usage example (a sample ScrapeJob)
  and the multi-arch caveat.

### §3.3 — Naming and labelling conventions

All resources are named `<release>-<component>` via the
`spectre.fullname` helper (`<release>-engine`,
`<release>-playwright-adapter`, etc.). The default release
name is `spectre`, so a vanilla install yields
`spectre-engine`, `spectre-control-plane`,
`spectre-playwright-adapter`, etc.

All resources carry the standard Kubernetes `app.kubernetes.io/*`
labels via `spectre.labels`:

- `app.kubernetes.io/name: spectre`
- `app.kubernetes.io/instance: <release>`
- `app.kubernetes.io/version: <chart appVersion>`
- `app.kubernetes.io/component: <component>` (e.g. `engine`)
- `app.kubernetes.io/managed-by: Helm`
- `app.kubernetes.io/part-of: spectre`

Selector labels (the subset that's stable across upgrades)
are emitted by `spectre.selectorLabels`:
`app.kubernetes.io/name`, `app.kubernetes.io/instance`,
plus `app.kubernetes.io/component` to distinguish per-service
Pods within a single release.

## §4 — Subchart pinning policy

The chart depends on four upstream Bitnami charts:

| Subchart | Pinned version (R7.1) | Repository |
|----------|----------------------|------------|
| `postgresql` | `16.0.0` | `https://charts.bitnami.com/bitnami` |
| `redis` | `19.6.0` | `https://charts.bitnami.com/bitnami` |
| `kafka` | `30.0.0` | `https://charts.bitnami.com/bitnami` |
| `minio` | `14.7.0` | `https://charts.bitnami.com/bitnami` |

The pins are exact (no `~`, no `^`). `Chart.lock` is
committed alongside `Chart.yaml`; reproducible chart
materialisation requires both.

**Bumping policy.** A subchart bump requires an amendment
to this ADR. The amendment records:

- Which subchart is bumped, from what version to what.
- Why — usually security or a feature the project
  consumes.
- Whether the bump is breaking. Bitnami publishes major
  bumps when their `values.yaml` shape changes
  incompatibly. The amendment notes any chart-side values
  changes required.

The amendment is appended to §10's revision history
(future) rather than mutating §4's table; the table tracks
**current** pins, the revision history tracks how they got
there.

**Why this policy.** Bitnami occasionally yanks old chart
versions (their support window for "old" versions is short
relative to v1alpha1's expected lifetime). Without a
committed `Chart.lock` and an explicit ADR-amendment
discipline, a chart that linted today can fail
`helm dep update` tomorrow with no source-controlled record
of what changed. The discipline costs a few minutes per
bump and saves significant debugging.

**Disabling subcharts.** Each subchart has a
`<name>.enabled` flag (`postgresql.enabled`, `redis.enabled`,
`kafka.enabled`, `minio.enabled`). Setting any to `false`
prevents the subchart from rendering, intended for
deployments that connect to externally-managed instances.
The flags are documented in `values.yaml`. The chart's
own services consume connection details from values
(`engine.extraEnv`, etc.) when the subchart is disabled;
when enabled, the helper template `spectre.commonEnv`
constructs URLs from Bitnami's documented service-name
conventions.

## §5 — Image reference policy

### §5.1 — Defaults

Each service's container image resolves to:

```
{{ .Values.image.registry }}/{{ .Values.<service>.image.repository }}:{{ .Values.<service>.image.tag | default .Chart.AppVersion }}
```

Defaults:

- `image.registry: docker.io/fabiocaffarello`
- `<service>.image.repository: spectre-<name>` (e.g.
  `spectre-engine`, `spectre-control-plane`,
  `spectre-playwright`, `spectre-seleniumbase`,
  `spectre-curl-impersonate`)
- `<service>.image.tag: ""` → falls through to
  `.Chart.AppVersion` → `0.1.0-alpha.0` at R7.1 ship time.
- `image.pullPolicy: IfNotPresent`

A vanilla `helm install spectre build/helm/spectre/` therefore
pulls `docker.io/fabiocaffarello/spectre-<name>:0.1.0-alpha.0`
for each of the five services. The defaults are load-bearing:
the publish ran during R7.1 development to make those
manifests exist (see §1).

### §5.2 — Override paths

The chart documents three common override patterns
(`build/helm/spectre/README.md` and `values.yaml` comments):

**Local (kind / minikube):**
```
helm install spectre build/helm/spectre/ \
    --set image.registry=docker.io/library \
    --set image.pullPolicy=Never
# after: kind load docker-image spectre-<name>:dev
```

**Private registry:**
```
helm install spectre build/helm/spectre/ \
    --set image.registry=registry.private.example/spectre \
    --set image.pullSecrets[0]=my-pull-secret
```

**Per-service tag pinning** (e.g. canary one service):
```
helm install spectre build/helm/spectre/ \
    --set engine.image.tag=0.1.0-alpha.1
```

The implementation of these paths is the cross-product of
`values.yaml` shape and the helper templates'
`spectre.<service>Image` definitions. No special-cased
override mechanism is introduced; everything flows through
the values surface that `values.schema.json` validates.

### §5.3 — Why Docker Hub

R6.5.3's update to ADR-0018 §5 chose Docker Hub
(`fabiocaffarello/spectre-*`) over ghcr.io as the primary
registry. ADR-0030 inherits that choice. The chart is
registry-agnostic via `image.registry`, so a future
migration to a different default (ghcr.io or quay.io) is a
one-line values change plus a CI publish-target update; the
chart structure doesn't lock the registry choice in.

## §6 — Multi-arch posture

### §6.1 — The five-image table at R7.1

| Service | Image | Multi-arch | Default `nodeSelector` |
|---------|-------|------------|-----------------------|
| Control plane (operator) | `spectre-control-plane` | ✅ amd64 + arm64 | none |
| Playwright adapter | `spectre-playwright` | ✅ amd64 + arm64 | none |
| Engine | `spectre-engine` | ❌ amd64 only | `kubernetes.io/arch: amd64` |
| SeleniumBase adapter | `spectre-seleniumbase` | ❌ amd64 only | `kubernetes.io/arch: amd64` |
| curl-impersonate adapter | `spectre-curl-impersonate` | ❌ amd64 only | `kubernetes.io/arch: amd64` |

This is exactly the table from ADR-0018 §5 R6.5.3 update,
mapped to chart-side scheduling state.

### §6.2 — `nodeSelector` over alternatives

Three alternatives were considered:

- **`nodeSelector: kubernetes.io/arch: amd64`** (chosen).
  Pods pending on a heterogeneous cluster get
  `0/N nodes are available: N node(s) didn't match Pod's
  node affinity/selector`, an actionable scheduler message.
- **Pod-level affinity rule.** More flexible but verbose;
  the constraint is a single fact (must run on amd64), not a
  preference, so affinity is the wrong tool.
- **No constraint, let `ImagePullBackOff` happen.** Worst
  UX — the Pod looks scheduled and healthy until the pull
  fails, then the user has to inspect events and decode the
  manifest mismatch. Reject.

The `nodeSelector` lines in `values.yaml` carry inline
comments referencing the unblock criterion per §6.3.

### §6.3 — Unblock criteria per service

Each amd64-only service has a documented unblock path. When
each lands, the corresponding `nodeSelector` line is deleted
from `values.yaml` and the table in §6.1 updates.

- **Engine.** Unblock: cross-compile to `aarch64-musl`.
  The engine's Rust binary is statically linked against
  musl; the Cargo target `aarch64-unknown-linux-musl` is
  not currently exercised by the build. ADR-0018 §5 R6.5.3
  update lists this as the canonical unblock path. Ships as
  its own per-image PR.
- **SeleniumBase adapter.** Blocker: Google Chrome stable
  for Linux is amd64-only. Unblock: switch to Chromium
  (which ships arm64) — an ADR-level decision, not a chart
  decision; or wait for upstream Chrome arm64 to ship.
- **curl-impersonate adapter.** Blocker: runtime base
  `lwthiker/curl-impersonate:0.6-chrome` is published
  amd64-only on Docker Hub. ADR-0018 §5 R6.5.3 update lists
  three unblock paths: (a) wait for upstream multi-arch;
  (b) fork the upstream image build; (c) cross-compile from
  source per upstream INSTALL.md.

Each unblock is its own focused per-image PR. ADR-0030 §6
is the single record of the chart-side state; its table
updates as each lands.

### §6.4 — What this means for production

A production cluster running on heterogeneous nodes
(amd64 + arm64 mixed) installs the chart and gets:

- Control-plane Pod on any node.
- Playwright Pods on any node.
- Engine, SeleniumBase, and curl-impersonate Pods on amd64
  nodes only.

A purely arm64 cluster installs the chart and gets the three
amd64-only Pods stuck in `Pending` with the
node-affinity-mismatch event. The `helm install` succeeds;
the cluster is in a discoverable broken state. R7.2's smoke
runs on amd64 (kind on amd64 GitHub runners), so the smoke
exercises a fully-scheduled state.

A pure-amd64 cluster (the most common case at v1alpha1)
installs without any constraint on scheduling — every Pod
schedules on the first available node.

## §7 — Probe policy

### §7.1 — Engine and adapters: native `grpc:` probes

Engine + the three adapters serve gRPC. R7.1 uses
Kubernetes' native `grpc:` readiness/liveness probes
(stable in 1.27+, field-tested broadly by 2025). Example
(engine):

```yaml
readinessProbe:
  grpc:
    port: 8090
  initialDelaySeconds: 10
  periodSeconds: 5
  timeoutSeconds: 3
livenessProbe:
  grpc:
    port: 8090
  initialDelaySeconds: 30
  periodSeconds: 30
  timeoutSeconds: 5
```

The `grpc:` probe issues a `grpc.health.v1.Health/Check` RPC
and treats the response status as the probe outcome. No
in-image binary is required — Kubelet does the dial.

This was forward-committed by **ADR-0025 §3**: R6.2
deliberately declined to bundle `grpc_health_probe` (~10 MB
extra per image, multiplied across four adapter bases) so
that R7.1 could rely on Kubernetes 1.27+ semantics rather
than per-image probe binaries. R7.1 honours the commitment.

### §7.2 — Control plane: HTTP probes

The control-plane operator runs controller-runtime's
manager. controller-runtime serves an HTTP probe endpoint at
`/healthz` (liveness) and `/readyz` (readiness) on a
configurable port. R7.1 wires them via standard `httpGet`:

```yaml
readinessProbe:
  httpGet:
    path: /readyz
    port: 8081
  initialDelaySeconds: 5
livenessProbe:
  httpGet:
    path: /healthz
    port: 8081
  initialDelaySeconds: 15
```

The operator binary already exposes these endpoints;
nothing additional ships in the image. HTTP is the natural
fit for controller-runtime; gRPC would require an extra
serving harness for no benefit.

### §7.3 — Per-service tuning

Per-service probe `initialDelaySeconds` reflects the actual
warm-up cost:

- Engine, control-plane, curl-impersonate: short (5–10s).
  These start fast.
- Playwright, SeleniumBase: longer
  (`initialDelaySeconds: 15`, `livenessProbe: 60`). Browser
  launch is slow; an aggressive liveness probe will kill
  the Pod mid-warm-up.

The values are tunable from `values.yaml`'s
`<service>.probes.{readiness,liveness}` blocks. Defaults
ship the conservative numbers above.

### §7.4 — Kubernetes version requirement

`grpc:` probe is **stable** in Kubernetes 1.27+. The chart's
README documents 1.27+ as a prerequisite. Users on 1.24-1.26
can fall back to `tcpSocket` probes via the values override
path (`values.schema.json` permits the alternative shape via
a probe-union type), but the chart does not officially
support those versions; bug reports against pre-1.27 will be
WONTFIX.

## §8 — CRD lifecycle

### §8.1 — `crds/` directory semantics

Helm 3 has a special-cased `crds/` directory at the chart
root. Files in `crds/` are:

- Installed exactly once, at `helm install` time.
- **Not** templated (raw YAML).
- **Not** updated by `helm upgrade`.
- **Not** removed by `helm uninstall` (CRDs persist after
  release deletion to preserve any custom resources still
  in the cluster).

R7.1's chart ships the v1alpha2 ScrapeJob CRD via
`build/helm/spectre/crds/scrapejob.yaml`. The file is a
direct copy of
`operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml`.

### §8.2 — Chart-CRD drift invariant

The operator's source-of-truth CRD lives at
`operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml`.
It is generated by `controller-gen` from the operator's Go
struct annotations. Any time the operator's CRD changes
(field add, validation tightening, status enum extension),
the chart's copy must update too — otherwise a `helm install`
of the new chart would deploy a new operator binary against
an old CRD schema.

R7.1 enforces this with a justfile recipe and a CI gate:

```just
chart-sync-crds:
    cp operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml \
       build/helm/spectre/crds/scrapejob.yaml

chart-check-crd-sync:
    diff operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml \
         build/helm/spectre/crds/scrapejob.yaml
```

`chart-check-crd-sync` runs in the new `helm-lint` job in
CI; PRs that touch the operator's CRD without resyncing the
chart fail there.

### §8.3 — Future CRD upgrade procedure

When v1alpha2 → vNext CRD shipping happens (post-refactor),
Helm 3's "no CRD updates on upgrade" semantics force a
documented upgrade procedure:

```
# 1. apply the new CRD manually
kubectl apply -f operators/control-plane/config/crd/bases/...

# 2. helm upgrade picks up the new operator binary
helm upgrade spectre build/helm/spectre/

# 3. restart operator to ensure it discovers the new schema
kubectl -n spectre rollout restart deployment/spectre-control-plane
```

R7.1 does not ship the migration script. The chart's README
documents the manual steps under "Upgrading"; the actual
v1alpha2 → vNext migration ADR will reference those steps
when that work happens.

The `crds/` choice trades upgrade ergonomics for
install-time correctness. Helm 3's templated-CRD-in-templates
alternative would let `helm upgrade` flow CRD changes, but
introduces other failure modes (CRD garbage-collection on
chart removal, ordering issues with CRs that depend on the
CRD existing first, schema-incompatibility errors that
surface mid-upgrade rather than at install). For v1alpha1's
single stable CRD, the `crds/` directory's semantics are the
right trade.

### §8.4 — Detailed CRD upgrade procedure (W1.5 update)

> **W1.5 evolution note (2026-05-07).** This subsection
> + §8.5 – §8.9 are added in-place per the precedent set by
> ADR-0018's R6.3 / R6.5.3 / R6.5.4 update notes, ADR-0007's
> R6.6 evolution notes, ADR-0023's §14 amendment (R9.2), and
> CONTRIBUTING.md "Architectural commitments" #6 (in-place
> evolution notes are the only allowed amendment to accepted
> ADRs). §8.1 – §8.3 above remain authoritative; §8.4 – §8.9
> replace §8.3's sketch with the operational procedure
> [`docs/architecture/helm-chart.md`](../architecture/helm-chart.md)
> + `docs/roadmap.md` §4.1's W1.5 entry forward to.
>
> The procedure covers the v1alpha2 → vNext CRD upgrade path
> R7.1 deferred and additionally the **Wave 6 CRD addition**
> (ScrapeBatch CRD per
> [ADR-0033](0033-input-management-subsystem.md)) — a new
> CRD landing in the chart, not a CRD-shape change.

### §8.5 — Wave 6: adding a new CRD (ScrapeBatch)

When [ADR-0033](0033-input-management-subsystem.md)
materialises in Wave 6, the chart's `crds/` directory grows
to include `scrapebatch.yaml` alongside the existing
`scrapejob.yaml`:

```
build/helm/spectre/crds/
  scrapejob.yaml      # v1alpha1 CRD (R7.1)
  scrapebatch.yaml    # v1alpha2 CRD (Wave 6 build PR)
```

Adding a new CRD to the chart's `crds/` directory is a
**low-risk** operation per Helm 3's semantics:

- `helm install` of the new chart version creates both CRDs
  (existing CRDs are skipped because they are already
  present; new CRDs are installed).
- `helm upgrade` does not touch the existing
  `scrapejob.yaml` CRD; the new `scrapebatch.yaml` requires
  the same manual `kubectl apply` flow §8.6 below details.
- The chart-CRD drift invariant from §8.2 extends to
  ScrapeBatch — the Wave 6 build PR's CI gate
  (`chart-check-crd-sync`) walks every CRD under
  `crds/` and verifies each against the operator's source.

The `chart-sync-crds` justfile recipe in §8.2 extends to a
loop over every CRD source file:

```just
chart-sync-crds:
    for crd in operators/control-plane/config/crd/bases/*.yaml; do
        cp "$crd" "build/helm/spectre/crds/$(basename $crd | sed 's/spectre.io_//')"
    done
```

(The exact recipe shape lands in the Wave 6 build PR; the
shape above is illustrative.)

### §8.6 — Standard CRD upgrade procedure (additive changes)

For **additive** CRD shape changes (new optional fields,
new enum values, type widening — every change that the
schema-registry's BACKWARD compatibility per
[ADR-0034 §6.2](0034-output-schema-validation.md) allows
analogously), the manual procedure is:

```bash
# 1. Pre-flight: diff the new CRD against the cluster's
#    existing one so the operator sees the change set.
kubectl get crd scrapejobs.spectre.io -o yaml > /tmp/cluster-crd.yaml
diff <(yq 'del(.metadata.creationTimestamp, .metadata.resourceVersion, .metadata.uid, .metadata.generation, .status)' /tmp/cluster-crd.yaml) \
     operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml

# 2. Apply the new CRD. Existing custom resources continue
#    to validate against the new schema (additive changes
#    accept all prior CR shapes).
kubectl apply -f operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml

# 3. Helm upgrade picks up the new operator binary + the
#    new CRD shape any new templated objects reference.
helm upgrade spectre build/helm/spectre/ \
  --namespace spectre \
  --values values-production.yaml

# 4. Restart the operator so it discovers the new schema
#    via its informer-cache rebuild. Without this step the
#    operator's cached CRD shape lags behind kubectl's view.
kubectl -n spectre rollout restart deployment/spectre-control-plane
kubectl -n spectre rollout status deployment/spectre-control-plane

# 5. Post-upgrade verification: existing ScrapeJobs continue
#    to reach their terminal phase; new ScrapeJobs accept
#    new fields.
kubectl get scrapejobs -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}: {.status.phase}{"\n"}{end}'
```

Step 4 is the most-often-missed step. Without it, the
operator's reconciliation loop continues to use the old
schema for validation; ScrapeJobs that submit new fields
appear to be accepted by the API server but fail
reconciliation with cryptic "unknown field" errors. The
rollout restart forces the operator to rebuild its informer
cache from the new CRD shape.

### §8.7 — Helm `pre-upgrade` hook alternative

For deployments comfortable with chart-managed CRD lifecycle,
Helm's `pre-upgrade` hook pattern lets the chart apply CRDs
before `helm upgrade` renders templates that reference the
new shape:

```yaml
# build/helm/spectre/templates/crd-upgrade-hook.yaml
{{- if .Values.crdUpgradeHook.enabled }}
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Release.Name }}-crd-upgrade
  annotations:
    "helm.sh/hook": pre-upgrade
    "helm.sh/hook-weight": "-5"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
spec:
  template:
    spec:
      serviceAccountName: {{ .Release.Name }}-crd-upgrader
      restartPolicy: Never
      containers:
        - name: kubectl-apply
          image: bitnami/kubectl:1.28
          command: ["/bin/sh", "-c"]
          args:
            - |
              # Apply each CRD shipped in /crds (mounted from a ConfigMap
              # that the chart auto-populates from build/helm/spectre/crds/)
              for crd in /crds/*.yaml; do
                kubectl apply -f "$crd"
              done
{{- end }}
```

The hook is **opt-in** via `crdUpgradeHook.enabled: false`
default. When enabled, `helm upgrade` becomes a single
command:

```bash
helm upgrade spectre build/helm/spectre/ --namespace spectre
# CRDs apply via the hook; operator rollout cascades from
# the chart's existing rollout strategy
```

Trade-offs vs §8.6's manual procedure:

| Concern | Manual (§8.6) | Hook (§8.7) |
|---|---|---|
| Visibility | each step is observable | hook output buried in `helm upgrade` logs |
| Rollback | each step rolls back independently | chart rollback **does not** revert CRD apply (see §8.9) |
| RBAC | operator's normal credentials | hook needs cluster-scoped `customresourcedefinitions` patch permissions |
| Failure mode | step-fails-step-stops | hook failure aborts upgrade; cluster left in mid-upgrade state |
| Operator rollout | explicit step 4 | cascades from chart's deployment rollout |

The hook trades operator visibility for ergonomics. For
**production deployments**, §8.6's manual procedure is
recommended; the hook is appropriate for **dev / staging /
ephemeral** clusters where ergonomics outweighs forensics.

### §8.8 — Breaking-change handling

For **breaking** CRD shape changes (required field additions
without defaults, removed fields, type narrowing — changes
that BACKWARD compatibility per
[ADR-0034 §6.2](0034-output-schema-validation.md) explicitly
forbids analogously), the upgrade is **not** a single-step
operation. Two patterns:

#### §8.8.1 — Conversion webhooks (kubebuilder feature)

The operator hosts a conversion webhook converting between
CRD `spec.versions` entries. The CRD ships with **both**
old and new versions enabled; clients submit one version,
the operator (via the conversion webhook) translates to the
storage version internally.

The kubebuilder skeleton supports this; the Wave 6+ build
PR adding the breaking change ships:

- A new `spec.versions[]` entry with `served: true,
  storage: true` for the new version
- The old `spec.versions[]` entry flipped to `served: true,
  storage: false`
- A conversion webhook implementation under
  `operators/control-plane/internal/webhooks/`
- The webhook's TLS certificate provisioned via cert-manager
  per [ADR-0032](0032-service-to-service-mtls.md)

The upgrade procedure for clients: continue submitting old-
version ScrapeJobs; the webhook converts them to the new
version transparently. Migrate clients to the new version on
the client's own schedule. After all clients migrate, a
follow-up PR removes the old version's `served: true` flag.

#### §8.8.2 — Dual-write window

Where conversion webhooks aren't viable (e.g., the change
involves data the operator cannot reconstruct mechanically),
the upgrade ships **both** the old and new CRD shape
simultaneously:

```
Step 1 (T+0): apply new CRD; both v1alpha2 and v2alpha1 active
Step 2 (T+0): operator binary upgraded; reads both versions
Step 3 (T+0 → T+N): clients gradually migrate
                    ScrapeJobs from v1alpha2 to v2alpha1
Step 4 (T+N): all v1alpha2 ScrapeJobs reach terminal state
              or migrate
Step 5 (T+N): follow-up PR removes v1alpha2 from the CRD
```

The `T+N` window is **deployment-side configurable** —
production deployments may run dual-write for weeks or
months; ephemeral deployments collapse it to hours.

### §8.9 — Rollback considerations

Chart rollback (`helm rollback spectre <revision>`) is
**asymmetric** with the upgrade procedure:

- **Templated resources** (Deployment, Service, ConfigMap)
  rollback cleanly — Helm reverts to the prior chart
  revision's manifests.
- **CRDs in `crds/`** do **not** rollback — Helm 3's `crds/`
  directory has no rollback semantics; the CRD applied via
  step 2 of §8.6 (or via the §8.7 hook) persists post-rollback.

The asymmetry means CRD shape changes are **forward-only at
the chart layer**. To rollback a CRD shape change, the
operator must:

1. Identify the prior CRD shape from version control
   (`git checkout <prior-tag> -- operators/control-plane/config/crd/bases/`)
2. `kubectl apply` the prior CRD shape (only if it is
   compatible with the cluster's existing custom resources;
   incompatible rollbacks corrupt CR data and require
   manual intervention)
3. `helm rollback spectre <revision>` to revert templated
   resources

For breaking CRD shape changes (§8.8), rollback is
**effectively impossible** without dual-write windows or
conversion webhooks; once the new shape is the storage
version, reverting requires data migration in the opposite
direction.

The roadmap's commitment per
[`docs/roadmap.md`](../roadmap.md) §9.6's DSL evolution
drift mitigation extends here: every Wave that ships a CRD
shape change includes the rollback path in its build PR's
acceptance criteria. Breaking changes without a rollback
path are review-rejected.

## §9 — Out of scope for R7.1

R7.1 ships the chart structure documented above. The
following are deliberately **not** in scope:

- **Production smoke (Helm install + sample ScrapeJobs to
  `Completed` against a real cluster, asserted in CI).**
  R7.2's territory. R7.1's CI gate is structural (lint,
  template, dry-run) only.
- **Helm chart publish to an OCI registry.** Post-refactor.
  The chart is consumed locally by `helm install` from a
  cloned repo at R7.1; OCI distribution
  (`oci://docker.io/fabiocaffarello/charts/spectre`) is a
  later concern.
- **Per-infra-service Helm chart fragments.** ADR-0028 §6's
  commitment: each future infra-service ships its chart
  fragment with its build PR. R7.1 establishes the layout
  (`build/helm/<chart>/`) those fragments will follow; it
  does not pre-create empty placeholders.
- **Multi-arch unblocks for engine, seleniumbase,
  curl-impersonate.** Each is a focused per-image PR;
  ADR-0030 §6 is the single record of state.
- **VERSION bump.** R7.1 ships at `0.1.0-alpha.0` (current
  VERSION). A bump is a separate PR.
- **Auto-trigger publish on tag push.** ADR-0018 §5 R6.5.3
  update §4.4 deferred this. R7.1 doesn't change that
  posture.
- **Helm value templating for SDK migrations.** ADR-0027's
  SDK strategy work happens post-refactor; chart values
  will adapt then.
- **Modifying any source code.** R7.1 is packaging only.
- **Modifying the proto schema, capabilities, or driver
  protocol.** Master strategy §2.1, §2.3.
- **Modifying the Compose stack topology.** Compose remains
  the development environment per master strategy §2.5;
  the chart is its production counterpart, not its
  replacement.

## §10 — References

- [ADR-0018](0018-devcontainer-and-engine-image.md) §5 R6.5.3
  update — multi-arch table; canonical unblock criteria;
  Docker Hub registry decision.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — operator architecture; CRD shape; manager port
  conventions; HTTP probe endpoints.
- [ADR-0020](0020-microservices-architecture-supersession.md)
  §5 R-series living audit table — phase log; ADR-0030
  appears as R7.1's delta.
- [ADR-0023](0023-stateful-services-architecture.md) —
  stateful services topology; Postgres / Redis / Kafka /
  MinIO selection; the Bitnami subchart pins follow this
  ADR's choice of upstream.
- [ADR-0024](0024-output-sinks.md) — output sinks; the
  chart's sink-related env defaults follow this ADR.
- [ADR-0025](0025-compose-stack.md) §3 — Compose service
  topology + the native gRPC probe forward commitment that
  ADR-0030 §7 honours; §10 — the original "ADR-0026 (when
  drafted)" reservation that ADR-0026 §1 reassigned to
  Platform Maturation, leaving the Helm chart's ADR number
  pending until R7.1.
- [ADR-0026](0026-platform-taxonomy.md) §1 — note on
  numbering reassigning ADR-0026 to Platform Maturation
  and deferring the Helm chart's ADR number to R7.1; §3.9 —
  out-of-band categories including `build/`, the slot the
  chart lives in.
- [ADR-0028](0028-ancillary-infra-services-catalog.md) §6 —
  Helm chart fragments for future infra-services; the
  layout commitment R7.1 honours.
- [Helm 3 documentation](https://helm.sh/docs/topics/charts/)
- [Helm best practices](https://helm.sh/docs/chart_best_practices/)
- [Bitnami chart conventions](https://charts.bitnami.com/)
- [Kubernetes gRPC probe](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/#define-grpc-liveness-probe)
