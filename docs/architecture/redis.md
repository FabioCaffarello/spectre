# Redis — adapter session metadata

R4.3 introduced Redis as the externalised store for adapter
session metadata. The architectural commitment lives in
[ADR-0023 §4 / §5 / §6](../adr/0023-stateful-services-architecture.md);
this document is the operator-facing companion: how the keyspace
is shaped, how the lifecycle works per RPC, what
`adapter_instance_id` means, and how the §5 restart-invalidation
contract surfaces in practice.

## Keyspace and value schema

One key per session, namespaced by adapter:

```
session:<adapter>:<session_id>
```

`<adapter>` is one of `playwright`, `seleniumbase`,
`curl-impersonate`. `<session_id>` is the UUIDv4 the adapter
returns from `Initialize`.

The value is a JSON document:

```json
{
  "session_id": "<uuid>",
  "adapter": "playwright | seleniumbase | curl-impersonate",
  "adapter_instance_id": "<uuid generated at adapter startup>",
  "created_at": "<ISO-8601 with milliseconds, UTC>",
  "last_active_at": "<ISO-8601 with milliseconds, UTC>",
  "metadata": {
    /* adapter-specific fields, optional */
  }
}
```

TTL: 3600 seconds (1 hour). The TTL is refreshed via SET-with-EX
on every successful read from a non-Initialize RPC, so active
sessions never expire mid-job.

## Lifecycle per RPC

The local registry remains the source of truth for browser /
WebDriver / cookie-jar state; Redis is the source of truth for
session existence and adapter ownership. Together they implement
the §5 contract.

| RPC          | Local                                | Redis                                                                            |
|--------------|--------------------------------------|----------------------------------------------------------------------------------|
| `Initialize` | Reserve the local entry              | SET the metadata document (1h TTL). Failure → `UNAVAILABLE`; local untouched.   |
| `Navigate`   | Lookup the local page / driver / jar | GET, validate `adapter_instance_id`, refresh TTL on the OK path                  |
| `Query`      | Same                                 | Same                                                                             |
| `Extract`    | Same                                 | Same                                                                             |
| `Screenshot` | Same                                 | Same                                                                             |
| `Close`      | Tear down local state                | DEL (best-effort; logged on failure, TTL is the safety net per phase prompt §4.6)|

For each non-Initialize RPC, the validation step has three
outcomes:

1. **Redis has the session and the stored
   `adapter_instance_id` matches.** RPC proceeds. The handler
   refreshes `last_active_at` and the TTL.
2. **Redis has no entry.** Returns the in-band
   `CODE_INVALID_ARGUMENT` envelope with the message _"unknown
   session_id; call Initialize first"_.
3. **Redis has an entry but the stored
   `adapter_instance_id` differs.** Returns the gRPC status
   `UNAVAILABLE` with the message _"session belongs to a
   different adapter instance; client must re-Initialize"_.
   This is the §5 restart-invalidation case and the conformance
   test asserts on it precisely.

Redis-unreachable failures during a non-Initialize RPC also
surface as `UNAVAILABLE` (with a different details prefix:
`"redis unreachable: ..."`); the wire status is the same so the
engine's retry logic does not need to distinguish.

## `adapter_instance_id` — process-startup UUID

Each adapter generates a fresh UUID at process startup and keeps
it in process memory only. Never persisted to disk. Never
derived from hostname / pod name.

The UUID is exposed via the `SPECTRE_ADAPTER_INSTANCE_ID` env
var **only for testing**. Production deployments leave it
unset; the adapter generates a fresh UUID per startup.

Why UUID per startup, not Pod hostname:

- **Kubernetes:** Pod restart = new process = new UUID. Foreign
  Pod's sessions become foreign-instance to the new Pod;
  clients see `UNAVAILABLE` and re-Initialize.
