---
status: accepted
date: 2026-04-29
deciders: [Fabio Caffarello]
---

# Compose stack: application services + profile-based topology

## §1 — Context and Problem Statement

Twelve PRs into the refactor, the architecture ADR-0020 commits
to is operationally complete at the service-mesh layer (R2.x),
the control-plane layer (R3.x), the stateful-services layer
(R4.x), the output-sink layer (R5.x), and — after R6.1 — the
container-image layer. Every component now has a per-service
image: `spectre-engine:dev`, `spectre-control-plane:dev`,
`spectre-curl-impersonate:dev`, `spectre-playwright:dev`,
`spectre-seleniumbase:dev`, all built through one
`docker buildx bake` invocation orchestrated by
`docker-bake.hcl`. What R6.1 did not deliver is the
*environment* those images run in.

Pre-R6.2's local-dev path was a hybrid: stateful services
(PostgreSQL, Redis, Kafka KRaft + Redpanda Console, MinIO + the
bucket-bootstrap one-shot) ran under `docker compose up`;
application services ran as native binaries via `just engine-run`,
`just pw-run`, `just sb-run`, `just curl-imp-run`, `just op-run`.
The hybrid worked because the stateful and application sides
addressed each other through host loopback ports — the engine
dialled `localhost:5432` for Postgres, `localhost:9092` for
Kafka, the adapters dialled `localhost:6379` for Redis, and the
operator dialled `localhost:9090` for the engine.

That hybrid has two structural costs the refactor's
master-strategy §2.5 commits to retiring. First, **the
local-dev shape diverges from the production shape**: production
under R7.1's Helm chart will run every service as a Pod on a
private Kubernetes network with service-DNS resolution; the
hybrid teaches contributors a different topology than the
production target. Second, **the application-port range
9090–9093 collides with Kafka's ecosystem-standard 9092**: the
collision was latent under the hybrid because each native
binary bound its own loopback port lazily, but it becomes a
hard failure the moment every service binds simultaneously
under one Compose stack.

R6.2 closes both gaps. Five new application services land in
`docker-compose.yml`. The port range moves to 8090–8093 per the
ADR-0021 §4 plan documented since R2.1. ADR-0025 records the
topology, profile design, port-migration scope, conformance-
suite scope, and the operator-outside-Compose deferral.

## §2 — Decision summary

R6.2 commits to:

- **Single `docker-compose.yml` with profiles.** The existing
  six stateful services (postgres, redis, kafka, kafka-console,
  minio, minio-bootstrap) are preserved exactly. Four
  application services (engine, playwright-adapter,
  seleniumbase-adapter, curl-impersonate-adapter) are added.
  Every service is tagged with one or more of five profiles
  (`infra`, `core`, `adapters`, `app`, `full`); the
  documented default invocation is
  `docker compose --profile full up -d`.
- **Image source = `image:` + `pull_policy: never`.** Compose
  consumes the local-built images bake produces; no `build:`
  directives, no registry pulls. `just images` is a hard
  prerequisite for `just compose-up`.
- **Port migration enacted.** The application ports move from
  9090–9093 (the pre-R6.2 hybrid) to 8090–8093 (the ADR-0021
  §4 plan). Kafka's 9092 stays unmolested.
- **Operator stays a host process for R6.2.** The control plane
  uses `ctrl.GetConfigOrDie()` to read `$KUBECONFIG` or an
  in-cluster service-account token; running it inside Compose
  while the Kubernetes API lives on the developer's host is
  deferred to R6.3, which adds a Docker-in-Docker devcontainer
  with `kind` running inside the Compose-hosting Docker daemon.
- **Conformance suite stays subprocess-based.** The
  per-test-isolation requirement (R4.3 restart-invalidation
  tests need fresh adapter instances with custom
  `SPECTRE_ADAPTER_INSTANCE_ID`) is fundamentally incompatible
  with long-lived Compose services. The harness continues to
  spawn native-binary adapter subprocesses; the
  `pw-build` / `sb-bootstrap` / `curl-imp-build` recipes are
  preserved.
- **Native-binary run recipes are retired.** `just pw-run`,
  `just sb-run`, `just curl-imp-run` are deleted (no
  fallback). `just engine-run` is renamed
  `just engine-run-native` and kept as a debugging escape hatch
  with a comment block pointing readers at `just compose-up`.
  `just op-run` is preserved for R6.2 (operator deferral).

