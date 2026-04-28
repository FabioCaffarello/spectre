# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Architectural commitment to a microservices refactor recorded
  in [ADR-0020](docs/adr/0020-microservices-architecture-supersession.md).
  No code changes in this release; subsequent phase PRs (R2–R8)
  deliver the implementation. Live progress is tracked in
  [`docs/refactoring-status.md`](docs/refactoring-status.md).
- Initial repository structure and foundational documents
- Driver Protocol skeleton at v1alpha1
- Skeleton implementations for three reference adapters
- gRPC standard health check (`grpc.health.v1.Health`) registration
  in every adapter (R2.2, ADR-0021 §6). The conformance harness
  polls `Check` until SERVING as the readiness signal; production
  deployments wire the same endpoint into Compose / Kubernetes
  readiness probes.
- `proto/grpc/health/v1/health.proto` vendored verbatim from the
  canonical gRPC source so the Playwright TS adapter can produce
  Connect-RPC bindings; the Go and Python adapters consume their
  ecosystem libraries (`google.golang.org/grpc/health` and
  `grpcio-health-checking`) directly. Buf lint exempts the
  vendored file so it stays byte-identical to the upstream source.
- `KNOWN_BREAKAGE.md` documented the R2.2 → R2.3 engine ↔ adapter
  transport mismatch. R2.3's first commit deleted the file as the
  engine-side TCP dial landed.
- Internal `spectre.engine.v1alpha1.Engine` service contract
  (R2.3) at `proto/spectre/engine/v1alpha1/engine.proto`. A
  single streaming RPC, `RunJob`, takes an inline DSL document
  and streams `Row` events followed by a terminal `Completed` or
  `Failed`. Cancellation is gRPC stream cancellation; status,
  metrics, and listing are control-plane responsibilities.
  Bindings are generated for Rust (via `core/engine/build.rs`)
  and Go (via `proto/buf.gen.engine.yaml`); Python and TS are
  intentionally not generated.
- Engine becomes a stateless gRPC service (R2.3, ADR-0020 §3).
  The binary at `core/engine/src/bin/spectre.rs` registers
  `spectre.engine.v1alpha1.Engine` and `grpc.health.v1.Health`
  on a single TCP listener (default `0.0.0.0:9090`, override
  via `SPECTRE_ENGINE_PORT`) and shuts down cleanly on
  SIGTERM/SIGINT. Adapter discovery flows through
  `AdapterRegistry`, which reads
  `SPECTRE_PLAYWRIGHT_ENDPOINT` /
  `SPECTRE_SELENIUMBASE_ENDPOINT` /
  `SPECTRE_CURL_IMPERSONATE_ENDPOINT` (defaults
  `127.0.0.1:909{1,2,3}`).
- Engine architecture document at
  `docs/architecture/engine.md` describing the service contract,
  discovery model, health-check registration, CLI-retirement
  rationale, and v1alpha1 statelessness invariant.
- Control plane is now a thin gRPC client of the engine service
  (R3.1, ADR-0020 §5). The new
  `core/control-plane/internal/runner/engine_client.go` implements
  the `JobRunner` interface by dialling
  `spectre.engine.v1alpha1.Engine.RunJob` per invocation and
  forwarding every `Row.json_line` event into the supplied
  writer. ADR-0019 §5's interface seam is vindicated at the
  second substitution: three implementations (`StubRunner`,
  `SubprocessRunner`, `EngineClientRunner`) share one signature
  and the reconciler is unaware of which is wired. The R2.3 → R3.1
  transitional window (operator broken at runtime) closes here.
- Operator startup honours `--engine-endpoint=<host:port>` and
  `SPECTRE_ENGINE_ENDPOINT` (default `127.0.0.1:9090`) — Compose
  (R6.2) and Helm (R7.1) renderings will inject the service-
  network address. Plain-text gRPC for v1alpha1 per ADR-0022 §6;
  TLS / mTLS deferred to v1alpha2.
- `ScrapeJob` CRD evolved to `spectre.io/v1alpha2` (R3.2). New
  fields:
  - `spec.engineRef` (optional, CEL-validated): per-job engine
    selection via Kubernetes Service reference (rendered as
    `<name>.<namespace>.svc.cluster.local:<port>`) or direct
    host:port endpoint. Nil falls back to the operator's
    startup-time `SPECTRE_ENGINE_ENDPOINT` configuration.
  - `spec.outputSink` (required, CEL-validated): discriminated
    union over `stdout`, `kafka`, `s3`, `webhook` variants. R3.2
    wires only `stdout` end-to-end; the other three variants are
    schema-only — the reconciler rejects them at the
    `Pending → Running` boundary with explicit "not yet
    implemented (R4.4 / R5.1)" errors. Schema-ahead-of-
    functionality is intentional and documented in
    `config/samples/spectre_v1alpha2_scrapejob_kafka_NOT_YET_IMPLEMENTED.yaml`.
  - `status.resolvedEngineEndpoint`: records the host:port the
    operator actually dialed (debug aid for `EngineRef`
    resolution).
  CEL `XValidation` rules (stable in Kubernetes 1.25+) enforce
  the discriminated-union shapes at admission, removing the
  operational overhead of custom validating webhooks.
