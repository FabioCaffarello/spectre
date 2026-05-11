# Spectre Helm chart

Production-deployment artifact for Spectre's v1alpha1 stack: the
engine, three driver adapters (Playwright, SeleniumBase,
curl-impersonate), the control-plane operator, and stateful
dependencies (Postgres, Redis, Kafka, MinIO).

> Architectural rationale: [ADR-0030](../../../docs/adr/0030-helm-chart-structure.md).
> Architecture-level companion doc: [docs/architecture/helm-chart.md](../../../docs/architecture/helm-chart.md).

---

## TL;DR

```bash
helm dependency update build/helm/spectre/
helm install spectre build/helm/spectre/ \
    --create-namespace --namespace spectre
```

A vanilla install pulls
`docker.io/fabiocaffarello/spectre-<name>:0.1.0-alpha.0` for each
of the five services and provisions Postgres / Redis / Kafka /
MinIO via Bitnami subcharts. The `NOTES.txt` printed on install
includes a sample ScrapeJob walkthrough.

---

## Prerequisites

- **Kubernetes 1.27+.** The chart uses native `grpc:`
  readiness/liveness probes (stable in 1.27); pre-1.27 clusters
  need `tcpSocket` overrides.
- **Helm 3.13+.** `crds/` directory semantics + dependency lock
  resolution.
