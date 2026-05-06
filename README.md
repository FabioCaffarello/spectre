# Spectre

**An open protocol for driver-agnostic browser automation at scale.**

> **Status: Alpha** — The protocol is `v1alpha1` and will change. Nothing
> is production-ready. If you are here, you are early.

---

## The thesis

Browser automation today is framework-locked. Choose Playwright? Your
selectors, stealth logic, session management, and deployment tooling are
all Playwright-specific. Switch to SeleniumBase for its UC Mode? Rewrite
everything. A new tool appears with better fingerprint evasion? Start over.

Spectre argues that the right primitive is not another framework but an
**open protocol**. Any browser automation tool — Playwright, SeleniumBase,
curl-impersonate, or something that doesn't exist yet — implements the
Spectre Driver Protocol and plugs into the ecosystem. Your extraction
logic, orchestration, stealth configuration, and scaling infrastructure
stay the same regardless of which driver runs underneath.

This is the same architectural insight behind Kubernetes CRI (which
separated Kubernetes from Docker), or OpenTelemetry (which separated
observability from vendor SDKs). Spectre applies it to browser automation.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  User / DSL Job                 │
├─────────────────────────────────────────────────┤
│              Spectre Engine (Rust)              │
│         DSL parsing, planning, execution        │
├──────────────┬──────────────┬───────────────────┤
│   Driver Protocol (protobuf v1alpha1)           │
├──────────────┼──────────────┼───────────────────┤
│  Playwright  │ SeleniumBase │ curl-impersonate  │
│  (TypeScript)│   (Python)   │    (Go/cgo)       │
└──────────────┴──────────────┴───────────────────┘
```

The protocol defines the contract. Drivers implement it. The engine
compiles jobs against driver capabilities and fails at compile time —
not at runtime — if a driver can't do what a job requires.

For the full architecture, see [docs/architecture/overview.md](docs/architecture/overview.md).

## Quick start

```yaml
# job.yaml — extract top stories from Hacker News
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://news.ycombinator.com
  - extract:
      selector: .titleline > a
      fields:
        title: textContent
        url: href
output:
  format: jsonl
  path: ./stories.jsonl
```

v1alpha1's runtime is a multi-process gRPC stack (R2.x →
ADR-0020 / ADR-0022): an engine, three reference adapters, the
control-plane operator, and the stateful services they persist
to (Postgres, Redis, Kafka, MinIO). R6.2 (ADR-0025) brought every
application service into a unified `docker-compose.yml`; R6.3
(ADR-0025 §6 R6.3 update + ADR-0018 §3a) closed the loop by
adding Docker-in-Docker to the devcontainer, so the Compose stack
and a local `kind` Kubernetes cluster live inside one
Reopen-in-Container session. Local development is one
"Reopen in Container" plus three commands:

```bash
git clone https://github.com/FabioCaffarello/spectre && cd spectre
code .                                # VS Code → "Reopen in Container"
# (first-time devcontainer build is ~10–15 minutes; post-create.sh
#  runs `just kind-up` + `just crds-install` automatically.)

# From inside the devcontainer:
cp .env.example .env                  # Postgres / Redis / Kafka / S3 defaults
just images                           # build the five service images via bake
just compose-up                       # docker compose --profile full up -d
docker compose ps                     # 11 services healthy

# Submit a job via grpcurl against the Compose-running engine.
grpcurl -plaintext \
    -import-path proto -proto spectre/engine/v1alpha1/engine.proto \
    -d "$(jq -n --arg dsl "$(cat examples/hello-hackernews/job.yaml)" '{job_dsl: $dsl}')" \
    127.0.0.1:8090 \
    spectre.engine.v1alpha1.Engine/RunJob
```

The control-plane operator joins the Compose stack as the
`control-plane` service (post-R6.3 unified shape, see
[ADR-0025 §6 R6.3 update](docs/adr/0025-compose-stack.md#r63-update--resolution)).
End-to-end via the kind cluster:

```bash
kubectl apply -f operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_endpoint.yaml
kubectl get scrapejob -w              # Pending → Running → Completed
docker compose logs control-plane     # reconciler events
```

> Contributors who decline VS Code can build the devcontainer
> image manually (`docker build -f .devcontainer/Dockerfile`)
> and `docker run` it with the DinD feature enabled. See
> [docs/architecture/development-environment.md](docs/architecture/development-environment.md)
> for the supported flow and the host-side Docker prerequisite.

The five service images shipped in R6.1 are orchestrated by
`docker buildx bake` (see
[docs/architecture/container-images.md](docs/architecture/container-images.md)):

```bash
just images           # build all five via docker buildx bake default
just images-smoke     # smoke each (binary exists / canonical missing-env error)
just images-list      # docker images "spectre-*" with sizes
```

Pre-built images for releases ship at
`fabiocaffarello/spectre-<name>` on Docker Hub; the publish
flow is manual (`workflow_dispatch`) and ships multi-arch where
supported — see
[docs/architecture/releases.md](docs/architecture/releases.md)
for the operator runbook.

## Deploying to Kubernetes

R7.1 ships a Helm chart at
[`build/helm/spectre/`](build/helm/spectre/) that installs the
v1alpha1 stack — engine, three driver adapters, control-plane
operator, and stateful dependencies (Postgres, Redis, Kafka,
MinIO) — into any conformant Kubernetes 1.27+ cluster:

```bash
helm dependency update build/helm/spectre/
helm install spectre build/helm/spectre/ \
    --create-namespace --namespace spectre