- `core/control-plane/config/samples/spectre_v1alpha2_*.yaml`
  (R3.2): five samples covering Service `EngineRef` for the
  three reference adapters, an `EngineRef.Endpoint` variant for
  ad-hoc local testing, and one schema-only Kafka sample
  (`_kafka_NOT_YET_IMPLEMENTED.yaml`) documenting the schema-
  ahead-of-functionality gap.

### Changed

- ADR-0019 (control plane and ScrapeJob CRD) gains an "Update
  (R3.2)" addendum recording v1alpha2 as the only registered
  version: §1, §2, §4 carry forward unchanged; §3 (subprocess
  execution model) was already superseded by ADR-0020 in R1.1;
  §5 (JobRunner interface) preserved through a construction-
  site refactor (per-Reconcile runner construction via a
  `RunnerFactory` closure) — the interface signature is byte-
  for-byte unchanged; §6 (OutputSink stdout-only commitment)
  honoured at the runtime level (the discriminated union now
  carries Kafka / S3 / Webhook field shapes, but the reconciler
  rejects them at admission until R4.4 / R5.1 wire them).
- ADR-0008 (UDS transport), ADR-0009 (session lifecycle),
  ADR-0019 (subprocess-in-pod) carry "Update (R1.1, ADR-0020)"
  notes recording per-section supersession. ADR-0019 §5
  (`JobRunner` interface) gains an "Update (R3.1, vindication)"
  addendum recording the seam's stability across three
  implementations. ADR-0013 (CLI as engine binary) is superseded
  in full. ADR-0012 (engine DSL + execution pipeline) carries an
  "Update (R2.3, ADR-0020)" note recording the launcher-contract
  supersession; §§1-3, 5, 6 are preserved unchanged. The ADR
  index reflects these changes.
- Adapter transport for all three reference adapters (Playwright,
  SeleniumBase, curl-impersonate) and the conformance harness
  switched from Unix-domain-socket gRPC to TCP gRPC (R2.2,
  ADR-0021 + ADR-0022). Each adapter binds `0.0.0.0:<port>` where
  the port is read from `SPECTRE_ADAPTER_GRPC_PORT`; the
  conformance harness allocates a free localhost port and injects
  it into the subprocess env. The wire-level driver protocol
  contract is unchanged. The 13 / 12 / 6 capability lists for
  Playwright / SeleniumBase / curl-impersonate are preserved
  byte-for-byte.
- `driver.yaml` schema for every adapter retires the `transports:`
  block; the spawn directive moves into `runtime.command`
  (transitional — R6.2's Compose stack supersedes the
  harness-spawn flow entirely). The conformance harness reads
  `runtime.command` rather than `transports[0].command`.
- `just <adapter>-run` recipes take a port argument (default
  matching the canonical port from ADR-0021 §4) instead of a
  socket path. The recipes survive R2.2 as developer
  conveniences and are scheduled for retirement in R6.2.
- Engine `Client::dial` (R2.3) accepts `host:port` or
  `grpc://host:port` endpoints and connects via
  `tonic::transport::Endpoint`'s TCP path. The
  `tower::service_fn` UDS connector and the
  `:authority=localhost` Node-http2 workaround are gone.
- Engine binary (R2.3) is service-only. The `run`, `validate`,
  and standalone `version` subcommands the CLI exposed via clap
  are gone with ADR-0013's CLI surface (ADR-0020 §3). The binary
  has no flags beyond `--help`/`--version` and its single
  responsibility is starting the gRPC service.
- `justfile` and `.github/workflows/ci.yml` (R2.3) drop the
  `spectre version` / `spectre validate` smoke steps. The
  release-binary smoke becomes "the binary built / exists at the
  canonical path"; deeper start-and-probe smokes are deferred to
  the Compose stack (R6.2). The `operator-smoke-kind` CI job is
  gated `if: false` until R3.1 lands `EngineClientRunner`.
- Example READMEs (R2.3) document the manual `grpcurl` flow
  honestly. The `seleniumbase-navigate` and
  `curl-impersonate-fetch` directories are deleted; they
  existed to demonstrate the legacy CLI's "minimum viable
  adapter run" and the `*-extract` examples cover the same
  adapters with richer functionality.
