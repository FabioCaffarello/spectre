# Development environment

This guide describes the post-R6.3 local-development flow: a
single Reopen-in-Container session that hosts the entire Compose
stack, a local kind Kubernetes cluster, and the control-plane
operator — all in one Docker daemon (Docker-in-Docker). After R6.3
the canonical inner-loop invocation from inside the devcontainer
is `just images && just compose-up`; on first devcontainer build
the post-create script runs `just kind-up` and `just crds-install`
automatically.

For the architectural commitment behind the Compose-as-environment
shape, see [ADR-0025](../adr/0025-compose-stack.md) (with the §6
R6.3 update resolving the operator deferral). For the
container-image build orchestration that produces what Compose
runs, see [container-images.md](container-images.md) and
[ADR-0018](../adr/0018-devcontainer-and-engine-image.md) (with
§3a R6.3 evolution recording the Docker-in-Docker addition).

## Quick start

From a clean checkout, the supported path is the devcontainer:

```bash
git clone https://github.com/FabioCaffarello/spectre && cd spectre
code .                                # VS Code → "Reopen in Container"
# First build runs ~10–15 minutes (DinD + multi-language toolchains
# + Playwright Chromium download + post-create.sh's kind-up +
# crds-install). Subsequent reopens are instant.
```

Inside the devcontainer terminal:

```bash
cp .env.example .env                  # Postgres / Redis / Kafka / S3 defaults
just images                           # build the five service images via bake
just compose-up                       # docker compose --profile full up -d
docker compose ps                     # 11 services healthy
```

Contributors who do not use VS Code can build the devcontainer
image manually with `docker build -f .devcontainer/Dockerfile`,
then run it with the DinD feature enabled — but VS Code's "Reopen
in Container" is the supported path. GitHub Codespaces consumes
the same `devcontainer.json` config.

The `--profile full` invocation brings up:

- **Stateful services**: Postgres 16, Redis 7, Apache Kafka 3.7
  (KRaft), Redpanda Console UI (Kafka inspection), MinIO + bucket
  bootstrap one-shot.
- **Application services**: engine, playwright-adapter,
  seleniumbase-adapter, curl-impersonate-adapter.