## §3 — Service topology

Eleven services on Compose's default network
(`<project>_default`). Hostnames on the network match the
service-block names; the `container_name:` fields exist for
`docker ps` legibility, not DNS.

```
                              ┌────────────────────────┐
                              │   docker-compose.yml   │
                              │     project_default    │
                              └────────────┬───────────┘
                                           │
        ┌──────────────┬───────────────┬───┴───────────┬──────────────┐
        ▼              ▼               ▼               ▼              ▼
   ┌─────────┐   ┌──────────┐   ┌─────────────┐   ┌─────────┐   ┌─────────┐
   │postgres │   │  redis   │   │    kafka    │   │  minio  │   │ engine  │
   │ :5432   │   │  :6379   │   │   :9092     │   │  :9000  │   │ :8090   │
   └─────────┘   └──────────┘   └─────────────┘   └─────────┘   └────┬────┘
                                       ▲                              │
                              ┌────────┴───────┐                      │
                              │ kafka-console  │                      │
                              │     :8080      │                      │
                              └────────────────┘                      │
                                                                      │
        ┌─────────────────────────┬──────────────────────────┐        │
        ▼                         ▼                          ▼        │
   ┌──────────────┐    ┌─────────────────────┐    ┌──────────────────┐│
   │  playwright- │    │   seleniumbase-     │    │curl-impersonate- ││
   │   adapter    │    │      adapter        │    │    adapter       ││
   │   :8091      │    │       :8092         │    │     :8093        ││
   └──────┬───────┘    └──────────┬──────────┘    └────────┬─────────┘│
          │                       │                        │          │
          └───────── redis ◀──────┴───────── redis ◀───────┘          │
                                                                      │
                                                  engine ◀────────────┘
                                                  dials adapters lazily
                                                  per-job at RunJob time
```

DNS resolution table on the Compose network:

| Service                  | DNS short name              | Internal port |
|--------------------------|------------------------------|---------------|
| engine                   | `engine`                     | 8090          |
| playwright-adapter       | `playwright-adapter`         | 8091          |
| seleniumbase-adapter     | `seleniumbase-adapter`       | 8092          |
| curl-impersonate-adapter | `curl-impersonate-adapter`   | 8093          |
| postgres                 | `postgres`                   | 5432          |
| redis                    | `redis`                      | 6379          |
| kafka                    | `kafka`                      | 9092          |
| kafka-console            | `kafka-console`              | 8080          |
| minio                    | `minio`                      | 9000          |

Host-port mappings are 1:1 for every application service: the
container's 8090 is mapped to the host's 8090, the container's
8091 to the host's 8091, and so on. A single mental model
covers both Compose-internal and host-side addressing.

### Healthcheck strategy

Asymmetric per runtime base. Three of the five new services
ship a Compose `HEALTHCHECK`; the engine does not.

- **Engine** — `gcr.io/distroless/static-debian12:nonroot`. No
  shell, no `nc`, no `wget`, no `grpc_health_probe`. The image
  has no probe binary that could express a healthcheck. R6.2
  declines to bundle `grpc_health_probe` (~10 MB extra; R7.1's
  Helm chart will use Kubernetes's native `grpc:` readinessProbe
  syntax, which needs no in-image binary). Compose-level
  healthcheck is **omitted** for the engine. Connection
  liveness from dependent services flows through gRPC client
  dial-retry (ADR-0022 §4); the engine's `start_period` plus
  `restart: unless-stopped` covers the bootstrap window.
- **playwright-adapter** —
  `mcr.microsoft.com/playwright:v<version>-noble` (Ubuntu Noble,
  has `bash`). Probe is bash's built-in TCP redirect:
  `bash -c 'cat < /dev/tcp/127.0.0.1/8091'`. No external binary
  dependency.
- **seleniumbase-adapter** — `python:3.12-slim-bookworm`. The
  slim base does not ship `nc`, but Python's `socket` is always
  available. Probe is
  `python -c "import socket;s=socket.socket();s.settimeout(2);s.connect(('127.0.0.1',8092));s.close()"`.