- `curl-imp-lint` pinned to `GOTOOLCHAIN=go1.25.3` mirroring the
  existing `cp-lint` pattern; without the pin `golangci-lint
  v2.8.0` (built with go1.25.5) panics on the Go 1.26 stdlib
  loaded by an unconstrained toolchain.
- `proto/buf.gen.yaml` pins the python plugins to revisions that
  emit gencode compatible with the protobuf 6.x runtime
  (`grpcio-health-checking==1.80.0` caps protobuf at <7.0.0).

### Removed

- **BREAKING**: `core/control-plane/api/v1alpha1/` (R3.2) — the
  ScrapeJob CRD's first version. Per master strategy §3.3, the
  v1alpha1 → v1alpha2 migration is a breaking change without a
  conversion webhook (no production users to migrate); v1alpha1
  ScrapeJob CRs in clusters on upgrade are orphaned. Upgrade
  procedure: `kubectl delete scrapejob --all` → install v1alpha2
  CRD → apply v1alpha2 CRs (see
  `docs/architecture/control-plane.md` and the ADR-0019 R3.2
  addendum). The retired surface includes
  `api/v1alpha1/{groupversion_info.go,scrapejob_types.go,
  zz_generated.deepcopy.go}`, the
  `config/samples/spectre_v1alpha1_scrapejob_*.yaml` set, and
  the v1alpha1 entry from `core/control-plane/PROJECT`.
- The Unix-domain-socket transport across all three reference
  adapters and the conformance harness (R2.2). No fallback path
  survives — strategy prompt §2.2 forbids "temporary" legacy
  fallbacks during refactor. The retired surface includes
  `resolveSocketPath` / `resolve_socket_path` resolvers, the
  `--socket` CLI flag, the `SPECTRE_DRIVER_SOCKET` env var, the
  stale-socket unlink logic, the `ready unix:<path>` stdout
  readiness banner, and the `transports:` block in every
  `driver.yaml`.
- `core/engine/src/launcher.rs` (R2.3) — 628 lines of subprocess
  management. The engine no longer spawns adapters; it dials
  them as long-running services via `AdapterRegistry`.
  `LauncherError` and `EngineError::Launcher` go with it.
- `core/engine/tests/integration.rs` (R2.3) — required
  `PLAYWRIGHT_AVAILABLE=1` and Chromium and exercised the same
  engine → adapter loop the conformance suite already covers
  across all three adapters.
- `Engine::new(adapters_path)`, `Engine::run_job(yaml, job_dir)`,
  `Engine::run_plan(plan, job_dir)`, `Engine::validate_only`
  (R2.3). The legacy CLI-shaped API gives way to
  `Engine::from_env` / `Engine::with_registry` and a single
  `Engine::run_plan_with_sink(plan, sink)` entry point that the
  gRPC server's `RunJob` handler drives.
- Engine Cargo.toml deps no longer used (R2.3): `nix` (SIGTERM
  helper), `regex` (readiness-line matching), `tower` (UDS
  connector via `service_fn`), `hyper-util` (production —
  `TokioIo` UDS wrapper), `clap` (CLI subcommand parser),
  dev-only `hyper` and `http-body-util` (integration-test
  fixture HTTP server). `tokio`'s `process` feature is dropped.
  `tonic-health` is added in their place.
- `examples/seleniumbase-navigate/` and
  `examples/curl-impersonate-fetch/` (R2.3) — navigate-only CLI
  demos; the `*-extract` siblings cover the same adapters.
- `core/control-plane/internal/runner/subprocess.go` (R3.1) —
  shelled out to the engine's retired `spectre run` CLI surface
  and bundled the JSONL scanner. `EngineClientRunner` replaces
  it with a gRPC stream consumer; `subprocess_test.go` and the
  `testdata/fake_spectre.go` fixture binary go with it.
- The operator image's bundled engine binary
  (`/usr/local/bin/spectre`) and three adapter trees
  (`/opt/spectre/adapters/{playwright,seleniumbase,curl-impersonate}/`)
  retire with the bundled-image execution model (R3.1). The
  Microsoft Playwright runtime base, the apt overlay for Google
  Chrome + ChromeDriver, the curl-impersonate release tarball
  download, and the per-adapter builder stages are gone. The new
  image is a Go static binary on
  `gcr.io/distroless/static:nonroot` (~50 MB on disk).
- `core/control-plane/hack/smoke-kind.sh` and the gated
  `operator-smoke-kind` CI job (R3.1) — drove the bundled-image
  in-cluster smoke. The multi-service end-to-end smoke returns
  with the Compose stack (R6.2) and the Helm production smoke
  (R7.2).
