# spectre-control-plane

The Spectre control plane: a Kubernetes-native operator that submits
DSL extraction jobs to the engine, tracks their lifecycle, and (in
PR16+) orchestrates fan-out and scheduling.

> **Status:** Phase 3 in progress. PR14 shipped the scaffolding,
> the `ScrapeJob` CRD, and the state-machine reconciler against a
> stub. PR15 wired `SubprocessRunner` so jobs actually run against
> the spectre engine binary the operator image bundles. PR16
> bundled the Playwright adapter into the operator image and
> closed the end-to-end loop with an in-cluster smoke against
> `hello-hackernews`. SeleniumBase (PR17) and curl-impersonate
> (PR18) replicate the builder-stage pattern. See
> [`docs/architecture/control-plane.md`](../../docs/architecture/control-plane.md)
> for the user-facing guide and
> [ADR-0019](../../docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md)
> for the design decisions.

## Module path

```
github.com/FabioCaffarello/spectre/core/control-plane
```

## Layout

The standard kubebuilder v4 layout. Notable files:

- [`api/v1alpha1/scrapejob_types.go`](api/v1alpha1/scrapejob_types.go)
  — `ScrapeJob` CRD Go types and `+kubebuilder:` markers. Edit here;
  `make manifests` regenerates the YAML.
- [`config/crd/bases/spectre.io_scrapejobs.yaml`](config/crd/bases/spectre.io_scrapejobs.yaml)
  — generated CRD manifest. Committed; do not hand-edit.
- [`internal/controller/scrapejob_controller.go`](internal/controller/scrapejob_controller.go)
  — the state-machine reconciler. Pending → Running → Completed |
  Failed.
- [`internal/runner/runner.go`](internal/runner/runner.go) — the
  `JobRunner` interface and PR14's `StubRunner`. ADR-0019 §5.
- [`internal/runner/subprocess.go`](internal/runner/subprocess.go)
  — PR15's `SubprocessRunner`: shells out to the spectre engine
  binary, streams JSONL through `bufio.Scanner`, reports row counts.
- [`cmd/main.go`](cmd/main.go) — manager bootstrap; wires
  `SubprocessRunner` into the reconciler. Exposes `--engine-binary`
  (default `/usr/local/bin/spectre`) and `--adapters-path` (default
  `/opt/spectre/adapters`, the path the operator image installs the
  bundled adapters to; overridden by `just op-run` to point at the
  workspace `adapters/` directory).
- [`config/samples/`](config/samples/) — three sample CRs, one per
  reference adapter.

## Build and test

From the repository root:

```bash
just op-test           # envtest reconciler suite
just op-build          # produce bin/manager (Go binary only)
just op-build-image    # multi-stage operator image; depends on
                       # the engine image (built first if missing)
just op-image-smoke    # build the image, verify the bundled spectre
                       # binary reports its version, and verify the
                       # bundled Playwright adapter assets are in place
just op-smoke-kind     # full in-cluster end-to-end smoke against
                       # hello-hackernews (linux/amd64 hosts only)
```

The operator image is multi-stage: the `manager` binary is built
from this directory, `/usr/local/bin/spectre` is copied out of
`spectre-engine:dev` (override with `ENGINE_IMAGE=<image>` to the
Makefile), and the Playwright adapter is built in a
`playwright-builder` stage and installed at
`/opt/spectre/adapters/playwright/`. The runtime base is the
official Microsoft Playwright image
(`mcr.microsoft.com/playwright:v1.59.1-noble`), pinned by digest in
[`adapters/playwright/.playwright-base-image`](../../adapters/playwright/.playwright-base-image)
so version bumps touch one file. The first build pulls ~600 MB of
Microsoft base layers; subsequent builds reuse them. The resulting
operator image is ~1.0 GB on disk (Microsoft base ships Chromium +
Firefox + WebKit; trimming to Chromium-only is a separate
optimisation PR). ADR-0019 §3 records the single-Pod execution
model.

Or directly via the kubebuilder Makefile:

```bash
cd core/control-plane
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

The CRD lives at `spectre.io/v1alpha1`. The kubebuilder default
would have produced `spectre.spectre.io` (group + domain
concatenation); we override `+groupName=spectre.io` in
[`api/v1alpha1/groupversion_info.go`](api/v1alpha1/groupversion_info.go)
so the canonical project domain is the API group. The `PROJECT`
file's `domain: spectre.io` is preserved because it is also used to
synthesise the LeaderElectionID.

## What this does not do (yet)

- **Bundle SeleniumBase / curl-impersonate.** PR16 ships only the
  Playwright adapter in the operator image. SeleniumBase (PR17)
  needs Google Chrome rather than Chromium and a uv-managed venv;
  curl-impersonate (PR18) needs the native libcurl-impersonate
  binary on PATH. Each replicates PR16's builder-stage pattern.
- **Multi-arch images.** PR16 ships linux/amd64 only — the engine
  Dockerfile cross-compiles to `x86_64-unknown-linux-musl`, and
  the manager / Microsoft base images run on the same arch.
  linux/arm64 is release-engineering follow-up.
- **Fan-out / scheduling.** `ScrapeFleet` and `ScrapeSchedule` CRDs
  are PR19+ work.
- **Deployment artifacts.** No Helm chart yet (PR19+); no
  validating/mutating webhooks yet (PR19+); no Prometheus metrics
  beyond controller-runtime's defaults.
- **Observability.** Structured logs, OpenTelemetry traces, and
  metrics are Phase 3 follow-up.

## Architectural references

- [Control plane guide](../../docs/architecture/control-plane.md)
- [ADR-0019 Control plane architecture and ScrapeJob CRD](../../docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md)
- [ADR-0001 Driver protocol as architectural primitive](../../docs/adr/0001-driver-protocol-as-architectural-primitive.md)
- [ADR-0002 Polyglot language selection](../../docs/adr/0002-polyglot-language-selection.md)
- [Roadmap — Phase 3](../../docs/roadmap.md#phase-3--distributed-execution)
