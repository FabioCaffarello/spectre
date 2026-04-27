---
status: accepted
date: 2026-04-27
deciders: [Fabio Caffarello]
---

# Service discovery

## Context and Problem Statement

ADR-0020 commits Spectre to a microservices architecture: each
component — engine, control plane, three reference adapters —
becomes a standalone service exposing gRPC over TCP. With the
subprocess-in-pod model retired (ADR-0019 §3 superseded), the
engine no longer spawns adapters. It dials them. That move
surfaces a question the demonstrator never had to answer: how
does a service consumer find its dependencies on the network?

Three answers are common in microservices: a service registry
(etcd, consul) where consumers look up endpoints at runtime; a
declarative configuration file (`services.yaml`) read at startup;
environment variables populated by the platform. Each has a
defensible niche; only one fits Spectre's v1alpha1 shape, which
is five fixed services deployed under either Compose or
Kubernetes.

This ADR records the discovery contract every refactor PR from
R2 onward implements. The companion ADR-0022 covers the
transport-level details (TCP bind, channel lifecycle, gRPC
health protocol). Reading either ADR alone is incomplete;
together they specify the new boundary between services that
ADR-0020 commits to.

## Decision Drivers

The maintainer locked the discovery model before R2.1 began.
Four constraints bound the design space.

- **Five fixed services, two known platforms.** v1alpha1 of
  the refactor produces five application services
  (control-plane, engine, three adapters) deployed under either
  Compose (locally) or Kubernetes (production). The number of
  services is small and the deployment topology is known at
  build time. Discovery mechanisms that pay for runtime
  flexibility — service registries, sidecar proxies — recover
  no value from those costs at this scale.
- **The Twelve-Factor pattern is platform-neutral.** Both
  Compose and Kubernetes natively populate environment
  variables in the container's process space (Compose's
  `environment:` directive, Kubernetes' `env:` field on a
  Deployment). The same env-var contract works in both
  platforms with no platform-specific code in the consumer.
- **Compose-only local development (ADR-0020 §2 driver four).**
  There is no native developer mode that bypasses the
  containerised stack. Every consumer runs inside a container
  on a Compose or Kubernetes network. Discovery does not need
  a fallback for a third runtime context.
- **Operational legibility.** A reader debugging a misconfigured
  deployment should be able to grep one place — the env vars on
  a service — to see what its dependencies are. The Twelve-
  Factor pattern keeps discovery state visible in the same
  place the operator already inspects (`kubectl describe pod`,
  `docker compose config`). A registry hides this state behind
  a network call.

These four drivers point at one answer: environment variables
populated by the platform, resolved at startup, with the
service-name component routed by the platform's built-in DNS.
ADR-0021 ratifies that answer; subsequent phases consume it.

## The discovery model

Each service consumer reads its dependencies' endpoints from
environment variables at process startup. Endpoints follow the
URI scheme `grpc://<service-name>:<port>`. The `<service-name>`
component is a DNS name that resolves via the container
platform's built-in DNS:

- **Kubernetes.** A `Service` of type `ClusterIP` named
  `engine` (in namespace `spectre`) is reachable as
  `engine.spectre.svc.cluster.local`. Inside the same
  namespace, the short form `engine` resolves to the same
  address via the cluster's search-domain configuration. The
  Helm chart introduced in R7 templates the
  `Service` objects so the names are stable.
- **Compose.** A service block named `engine` in
  `docker-compose.yml` is reachable as `engine` on the
  Compose network — Compose's embedded DNS resolver maps the
  service name to the container IP. The R6.2 Compose stack
  pins these names to the same identifiers Helm uses.
- **No third platform.** ADR-0020 §2 driver four retires
  native local development. A contributor who cannot run
  Docker cannot exercise the discovery contract; this is an
  accepted trade-off (ADR-0020 §4 fourth "bad" consequence).

Endpoints are read once at startup. Consumers do not subscribe
to changes at runtime; if a service's DNS record updates (Pod
recreation, Compose restart) the consumer's next dial picks up
the new address through the platform's DNS TTL. Connection
liveness is handled by the gRPC channel and ADR-0022 §4.

Reading endpoints from environment variables — rather than
hard-coding them or reading them from a config file — keeps the
consumer's dependency graph visible to the platform layer
(`docker compose config`, `kubectl describe pod`). The Helm
chart and the Compose file are the single source of truth for
which service points at which.

This ADR records the contract every R2-and-later phase consumes;
ADR-0022 specifies what consumers do once they have the endpoint
string.

## Port allocation

Five fixed application ports plus the three stateful service
ports introduced in R4. The application ports sit in the
8090–8099 range; the stateful services keep the upstream
ecosystem-standard ports.