kubectl -n spectre rollout status deployment --all --timeout=300s
```

A vanilla install pulls
`docker.io/fabiocaffarello/spectre-<name>:<chart appVersion>`
for each service. The structural decisions are recorded in
[ADR-0030](docs/adr/0030-helm-chart-structure.md); the operator
runbook lives at
[docs/architecture/helm-chart.md](docs/architecture/helm-chart.md).

R2.3 retired the standalone `spectre run` / `validate` CLI; the
`spectre` binary is now the engine's gRPC service entry point
(ADR-0020 §3 supersedes ADR-0013). Job execution flows from a
ScrapeJob CR through the control plane's gRPC client, into the
engine's `RunJob` stream, and out to the configured `OutputSink`.
For per-service specifics see
[docs/architecture/postgres.md](docs/architecture/postgres.md),
[docs/architecture/redis.md](docs/architecture/redis.md),
[docs/architecture/kafka.md](docs/architecture/kafka.md), and
[docs/architecture/output-sinks.md](docs/architecture/output-sinks.md).
The Redpanda Console UI for Kafka inspection runs at
<http://localhost:8080> (`just kafka-console`); the MinIO
Console for S3 / `OutputSink.S3` debugging runs at
<http://localhost:9001> (`just minio-console`).

## How Spectre compares

| Capability                  | Playwright | SeleniumBase | Browserless | Spectre    |
|-----------------------------|-----------|-------------|-------------|------------|
| Driver-agnostic protocol    | No        | No          | No          | **Yes**    |
| Declarative DSL             | No        | No          | Partial     | **Yes**    |
| Capability negotiation      | No        | No          | No          | **Yes**    |
| Distributed execution (K8s) | Manual    | Manual      | Yes         | **Planned**|
| Stealth / anti-detection    | Plugins   | UC Mode     | Limited     | **Core**   |
| AI selector auto-healing    | No        | No          | No          | **Planned**|
| Multi-language drivers      | JS only   | Python only | API only    | **Any**    |
| Production-ready            | Yes       | Yes         | Yes         | **No**     |
| Open source                 | Yes       | Yes         | Partial     | **Yes**    |

Honesty: Spectre is alpha software. Playwright and SeleniumBase work
today. Spectre's advantage is architectural — it pays off when you need
to swap drivers, scale across clusters, or integrate tools that don't
exist yet.

## Project status

> **v1alpha1 refactor complete; v1alpha2 architectural
> foundation complete; v1alpha2 implementation begins next.**
> The microservices refactor (R1 → R8.1, completed
> 2026-05-03) closed at `0.1.0-alpha.0`. Phase R9
> (v1alpha2 architectural foundation, completed 2026-05-06)
> shipped 9 sequential documentation-only PRs that
> crystallised every post-refactor architectural commitment
> into canonical artefacts: 9 new ADRs (0031 – 0039) + 1
> in-place ADR amendment (ADR-0023 §14, MongoDB tier) + 7
> new architecture docs at `docs/architecture/` + 13
> existing architecture-doc forward-reference subsections +
> a substantially expanded `docs/roadmap.md` §4 Wave 1 – 12
> plan + a CONTRIBUTING.md v1alpha2 process rigor matrix +
> this audit's companion at
> [`docs/v1alpha2-audit.md`](docs/v1alpha2-audit.md). Status
> is "v1alpha1 production posture: complete; v1alpha2
> architectural foundation: complete; v1alpha2 implementation:
> Wave 1 opens next." The platform is feature-complete against
> the v1alpha1 surface; the protocol remains marked alpha and
> will evolve. For per-release detail see
> [CHANGELOG.md](CHANGELOG.md); for per-PR refactor history
> see the frozen
> [`docs/refactor-audit.md`](docs/refactor-audit.md); for
> per-PR v1alpha2 phase history see
> [`docs/v1alpha2-audit.md`](docs/v1alpha2-audit.md); for
> architectural commitments that bind v1alpha2 contributors
> see [CONTRIBUTING.md](CONTRIBUTING.md)'s "Architectural
> commitments" section.

See the [roadmap](docs/roadmap.md) §4 for the v1alpha2
trajectory — Waves 1 – 10 with constituent PRs, calendar
estimates, and per-Wave ADR cross-references. The
**14-of-15-services catalog** + the 3-tier persistent
storage architecture (Postgres + Redis + MongoDB; plus
Kafka for streaming) + the engine's evolution to
orchestrator pattern + cross-cutting observability and mTLS
frameworks land across Waves 5 – 10 over a roughly 18 – 30
month horizon.

## Contributing

Spectre welcomes contributions. The most impactful path is **writing a
new driver** — see [the driver guide](docs/guides/writing-a-driver.md).
For everything else, read [CONTRIBUTING.md](CONTRIBUTING.md).

## Documentation

- [Architecture overview](docs/architecture/overview.md)
- [Driver Protocol deep dive](docs/architecture/driver-protocol.md)
- [Control plane](docs/architecture/control-plane.md)
- [Development environment (Devcontainer)](docs/architecture/development-environment.md)
- [Helm chart](docs/architecture/helm-chart.md) +
  [Production smoke gate](docs/architecture/production-smoke.md)
- [Writing a driver](docs/guides/writing-a-driver.md)
- [Architecture Decision Records](docs/adr/README.md)
- [Roadmap](docs/roadmap.md)
- [CHANGELOG](CHANGELOG.md) — per-release notes
- [`docs/refactor-audit.md`](docs/refactor-audit.md) — frozen
  per-PR refactor history (R1 → R8.1)
- [`docs/v1alpha2-audit.md`](docs/v1alpha2-audit.md) —
  forward-tracking per-PR v1alpha2 phase history (Phase R9+)

## License

Apache 2.0 — see [LICENSE](LICENSE).
