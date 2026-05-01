# spectre-control-plane

The Spectre control plane: a Kubernetes-native operator that submits
DSL extraction jobs to the engine over gRPC, tracks their lifecycle,
and (in Phase 3 follow-ups) orchestrates fan-out and scheduling.

> **Status:** Phase 3 in progress, microservices refactor in
> flight. The scaffolding, the `ScrapeJob` CRD, and the
> state-machine reconciler are in place. R1.1–R2.3 turned the
> engine into a stateless gRPC service and the adapters into
> long-running services. R3.1 retired the bundled
> `SubprocessRunner` in favour of `EngineClientRunner`, a thin
> gRPC client that streams `RunJob` events from the engine
> service back into the reconciler. Subsequent phases (R4–R7)
> add stateful services, output sinks beyond stdout, per-service
> Dockerfiles, the Compose stack, and the Helm chart. See
> [`docs/architecture/control-plane.md`](../../docs/architecture/control-plane.md)
> for the user-facing guide,
> [ADR-0019](../../docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md)
> for the original Phase 3 decisions, and
> [ADR-0020](../../docs/adr/0020-microservices-architecture-supersession.md)
> for the refactor's architectural commitment.

## Module path

```
github.com/FabioCaffarello/spectre/operators/control-plane
```

## Layout

The standard kubebuilder v4 layout. Notable files:

- [`api/v1alpha2/scrapejob_types.go`](api/v1alpha2/scrapejob_types.go)
  — `ScrapeJob` CRD Go types and `+kubebuilder:` markers. Edit here;
  `make manifests` regenerates the YAML. v1alpha1 was deleted in
  R3.2 (master strategy §3.3 — breaking change without conversion
  webhook).
- [`config/crd/bases/spectre.io_scrapejobs.yaml`](config/crd/bases/spectre.io_scrapejobs.yaml)
  — generated CRD manifest. Committed; do not hand-edit.
- [`internal/controller/scrapejob_controller.go`](internal/controller/scrapejob_controller.go)
  — the state-machine reconciler. Pending → Running → Completed |
  Failed.
- [`internal/runner/runner.go`](internal/runner/runner.go) — the
  `JobRunner` interface and `StubRunner` (envtest). ADR-0019 §5.
- [`internal/runner/engine_client.go`](internal/runner/engine_client.go)
  — R3.1's `EngineClientRunner`: dials the engine's
  `spectre.engine.v1alpha1.Engine.RunJob` streaming RPC, copies
  every Row event's `json_line` to the writer, and returns the
  Completed event's row count. ADR-0020 §5.
- [`cmd/main.go`](cmd/main.go) — manager bootstrap; wires
  `EngineClientRunner` into the reconciler. Exposes
  `--engine-endpoint` (default `127.0.0.1:8090`, override via
  `SPECTRE_ENGINE_ENDPOINT` env var); Compose / Helm deployments
  set the env var to the engine service's DNS name.
- [`config/samples/`](config/samples/) — five v1alpha2 sample CRs:
  three driver variants (Playwright, SeleniumBase, curl-impersonate)
  using the Service `EngineRef` form, one EngineRef.Endpoint
  variant for ad-hoc local testing, and one schema-only Kafka
  sample documenting R3.2's schema-ahead-of-functionality pattern
  (the reconciler rejects it until R4.4 wires the producer).

## Build and test

From the repository root:

```bash
just op-test           # envtest reconciler suite
just op-build          # produce bin/manager (Go binary only)
just op-build-image    # multi-stage operator image; depends on
                       # the engine image (built first if missing)
just op-image-smoke    # build the image, verify the bundled spectre
                       # binary reports its version, and verify all
                       # three bundled adapters' assets (Playwright +
                       # SeleniumBase + curl-impersonate) are in place
just op-smoke-kind     # full in-cluster end-to-end smoke against
                       # hello-hackernews + seleniumbase-extract +
                       # curl-impersonate-extract, sequentially
                       # (linux/amd64 hosts only)
```

