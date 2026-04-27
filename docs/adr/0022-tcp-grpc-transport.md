---
status: accepted
date: 2026-04-27
deciders: [Fabio Caffarello]
---

# TCP / gRPC transport

## Context and Problem Statement

ADR-0008 selected gRPC over a Unix domain socket as the
adapter-engine transport. The choice was correct for the
demonstrator: PR3 needed the lowest-friction path to a working
handshake on a single host, and a UDS path under `/tmp` removed
every networking concern (binds, ports, addresses, host
resolution) from the smallest possible end-to-end exercise.
ADR-0008 §2 records the lifecycle decisions that flow from a
UDS bind: a `--socket=` flag, a `SPECTRE_DRIVER_SOCKET` env var,
a `ready unix:<path>` readiness banner on stdout, a SIGTERM-
driven socket unlink on shutdown.

ADR-0020 retires the subprocess-in-pod model. Adapters become
standalone services in their own Pods or Compose containers;
the engine dials them across container boundaries. A UDS path
under `/tmp` is not visible across a network namespace boundary,
so the transport that ADR-0008 §2 selected no longer fits the
deployment shape ADR-0020 §2 commits to.

This ADR records the TCP transport contract that replaces UDS
and the inventory of changes required to retire UDS from the
codebase. The companion ADR-0021 covers service discovery — how
consumers find producers — without which the transport is
unreachable. The two ADRs are bundled because they specify the
same boundary at two layers.

## Decision Drivers

Three constraints bound the transport choice.

- **Cross-container reachability.** Services in separate Pods
  (Kubernetes) or separate Compose containers cannot share a
  filesystem-anchored UDS path. The transport must work across
  network namespace boundaries — TCP is the universal answer
  for inter-container traffic on both platforms.
- **Protocol freeze (ADR-0020 §4 invariant one).** The
  `proto/spectre/driver/v1alpha1/` directory is read-only
  across the refactor. The wire contract — the message
  definitions, the service definitions, the streaming
  semantics — does not change. Only the bind address and the
  dial address change. Switching from UDS to TCP at the
  transport layer satisfies this; both transports carry the
  same gRPC frames.
- **Conformance assertions byte-identical (ADR-0020 §4
  invariant two).** Each adapter's capability list — Playwright
  13, SeleniumBase 12, curl-impersonate 6 — must remain
  byte-for-byte identical pre and post refactor. The transport
  switch is permitted to alter how the conformance harness
  reaches the adapter, but the assertions inside the tests
  (capability counts, capability names, message-shape
  equivalence) are unchanged.

These three constraints rule out anything other than gRPC over
TCP. Alternative transports (HTTP/1.1 + JSON, message-broker-
mediated RPC) would require either a wire-format change
(protocol freeze violation) or a translation layer (operational
complexity with no offsetting value at v1alpha1 scale). gRPC
keeps the protocol intact and the transport switch surgical.

## The TCP transport contract

Every gRPC service in the topology — the engine and the three
reference adapters — exposes a TCP listener on the port
ADR-0021 §4 assigns. Five rules govern the bind and dial sides.

### Bind

- Each producer binds `0.0.0.0:<port>` so cross-container
  traffic reaches it. Binding `127.0.0.1` is forbidden — the
  loopback interface is not visible from peer containers, and
  silent unreachability would mimic UDS-path-not-found failure
  modes the refactor is meant to eliminate.
- Each producer reads its bind port from the env var ADR-0021
  §5 specifies for it (`SPECTRE_ENGINE_GRPC_PORT` for the
  engine, `SPECTRE_ADAPTER_GRPC_PORT` for the adapters). The
  defaults match the canonical port table.
- Each producer registers the standard
  `grpc.health.v1.Health/Check` service alongside its own
  service implementation (ADR-0021 §6).

### Dial

- Each consumer reads its dependency endpoints from env vars
  with the `grpc://<host>:<port>` URI scheme (ADR-0021 §5).
- Each consumer constructs one gRPC channel per dependency at
  startup. Channels are kept alive across the consumer's
  lifetime; the gRPC implementation handles the underlying TCP
  reconnect on transient failures.
- Streaming RPCs (`WatchEvents`, `WatchDom` from
  `proto/spectre/driver/v1alpha1/`) ride the same channels.
  TCP supports gRPC streaming natively; no transport-layer
  workaround is needed.
- The `:authority` pseudo-header workaround that ADR-0008 §1
  introduced for Node's `http2`-over-UDS quirk is retired.
  TCP-bound HTTP/2 servers do not exhibit the constraint;
  consumers no longer need to set
  `grpc.default_authority=localhost`. The retirement is part
  of ADR-0022 §5's inventory.

The combination of the env-var endpoint and the gRPC channel is
the entire client-side surface. There is no UDS-or-TCP
parameterisation; the no-legacy principle (strategy prompt
§2.2) requires the UDS path be deleted in the same PR that
introduces the TCP path.

## Connection lifecycle

The engine and the conformance harness are the two consumers
that dial gRPC services in v1alpha1. Both follow the same
lifecycle.

### Startup: eager dial, eager fail

On startup, a consumer dials every dependency listed in its
env-var contract. Each dial issues an initial
`grpc.health.v1.Health/Check` request before declaring the
channel ready. A failure at this stage — DNS resolution
failure, TCP connect refused, health check returning
`NOT_SERVING` or timing out — is a startup failure, not a
runtime fallback. The consumer logs the unreachable endpoint
and exits non-zero.

The eager-fail model trades a slower startup window (each
dependency must be reachable before the consumer enters its
serve loop) for a deterministic failure signal. A
misconfigured deployment fails at startup — visible to
`kubectl get pod` and `docker compose up` as the consumer
crashing — instead of failing on the first request. The
alternative (lazy dial on first request) was rejected because
it converts deployment misconfiguration into per-job latency
and per-job error noise.

To accommodate slow-starting dependencies the consumer applies
a bounded retry on the initial dial: up to thirty seconds of
exponential-backoff dial attempts before giving up. The thirty-
second budget covers Playwright runtime startup (≈ 4 s on a
warm image, slower under cold image-pull) and Compose's
`depends_on:` ordering with healthchecks. Beyond thirty
seconds the consumer treats the dependency as broken and
exits.

### Steady state: gRPC keepalive

Once channels are established, the consumer relies on gRPC's
built-in connection management. Each channel uses HTTP/2
keepalive (PING frames every thirty seconds, idle timeout of
ten minutes) so transient TCP failures surface as channel
state changes rather than per-RPC errors. Per-RPC retries are
deferred to the application layer — the engine, the
conformance harness, and the control plane each apply their
own retry policy as appropriate to the call.

### Shutdown: graceful close

On SIGTERM, a consumer closes each channel via context
cancellation. In-flight RPCs are aborted with the
`CANCELLED` status; the gRPC implementation returns when the
underlying TCP teardown completes. A producer that receives a
SIGTERM transitions its health status to `NOT_SERVING` so
dependents observing health-check streams (ADR-0021 §6) see
the state change before the process exits, then drains
in-flight RPCs up to a five-second deadline before exiting.

The lifecycle is the same on Compose, Kubernetes, and the
conformance suite. Per-platform divergence is confined to how
the platform sends SIGTERM (Compose's `docker compose down`,
Kubernetes' Pod termination grace period, the conformance
harness's `Popen.send_signal`).
