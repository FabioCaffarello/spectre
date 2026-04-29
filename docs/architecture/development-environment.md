# Development environment

This guide describes the post-R6.2 local-development flow: the
unified Compose stack that runs every application service alongside
its stateful dependencies on a single network. After R6.2 the
canonical inner-loop invocation is one command:
`just images && just compose-up`.

For the architectural commitment behind the Compose-as-environment
shape, see [ADR-0025](../adr/0025-compose-stack.md). For the
container-image build orchestration that produces what Compose
runs, see [container-images.md](container-images.md) and
[ADR-0018](../adr/0018-devcontainer-and-engine-image.md).

## Quick start

From a clean checkout:

```bash
git clone https://github.com/FabioCaffarello/spectre && cd spectre
cp .env.example .env                  # Postgres / Redis / Kafka / S3 defaults
just bootstrap                        # fetch every component's deps
just images                           # build the five service images via bake
just compose-up                       # docker compose --profile full up -d
docker compose ps                     # 10 services healthy
```

The first `just images` build is slow (15–25 minutes for upstream
base pulls + toolchain compile). Subsequent rebuilds amortise via
buildx's per-stage cache (~1–2 minutes when only one component's
source changes).

The `--profile full` invocation brings up:

- **Stateful services**: Postgres 16, Redis 7, Apache Kafka 3.7
  (KRaft), Redpanda Console UI (Kafka inspection), MinIO + bucket
  bootstrap one-shot.
- **Application services**: engine, playwright-adapter,
  seleniumbase-adapter, curl-impersonate-adapter.

The control-plane operator is **not** part of the Compose stack
in R6.2 — it stays a host process per the
[ADR-0025 §6](../adr/0025-compose-stack.md#§6-—-operator-outside-compose-for-r6.2)
deferral. R6.3 (Devcontainer with Docker-in-Docker) brings the
operator into the unified shape alongside a Compose-managed `kind`
cluster.

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

| Service                  | Image                                                            | Host port | Compose DNS                  |
|--------------------------|------------------------------------------------------------------|-----------|------------------------------|
| postgres                 | `postgres:16-alpine`                                             | 5432      | `postgres:5432`              |
| redis                    | `redis:7-alpine`                                                 | 6379      | `redis:6379`                 |
| kafka                    | `apache/kafka:3.7.1`                                             | 9092      | `kafka:9092`                 |
| kafka-console            | `redpandadata/console:latest`                                    | 8080      | `kafka-console:8080`         |
| minio                    | `quay.io/minio/minio:RELEASE.2024-12-18T13-15-44Z`               | 9000/9001 | `minio:9000`                 |
| minio-bootstrap          | `quay.io/minio/mc:latest`                                        | —         | (one-shot, exits)            |
| engine                   | `spectre-engine:dev` (built via `bake engine`)                   | 8090      | `engine:8090`                |
| playwright-adapter       | `spectre-playwright:dev` (built via `bake playwright`)           | 8091      | `playwright-adapter:8091`    |
| seleniumbase-adapter     | `spectre-seleniumbase:dev` (built via `bake seleniumbase`)       | 8092      | `seleniumbase-adapter:8092`  |
| curl-impersonate-adapter | `spectre-curl-impersonate:dev` (built via `bake curl-impersonate`)| 8093     | `curl-impersonate-adapter:8093` |

Host-port mappings are 1:1: container 8090 → host 8090, and so on.
The same address resolves both inside Compose (`engine:8090`) and
from the host (`127.0.0.1:8090`).

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

R6.2 keeps the operator a host process while the Compose stack
hosts the application services. The operator runs against the
developer's external `kind` cluster and dials the Compose-running
engine via the host-port mapping:

```bash
# Bring up the Compose stack first (engine on 127.0.0.1:8090).
just images && just compose-up

# In a separate terminal, ensure a kind cluster exists.
kind get clusters || kind create cluster

# Install the v1alpha2 CRD into the kind cluster.
just op-install-crds

# Run the operator from the host. SPECTRE_ENGINE_ENDPOINT defaults
# to 127.0.0.1:8090, which is the Compose host-port mapping for
# the engine container.
just op-run

# In another terminal, apply a sample ScrapeJob.
kubectl apply -f core/control-plane/config/samples/spectre_v1alpha2_scrapejob_endpoint.yaml
kubectl get scrapejob -w     # Pending → Running → Completed
```

The `_endpoint` sample uses `EngineRef.Endpoint` form
(`127.0.0.1:8090`) so the operator dials the host-mapped port
without needing a Kubernetes Service. Service-form samples
(`spectre_v1alpha2_scrapejob_hello-hackernews.yaml` etc.) require
a `spectre-engine` Service in the cluster — useful for in-cluster
testing once R7.1's Helm chart lands.

R6.3 will resolve the host/Compose split: the devcontainer adds
Docker-in-Docker so the operator container, the kind API server,
and the Compose stack all live in one Docker daemon. Until then,
`just op-run` is the canonical operator flow.

## Toolchain prerequisites

For the Compose flow:

- Docker (any recent version; Apple Silicon, Linux, and
  Windows-WSL2 all work).
- `just`, `grpcurl`, `jq` on PATH.

For the conformance flow (in addition to the above):

- Rust stable (engine native builds for `engine-run-native`).
- Go 1.25.
- Node 20 + pnpm 9.
- Python 3.12 + uv.
- `kubectl` and `kind` for the operator dev flow.

The Devcontainer (revisited in R6.3 with DinD) provisions every
toolchain plus Chrome / ChromeDriver / curl-impersonate for the
conformance flow.

### Devcontainer

Open the repository in VS Code → Command Palette →
**Dev Containers: Reopen in Container**. The current devcontainer
(R6.1 era, pre-DinD) provides every toolchain `just check` /
`just conf-test` need. R6.3 adds Docker-in-Docker so the
devcontainer also hosts the Compose stack and the kind cluster
the operator targets — at which point `just compose-up` and
`just op-run` from inside the devcontainer cover the unified flow.

For the pre-R6.3 devcontainer, run the Compose stack on the host
and run `just check` / `just conf-test` from within the
devcontainer.

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
