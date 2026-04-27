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
