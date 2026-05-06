# Canonical service shape (v1alpha2)

> **Operational companion** to
> [ADR-0036 §5](../adr/0036-microservices-catalog-expansion.md).
> ADR-0036 codifies the canonical service shape; this document
> walks the shape in operational form and provides a
> contributor checklist for materialising a new service.

## §1 — Why a canonical shape

15 catalog services × inconsistent shapes = unmanageable
operational surface. Even with discipline, per-service ad-hoc
choices accumulate: one service uses port 9100 for metrics,
another 9200; one ships its proto under a non-standard path;
one has no per-service CHANGELOG; one's chart fragment uses
different helper conventions. By the 5th or 6th service the
divergence is real.

The canonical shape **prevents drift** by codifying eight
patterns every service follows. Adding a service becomes
mostly *filling in the blanks* of established templates;
deviations require an ADR amendment to ADR-0036.

The shape extends ADR-0028 §3 (the original infra-services
canonical shape, scoped to 5 services) to the operational
patterns that emerge only at multi-service scale.

## §2 — Directory structure

```
infra-services/<slot>/
  proto -> ../../proto/spectre/<slot>/v1alpha1/   # symlink
  cmd/<slot>/main.<ext>                           # binary entrypoint
  internal/                                       # service-specific logic
    server/                                       # gRPC server impl
    providers/                                    # vendor implementations (private)
    state/                                        # state-backend client
  config/                                         # default config + env loader
  Dockerfile                                      # builds the service image
  Makefile                                        # build / test / lint
  README.md                                       # service-specific docs
  CHANGELOG.md                                    # per-service changelog (§7)
  adr/                                            # per-service ADRs (§7)
    0000-template.md
```

The `proto` symlink (rather than copy) is **normative** —
one source of truth for the protocol contract; service tree,
SDK tree, engine tree all reference it. Per
[ADR-0026 §5](../adr/0026-platform-taxonomy.md)'s dependency
DAG.

`just new-service <slot> <language>` scaffolds the directory
from a template (lives in `tools/build/` once Wave 5 opens).

## §3 — Helm chart fragment

Every service contributes a chart fragment under
`build/helm/spectre/templates/`:

```
templates/
  <slot>.yaml                   # Deployment + Service + ServiceMonitor
  <slot>-rbac.yaml              # ServiceAccount + Role + RoleBinding (if needed)
  <slot>-config.yaml            # ConfigMap with defaults
  <slot>-cert.yaml              # cert-manager Certificate (per ADR-0032)
```

`values.yaml` gets a top-level `<slotCamelCase>:` block
matching the existing `engine:` / `controlPlane:` shape from
[ADR-0030](../adr/0030-helm-chart-structure.md):

```yaml
proxyBroker:
  enabled: true
  replicas: 2
  image:
    registry: docker.io
    repository: fabiocaffarello/spectre-proxy-broker
    tag: ""                 # defaults to chart appVersion when empty
  service:
    port: 8094
  probes:
    readiness: { grpc: { port: 8094 } }
    liveness:  { grpc: { port: 8094 } }
  resources: {}
  nodeSelector: {}
```

ADR-0030 §3 commits the chart to **single-chart-with-named-
templates** for first-party services (no subcharts; subcharts
reserved for stateful tiers). Each new service is a
**fragment** within `build/helm/spectre/templates/`, not a
subchart. The `_helpers.tpl` named-template helpers are
reused across fragments.

## §4 — Observability surface

Every service exposes the canonical observability surface per
[ADR-0031 §3](../adr/0031-observability-framework.md):