- **curl-impersonate-adapter** — `lwthiker/curl-impersonate:0.6-chrome`
  (Alpine, busybox `nc`). Probe is
  `nc -z 127.0.0.1 8093`. The `-z` flag is busybox-supported.

The asymmetry is documented here so a future contributor does
not "harmonise" by bundling a single probe binary across all
four images. The choice trades off image-size discipline
against probe uniformity; v1alpha1 keeps the per-base
minimal-footprint posture and lets R7.1's Helm chart replace
the Compose probes with native `grpc:` syntax that needs no
binary at all.

### `depends_on` graph

Conservative — services wait only for direct stateful
dependencies:

- `engine` → `postgres` (healthy) + `kafka` (healthy) +
  `minio-bootstrap` (completed_successfully)
- `playwright-adapter` / `seleniumbase-adapter` /
  `curl-impersonate-adapter` → `redis` (healthy)
- `kafka-console` → `kafka` (healthy)
- `minio-bootstrap` → `minio` (healthy)

The engine does **not** wait for the adapters: per ADR-0021 §3
the engine's adapter dial is lazy (deferred to RunJob time).
Adapters can come up after the engine without the engine's
startup blocking.

### SeleniumBase shared-memory allocation

Chrome's tab-isolation crashes with the default 64 MiB
`/dev/shm` (`SIGBUS` / `BadAlloc` mid-Navigate). The
seleniumbase-adapter service sets `shm_size: 1gb`, matching
the upstream `selenium/standalone-chrome` convention. R7.1's
Helm chart applies the same allocation on the Pod spec.

## §4 — Profiles

Five profile names. Each service is tagged with one or more.
Services with no profile tag would be brought up by a
profile-less `docker compose up`; **after R6.2 every service
has at least one profile tag**, so a profile-less invocation
brings up nothing — the user must select a profile.

| Profile      | Members                                                                                                                                            | Purpose                                                                          |
|--------------|----------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------|
| `infra`      | postgres, redis, kafka, kafka-console, minio, minio-bootstrap                                                                                      | The pre-R6.2 default — stateful services only. Used by the conformance suite (subprocess-based adapter spawn) and by contributors who run application services as native binaries via `just engine-run-native`. |
| `core`       | postgres, kafka, minio, minio-bootstrap, engine                                                                                                    | Engine integration tests (`engine-db-test` / `engine-kafka-test` / `engine-s3-test`). The engine plus its stateful deps; no adapters needed because the integration tests use stub adapters or no adapter at all. |
| `adapters`   | redis, playwright-adapter, seleniumbase-adapter, curl-impersonate-adapter                                                                          | Adapter-only experimentation. Useful for manual driver-protocol exploration via `grpcurl` against the adapter ports without bringing up the engine.            |
| `app`        | postgres, redis, kafka, minio, minio-bootstrap, engine, playwright-adapter, seleniumbase-adapter, curl-impersonate-adapter                         | Headless full stack — every dependency end-to-end runtime needs, minus observability UIs (kafka-console). Aimed at CI runs where the visual UIs add boot time without serving a non-interactive run. |
| `full`       | every service in `app` plus `kafka-console`                                                                                                        | Default human-facing dev flow. The README's quick-start uses `--profile full`. `just compose-up` becomes `docker compose --profile full up -d`.                |

The exclusion of `kafka-console` from `app` is deliberate. The
Redpanda Console UI is operationally useful for humans
inspecting topics during development; it adds a non-trivial
startup window and serves no purpose in a CI run. `app` is the
machine-facing profile; `full` is the human-facing one. A
future contributor who proposes "promote kafka-console into
app" should reach for the `full` profile instead.

The justfile recipes alias the common combinations:

```
just compose-up           # docker compose --profile full up -d
just compose-up-app       # docker compose --profile app up -d
just compose-up-core      # docker compose --profile core up -d
just compose-up-adapters  # docker compose --profile adapters up -d
just compose-up-infra     # docker compose --profile infra up -d  (the pre-R6.2 default)
```

`just compose-down` and `just compose-reset` remain
profile-agnostic — `down` stops every running service
regardless of which profile brought it up; `reset` drops
volumes and restarts under `--profile full`.

## §5 — Conformance stays subprocess-based

