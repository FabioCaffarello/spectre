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

## Removal targets

The transport switch deletes every UDS-shaped construct from
the codebase. The list below is grep-derived from the repo
state at R2.1 and is the prescriptive work plan for R2.2 and
R2.3. The companion file [`docs/refactor-audit.md`](../refactor-audit.md)
restates the same inventory as a table for review-time
spot-checking.

The no-legacy principle (strategy prompt §2.2) requires that
each item in the list be deleted, not parameterised. There is
no UDS-or-TCP toggle, no fallback path, no environment-gated
branch.

### R2.2 — adapter-side removals

Per-adapter source code that binds, advertises, or signals on
a UDS:

- **Playwright adapter** (`adapters/playwright/`).
  - `src/server.ts` lines 626 and 639: replace
    `http2.createServer` + `server.listen(socketPath)` with a
    TCP-bound HTTP/2 server (or the `@connectrpc/connect-node`
    Node-server adaptor) listening on `0.0.0.0:<port>`.
  - `src/index.ts` lines 37-40 and 55: delete the
    `resolveSocketPath` helper, the `--socket` /
    `SPECTRE_DRIVER_SOCKET` precedence logic, and the
    `ready unix:<path>` stdout banner. Replace with a
    `SPECTRE_ADAPTER_GRPC_PORT` env-var read and a gRPC health
    service registration.
  - `src/index.test.ts` lines 34-49: delete the three
    `resolveSocketPath` tests (precedence, fallback, relative-
    path rejection); add an equivalent test for the bind-port
    resolver.
- **SeleniumBase adapter**
  (`adapters/seleniumbase/src/spectre_seleniumbase/adapter.py`).
  - Lines 39-61: delete the `resolve_socket_path` function and
    the `--socket` / `SPECTRE_DRIVER_SOCKET` precedence logic.
  - Lines 74-76: replace
    `server.add_insecure_port(f"unix:{socket_path}")` with
    `server.add_insecure_port(f"0.0.0.0:{port}")`.
  - Lines 84-100: delete the `ready unix:<path>\n` stdout
    banner; the gRPC health service is the new readiness
    signal.
  - `tests/test_smoke.py` lines 25-38: delete the
    `resolve_socket_path` test trio.
- **curl-impersonate adapter**
  (`adapters/curl-impersonate/cmd/adapter/`).
  - `main.go` lines 93, 106, 156-159 and the file header
    block (lines 15-22): replace
    `net.Listen("unix", socketPath)` with
    `net.Listen("tcp", fmt.Sprintf(":%s", port))`, delete the
    socket-path resolver and the `ready unix:` banner.
  - `main_test.go` lines 14-59: delete the
    `resolveSocketPath` test set.

Per-adapter manifests:

- `adapters/playwright/driver.yaml` line 6,
  `adapters/seleniumbase/driver.yaml` line 6,
  `adapters/curl-impersonate/driver.yaml` line 6:
  remove the `transports[0].kind: grpc-uds` block. The
  `transports` array is retired entirely — discovery moves to
  the env-var contract in ADR-0021 §5, and the manifest's
  `command` field becomes irrelevant once the adapter runs as
  its own container image (R6.1).

Per-adapter READMEs:

- `adapters/playwright/README.md`,
  `adapters/seleniumbase/README.md`,
  `adapters/curl-impersonate/README.md`: rewrite the "Run
  locally" sections to reference the Compose stack
  (R6.2). Remove the `--socket=` examples, the
  `ready unix:<path>` documentation, and the absolute-path
  warnings.

Conformance harness:

- `tools/conformance/src/spectre_conformance/harness.py`:
  retire the subprocess-spawning `DriverHarness`. The
  replacement is a TCP-dialling client that reads its target
  endpoint from an env var (or a fixture-supplied
  `grpc://<host>:<port>` URL when the harness is exercised
  outside the Compose stack). The class name `DriverHarness`
  may be preserved for test-source compatibility; the
  internals — process management, UDS allocation, readiness
  parsing — are deleted. The
  `from_driver_yaml` constructor retires; the new constructor
  reads endpoints from the Compose service or an explicit URL
  parameter.