| Surface | Where |
|---|---|
| **gRPC reflection** | `grpc.reflection.v1alpha.ServerReflection` (per ADR-0021's pattern) |
| **gRPC Health** | `grpc.health.v1.Health` on the service port; Kubernetes-native `grpc:` probes dial it directly (no `grpc_health_probe` binary) |
| **Prometheus `/metrics`** | Sidecar port `9090` (uniform across services) |
| **OpenTelemetry traces** | OTLP/gRPC to deployment-local `opentelemetry-collector`; W3C Trace Context propagation via gRPC metadata |
| **Structured logs** | JSON to stdout with the nine mandatory fields (timestamp, level, service, service_version, caller, message, trace_id, span_id, request_id, job_id, tenant_id; `latency_ms` conditionally mandatory) |

Per-language OTel SDK choices per ADR-0031 §3.5:
`opentelemetry-rust` + `tracing-opentelemetry` (Rust);
`go.opentelemetry.io/otel` + `slog` (Go); `opentelemetry-python`
(Python); `@opentelemetry/api` + Pino (TypeScript).

## §5 — CI surface

When a new service lands under `infra-services/<slot>/`, the
following CI surfaces auto-extend (lightweight glob-based
discovery):

| Workflow | What auto-extends |
|---|---|
| `build.yml` | Bake matrix entry (one new line per service) |
| Per-language lint job | Auto-discovers source tree |
| Per-language test job | Auto-discovers test tree |
| `scan.yml` (Wave 1) | Trivy scan on the built image |
| `sign.yml` (Wave 1) | cosign signing post-publish |
| `helm-lint.yml` | Chart fragment lints via existing chart-lint gate |
| `production-smoke.yml` | Included in smoke when on the smoke-cluster topology |

Adding a service is **a single PR** with multiple file changes
following the templates; the CI matrix updates mostly
automatically.

## §6 — Service-to-service mTLS

Every service receives a cert-manager `Certificate` via the
chart's `_helpers.tpl` template, gated by the chart's
`cert-manager.enabled` flag (default `false`; users with
cert-manager already installed flip it on). When the flag is
on, service-to-service gRPC traffic uses mTLS by default.

Per-service certificate identity:
- **CN**: `<slot>.<release-namespace>.svc` (matches Kubernetes
  Service DNS name per [ADR-0021](../adr/0021-service-discovery.md))
- **SANs**: `<slot>` / `<slot>.<release-namespace>` /
  `<slot>.<release-namespace>.svc` /
  `<slot>.<release-namespace>.svc.cluster.local`
- **Validity**: 90 days; renewal at 30 days before expiry;
  ECDSA P-256 preferred, RSA 2048 fallback

cert-manager handles rotation automatically. Per-language
TLS-credential reload plumbing lives in `sdks/<lang>/common/`
per ADR-0032 §5.1. See
[ADR-0032](../adr/0032-service-to-service-mtls.md) for the
full contract.

## §7 — Per-service CHANGELOG and ADR conventions

Each service has its own:

- **`CHANGELOG.md`** — per-service changelog for independent
  release cadence. The repo-level `CHANGELOG.md` tracks
  platform-level changes (ADR landings, cross-service
  refactors, chart bumps); per-service changes (provider
  additions, retry policy tweaks, error-mapping fixes) go in
  the service's local file.
- **`adr/`** — per-service ADR tree for service-internal
  decisions (provider integration choices; state-backend
  trade-offs; retry policy decisions). Repo-level ADRs at
  `docs/adr/` remain for platform-wide decisions.

The split prevents the platform-wide ADR set from bloating to
hundreds of entries when each of 15 services has 5+ internal
decisions.

## §8 — New-service onboarding checklist

When materialising a new service (e.g., Wave 5's first
inhabitant `proxy-broker`), the build PR includes:

1. **Catalog entry confirmation**: cite the slot's
   [ADR-0036 §3](../adr/0036-microservices-catalog-expansion.md)
   entry; demonstrate which gates A–F apply.
2. **Protocol contract**: `proto/spectre/<slot>/v1alpha1/<slot>.proto`
   lands per [ADR-0028 §3.1](../adr/0028-ancillary-infra-services-catalog.md)'s
   canonical proto-package convention.
3. **Service tree**: `infra-services/<slot>/` per §2.
4. **Helm fragment**: `templates/<slot>*.yaml` + values block
   per §3.
5. **Compose block**: `docker-compose.yml` entry per
   [ADR-0025](../adr/0025-compose-stack.md) §3 pattern.
6. **Observability surface wired**: gRPC reflection +
   Health + `/metrics` on 9090 + OTel SDK + structured logs
   per §4.
7. **mTLS template invocation**: `_helpers.tpl` certificate
   include in the chart fragment per §6.
8. **CI auto-extension**: bake matrix entry; per-language
   lint / test jobs; helm-lint inclusion; production-smoke
   inclusion if applicable.
9. **Per-service CHANGELOG.md**: initial entry recording the
   service's first release.
10. **Per-service ADR template**: `infra-services/<slot>/adr/0000-template.md`
    plus any service-internal decisions made during the
    build PR.
11. **SDK packages**: per ADR-0027's "first non-trivial
    consumer" admission gate. Wave 6+ services typically
    ship Rust (engine consumer) + Go (operator consumer); TS
    / Python SDKs land when their first consumer materialises.
12. **Stateful dependency declaration**: chart fragment +
    Compose `depends_on` per the service's backend per
    [`storage-tiers.md`](storage-tiers.md).
13. **README.md**: service-specific docs (purpose; provider
    list; configuration; operational notes).
14. **Production-smoke fixture**: extension to R7.2's gate
    that exercises the service in the smoke cluster.

The checklist is **normative** — reviewers verify each item
before approving a build PR. Deviations require an ADR
amendment to ADR-0036.

## §9 — Reference materials

- [ADR-0021](../adr/0021-service-discovery.md) — service
  discovery; gRPC reflection (§4) carries forward.
- [ADR-0025](../adr/0025-compose-stack.md) — Compose stack;
  per-service Compose block (§8 step 5).
- [ADR-0026 §5](../adr/0026-platform-taxonomy.md) —
  dependency DAG; the `proto` symlink (§2) honours category
  rules.
- [ADR-0027](../adr/0027-sdk-strategy.md) — SDK strategy;
  per-language SDKs (§8 step 11).
- [ADR-0028 §3](../adr/0028-ancillary-infra-services-catalog.md)
  — the original five-slot canonical shape this doc extends.
- [ADR-0030](../adr/0030-helm-chart-structure.md) — Helm
  chart structure; fragment pattern (§3).
- [ADR-0031](../adr/0031-observability-framework.md) —
  observability framework; per-service surface (§4).
- [ADR-0032](../adr/0032-service-to-service-mtls.md) —
  service-to-service mTLS (§6).
- [ADR-0036 §5](../adr/0036-microservices-catalog-expansion.md)
  — canonical service shape (the source).
- [`platform-architecture.md`](platform-architecture.md) —
  umbrella v1alpha2 architectural overview.
- [`service-catalog.md`](service-catalog.md) — per-service
  status.
- [`storage-tiers.md`](storage-tiers.md) — backend tier
  reference for §8 step 12.
- [`observability.md`](observability.md) — operational guide
  for §4's surface.