| Service                   | Port | Notes                                                                  |
|---------------------------|------|------------------------------------------------------------------------|
| control-plane (HTTP)      | 8080 | Kubernetes-controller idiom; serves `/healthz`, `/readyz`, `/metrics`. |
| engine                    | 8090 | gRPC. Dialled by control-plane (R3.1) and conformance suite (R2.2).   |
| playwright-adapter        | 8091 | gRPC. Dialled by engine.                                               |
| seleniumbase-adapter      | 8092 | gRPC. Dialled by engine.                                               |
| curl-impersonate-adapter  | 8093 | gRPC. Dialled by engine.                                               |
| PostgreSQL (R4.2)         | 5432 | Upstream default. Control-plane job state.                             |
| Kafka broker (R4.4)       | 9092 | Upstream default. Engine output topic.                                 |
| Redis (R4.3)              | 6379 | Upstream default. Adapter session cache.                               |

The application range starts at 8090, not 9090, on purpose. Kafka
in R4.4 binds the ecosystem-standard 9092; placing adapters at
9091 / 9092 / 9093 would force Kafka onto a non-default port,
which in turn forces every external Kafka consumer (operator
tooling, debug shells, downstream sinks) to pass an explicit
bootstrap-server override. Holding the application ports below
the Kafka range keeps Kafka's defaults intact and avoids cargo-
culting a Kafka port into a refactor that has no Kafka-related
reason to touch it.

8080 for the control plane mirrors the Kubebuilder scaffold the
control plane was generated from (ADR-0019 §6) and the broader
Kubernetes-controller convention. The control plane does not
expose a gRPC interface; its job is to reconcile `ScrapeJob`
resources and dial the engine over gRPC. Its 8080 port carries
HTTP-only traffic (probes, metrics) that the cluster scrapes.

## Environment variable contract

Each consumer reads the endpoints of the services it talks to
from environment variables. Each producer reads the bind port
of its own gRPC server from a separate variable. The two
families do not overlap — a service that produces an endpoint
does not read the same name a consumer of that endpoint reads.

### Endpoints (read by consumers)

| Variable                                | Read by                          | Default                                  |
|-----------------------------------------|----------------------------------|------------------------------------------|
| `SPECTRE_ENGINE_ENDPOINT`               | control-plane, conformance suite | `grpc://engine:8090`                     |
| `SPECTRE_PLAYWRIGHT_ENDPOINT`           | engine                           | `grpc://playwright-adapter:8091`         |
| `SPECTRE_SELENIUMBASE_ENDPOINT`         | engine                           | `grpc://seleniumbase-adapter:8092`       |
| `SPECTRE_CURL_IMPERSONATE_ENDPOINT`     | engine                           | `grpc://curl-impersonate-adapter:8093`   |

The defaults match the Compose stack the R6.2 PR introduces.
The Helm chart in R7 templates the same names against the
namespaced cluster DNS (`engine.<ns>.svc.cluster.local` and so
on); the Helm values surface the namespace and the port for
operators who need to override.

### Bind ports (read by producers)

| Variable                       | Read by                       | Default |
|--------------------------------|-------------------------------|---------|
| `SPECTRE_ENGINE_GRPC_PORT`     | engine                        | `8090`  |
| `SPECTRE_ADAPTER_GRPC_PORT`    | each adapter (binary-scoped)  | per adapter (8091 / 8092 / 8093) |

A single `SPECTRE_ADAPTER_GRPC_PORT` name covers all three
adapters because each adapter binary is built and deployed
separately; the variable's value differs per Pod / per Compose
service. Sharing the variable name keeps the adapter source code
identical (ADR-0020 §4 protocol-freeze invariant) and pushes the
deployment-shape decision out to the platform configuration.

### URI scheme

The `grpc://` prefix is informational. Adapter and engine clients
parse the scheme, host, and port, then dial via standard gRPC
client APIs (Tonic on Rust, `grpcio` on Python, `@connectrpc/connect-node`
on Node, `google.golang.org/grpc` on Go). The scheme reserves the
right to add `grpcs://` later (mTLS, ADR-0022 §6) without
changing the variable's name. v1alpha1 of the refactor accepts
only `grpc://`; consumers that see anything else fail at startup.

### What the contract does not include

Endpoints for the stateful services (PostgreSQL, Kafka, Redis)
are deferred to ADR-0023 (R4.1). This ADR is scoped to the gRPC
service-to-service boundary the engine and adapters expose.

## Healthcheck contract

Every gRPC service in the topology — the engine and the three
adapters — exposes the standard
[gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md):
the `grpc.health.v1.Health/Check` unary RPC and the
`grpc.health.v1.Health/Watch` streaming RPC, both registered
under the empty service name (`""`) so a generic probe can
verify the server is alive without knowing the application's
service names.

The protocol is defined upstream and supported across all four
target languages (Rust via `tonic-health`, Node via
`@grpc/grpc-js-health-check` or `@connectrpc/connect-node`'s
built-in registry, Python via `grpcio-health-checking`, Go via
`google.golang.org/grpc/health`). Choosing the standard means
no protocol invention work in the refactor and uniform tooling
across services.

Operators consume the contract three ways:

- **Compose `healthcheck:` blocks (R6.2).** Each adapter and
  the engine ship a `grpc_health_probe` binary in their image
  and declare a `healthcheck:` directive that exits zero on a
  `SERVING` response. `depends_on:` blocks elsewhere in the
  Compose file gate startup on the dependency reporting
  healthy.