- `tools/conformance/src/spectre_conformance/__init__.py`:
  update the module docstring to reflect the new harness
  shape; the `DriverHarness` re-export stays.
- `tools/conformance/tests/conftest.py` lines 50-146: rewrite
  the three adapter fixtures (`playwright_adapter`,
  `seleniumbase_adapter`, `curl_impersonate_adapter`) to dial
  TCP endpoints supplied by env vars or Compose service names;
  delete the manifest-reading `from_driver_yaml` calls and the
  manual `DriverHarness(command=…)` construction for
  SeleniumBase.
- `tools/conformance/README.md` lines 55, 93-97, 134-139:
  rewrite the harness-architecture section and the demo
  invocation examples.
- `tools/conformance/src/spectre_conformance/demo_navigate.py`,
  `tools/conformance/src/spectre_conformance/demo_full_cycle.py`:
  replace the `--socket=` invocations with the Compose
  workflow.

Build orchestration:

- `justfile` lines 307-313 (`curl-imp-run`), 350-354
  (`pw-run`), 389-395 (`sb-run`): rewrite or delete the
  per-adapter run recipes. The Compose stack (R6.2) is the
  canonical local-run path; standalone `just <adapter>-run`
  recipes lose their reason to exist.

Examples (touched for documentation correctness):

- `examples/hello-hackernews/README.md` line 57,
  `examples/seleniumbase-extract/README.md` line 73,
  `examples/curl-impersonate-extract/README.md` line 75:
  replace the `ready unix:<path>` narrative with a Compose
  stack reference.

### R2.3 — engine-side removals

The engine's UDS-shaped subprocess launcher and UDS gRPC
client retire entirely. The replacement is a TCP gRPC client
that reads adapter endpoints from env vars per ADR-0021 §5.

Engine launcher and client:

- `core/engine/src/launcher.rs`: retire the entire file. The
  module's responsibilities — manifest reading for the
  `grpc-uds` transport, UDS path allocation, subprocess spawn
  with `--socket=` and `SPECTRE_DRIVER_SOCKET`, stdout-line
  readiness parsing, SIGTERM-driven socket unlink — disappear
  with the subprocess-in-pod model. The crate's `lib.rs`
  removes the `pub mod launcher` line; consumers of
  `DriverHandle` migrate to ADR-0022 §3's TCP dial path.
- `core/engine/src/client.rs`: rewrite the UDS-specific
  `Client::dial(socket: &Path)` into a TCP-dial signature
  taking `&str` endpoint URLs (`grpc://host:port`). The
  `UnixStream::connect` connector, the
  `grpc.default_authority=localhost` `:authority` workaround
  (a Node-http2-over-UDS quirk that does not surface on TCP),
  and the path-only validation logic are deleted. The
  `tonic-health` health-check shim per ADR-0021 §6 is added
  for startup probing.
- `core/engine/src/engine.rs` line 117:
  `Client::dial(handle.socket_path())` becomes
  `Client::dial(<endpoint-from-env>)`. The `handle` source
  changes from `launcher::launch(…).await?` to a discovery
  helper that resolves the env var.

Engine binary entry points:

- `core/engine/src/bin/spectre.rs`: retire the `spectre run`
  CLI binary entirely. ADR-0020 §3 retires the CLI; the engine
  binary that survives is the gRPC service-mode binary
  introduced in R2.3. The workspace's `Cargo.toml` `[[bin]]`
  registration for `spectre` is removed alongside.

Engine tests:

- `core/engine/tests/integration.rs` line 14 and the
  `LocalServer::spawn()` helper at line 49: rewrite the
  integration test to dial a TCP-bound stub server instead of
  spawning a subprocess and dialling its UDS path.
- `core/engine/src/launcher.rs` test module (lines 494-612):
  removed alongside the production module.

Engine packaging:

- `core/engine/Cargo.toml` lines 28-36 and 66-73: update the
  dependency-rationale comments to remove UDS / Node-http2
  references; drop dependencies that exist solely for
  subprocess management (`nix` for SIGTERM signalling, the
  `regex` crate's UDS-path role) if no remaining call site
  uses them after the launcher retires.
- `core/engine/Dockerfile` line 12: rewrite the comment that
  documents the engine spawning adapters; the engine's
  Dockerfile becomes service-mode-only in R6.1.
- `core/engine/README.md` lines 5, 146, 154, 156: update the
  module overview and the file map to reflect TCP transport
  and the absence of the launcher module.

Cross-cutting documentation touched in R2.3 for accuracy:

- `docs/architecture/overview.md` and
  `docs/architecture/development-environment.md`: revise the
  UDS-coloured sections to point at the new transport and
  refer back to ADR-0021 / ADR-0022 for the contracts.
- `docs/adr/0012-engine-dsl-and-execution-pipeline.md` lines
  292-301 and 334-335: per ADR-0020 §6, ADR-0012 receives
  per-section status notes when its revision lands. The UDS
  references in §4 of ADR-0012 fall under that update; R3 is
  the phase that lands the note (engine becomes a gRPC
  server). R2.3 limits its ADR-0012 touches to factual cross-
  references.

### Out of scope for R2

The control plane's `SubprocessRunner`
(`core/control-plane/internal/runner/subprocess.go`) and the
related `SubprocessRunner` references in
`core/control-plane/cmd/main.go`, the e2e test scaffolding,
and the operator Dockerfile, are not removed in R2. ADR-0019
§3 is superseded by ADR-0020, but the deletion lands in R3.1
(`EngineClientRunner` replaces `SubprocessRunner`). R2 leaves
those files untouched so the R3.1 PR's diff is focused.

The CI workflows that build and load the operator image
(`.github/workflows/ci.yml`) are similarly deferred to R3.1
and R6.1 — R2 does not edit CI surface beyond what its own
test changes require.

## Security posture

v1alpha1 of the refactor uses **plaintext gRPC**. The transport
carries no TLS, the channels are not authenticated, and the
producers do not authorise their callers. This section documents
the choice plainly so the audit trail is honest about what the
v1alpha1 stance covers and what it does not.

### What the model assumes

The plaintext-gRPC stance treats the network namespace itself
as the trust boundary. Specifically:

- On Compose, the boundary is the user-defined Docker network
  the stack runs on. Only the containers in the stack can
  reach the application ports; the host's external interface
  does not expose them unless the operator publishes them.
- On Kubernetes, the boundary is the namespace plus any
  cluster-level network policy. A `ClusterIP` Service is
  reachable from any Pod in the cluster by default; an
  operator who needs stricter isolation applies a
  `NetworkPolicy` admitting only intended consumers.

A consumer dialling an adapter trusts that the producer behind
the endpoint is the intended adapter. A producer accepting an
RPC trusts that the caller is an intended consumer. Both trust
the platform layer to enforce that boundary.

### What the model does not protect against

The plaintext stance is suitable for trusted networks. It is
**not** an answer to the following:

- An attacker with access to the cluster network or the
  Compose network can read or replay traffic.
- A second workload sharing the namespace can dial the engine
  or any adapter without authorisation.
- A producer cannot distinguish a legitimate consumer from a
  rogue one. There is no per-call identity beyond the TCP
  connection's source address.

These properties are weaker than what an authenticated, mutual-
TLS deployment provides. v1alpha1 does not pretend otherwise.
Operators who deploy Spectre into untrusted networks need
additional layers — see "v1alpha2 path" below.

### v1alpha2 path

Three options become viable once the v1alpha1 baseline is
stable. None ship in the refactor; each is a future ADR
candidate.

- **Service-mesh mTLS (Istio, Linkerd).** The mesh injects
  sidecars that terminate mTLS at the Pod boundary. The
  application code stays plaintext-gRPC; the mesh handles
  identity, authorisation, and observability. Operationally
  heavy, but well-trodden in production Kubernetes.
- **Application-level mTLS.** Each service holds a client and
  server certificate signed by a project CA (cert-manager
  issuing into the Helm chart). gRPC channels and listeners
  are configured with TLS. The discovery URI scheme moves
  from `grpc://` to `grpcs://`. No mesh dependency, but the
  certificate distribution becomes the operator's
  responsibility.
- **Helm-values opt-in.** R7's Helm chart introduces a
  `tls.enabled` value. When true, the chart materialises
  Issuer / Certificate resources and patches Deployment env
  vars to `grpcs://` URLs. Off by default to keep the
  plaintext baseline, on by Helm-value flip when the operator
  is ready.

The R7 Helm chart opens the configuration gate; the actual
mTLS implementation is deferred to a v1alpha2 candidate ADR.
Until then, Spectre operates under the trusted-network
assumption, and that assumption is documented every place a
contributor might look — README, CONTRIBUTING, the Helm chart
README, and this ADR.

## What this ADR does not decide

Items that surface in adjacent reviews and are explicitly out
of scope for v1alpha1.

- **mTLS implementation.** Covered above; v1alpha2 candidate.
- **Authorisation model.** Beyond the trusted-network
  assumption, no per-call identity check exists in v1alpha1.
  An authorisation framework (RBAC, signed JWTs, mTLS-derived
  identity) is a v1alpha2 candidate.
- **Network policies.** The Helm chart in R7 may ship a
  default `NetworkPolicy` admitting only intended consumers,
  but the policy details are R7's concern, not this ADR's.
- **Connection-pool tuning.** The gRPC channel defaults
  (HTTP/2 keepalive, idle timeout, max concurrent streams)
  are accepted as-is. Tuning is deferred to operational
  evidence; load-driven changes are documented per their
  phase.
- **Cross-cluster topologies.** The discovery model assumes
  one cluster (or one Compose stack). Multi-cluster
  federation is not addressed.

## Migration semantics

The refactor's scope at the protocol level is precisely zero.
The driver protocol v1alpha1 directory at
`proto/spectre/driver/v1alpha1/` is treated as read-only across
every refactor phase (strategy prompt §2.1; ADR-0020 §4
invariant one).

Three concrete commitments follow.

- **Wire contract preserved.** The same `.proto` files compile
  to the same generated bindings before and after R2.2 / R2.3.
  An adapter built from v1alpha1 source against the post-
  refactor codebase produces byte-identical wire frames to one
  built before the refactor; only the bind address differs.
- **Conformance assertions byte-identical.** Every test in
  `tools/conformance/tests/` asserts the same capability lists,
  the same message shapes, and the same negative behaviours
  (`UNIMPLEMENTED` for absent capabilities, etc.). The Step 5
  inventory rewrites the harness's transport layer; the
  assertions inside the tests are untouched.
- **Capability divergence preserved (ADR-0017 §1).**
  Playwright 13, SeleniumBase 12, curl-impersonate 6 — the
  strict-subset chain — is unchanged. Each adapter's
  `driver.yaml` `capabilities:` block is preserved verbatim
  even as the `transports:` block retires.

These three commitments are the audit-grade promise the
refactor makes to readers of the codebase: the architecture
moves; the protocol does not.

## More Information

- [ADR-0008 — Driver handshake and conformance harness](0008-driver-handshake-and-conformance-harness.md)
  (the predecessor; §2 is superseded by this ADR per the
  R2.1 update note appended to ADR-0008)
- [ADR-0020 — Microservices architecture supersession](0020-microservices-architecture-supersession.md)
  (the architectural anchor; this ADR is the §5 phase R2 work)
- [ADR-0021 — Service discovery](0021-service-discovery.md)
  (companion document; defines the discovery contract this
  transport ADR references)
- [ADR-0001 — Driver protocol as architectural primitive](0001-driver-protocol-as-architectural-primitive.md)
  (the protocol-freeze invariant cited in §2 and §8)
- [`docs/refactor-audit.md`](../refactor-audit.md) — the
  full removal inventory in tabular form
- gRPC over HTTP/2:
  <https://grpc.io/docs/what-is-grpc/core-concepts/>
- Tonic TCP transport guide:
  <https://docs.rs/tonic/latest/tonic/transport/index.html>