- **Control plane**: the `control-plane` operator, joining both
  the Compose default network (for `engine:8090`, `postgres:5432`)
  and the external `kind` Docker network (for the local kind API
  server's `spectre-dev-control-plane:6443`).

The control-plane operator joins the Compose stack via R6.3's
DinD + kind shape ([ADR-0025 §6 R6.3 update](../adr/0025-compose-stack.md#r63-update--resolution)).
The host-process `op-run` recipe is gone.

## Profiles

`docker-compose.yml` tags every service with one or more of five
profiles. The justfile aliases the common combinations:

| Recipe                       | Profile     | Members                                                                          | Use case                                                                          |
|------------------------------|-------------|----------------------------------------------------------------------------------|-----------------------------------------------------------------------------------|
| `just compose-up`            | `full`      | everything (stateful + application + Redpanda Console)                           | Default human dev flow.                                                           |
| `just compose-up-app`        | `app`       | full minus Redpanda Console                                                      | Headless full stack — aimed at CI runs.                                           |
| `just compose-up-core`       | `core`      | postgres + kafka + minio + minio-bootstrap + engine                              | Engine integration tests (`engine-db-test` / `engine-kafka-test` / `engine-s3-test`). |
| `just compose-up-adapters`   | `adapters`  | redis + the three adapters                                                       | Adapter-only experimentation via grpcurl against host port mappings.              |
| `just compose-up-infra`      | `infra`     | six stateful services only (the pre-R6.2 default shape)                          | Conformance-suite runs (subprocess-spawned adapters); native-binary debug flows. |

Profile design rationale lives in
[ADR-0025 §4](../adr/0025-compose-stack.md#§4-—-profiles).

After R6.2 every service in `docker-compose.yml` is tagged with at
least one profile, so a profile-less `docker compose up` brings up
nothing — pass `--profile <name>` (or use the justfile recipes).

## What runs where

| Service                  | Image                                                            | Host port  | Compose DNS                  |
|--------------------------|------------------------------------------------------------------|------------|------------------------------|
| postgres                 | `postgres:16-alpine`                                             | 5432       | `postgres:5432`              |
| redis                    | `redis:7-alpine`                                                 | 6379       | `redis:6379`                 |
| kafka                    | `apache/kafka:3.7.1`                                             | 9092       | `kafka:9092`                 |
| kafka-console            | `redpandadata/console:latest`                                    | 8080       | `kafka-console:8080`         |
| minio                    | `quay.io/minio/minio:RELEASE.2024-12-18T13-15-44Z`               | 9000/9001  | `minio:9000`                 |
| minio-bootstrap          | `quay.io/minio/mc:latest`                                        | —          | (one-shot, exits)            |
| engine                   | `spectre-engine:dev` (built via `bake engine`)                   | 8090       | `engine:8090`                |
| playwright-adapter       | `spectre-playwright:dev` (built via `bake playwright`)           | 8091       | `playwright-adapter:8091`    |
| seleniumbase-adapter     | `spectre-seleniumbase:dev` (built via `bake seleniumbase`)       | 8092       | `seleniumbase-adapter:8092`  |
| curl-impersonate-adapter | `spectre-curl-impersonate:dev` (built via `bake curl-impersonate`)| 8093      | `curl-impersonate-adapter:8093` |
| control-plane            | `spectre-control-plane:dev` (built via `bake control-plane`)     | (8081 unmapped) | `control-plane`         |

Host-port mappings are 1:1: container 8090 → host 8090, and so on.
The same address resolves both inside Compose (`engine:8090`) and
from the devcontainer host (`127.0.0.1:8090`). The operator's
`--health-probe-bind-address=:8081` port is unmapped by default
([ADR-0025 §6 R6.3 update](../adr/0025-compose-stack.md#r63-update--resolution))
— operator activity surfaces through `kubectl get scrapejob -w`
and `docker compose logs control-plane` rather than HTTP probes.

## Port reference

| Component                | Default | Source of truth                       |
|--------------------------|---------|---------------------------------------|
| engine                   | 8090    | `SPECTRE_ENGINE_PORT`                 |
| playwright-adapter       | 8091    | `SPECTRE_ADAPTER_GRPC_PORT` (per image)|
| seleniumbase-adapter     | 8092    | `SPECTRE_ADAPTER_GRPC_PORT` (per image)|
| curl-impersonate-adapter | 8093    | `SPECTRE_ADAPTER_GRPC_PORT` (per image)|
| control-plane HTTP       | 8080    | `--health-probe-bind-address` flag    |
| Postgres                 | 5432    | upstream                              |
| Redis                    | 6379    | upstream                              |
| Kafka broker             | 9092    | upstream                              |
| MinIO S3 API             | 9000    | upstream                              |
| MinIO Console            | 9001    | upstream                              |

The application range moved from 9090–9093 to 8090–8093 in R6.2
to avoid colliding with Kafka's ecosystem-standard 9092 under the
unified Compose stack. ADR-0021 §4 has documented 8090–8093 since
R2.1; ADR-0025 §7 records the migration.

## Submitting a job

The engine's gRPC service exposes
`spectre.engine.v1alpha1.Engine.RunJob` on `127.0.0.1:8090`.
With the Compose stack up, submit a job via `grpcurl`:

```bash
grpcurl -plaintext \
    -import-path proto -proto spectre/engine/v1alpha1/engine.proto \
    -d "$(jq -n --arg dsl "$(cat examples/hello-hackernews/job.yaml)" '{job_dsl: $dsl}')" \
    127.0.0.1:8090 \
    spectre.engine.v1alpha1.Engine/RunJob
```

`grpcurl` and `jq` are available via Homebrew
(`brew install grpcurl jq`) and standard Linux package managers.
The streaming response carries one `RunJobResponse.Row` event per
extracted row followed by a terminal
`RunJobResponse.Completed { rows_extracted }`.

## Conformance-suite flow

The conformance suite intentionally **does not** consume the
Compose-running adapters. Per
[ADR-0025 §5](../adr/0025-compose-stack.md#§5-—-conformance-stays-subprocess-based)
the harness spawns adapter subprocesses with custom
`SPECTRE_ADAPTER_INSTANCE_ID` values to exercise the R4.3
restart-invalidation contract — a per-test isolation requirement
incompatible with long-lived Compose services.

Build artefacts the suite consumes:

```bash
just pw-build                   # adapters/playwright/dist/index.js
just sb-bootstrap               # adapters/seleniumbase/.venv/
just sb-install-chromedriver    # ChromeDriver matched to local Chrome
just curl-imp-build             # adapters/curl-impersonate/bin/adapter
just pw-install-browsers        # Chromium for Playwright (one-time)
```

Then:

```bash
just compose-up-infra           # stateful deps (postgres, redis, kafka, minio)
just conf-test                  # full suite — 49 passed / 14 skipped
```

Two artefact-ownership shapes coexist after R6.2:

- **Conformance flow** consumes native binaries via
  `*-build` / `*-bootstrap`.
- **Dev-loop flow** consumes Compose images via `just images`.

The two are independent. A change in
`adapters/playwright/src/server.ts` requires `just pw-build` to
test against conformance and `just compose-rebuild playwright` to
test against the Compose stack.

## Operator dev flow

R6.3 places the operator inside the Compose stack as the
`control-plane` service. There is no separate "run the operator
from the host" step — `just compose-up` brings it up alongside
the engine, the adapters, and the stateful deps. CRD applies and
ScrapeJob reconciliation work end-to-end without leaving the
unified shape:

```bash
# From inside the devcontainer, with the kind cluster + CRDs
# already provisioned by post-create.sh on first build:
just images && just compose-up

# Apply a sample ScrapeJob. The Endpoint-form sample dials the
# Compose-internal `engine:8090` hostname directly — the
# canonical post-R6.3 dev-flow demo.
kubectl apply -f core/control-plane/config/samples/spectre_v1alpha2_scrapejob_endpoint.yaml
kubectl get scrapejob -w       # Pending → Running → Completed

# Inspect operator activity.
docker compose logs -f control-plane | grep -E 'reconciling|completed|failed'

# Confirm Postgres has the row.
docker exec spectre-postgres psql -U spectre -d spectre \
  -c 'SELECT id, status, output_sink_kind, rows_extracted FROM jobs ORDER BY created_at DESC LIMIT 1'
```

The `_endpoint` sample's `engineRef.endpoint: engine:8090` is the
post-R6.3 idiom: the operator container's network reach lets
service-name DNS resolve directly. The same address matches the
operator's `SPECTRE_ENGINE_ENDPOINT` fallback, so a sample
without an `engineRef` field would resolve identically.

`spectre_v1alpha2_scrapejob_hello-hackernews.yaml` (Service form,
`spectre-engine.spectre-system.svc.cluster.local:8090`) is the
in-cluster pattern R7.1's Helm chart targets — it does not work
in the post-R6.3 dev flow because the engine runs as a Compose
service rather than a Kubernetes Pod.

## Kubernetes-in-Docker (kind)

R6.3 ships a local `spectre-dev` kind cluster as the operator's
Kubernetes API surface. The cluster runs in the devcontainer's
Docker-in-Docker daemon (see "DinD model" below); recipes manage
its lifecycle:

| Recipe              | Effect                                                                       |
|---------------------|-------------------------------------------------------------------------------|
| `just kind-up`      | Idempotent. Creates `spectre-dev` if missing; writes `build/kind/kubeconfig`. |
| `just kind-down`    | Deletes `spectre-dev`; removes `build/kind/kubeconfig`.                       |
| `just kind-status`  | Lists the kind clusters known to the local Docker daemon.                     |
| `just crds-install` | Applies the v1alpha2 ScrapeJob CRD (kustomize build of `config/crd`).         |
| `just crds-uninstall` | Removes the v1alpha2 ScrapeJob CRD.                                         |

The cluster is independent of Compose's lifecycle — `compose-down`
and `compose-reset` do **not** touch the kind cluster. Run
`kind-down` separately for a clean reset.

The kubeconfig dance has two server URLs by design:

- **`build/kind/kubeconfig`** is written by
  `kind get kubeconfig --internal` — server URL
  `https://spectre-dev-control-plane:6443` (in-network DNS). The
  operator container mounts it read-only at
  `/home/nonroot/.kube/config`. Reachable only from containers on
  the `kind` Docker network (the operator joins it explicitly in
  Compose).
- **`~/.kube/config`** is written by `kind create cluster` —
  server URL `https://127.0.0.1:<random-port>` (host-port forward).
  Used by `kubectl` from the devcontainer terminal directly. The
  contributor does not need to set `KUBECONFIG`; kind populates
  this file automatically.

The two-config dance is standard kind practice; documented here
so the operator container's mount and the contributor's terminal
do not look like project-specific magic.

## DinD model

R6.3's devcontainer ships the official VS Code
`docker-in-docker:2` feature (Moby variant). Inside the
devcontainer, `docker` talks to a `dockerd` running **inside** the
devcontainer — not the host's Docker daemon. Implications:

- `docker images` from inside the devcontainer shows DinD-internal
  images. The host's images are invisible (Docker Desktop's
  layer-cache is shared at the VM level — first-build savings;
  not a runtime concern).
- `docker volume ls` similarly shows DinD-internal volumes.
- Tearing down the devcontainer (Dev Containers: Rebuild
  Container) destroys the DinD volumes, the Compose stack, and
  the kind cluster — clean slate.

This is **deliberate** and matches ADR-0020 §85's commitment.
ADR-0018 §3a R6.3 evolution records the audit trail.

Failure modes worth knowing:

- **Codespaces compatibility.** The DinD feature works in
  Codespaces — the host VM supports DinD. First-build cost is the
  same ~10–15 minutes as a local Reopen-in-Container.
- **macOS / Apple Silicon.** Docker Desktop on macOS runs Linux
  containers in a lightweight VM; the devcontainer's nested
  daemon runs inside that VM. ARM64 / AMD64 mismatches are
  invisible at the DinD layer because the devcontainer is
  single-arch (`linux/amd64` per ADR-0018 §5; emulated under
  Rosetta on Apple Silicon).
- **Linux native host.** The DinD feature runs without Docker
  Desktop's VM layer; cgroups + privileged-mode requirements are
  the most common failure surface. The official feature handles
  these uniformly.
- **kind network missing.** Compose's `external: true name: kind`
  declaration on the operator service expects the network to
  exist. If a contributor runs `compose-up` before `kind-up`,
  Compose errors with "network kind not found". The fix:
  `just kind-up && just compose-up`. The post-create script
  arranges this on first devcontainer build.

## Toolchain prerequisites

The supported path is the devcontainer; the only host
prerequisite is Docker (Docker Desktop on macOS / Windows;
Docker Engine on Linux). Everything else — Rust, Go, Node 20 +
pnpm 9, Python 3.12 + uv, Chrome, ChromeDriver, curl-impersonate,
kind, kubectl, kubebuilder, buf, just, actionlint, gitleaks —
ships inside the devcontainer image.

For contributors who decline the devcontainer, the host needs the
toolchains listed above. The conformance suite (which spawns
adapter subprocesses in three languages) is the most demanding
consumer; the devcontainer exists primarily to keep that suite
turnkey.

### Devcontainer

Open the repository in VS Code → Command Palette →
**Dev Containers: Reopen in Container**. The R6.3 devcontainer
ships:

- Docker-in-Docker (`docker-in-docker:2` feature) — the Compose
  stack and the kind cluster run inside the devcontainer's own
  Docker daemon. ADR-0018 §3a R6.3 evolution + ADR-0025 §6 R6.3
  update.
- Every language toolchain (Rust 1.88, Go 1.25, Node 20, Python
  3.12) plus Chrome / ChromeDriver / curl-impersonate for the
  conformance suite.
- Kubernetes tooling — `kind` 0.24.0, `kubectl` 1.31.0, `kubebuilder`
  4.13.1.
- `just`, `buf`, `actionlint`, `gitleaks`, `pre-commit`.

First-build cost is ~10–15 minutes (DinD install +
multi-language toolchains + Playwright Chromium download +
post-create.sh's `kind-up` + `crds-install`). Subsequent reopens
are instant. The post-create script is idempotent — rebuilding
the container is safe and re-runs are cheap.

`forwardPorts` in `.devcontainer/devcontainer.json` exposes every
application + stateful service port (8080 / 8090–8093 / 5432 /
6379 / 9000 / 9001 / 9092) through VS Code's port-forwarding
tunnel, so a browser / `grpcurl` invocation on the contributor's
host machine reaches the in-DinD services without extra setup.

## Engine native binary (debugging)

`just engine-run-native` builds and runs the engine directly on
the host (skipping the image build). Useful for tight live-coding
loops where a `just compose-rebuild engine` round-trip is
heavier than the change warrants. Listens on `127.0.0.1:8090` by
default; reads the same env vars as the Compose-running engine
(`SPECTRE_POSTGRES_URL`, `SPECTRE_KAFKA_BROKERS`, etc. — populate
from `.env`).

The native-binary recipes for the three adapters
(`pw-run` / `sb-run` / `curl-imp-run`) were retired in R6.2.
Compose is the canonical adapter-runtime path; native-adapter
runs require building artefacts via the per-adapter `*-build` /
`*-bootstrap` recipes and launching directly with the right
`SPECTRE_ADAPTER_GRPC_PORT` / `SPECTRE_REDIS_URL` env vars.

## Tearing down

```bash
just compose-down               # stop services, preserve volumes
just compose-down -v            # ❌ does not exist — use the next two
docker compose down -v          # stop, drop volumes (full reset)
just compose-reset              # equivalent: down -v + compose-up --profile full
```

The named volumes (`spectre_postgres_data`, `spectre_redis_data`,
`spectre_kafka_data`, `spectre_minio_data`) survive
`compose-down`; only `compose-down -v` / `compose-reset` drops
them.

## Related ADRs

- [ADR-0025](../adr/0025-compose-stack.md) — Compose stack
  topology, profile design, port migration, conformance
  carve-out, operator deferral.
- [ADR-0021](../adr/0021-service-discovery.md) — service
  discovery via env vars + DNS; §4 port plan enacted by ADR-0025.
- [ADR-0022](../adr/0022-tcp-grpc-transport.md) — TCP gRPC
  transport contract.
- [ADR-0023](../adr/0023-stateful-services-architecture.md) —
  stateful services architecture; §9 Compose-side topology.
- [ADR-0024](../adr/0024-output-sinks.md) — output sinks; MinIO
  in §3 R5.1 addendum.
- [ADR-0018](../adr/0018-devcontainer-and-engine-image.md) —
  pre-R6.1 devcontainer + engine image; revisited by R6.3 (DinD).