- **Compose:** `docker compose restart` = new process = new
  UUID, even though container hostname unchanged. Hostname-based
  identification would mis-treat a restarted container as the
  same instance.

A process-startup UUID is the only mechanism that works
identically across both deployment shapes without depending on
orchestrator-specific identity. ADR-0023 §5 R4.3 addendum
records the rationale in full.

## Restart-invalidation contract

The §5 contract: when an adapter's process is replaced (Pod
restart, container restart, crash + relaunch), every session
the prior process owned becomes foreign-instance to the new
process. The next non-Initialize RPC against any of those
sessions returns gRPC `UNAVAILABLE`. Clients re-call
`Initialize` to allocate a fresh session and re-Navigate.

The contract is deliberately honest: the adapter has no way to
recover the actual browser / WebDriver / cookie-jar state that
existed in the prior process, and any "warm recovery" pattern
would create harder-to-debug failures when the recovery is
incomplete. v1alpha2 may revisit if real users run longer-lived
sessions whose restart cost is operationally significant; ADR-
0023 §5 records both the cost and the v1alpha2 growth path.

## Local development

Redis is brought up via the Compose stack:

```bash
docker compose up -d        # postgres + redis
docker compose ps           # both healthy
redis-cli -h 127.0.0.1 ping # PONG
```

The adapter binaries read `SPECTRE_REDIS_URL` from the env (or
the `.env` file the justfile auto-loads via `set dotenv-load
:= true`). The default is `redis://127.0.0.1:6379/0` — matches
the Compose service binding.

```bash
# Start an adapter; it PINGs Redis at startup and exits non-zero
# if Redis is unreachable (ADR-0023 §6).
just pw-run 9091
# stderr:
#   redis ready at redis://127.0.0.1:6379/0 (adapter_instance_id=...)
#   spectre-playwright 0.1.0-alpha.0 (driver protocol spectre.driver.v1alpha1) listening on 0.0.0.0:9091
```

Inspect a live session:

```bash
# After Initialize:
redis-cli GET "session:playwright:<session_id>"
# Returns the JSON document with adapter_instance_id, created_at, etc.
redis-cli TTL "session:playwright:<session_id>"
# ~3600 (refreshed on every non-Initialize RPC)

# After Close:
redis-cli GET "session:playwright:<session_id>"
# (nil)
```

## Production deployment

- Redis is a separate service per ADR-0023 §7; only adapters
  connect (the engine and control plane never touch Redis in
  v1alpha1).
- Single-node Redis is sufficient for v1alpha1. ADR-0023 §6
  defers Redis Cluster / Sentinel topology to v1alpha2.
- `SPECTRE_REDIS_URL` is sourced from a Kubernetes Secret per
  ADR-0023 §10.
- `SPECTRE_ADAPTER_INSTANCE_ID` is **never set** in production —
  always leave it unset so the adapter generates a fresh UUID
  per startup. Production-side overrides defeat the §5 contract
  silently.
- mTLS / AUTH / TLS for Redis are deferred to v1alpha2 per
  ADR-0023 §6.

## References

- [ADR-0023 §4](../adr/0023-stateful-services-architecture.md) —
  keyspace + value schema
- [ADR-0023 §5 (incl. R4.3 addendum)](../adr/0023-stateful-services-architecture.md) —
  the restart-invalidation contract and `adapter_instance_id`
  mechanism
- [ADR-0023 §6](../adr/0023-stateful-services-architecture.md) —
  Redis required at adapter startup
- [ADR-0023 §8](../adr/0023-stateful-services-architecture.md) —
  per-language Redis libraries (`ioredis`, `redis-py`,
  `go-redis/v9`)
- [ADR-0010](../adr/0010-element-lifecycle-and-capability-gating.md) —
  per-Pod ElementRegistry that the §5 contract makes coherent
  with the Redis-resident session metadata
- `tools/conformance/tests/test_session_restart_invalidation.py` —
  the parallel-instances conformance pattern
