# spectre-sdk-proxy-v1alpha1

Rust SDK for the Spectre `proxy-broker` infra-service —
slot 1 of [ADR-0036 §3.1](../../../../docs/adr/0036-microservices-catalog-expansion.md);
gRPC protocol at [`spectre.proxy.v1alpha1`](../../../../proto/spectre/proxy/v1alpha1/proxy.proto);
service implementation at [`infra-services/proxy-broker/`](../../../../infra-services/proxy-broker/).

Per [ADR-0027](../../../../docs/adr/0027-sdk-strategy.md), this
crate is the Rust consumer-facing surface — the engine's
`engines/engine/src/services/proxy.rs` consumes it via a `path`
dependency. Go / Python / TypeScript SDKs are deferred per D14
(SDK lands when consumer exists, not before).

## What it provides

- `ProxyClient` — typed wrapper over the tonic-generated
  `proxy_client::ProxyClient<Channel>`.
- Configurable `RetryPolicy` — defaults to 3 attempts with
  exponential backoff (100ms → 2s cap). Retries only on
  `UNAVAILABLE` / `DEADLINE_EXCEEDED` / `INTERNAL` (transient
  category); application errors propagate without retry.
- `tracing` integration — every RPC opens a client-kind span
  so the OpenTelemetry context propagates downstream via the
  engine's `otelgrpc` stats handler.
- Generated message types re-exported under `pb::*`.

## What it doesn't provide

- Connection pooling — the caller manages the tonic `Channel`.
- TLS configuration — the caller constructs the channel with
  `ClientTlsConfig` (per ADR-0032 §4.3 the engine's
  `tls::build_client_tls_config` does this from the standard
  `SPECTRE_TLS_*_PATH` env contract).
- Service-specific business logic — the broker owns provider
  selection, cooldown semantics, ban tracking.

## Usage

```rust
use spectre_sdk_proxy_v1alpha1::{ProxyClient, pb::AcquireRequest};
use tonic::transport::Channel;

async fn example(channel: Channel) -> anyhow::Result<()> {
    let client = ProxyClient::new(channel);
    let resp = client.acquire(AcquireRequest {
        region: "US".into(),
        target_domain: "example.com".into(),
        tenant_id: "tenant-A".into(),
        ..Default::default()
    }).await?;
    println!("got lease: {}", resp.lease.unwrap().proxy_url);
    Ok(())
}
```

For a service-aware wrapper that adds caching for sticky
leases + circuit breaker for graceful degradation, see
[`engines/engine/src/services/proxy.rs`](../../../../engines/engine/src/services/proxy.rs)
(`EngineProxyClient`).

## Versioning

Crate version mirrors the platform's `[Unreleased]` /
`0.1.0-alpha.3` window via the `sdks/rust/Cargo.toml`
workspace.package version. Per-service independent semver
split is a future possibility (ADR-0036 §5.6); v1alpha2 keeps
the unified release train.

The proto package is `spectre.proxy.v1alpha1` — the **proto**
version is independent of the **crate** version and bumps
only on protocol-breaking change per
[ADR-0004](../../../../docs/adr/0004-protocol-versioning-strategy.md).
