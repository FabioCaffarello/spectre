---
status: accepted
date: 2026-04-28
deciders: [Fabio Caffarello]
---

# Stateful services architecture

## Context and Problem Statement

Six PRs into the refactor, the architecture Spectre commits to in
ADR-0020 is operationally complete at the service-mesh layer.
ADR-0021 / ADR-0022 retired the subprocess-in-pod transport in
favour of TCP gRPC with env-var discovery (R2.1–R2.3); ADR-0019
+ ADR-0020 §5 reshaped the control plane into a thin gRPC client
of the engine (R3.1); ADR-0019's R3.2 addendum evolved the
ScrapeJob CRD to v1alpha2 with EngineRef and a four-variant
OutputSink discriminated union (R3.2). Every component that
needs to talk to every other component now does so over the
network. What remains is what the network has, so far, been
asked to carry: nothing durable. Job state lives in the engine's
Tokio task and on the operator's `Status` subresource — both
volatile across Pod restart. Output rows leave the engine via
stdout and reach `kubectl logs` as soon as the operator buffers
them, with no streaming surface for downstream consumers. Adapter
session metadata — the UUIDs the conformance suite already exercises
under ADR-0010, the cookie-jar paths curl-impersonate emits,
the per-session generation counter Playwright uses for stable-
node tracking — lives entirely in the adapter's process memory.
A single Pod restart on any of the three layers loses every job
in flight and every session a client thought it held.

R4 closes that gap. Three stateful services land together in the
architecture: PostgreSQL for job state and audit, Kafka for
output streaming, Redis for adapter session metadata. The PRs
that wire them — R4.2 (Postgres), R4.3 (Redis), R4.4 (Kafka) —
land in series, but the architectural commitment is one
decision, not three, because the three services interlock.
Postgres alone cannot recover a job whose adapter session was
lost on Redis-less Pod restart. Kafka alone cannot publish rows
the engine never persisted. Redis alone externalises session
metadata that no other service knows how to index. Introducing
them piecemeal would leave the system in a half-stateful state
no operator can reason about: some workloads recoverable, some
not, with the matrix depending on which PR landed when. This
ADR records the full commitment up front so the implementation
PRs can each ship a coherent slice of one architecture rather
than three negotiations of an emerging one. The companion
[`docs/refactor-audit.md`](../refactor-audit.md) tracks the
per-PR work plan; this ADR is the architectural reference R4.2
/ R4.3 / R4.4 each implement against.