- **At least 2 amd64 nodes worth of capacity.** Three of the
  five services (engine, seleniumbase-adapter,
  curl-impersonate-adapter) carry
  `nodeSelector: kubernetes.io/arch: amd64` per
  [ADR-0030 §6](../../../docs/adr/0030-helm-chart-structure.md#§6--multi-arch-posture).
  A pure-arm64 cluster will leave those Pods Pending.
- **A pull-capable runtime.** The default registry is Docker
  Hub's `fabiocaffarello/spectre-*`; for private clusters use
  the override path below.

---

## Installation

### Default install

```bash
helm dependency update build/helm/spectre/
helm install spectre build/helm/spectre/ \
    --create-namespace --namespace spectre
```

### With a custom values file

```bash
helm install spectre build/helm/spectre/ \
    --namespace spectre --create-namespace \
    -f my-values.yaml
```

### Common overrides

Local-built images (kind / minikube), after
`kind load docker-image spectre-<name>:dev`:

```bash
helm install spectre build/helm/spectre/ \
    --set image.registry=docker.io/library \
    --set image.pullPolicy=Never
```

Private registry:

```bash
helm install spectre build/helm/spectre/ \
    --set image.registry=registry.private.example/spectre \
    --set image.pullSecrets[0]=my-pull-secret
```

External Postgres (subchart disabled):

```bash
helm install spectre build/helm/spectre/ \
    --set postgresql.enabled=false \
    --set engine.extraEnv[0].name=SPECTRE_POSTGRES_URL \
    --set engine.extraEnv[0].value="postgres://user:pass@db.example:5432/spectre"
```

The full values surface is documented inline in
[`values.yaml`](values.yaml) and validated at install time by
[`values.schema.json`](values.schema.json).

---

## Verification

R7.2 ships a production-smoke CI gate that exercises this
chart against three reference ScrapeJobs (kafka, s3, webhook
sinks) and asserts row events arrive at each sink boundary.
The gate runs on every PR that could regress the smoke and
on a daily cron for drift detection. Local reproduction is
two commands:

```bash
just chart-smoke-up    # build images, kind cluster, helm install
just chart-smoke-test  # apply 3 ScrapeJobs + run 3 sink verifiers
```

See [`docs/architecture/production-smoke.md`](../../../docs/architecture/production-smoke.md)
for the full flow and debugging guide.

---

## Upgrading

```bash
helm upgrade spectre build/helm/spectre/ \
    --namespace spectre
```

> **CRD upgrade caveat.** Helm 3 installs files under `crds/`
> once at install time and **does not update them on
> `helm upgrade`**. When the operator's CRD shape changes
> (post-v1alpha2 work), apply the new CRD manually first:
>
> ```bash
> kubectl apply -f operators/control-plane/config/crd/bases/spectre.io_scrapejobs.yaml
> helm upgrade spectre build/helm/spectre/ --namespace spectre
> kubectl -n spectre rollout restart deployment/spectre-control-plane
> ```
>
> See [ADR-0030 §8](../../../docs/adr/0030-helm-chart-structure.md#§8--crd-lifecycle).

---

## Uninstalling

```bash
helm uninstall spectre --namespace spectre
kubectl delete namespace spectre
```

CRDs survive `helm uninstall` (Helm 3 design — preserves any
custom resources still in the cluster). Remove explicitly when
you're sure no ScrapeJobs depend on them:

```bash
kubectl delete crd scrapejobs.spectre.io
```

---

## Configuration

Every value is documented inline in `values.yaml`; the JSON
Schema in `values.schema.json` validates `--set` overrides at
install time. The most-touched keys:

| Key | Purpose |
|-----|---------|
| `image.registry` | Container registry (default `docker.io/fabiocaffarello`) |
| `image.pullPolicy` | `IfNotPresent` (default), `Always`, `Never` |
| `<service>.replicas` | Replica count per service |
| `<service>.resources` | Pod resource requests/limits |
| `<service>.image.tag` | Override image tag (falls back to `.Chart.AppVersion`) |
| `postgresql.enabled` / `redis.enabled` / `kafka.enabled` / `minio.enabled` | Toggle Bitnami subcharts on/off |
| `observability.otlpEndpoint` | OTLP/gRPC trace endpoint (empty → no-op tracer) |
| `observability.metricsPort` | Uniform `:9090` Prometheus port (ADR-0031 §3.3) |
| `observability.serviceMonitor.enabled` | Render a Prometheus Operator ServiceMonitor |
| `opentelemetry-collector.enabled` | Install the optional collector subchart |

---

## Observability

The chart wires ADR-0031's observability surface for engine +
control-plane (W3.1 Cluster F). Adapters extend the same surface
in W3.2.

**Default (no collector, no ServiceMonitor):**

```bash
helm install spectre build/helm/spectre/
# /metrics on port 9090 on both engine + control-plane Services;
# `kubectl port-forward svc/spectre-engine 9090:9090` then
# `curl localhost:9090/metrics`. No OTLP push.
```

**With a co-installed collector:**

```bash
helm install spectre build/helm/spectre/ \
  --set opentelemetry-collector.enabled=true \
  --set observability.otlpEndpoint=spectre-opentelemetry-collector:4317
```

The subchart's full upstream value surface is reachable under
the `opentelemetry-collector:` block — see [the upstream
chart](https://github.com/open-telemetry/opentelemetry-helm-charts/tree/main/charts/opentelemetry-collector)
for the documented overrides.

**With Prometheus Operator scrape:**

```bash
# Requires monitoring.coreos.com/v1 in the cluster
kubectl get crd servicemonitors.monitoring.coreos.com
helm install spectre build/helm/spectre/ \
  --set observability.serviceMonitor.enabled=true \
  --set observability.serviceMonitor.additionalLabels.release=kube-prometheus-stack
```

The rendered ServiceMonitor selects engine + control-plane
Services by their `app.kubernetes.io/component` label and
scrapes the `metrics` named port — `observability.metricsPort`
can shift without breaking the selector.

For the metric / trace / log shape engine + operator emit see
[`docs/architecture/observability.md`](../../../docs/architecture/observability.md);
ADRs are
[ADR-0031](../../../docs/adr/0031-observability-framework.md)
(framework) and [ADR-0032](../../../docs/adr/0032-service-to-service-mtls.md)
(transport security, W3.3).

---

## Multi-arch notes

Three of the five services are amd64-only at v1alpha1:

| Service | Multi-arch | Unblock path |
|---------|------------|--------------|
| `spectre-control-plane` | ✅ amd64 + arm64 | already multi-arch |
| `spectre-playwright` | ✅ amd64 + arm64 | already multi-arch |
| `spectre-engine` | ❌ amd64 only | aarch64-musl cross-compile |
| `spectre-seleniumbase` | ❌ amd64 only | switch to Chromium, or wait Chrome arm64 |
| `spectre-curl-impersonate` | ❌ amd64 only | upstream multi-arch / fork / source build |

The chart renders `nodeSelector: kubernetes.io/arch: amd64` on
the three amd64-only services so a heterogeneous cluster
schedules them only where they can run. Delete the matching
`<service>.nodeSelector` block in `values.yaml` (or `--set
<service>.nodeSelector=null`) once each service's multi-arch
unblock lands. Reference: [ADR-0018 §5 R6.5.3
update](../../../docs/adr/0018-devcontainer-and-engine-image.md).

---

## Troubleshooting

**`ImagePullBackOff` on a service Pod.** Verify the registry +
image tag exist (`docker buildx imagetools inspect <ref>`) and
the cluster has pull credentials (`image.pullSecrets`).

**Pod stuck `Pending` with `0/N nodes available, N node(s)
didn't match Pod's node affinity/selector`.** The Pod hit a
`nodeSelector` constraint. For amd64-only services on an arm64
node, this is expected; either run on amd64 nodes or wait for
the unblock.

**`CRD not found: scrapejobs.spectre.io`.** Helm 3 installs
CRDs only at install time. If installation skipped them
(unusual), apply manually:
`kubectl apply -f build/helm/spectre/crds/scrapejob.yaml`.

**Postgres / Redis / Kafka / MinIO Pod errors.** Refer to the
upstream Bitnami chart docs for the pinned version
(see [`Chart.yaml`](Chart.yaml) `dependencies`).