- **Kubernetes readiness / liveness probes (R7.1).** The Helm
  chart's Pod templates declare `readinessProbe.grpc.port`
  pointing at each service's gRPC port. Kubernetes 1.24+
  supports gRPC probes natively; earlier clusters fall back to
  a `grpc_health_probe` `exec` probe.
- **Engine startup dial (ADR-0022 §4).** When the engine dials
  an adapter, it issues an initial `Health/Check` before
  declaring the channel ready. A failed health check is a
  startup failure, not a silent half-open state.

Each service registers its own status, marks itself `SERVING`
once initialisation is complete (e.g. the Playwright adapter
once the Playwright runtime has imported), and can transition
to `NOT_SERVING` during graceful shutdown so dependents see
the state change before the process exits.

## What this ADR does not decide

Three concerns surface in adjacent reviews and are explicitly
out of scope for v1alpha1 of the refactor.

- **Mutual TLS and authentication.** The discovery contract is
  agnostic to TLS. ADR-0022 §6 covers v1alpha1's trusted-
  network posture and the v1alpha2 path. If TLS lands later,
  the env-var values change from `grpc://` to `grpcs://`; no
  discovery-shaped change is required.
- **Service mesh integration.** Istio, Linkerd, Consul-Connect,
  and similar meshes inject sidecars that subsume parts of the
  discovery and transport stack. The current discovery model
  is mesh-compatible (a sidecar can intercept the env-var
  endpoint and route through mTLS / observability layers), but
  no mesh-specific configuration ships in v1alpha1.
- **Service registry adoption.** If the topology grows beyond
  the five fixed services that v1alpha1 specifies — for
  example, a community-authored adapter pool with dynamic
  membership — a registry such as Consul or etcd becomes
  appropriate. The refactor declines that complexity now and
  reserves the right to revisit when the conditions arise.

## Considered alternatives

Three alternatives were on the table during the design pass.
Each is documented honestly so a future reader can audit the
choice.

### Service registry (etcd, Consul, Eureka)

A central registry where each service registers its endpoint at
startup and consumers look up dependencies at runtime. The
canonical microservices answer.

- Good, because dynamic topologies — services scaled by an
  external operator, services moving across nodes mid-flight,
  community-contributed services with no a-priori build-time
  knowledge — work without configuration churn.
- Good, because health information centralises in one place;
  an operator can audit which services are currently
  registered.
- Bad, because v1alpha1 has five fixed services. The
  flexibility a registry provides recovers no value at this
  scale. Kubernetes already provides DNS-based discovery as a
  cluster primitive; layering a registry on top duplicates a
  capability the platform offers.
- Bad, because adopting a registry adds a fourth stateful
  service alongside PostgreSQL, Kafka, and Redis. The Compose
  stack would carry a registry container that exists only to
  resolve five endpoints — a poor cost / value ratio.
- Bad, because every service language has to acquire a
  registry-client dependency. Cross-language adoption is
  uneven, and the bootstrap problem (how does a service find
  the registry?) reintroduces the exact env-var lookup the
  registry was meant to replace.
- Bad, because bringing a registry into a portfolio-grade
  refactor risks signalling cargo-cult engineering — adopting
  an enterprise pattern for the wrong scale.

Not chosen.

### Declarative configuration file (`services.yaml`)

A repository-tracked YAML file enumerating service names,
ports, and endpoints. Each service reads it at startup; the
Helm chart and Compose file consume the same source.

- Good, because the file becomes a single source of truth;
  drift between Compose and Helm is impossible.
- Good, because reviewers see the topology in one place,
  diff-tracked.
- Bad, because the file's value rises with topology size and
  mutability. Five fixed services do not justify the
  abstraction; the file would be a thin wrapper around the
  same names env vars already carry.
- Bad, because the file introduces a parser dependency in
  every service. The Twelve-Factor pattern needs no parser
  beyond the platform's own env-var handling.
- Neutral, because the file becomes attractive at v1alpha2
  scale. If endpoints turn dynamic (per-region adapter pools,
  service replicas with distinct addresses) `services.yaml`
  is the natural evolution. The refactor reserves that path.

Not chosen for v1alpha1; documented as the v1alpha2 evolution
candidate if the conditions develop.

### Hardcoded constants

Each consumer compiles its dependency endpoints as constants
in source code.

- Good, because nothing simpler exists.
- Bad, because deployment shape becomes a source-tree concern.
  Changing a port or a service name forces a recompile of every
  consumer. Helm and Compose customisation is impossible
  without source patching.
- Bad, because the practice contradicts the Twelve-Factor
  separation between code and configuration.

Not chosen.

## More Information

- [ADR-0020 — Microservices architecture supersession](0020-microservices-architecture-supersession.md)
- [ADR-0022 — TCP / gRPC transport](0022-tcp-grpc-transport.md) (companion document)
- [ADR-0008 — Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md)
  (the superseded UDS transport this ADR's discovery contract
  replaces; see ADR-0022 §5 for the removal inventory)
- gRPC Health Checking Protocol:
  <https://github.com/grpc/grpc/blob/master/doc/health-checking.md>
- The Twelve-Factor App, "Config":
  <https://12factor.net/config>