The operator image is multi-stage: the `manager` binary is built
from this directory, `/usr/local/bin/spectre` is copied out of
`spectre-engine:dev` (override with `ENGINE_IMAGE=<image>` to the
Makefile), the Playwright adapter is built in a
`playwright-builder` stage and installed at
`/opt/spectre/adapters/playwright/`, the SeleniumBase adapter is
built in a `seleniumbase-builder` stage (uv-managed venv at the
final runtime path) and installed at
`/opt/spectre/adapters/seleniumbase/`, and the curl-impersonate
adapter is built in a `curl-impersonate-builder` stage (Go,
`CGO_ENABLED=0`) and installed at
`/opt/spectre/adapters/curl-impersonate/`. The runtime base is
the official Microsoft Playwright image
(`mcr.microsoft.com/playwright:v1.59.1-noble`), pinned by digest in
[`adapters/playwright/.playwright-base-image`](../../adapters/playwright/.playwright-base-image)
so version bumps touch one file. The runtime stage carries
apt-installed Python 3.12, `google-chrome-stable`, and a
matching ChromeDriver provisioned via SeleniumBase's installer,
plus the upstream `curl-impersonate` release tarball; the
version + SHA-256 are
pinned in
[`adapters/curl-impersonate/.curl-impersonate-version`](../../adapters/curl-impersonate/.curl-impersonate-version),
the variant binaries (`curl_chrome116`, `curl_chrome110`, …) land
on `/usr/local/bin/`, and the Dockerfile verifies the SHA-256
before extracting. The first build pulls ~600 MB of Microsoft
base layers and ~30 MB of curl-impersonate; subsequent builds
reuse them. The resulting operator image is ~1.95 GB on disk
(Microsoft base ships Chromium + Firefox + WebKit; trimming to
Chromium-only is a separate optimisation PR). ADR-0019 §3 records
the single-Pod execution model.

Or directly via the kubebuilder Makefile:

```bash
cd operators/control-plane
make test              # envtest reconciler suite
make build             # produce bin/manager
make manifests         # regenerate config/crd/bases/ and config/rbac/
make help              # list every target
```

The first `make test` downloads apiserver / etcd / kubectl binaries
into `bin/k8s/<version>-<platform>/` via setup-envtest (~150MB).
Subsequent runs are cached.

## Running locally

`make run` starts the operator in the foreground against the
developer's current `kubectl` context. The full local-cluster
walkthrough lives in
[`docs/architecture/control-plane.md`](../../docs/architecture/control-plane.md#local-cluster-kind-minikube).

## API group

The CRD lives at `spectre.io/v1alpha2`. The kubebuilder default
would have produced `spectre.spectre.io` (group + domain
concatenation); we override `+groupName=spectre.io` in
[`api/v1alpha2/groupversion_info.go`](api/v1alpha2/groupversion_info.go)
so the canonical project domain is the API group. The `PROJECT`
file's `domain: spectre.io` is preserved because it is also used to
synthesise the LeaderElectionID.

## What this does not do (yet)

- **Multi-arch images.** Every Spectre image ships linux/amd64
  only — the engine Dockerfile cross-compiles to
  `x86_64-unknown-linux-musl`, the manager / Microsoft base /
  Google Chrome apt repo / curl-impersonate release tarball all
  run on the same arch. linux/arm64 is release-engineering
  follow-up (R6.5.3).
- **Fan-out / scheduling.** `ScrapeFleet` and `ScrapeSchedule` CRDs
  are Phase 3 follow-up work.
- **Deployment artifacts.** No Helm chart yet (R7.1); no
  validating/mutating webhooks yet; no Prometheus metrics
  beyond controller-runtime's defaults.
- **Observability.** Structured logs, OpenTelemetry traces, and
  metrics are Phase 3 follow-up.

## Architectural references

- [Control plane guide](../../docs/architecture/control-plane.md)
- [ADR-0019 Control plane architecture and ScrapeJob CRD](../../docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md)
- [ADR-0001 Driver protocol as architectural primitive](../../docs/adr/0001-driver-protocol-as-architectural-primitive.md)
- [ADR-0002 Polyglot language selection](../../docs/adr/0002-polyglot-language-selection.md)
- [Roadmap — Phase 3](../../docs/roadmap.md#phase-3--distributed-execution)