A reasonable reader looking at R6.2 might propose: "The
adapters now run as Compose services with stable hostnames and
ports. Migrate the conformance harness to dial them directly,
eliminating the per-test subprocess spawn." R6.2 explicitly
rejects that proposal.

The reason is **per-test isolation**. The R4.3 restart-
invalidation tests (`test_session_restart_invalidation.py`)
deliberately spawn parallel adapter processes with distinct
`SPECTRE_ADAPTER_INSTANCE_ID` values. They verify that a
session created against instance A returns gRPC `UNAVAILABLE`
when accessed via instance B — the §5 contract from ADR-0023.
A single long-lived Compose-service adapter cannot host this
test; the test's premise is two adapter instances coexisting
with different identities.

Beyond the restart-invalidation tests, the broader conformance
discipline depends on per-test fresh state. Cross-test session
leakage from a long-lived adapter — a failing test leaving
behind a half-closed browser context, a stale cookie jar, an
unevicted session document in Redis — has bitten browser-
automation suites since selenium-grid days. The subprocess
harness gives each test a fresh adapter process whose state
ends with its `wait()`. Compose-shared adapters would
reintroduce the test-pollution surface the harness was
designed to avoid.

The architectural insight: **conformance and dev-iteration are
different workflows.** Dev iteration wants long-lived services
with stable state — Compose's model. Correctness verification
wants per-test fresh state with controlled identity —
subprocess's model. Conflating them optimises for one workflow
at the cost of the other; preserving both is cheap (build
artefacts cost ~10 MB per adapter on disk).

The build-artefact recipes the conformance harness consumes
are preserved:

- `just pw-build` produces `adapters/playwright/dist/index.js`
- `just sb-bootstrap` produces `adapters/seleniumbase/.venv/`
- `just curl-imp-build` produces
  `adapters/curl-impersonate/bin/adapter`

After R6.2 contributors carry two artefact ownership shapes:

- **Conformance flow:** `just <adapter>-build` (or `*-bootstrap`)
  produces native binaries the conformance harness spawns.
- **Dev-loop flow:** `just images` (bake umbrella) produces
  Compose-consumable images.

Each flow's artefact source is independent. A change in
`adapters/playwright/src/server.ts` requires a `just pw-build`
to test against conformance and a `just compose-rebuild
playwright-adapter` to test against the Compose stack. The
recipes are independent because the verification surfaces are
independent.

## §6 — Operator outside Compose for R6.2

The most consequential scope decision. The control plane
(`core/control-plane/cmd/main.go`) calls
`ctrl.GetConfigOrDie()`, which reads `$KUBECONFIG` or
in-cluster service-account credentials. To run inside Compose
the operator needs **either** a kubeconfig pointing at a
Kubernetes API server reachable from the Compose network, **or**
in-cluster service-account credentials, which only exist when
running inside a Pod with a mounted token.

Three options were on the table:

- **(A) Operator in Compose, kind cluster as a Compose service.**
  Requires Docker-in-Docker (kind launches its own Docker
  daemon to run cluster nodes) or a privileged kind-in-Docker
  container. Cross-platform networking between the in-Compose
  kind API server and the in-Compose operator container is
  brittle (kind's CNI vs Compose's bridge network). The
  developer's host does not have native access to the kind API
  server — `kubectl` from the host needs port-forward or DinD
  awareness.
- **(B) Operator in Compose, kind cluster on the host,
  KUBECONFIG mounted into the operator.** Requires careful
  cross-platform handling: `host.docker.internal` works on
  Docker Desktop (macOS / Windows) but needs explicit setup on
  Linux; the kubeconfig path inside the operator container must
  match the path the host's kind references; the operator
  container's network must reach the host's kind API
  (port-forwarded or via `host.docker.internal`).
- **(C) Operator on the host, dialling the Compose-running
  engine via the host-port mapping.** Pre-R6.2 status quo. The
  operator runs via `just op-run` against the developer's
  external kind cluster; it dials `127.0.0.1:8090` (Compose's
  host-side mapping for the engine container). On Linux this
  resolves directly; on Docker Desktop the same address
  resolves through Docker Desktop's port-publishing mechanism.
  Operationally identical to today's flow.

**Decision: (C) for R6.2; (A)-shaped resolution deferred to R6.3.**

