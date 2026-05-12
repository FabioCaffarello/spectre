# Service-to-service authentication

Spectre uses **mutual TLS** for service-to-service authentication.
The framework is committed by [ADR-0032](../adr/0032-service-to-service-mtls.md);
this document captures the **operational shape** the v1alpha2
platform actually ships, the env-var contract per-language SDKs
honour, and the cluster-side prerequisites operators provision.

The W3.3 first auth PR landed the operator ↔ engine wiring;
**W3.4 extended mTLS to all three reference adapters** (closes
Wave 3's auth scope); Wave 5+ infra-services inherit the
canonical shape per [ADR-0036 §5.5](../adr/0036-microservices-catalog-expansion.md).

## When mTLS is on, when it is off

`certManager.enabled` is the chart's top-level switch
(`build/helm/spectre/values.yaml`). Two postures result:

| Posture | Chart flag | Engine bind | Operator dial |
|---|---|---|---|
| Plaintext (v1alpha1 default) | `certManager.enabled: false` | HTTP/2 + gRPC, no TLS | `insecure.NewCredentials()` |
| mTLS (W3.3+) | `certManager.enabled: true` | HTTP/2 over TLS + client-cert verification | `credentials.NewTLS` with `ReloadingCredentials` |

**Compose stays plaintext** regardless of the chart flag. The
Compose stack has no cert-manager equivalent, and provisioning a
parallel certificate workflow would diverge from the canonical
k8s shape. Contributors who need to exercise the mTLS path run the
chart in `kind` (or any cluster with cert-manager installed). See
the comment block in `docker-compose.yml`.

## Env-var contract

Every Spectre service (engine, operator, and Wave 5+ infra-services)
honours the same three env vars to switch between postures:

```
SPECTRE_TLS_CERT_PATH   — server certificate (PEM)
SPECTRE_TLS_KEY_PATH    — server private key (PEM)
SPECTRE_TLS_CA_PATH     — peer trust bundle (PEM)
```

Classification rules:

- **All three unset** → `Plaintext`. Service binds / dials over
  plain HTTP/2 + gRPC (the v1alpha1 posture).
- **All three set** → `Mutual`. Service loads the PEM material,
  binds with client-cert-required TLS, and dials over verified
  mTLS.
- **One or two set** → fail-fast. The startup error message
  echoes which vars were set and which were missing. The chart's
  `_helpers.tpl::spectre.tlsEnv` only emits all three together,
  so partial state surfaces only hand-rolled deployments.

The chart wires the three vars from the per-service Secret
mounted at `/etc/spectre/tls/` (the same paths cert-manager's
issued Secret carries as `tls.crt`, `tls.key`, `ca.crt`).

## Cluster-side prerequisites

`certManager.enabled: true` requires the deployment to provide:

1. **cert-manager** installed in the cluster. The chart's
   `Chart.yaml` does NOT bundle cert-manager — it is a
   deployment-side prerequisite. Many existing clusters already
   run it; deployments without it stay on the plaintext default.
2. **A cert-manager `Issuer` or `ClusterIssuer`** that the chart's
   `certManager.issuerRef` references. The chart defaults to
   `spectre-selfsigned` (a CA ClusterIssuer) — the SelfSigned →
   CA → CA-Issuer chain in
   `build/helm/test/cluster-issuer-ci.yaml` provides this for the
   mtls-smoke workflow. Production deployments override
   `issuerRef` to their own internal CA (Vault, ACME, etc.) per
   [ADR-0032 §3.1](../adr/0032-service-to-service-mtls.md#31--the-issuer-is-deployment-side).

## Certificate provisioning shape

The chart's `_helpers.tpl::spectre.certificate` named template
emits per-service `Certificate` resources when `certManager.enabled:
true`. Naming uses `spectre.fullname` (not `Release.Name`):

```
<spectre.fullname>-<slot>-cert     # Certificate name
<spectre.fullname>-<slot>-cert     # Secret name (same)
<spectre.fullname>-<slot>          # CN / SAN DNS prefix
```

W3.3 lands two `Certificate` resources (`<fullname>-engine-cert`,
`<fullname>-control-plane-cert`). Each cert's SAN list enumerates
short / namespaced / cluster-local / plain DNS forms so any dial
target the operator + engine emit verifies cleanly. ECDSA P-256;
90-day validity; 30-day renewal lead (cert-manager default
cadence).

## Per-language reload behaviour

`ADR-0032 §5.1` commits the platform to dynamic credential reload
without service restart so cert-manager rotations propagate at
the 30-day cadence. Per-language realities differ:

| Language | Service | Path | Reload behaviour |
|---|---|---|---|
| Rust | engine (server + client) | `engines/engine/src/tls/`; `tonic::{Server,Endpoint}::tls_config` | **Static load at startup.** tonic 0.13's `ServerTlsConfig` and `ClientTlsConfig` don't expose a rustls `cert_resolver` injection; dynamic reload requires a tonic 0.14 migration. Rotation triggers Pod restart via cert-manager annotation pattern. |
| Go | operator (client) | `operators/control-plane/internal/tls/`; `credentials.NewTLS` + `GetClientCertificate` | **Dynamic 30-second reload** via `ReloadingCredentials`. Reads cert + key off disk at most every 30s; subsequent dials reuse the in-memory `tls.Certificate`. RootCAs loaded once (bundle rotation per `ADR-0032 §5.3` is rare). |
| Go | curl-impersonate adapter (server) | `adapters/curl-impersonate/internal/tls/`; `credentials.NewTLS` + `GetCertificate` | **Dynamic 30-second reload** via `ReloadingCredentials` (server-side symmetry to the operator's client-side hook). |
| Python | seleniumbase adapter (server) | `adapters/seleniumbase/src/spectre_seleniumbase/tls.py`; `grpc.ssl_server_credentials` | **Static load at startup.** Python's gRPC bindings only accept static keypair bytes for server credentials; restart-on-rotation per ADR-0032 §5.1 Python entry. |
| TypeScript | playwright adapter (server) | `adapters/playwright/src/tls.ts`; `http2.createSecureServer` | **Static load at startup.** Node's `http2.createSecureServer` consumes static PEM material; restart-on-rotation per ADR-0032 §5.1 TS entry. |

The asymmetry is operationally acceptable: cert-manager rotates
30 days before expiry, and Kubernetes' rolling update flow
restarts Pods within that window when the Secret content
changes (deployments that need it wire `stakater/Reloader` or a
hashed annotation on the Pod template). A 60-second worst-case
unavailability window per Pod every 60 days is well below the
platform's availability target.

## Verification

The mtls-smoke workflow (`.github/workflows/mtls-smoke.yml`,
daily cron) installs cert-manager + the chart with
`certManager.enabled: true` and runs two assertions:

- `tools/test/verify-mtls-handshake.sh` — positive: operator
  initialised with mutual creds; engine bound with mutual-TLS;
  each of the 3 adapters bound with mutual-TLS; 3 ScrapeJobs
  (one per driver) reach a terminal phase with no handshake
  errors anywhere across operator + engine + 3 adapter logs.
- `tools/test/verify-mtls-rejects-plaintext.sh` — negative:
  plaintext gRPC dials to engine + each of the 3 adapter ports
  (8090, 8091, 8092, 8093) all fail. Every service enforces
  `RequireAndVerifyClientCert`-equivalent semantics:
  - Rust (engine) — tonic's `ServerTlsConfig::client_ca_root`
    set leaves `client_auth_optional` at its `false` default.
  - Go (curl-impersonate) — `tls.RequireAndVerifyClientCert`
    explicitly set on the `tls.Config`.
  - Python (seleniumbase) — `grpc.ssl_server_credentials(...,
    require_client_auth=True)`.
  - TypeScript (playwright) — `http2.createSecureServer({
    requestCert: true, rejectUnauthorized: true })`.

Production-smoke (`.github/workflows/production-smoke.yml`) runs
the plaintext path; both gates run on every relevant PR.

## What's not covered

- **Per-service authorisation policies** — mTLS authenticates
  identity; it does not authorise per-RPC access. Per-service
  policies (`audit-log`, `secret-broker`, etc.) ship with their
  build PRs per [ADR-0032 §4.4](../adr/0032-service-to-service-mtls.md#44--per-service-authorisation-policies).
- **Webhook authentication** — outbound webhook posts use HMAC
  signing or bearer tokens, not mTLS (the engine is the client,
  not the server; receiver identity ≠ service identity). Deferred
  to a follow-up ADR per [ADR-0032 §7](../adr/0032-service-to-service-mtls.md#7--webhook-authentication-deferred).
- **Stateful-service auth** — Postgres / Redis / Kafka use their
  own auth schemes (SCRAM-SHA-256, ACLs). [ADR-0023 §14.4](../adr/0023-stateful-services-architecture.md)
  commits Mongo X.509 with the same per-service certificate
  provisioning, but that lands with the Mongo service PR.
- **Cert rotation observability** — cert-manager's own metrics
  cover certificate state; the engine + operator do not yet emit
  TLS handshake counters / rejection counters per
  [ADR-0031 §5.2](../adr/0031-observability-framework.md). Wave
  5+ work.
