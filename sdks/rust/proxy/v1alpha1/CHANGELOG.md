# Changelog — spectre-sdk-proxy-v1alpha1

Per-crate changelog. Mirrors the platform's `[Unreleased]`
window during v1alpha2; independent semver split is a future
possibility per [ADR-0036 §5.6](../../../../docs/adr/0036-microservices-catalog-expansion.md).

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this crate adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **First Rust SDK landed (W5.1 cluster C).** Typed gRPC
  client `ProxyClient` wrapping the tonic-generated
  `proxy_client::ProxyClient<Channel>` with configurable
  `RetryPolicy` (defaults: 3 attempts, 100ms initial backoff
  doubled per retry, 2s ceiling). Retries restricted to the
  transient gRPC code set (`UNAVAILABLE` /
  `DEADLINE_EXCEEDED` / `INTERNAL`); application errors
  (`INVALID_ARGUMENT`, `PERMISSION_DENIED`, etc.) propagate
  without retry via `ProxyClientError::Status`. `tracing`
  instrumentation opens a client-kind span per RPC so the
  caller's OpenTelemetry context propagates downstream via
  `otelgrpc` interceptors. Per ADR-0027 §5: SDK exposes
  client wrapper + retry + tracing; no connection pooling, no
  TLS configuration (caller's concern), no service-specific
  business logic.