Reasoning:

- Master strategy §2.5 ("Compose is the development environment")
  is stated against application services. The operator is the
  sole exception R6.2 carries forward. R6.3 in the master plan
  is "Devcontainer with Docker-in-Docker" — the natural home for
  the Docker-daemon + kind-cluster + Compose-stack triangulation
  that (A) requires.
- Splitting "application services into Compose" (R6.2) from
  "operator + kind into the unified shape" (R6.3) is the
  cleaner decomposition. R6.2's diff is large but
  architecturally uniform (Compose YAML extension + port
  migration); R6.3's diff is mechanically different
  (devcontainer Dockerfile + kind setup script + DinD wiring).
  Combining them doubles the reviewable surface area.
- For R6.2 `just op-run` works without ceremony. The operator
  dials the host's port 8090 (Compose's mapping for the
  engine); Compose handles the cross-network bridging.

R6.3's problem statement, recorded explicitly here so the next
contributor inherits the precise problem to solve:

> **R6.3 must place the control-plane operator container in the
> same Docker daemon as the Compose stack, alongside a `kind`
> cluster running in that same Docker daemon (Docker-in-Docker
> inside the devcontainer). The operator dials `engine:8090` on
> the Compose network; the operator's kubeconfig points at the
> in-DinD `kind` API server; `kubectl` from the host (or from
> within the devcontainer's shell) reaches both the operator's
> Pod logs and the kind API server through the devcontainer's
> port forwarding. The justfile loses `op-run` as the canonical
> recipe and gains a Compose-side equivalent under the
> `--profile full` topology.**

R6.2 commits to this future shape by:

- Reserving the DNS name `control-plane` (or `operator` —
  R6.3's choice) on the Compose network. R6.2 does not add a
  service block under that name; R6.3 does.
- Reserving the host-port mapping space `8081` (the operator's
  health-probe port from `cmd/main.go`'s
  `--health-probe-bind-address=:8081` default) so R6.3's
  Compose extension does not have to negotiate with another
  service.
- Documenting the deferral in `docs/architecture/control-plane.md`
  §6 (R6.2 update) and pointing forward to R6.3.

### R6.3 update — resolution

> **Status — resolved in R6.3 (2026-04-29).** The §6 problem
> statement above is the spec; R6.3 is the execution. No new
> ADR was introduced (per ADR-0020 §4 the refactor table locks
> Phase R6 to ADR-0025); R6.3's audit trail is this update note
> plus ADR-0018 §3a R6.3 evolution.

R6.3 places the operator inside the Compose stack as the
`control-plane` service (image `spectre-control-plane:dev`,
built by R6.1's `op-image` bake target). The kind cluster runs
in the devcontainer's Docker-in-Docker daemon, managed by `just
kind-up` / `kind-down` / `kind-status`. The operator container
joins both the Compose default network (for `engine:8090` and
`postgres:5432`) and the external `kind` Docker network (for
`spectre-dev-control-plane:6443` — kind names the API-server
node after the cluster); the kubeconfig at
`build/kind/kubeconfig` (written by `just kind-up --internal`)
is mounted read-only at `/home/nonroot/.kube/config`.

The justfile changes match the §6 problem statement's
prescription:

- `op-run` is **deleted** (no legacy paths; ADR-0020 §4 + master
  strategy §2.2). The host-process operator flow is gone.
- `op-install-crds` / `op-uninstall-crds` are renamed
  `crds-install` / `crds-uninstall` and repointed at
  `build/kind/kubeconfig`. The old names survive one cycle as
  deprecation aliases (printed note + forward to the new
  recipes); removed in R7.1.
- New recipes: `kind-up`, `kind-down`, `kind-status` for the
  cluster lifecycle.

The unified flow is `just kind-up && just compose-up`; on first
devcontainer build the post-create script runs `kind-up` +
`crds-install` automatically. `compose-up` and `compose-down` do
not touch the kind cluster — the lifecycles are independent
(§4.5 of the R6.3 phase prompt records the rationale: kind
startup is paid once per devcontainer session, not per
`compose-up`).

The four R6.3 decisions that warrant explicit recording here
(carried forward from the phase prompt's §4):

1. **DinD over socket-mount.** Master plan ADR-0020 §85–91
   commitment; the official `docker-in-docker` feature carries
   the cross-platform load.
2. **kind via lifecycle recipes, not as a Compose service.**
   Lifecycle independence; kind is infrastructure for the
   operator's reconciliation API, not an application service in
   the Compose sense.
3. **Operator joins both networks** via Compose's
   per-service `networks:` list + a top-level
   `networks: kind: external: true name: kind` declaration.
   `kind get kubeconfig --internal` produces the in-network
   URL the operator's mounted kubeconfig points at.
4. **Conformance flow unchanged.** §5 above remains in force —
   R6.3's DinD + kind work does not migrate the conformance
   harness onto the Compose adapters.

After R6.3 merges:

- `docker compose --profile full up -d` brings up eleven
  services (six stateful + engine + three adapters + operator).
- A `spectre-dev` kind cluster runs in the devcontainer's DinD
  daemon.
- `kubectl apply -f core/control-plane/config/samples/spectre_v1alpha2_scrapejob_endpoint.yaml`
  reconciles Pending → Running → Completed end-to-end through
  the operator → engine → adapter chain, with the row visible in
  Postgres (`SELECT id, status, output_sink_kind, rows_extracted FROM jobs`).
- The host-process `op-run` recipe is gone.
- ADR-0018 §3a R6.3 evolution records the devcontainer side of
  the change.
- **Phase R6 closes** with this PR. The master-strategy §2.5
  promise — "what runs in development equals what runs in
  production" — holds for application services and their direct
  dependencies. v1alpha1 deferrals (mTLS, multi-arch images,
  Helm chart) are Phase R7 work.

## §7 — Port migration: 9090–9093 → 8090–8093

ADR-0021 §4's port plan has read 8090–8093 since R2.1; the
implementation has been at 9090–9093 since R2.3 ("the engine's
default bind port"). The discrepancy was tolerable under the
hybrid because each native binary bound its own loopback port
in isolation — Kafka's 9092 collision with the seleniumbase
adapter only manifested if a contributor explicitly ran both
simultaneously. R6.2 introduces the unified Compose stack
where every service binds a host port at the same time; the
collision becomes a hard failure mode.

The migration is enacted in the same PR that introduces the
Compose extension, as a single sweeping commit reviewable in
isolation. The grep pattern is exhaustive across the
repository:

```bash
git grep -n -E '\b(9090|9091|9092|9093)\b' \
  -- ':!Cargo.lock' ':!**/uv.lock' ':!**/pnpm-lock.yaml' \
     ':!**/go.sum'
```

Each match is reviewed by hand and categorised:

- **Real port literal** — application port; flips to the
  8090–8093 equivalent.
- **Kafka 9092** — the broker port; left alone (the reason this
  whole migration exists is to *preserve* Kafka's default).
- **Historical reference** — CHANGELOG entries, ADR-0021 §4's
  numeric table, the rationale paragraph in ADR-0021 §4 that
  explains "starts at 8090, not 9090, on purpose"; the
  ADR-0023 §3 R4.4 addendum that mentions Apache Kafka KRaft
  mode's listeners; this ADR's own §7. Left alone — historical
  text is the authoritative trail of what the migration
  preserved.
- **Numeric coincidence** — none materialised in practice
  (years are 2026; no histogram bucket bands fall in 9090–9093).

| Domain                  | Old | New | Source of truth                               |
|-------------------------|-----|-----|-----------------------------------------------|
| engine                  | 9090| 8090| `SPECTRE_ENGINE_PORT`                         |
| playwright-adapter      | 9091| 8091| `SPECTRE_ADAPTER_GRPC_PORT` (per image)       |
| seleniumbase-adapter    | 9092| 8092| `SPECTRE_ADAPTER_GRPC_PORT` (per image)       |
| curl-impersonate-adapter| 9093| 8093| `SPECTRE_ADAPTER_GRPC_PORT` (per image)       |
| control-plane HTTP      | 8080| 8080| unchanged (was already canonical)             |

What stays:

- **Kafka 9092** — broker port; this whole migration's reason
  for being.
- **Kafka container internal listeners** (PLAINTEXT 9092,
  CONTROLLER 9093, HOST 9094) — internal to the Kafka
  container's network namespace; not application ports.
- **Compose host-side mapping conventions** — every
  application service maps its container port 1:1 on the host
  (`8090:8090`, `8091:8091`, ...). Pre-R6.2 host-port
  conventions are dropped wholesale.

Files affected in the sweep span the engine binary, the
control-plane binary, the controller, the runner, the
controller and runner test suites, the v1alpha2 type defaults,
the regenerated CRD YAML, the three adapter Dockerfiles, the
three adapter READMEs and adapter test fixtures, the
conformance demo CLIs, the example READMEs, the architecture
docs (control-plane.md, engine.md, redis.md, postgres.md), the
top-level README quick-start, `.env.example`, and the justfile.
The CHANGELOG records the migration's scope.

ADR-0021 §4 receives a "R6.2 implementation note" addendum
recording the realisation. The numeric table itself is unchanged
— ADR-0021 §4 has been right since R2.1; the pre-R6.2
implementation was lazy.

## §8 — Image-source policy: `image:` + `pull_policy: never`

Compose's `services.<name>.image:` directive accepts a local
image reference; combined with `pull_policy: never` it commits
Compose to "use the local image if present; fail with image-
not-found otherwise". `services.<name>.build:` would chain the
build through Compose itself; R6.2 declines that path.

Reasoning:

- **Image-freshness contract is honest.** A user who edits
  `core/engine/src/server.rs` and runs `just compose-up`
  cannot reasonably expect the running engine to reflect the
  edit unless `just images` (or the targeted equivalent) has
  rebuilt the image. Compose's `build:` directive does not
  rebuild on source change; it builds on `compose build` or
  `compose up --build`. The implicit contract is broken either
  way; the explicit contract — "`just images` then `just
  compose-up`" — keeps the rebuild step visible.
- **R7.1's Helm chart is shape-symmetric.** The Helm chart will
  consume pre-built images from a registry (`spectre-engine`
  at `ghcr.io/...:<sha>`); there is no Helm equivalent of
  `build:`. Keeping the Compose flow shape-symmetric with the
  production flow reduces mental-model drift between
  environments.
- **Bake is the canonical build path.** R6.1 committed to
  `docker buildx bake` as the single orchestrator for image
  builds. Letting Compose own a parallel `build:` path would
  create two image-build entry points with potentially
  different argument resolution and label injection.

`pull_policy: never` is the deliberate choice over `missing`
(Compose's default — pull from registry if not local) and
`always`. `never` prevents Compose from contacting a registry
under any circumstances; the local image is the only source.
Without the policy, a typo'd image reference would surface as
a slow registry pull-and-fail rather than an immediate
"image not found" error.

The justfile makes the contract explicit:

- `just compose-up` documents that `just images` must run
  first.
- `just compose-rebuild SERVICE` chains
  `bake <SERVICE> && compose up -d --no-deps SERVICE` for the
  dev-iteration flow when only one service's source has
  changed.

## §9 — What's deferred to R6.3

> **Status — resolved in R6.3 (2026-04-29).** Every deferral
> below is closed by the §6 R6.3 update above. Phase R6 is
> CLOSED with R6.3's merge.

R6.3 is "Devcontainer with Docker-in-Docker" in the master
strategy plan. The deferrals from R6.2 are:

- **Operator in Compose.** §6 above. **Resolved in R6.3** by the
  DinD + kind + dual-network-join shape (§6 R6.3 update).
- **`kind` cluster as a Compose-managed service.** **Resolved in
  R6.3 with a small reframe.** Per §4.2 of the R6.3 phase
  prompt, kind is managed by lifecycle recipes (`just kind-up`
  / `kind-down` / `kind-status`), not by Compose's profile
  system; the Compose stack attaches to kind's existing Docker
  network via `external: true`. Lifecycle independence beat the
  "kind-as-Compose-service" sketch on simplicity and on
  per-`compose-up` startup cost.
- **Devcontainer Docker-in-Docker.** **Resolved in R6.3** by
  adding the official
  `ghcr.io/devcontainers/features/docker-in-docker:2` feature
  to `.devcontainer/devcontainer.json`. ADR-0018 §3a R6.3
  evolution records the audit trail.
- **The unified `just compose-up` shape that brings up
  everything.** **Resolved in R6.3** — `--profile full` brings up
  eleven services (the R6.2 ten + the new `control-plane`).
  README quick-start updated; the host-process `op-run` recipe
  is deleted entirely.

Phase R6 closes when R6.3 lands — done.

## §10 — Out of scope

These belong to later phases. R6.2 declines them deliberately.

- **Helm chart packaging.** R7.1; tracked via ADR-0026 (when
  drafted).
- **Multi-arch image builds and registry publishing.** R7.1.
  R6.1 reaffirmed single-arch (`linux/amd64`) for v1alpha1.
- **Production smoke (Helm-installed cluster).** R7.2.
- **Per-job Pod isolation.** v1alpha1's engine is a long-lived
  service that handles every `RunJob`; per-job containerisation
  is a v1alpha2-or-later concern.
- **Service mesh (Istio / Linkerd / Consul-Connect) integration.**
  Compose's default network covers v1alpha1's needs; mesh
  adoption is documented as out-of-scope in ADR-0021 §
  "What this ADR does not decide".
- **mTLS between application services.** ADR-0022 §6 commits
  v1alpha1 to plaintext gRPC on a trusted private network.
  R6.2 inherits that posture.
- **Operator running across multiple Compose hosts (Swarm /
  Compose v3 deploy).** Compose v3 deploy directives are
  Kubernetes-shaped; production deployment is R7.1's Helm.
- **Modifying the proto schema, capabilities, or driver
  protocol.** Master strategy §2.1, §2.3.
- **Modifying any service's source code beyond port-default
  literals.** R6.2 is environment + topology, not business
  logic. The port-default flips touch
  `core/engine/src/bin/spectre.rs` (DEFAULT_PORT),
  `core/engine/src/registry.rs` (adapter endpoint defaults),
  `core/control-plane/cmd/main.go` (defaultEngineEndpoint),
  `core/control-plane/internal/controller/scrapejob_controller.go`
  (`defaultEnginePort`), and the corresponding test fixtures.
  No business-logic source is touched.
- **Migrating conformance to dial Compose-running adapters.**
  §5 above — explicitly rejected.
- **Pulling images from a registry on `compose up`.**
  `pull_policy: never` is the contract per §8.

## §11 — Reference materials

- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane architecture; §3 (post-R3.1 supersession)
  carries forward the operator-in-Compose deferral.
- [ADR-0020](0020-microservices-architecture-supersession.md)
  — refactor architectural commitment; §4 (R6 phase plan)
  positions R6.2 within Phase R6.
- [ADR-0021](0021-service-discovery.md) — service discovery
  via env vars + DNS. §4's port plan is enacted by this ADR §7;
  §6's healthcheck contract is honoured by §3 above (with the
  asymmetric-base adaptation).
- [ADR-0022](0022-tcp-grpc-transport.md) — TCP / gRPC
  transport. §4 (gRPC client retry semantics) is what the
  engine's no-Compose-healthcheck choice (§3 above) leans on.
- [ADR-0023](0023-stateful-services-architecture.md) — stateful
  services. §6 (required-vs-optional matrix) and §9 (Compose-
  side topology) are extended by this ADR's §3 with the
  application services.
- [ADR-0024](0024-output-sinks.md) — output sinks. §3 (S3 +
  MinIO) is consumed by the engine service in §3 above via
  the `SPECTRE_S3_*` env vars.
- [ADR-0018](0018-devcontainer-and-engine-image.md) — pre-R6.1
  devcontainer + engine image; revisited by R6.3 (DinD).
- Docker Compose profiles:
  <https://docs.docker.com/compose/how-tos/profiles/>
- `pull_policy` directive:
  <https://docs.docker.com/reference/compose-file/services/#pull_policy>
- `depends_on` conditions:
  <https://docs.docker.com/reference/compose-file/services/#depends_on>
- Compose service network model:
  <https://docs.docker.com/compose/how-tos/networking/>
- Bash TCP redirect (`/dev/tcp/`):
  <https://www.gnu.org/software/bash/manual/bash.html#Redirections>
- Python `socket` module:
  <https://docs.python.org/3/library/socket.html>
- `docs/architecture/development-environment.md` — user-facing
  guide for the R6.2 dev flow.
