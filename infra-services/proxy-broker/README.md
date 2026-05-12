# proxy-broker

Spectre's proxy-broker infra-service — **slot 1** of the
[ADR-0036 §3.1 catalog](../../docs/adr/0036-microservices-catalog-expansion.md).
Centralises proxy acquisition, cooldown tracking, budget
accounting, and provider-agnostic vendor routing across every
Spectre engine instance.

Defined by [ADR-0028 §4.1](../../docs/adr/0028-ancillary-infra-services-catalog.md);
canonical service shape per [ADR-0036 §5](../../docs/adr/0036-microservices-catalog-expansion.md);
consumed by the engine via the orchestrator scaffolding
landing in W5.1 Cluster D.

## What it does

- **`Acquire`** — issues a proxy lease matching constraints
  (region, type, sticky-vs-rotating, target domain, tenant
  ID). Returns `{ lease_id, proxy_url, provider, region,
  expires_at }`. The caller dials `proxy_url` directly; the
  broker does not proxy traffic.
- **`AcquireBatch`** — N leases in one call per
  [ADR-0037 §4.1](../../docs/adr/0037-engine-as-orchestrator.md)
  batch strategy.
- **`Release`** — idempotent lease return; cross-tenant
  releases rejected with `PERMISSION_DENIED`.
- **`ReportFailure`** — proxy-misbehaviour report driving
  Redis-backed cooldown + health-score state per
  [ADR-0039 §3.1](../../docs/adr/0039-mongodb-third-storage-tier.md).
  `BANNED` / `CAPTCHA` additionally record against the
  per-(provider, domain) ban set.
- **`BudgetStatus`** — per-provider / per-region / per-tenant
  usage + remaining capacity. W5.1 returns synthetic shape;
  real provider-billing-API integration is W5.1b.

## Architecture

```
                ┌─────────────────────┐
   Engine ──gRPC▶│ proxy-broker        │──Redis──▶ state
                │                     │              ├─ cooldown
                │  ┌───────────────┐  │              ├─ bans
                │  │ providers     │  │              ├─ health
                │  │  ├ brightdata │──▶ Super Proxy  └─ leases
                │  │  └ stub       │──▶ stub URLs
                │  └───────────────┘  │
                └─────────────────────┘
```

- **Stateless across restarts.** All state lives in Redis
  (per ADR-0039 §3.1). The broker scales horizontally; any
  instance can serve any request.
- **Provider-agnostic.** The `providers.Provider` interface
  absorbs vendor variation. Adding a new provider is a
  new subpackage + a registry entry — no broker-layer changes.

## Configuration

All env vars; no flags or config files in W5.1.

| Env | Default | Purpose |
|---|---|---|
| `SPECTRE_PROXY_BROKER_LISTEN_ADDR` | `:8094` | gRPC bind address |
| `SPECTRE_PROXY_BROKER_REDIS_URL` | `redis://127.0.0.1:6379/1` | Redis backend (logical DB 1 by convention to isolate from other services) |
| `SPECTRE_METRICS_PORT` | `9090` | Prometheus `/metrics` sidecar port (ADR-0031 §3.3) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | OTLP/gRPC trace endpoint; unset = no-op tracer (spans still propagate via `traceparent` metadata) |
| `SPECTRE_TLS_CERT_PATH` | unset | mTLS server cert path |
| `SPECTRE_TLS_KEY_PATH` | unset | mTLS server key path |
| `SPECTRE_TLS_CA_PATH` | unset | mTLS trust bundle for client cert validation |
| `SPECTRE_PROXY_BROKER_STUB_ENABLED` | `false` | Enable stub provider |
| `SPECTRE_PROXY_BROKER_STUB_URLS` | — | Comma-separated stub proxy URLs |
| `BRIGHTDATA_USERNAME` | unset | BrightData Super Proxy username; enabling BrightData requires this + `BRIGHTDATA_PASSWORD` |
| `BRIGHTDATA_PASSWORD` | unset | BrightData password (never logged) |
| `BRIGHTDATA_ZONE` | unset | BrightData zone name (when not embedded in username) |

**TLS env contract** (mirrors W3.3 platform-wide pattern):
all three of `SPECTRE_TLS_CERT_PATH` / `_KEY_PATH` / `_CA_PATH`
set → mTLS posture; all three unset → plaintext (v1alpha1
baseline); partial → fail-fast at startup. The chart's
`certManager.enabled` gates whether the env vars are populated.

**Provider configuration.** At least one provider must be
enabled. Both can run side-by-side; BrightData is preferred
when credentials are present (the engine sees `provider:
"brightdata"` on issued leases); stub falls in as fallback
or sole provider when BrightData credentials are absent.

## Build + run

```bash
# Local build (produces ./bin/proxy-broker)
make build

# Run with stub provider
export SPECTRE_PROXY_BROKER_STUB_ENABLED=true
export SPECTRE_PROXY_BROKER_STUB_URLS="http://localhost:1080,http://localhost:1081"
./bin/proxy-broker

# Run with BrightData
export BRIGHTDATA_USERNAME="lum-customer-<account>"
export BRIGHTDATA_PASSWORD="<pass>"
export BRIGHTDATA_ZONE="<zone>"
./bin/proxy-broker

# Container build (multi-arch via bake)
docker buildx bake proxy-broker
```

## Tests

```bash
make test
```

Three test layers:
- `internal/state/` — Redis state operations via miniredis
- `internal/providers/` — provider abstraction matrix
  proving both BrightData + stub implement the same surface
  per [ADR-0028 §5 criterion #2](../../docs/adr/0028-ancillary-infra-services-catalog.md)
- `internal/server/` — gRPC handler flows end-to-end with
  miniredis-backed state + stub provider

Integration coverage lives in
`tools/test/verify-proxy-broker-smoke.sh` and runs as part
of `production-smoke` + `mtls-smoke` CI workflows (added in
W5.1 Cluster G).

## ADRs

Per-service ADRs live under `adr/`. Repo-level architectural
commitments live at [`docs/adr/`](../../docs/adr/).

- [`adr/0001-provider-pick-rationale.md`](adr/0001-provider-pick-rationale.md)
  — why BrightData first, why stub second, pending W5.1b
  second-provider pick.

## What's next

- **W5.1b** — wire a real second provider (Oxylabs /
  Smartproxy candidates per ADR-0028 §4.1; pilot data from
  the Wave 4 questionnaire informs the pick) and replace
  the stub.
- **Wave 5+** — captcha-solver (slot 2; W5.2) reuses the
  canonical service shape established here.
