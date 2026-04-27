# spectre-control-plane

The Spectre control plane: a Kubernetes-native operator that submits
DSL extraction jobs to the engine, tracks their lifecycle, and (in
PR16+) orchestrates fan-out and scheduling.

> **Status:** Phase 3 in progress. PR14 shipped the scaffolding,
> the `ScrapeJob` CRD, and the state-machine reconciler against a
> stub. PR15 wired `SubprocessRunner` so jobs actually run against
> the spectre engine binary the operator image bundles. PR16 picks
> up adapter bundling and the in-cluster smoke test. See
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
  (default `/usr/local/bin/spectre`, the path the operator image
  installs to) and `--adapters-path` (optional engine override).
- [`config/samples/`](config/samples/) — three sample CRs, one per
  reference adapter.

## Build and test

From the repository root:

```bash
just op-test           # envtest reconciler suite
just op-build          # produce bin/manager (Go binary only)
just op-build-image    # multi-stage operator image; depends on
                       # the engine image (built first if missing)
just op-image-smoke    # build the image and verify the bundled
                       # spectre binary reports its version
```

The operator image is multi-stage: the `manager` binary is
built from this directory, and `/usr/local/bin/spectre` is copied
out of `spectre-engine:dev` (override with `ENGINE_IMAGE=<image>`
to the Makefile). PR15 §4.3 records the rationale; ADR-0019 §3
records the single-Pod execution model that motivates bundling
the engine into the operator image.

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

- **Bundle adapters.** The operator image carries the engine
  binary but not Playwright/Chromium, SeleniumBase, or
  curl-impersonate. In-cluster jobs need adapters mounted into the
  Pod or available at `--adapters-path`. PR16 lands adapter
  bundling.
- **Fan-out / scheduling.** `ScrapeFleet` and `ScrapeSchedule` CRDs
  are PR16+ work.
- **Deployment artifacts.** No Helm chart yet (PR17+); no
  validating/mutating webhooks yet (PR18+); no Prometheus metrics
  beyond controller-runtime's defaults.
- **Observability.** Structured logs, OpenTelemetry traces, and
  metrics are Phase 3 follow-up.

## Architectural references

- [Control plane guide](../../docs/architecture/control-plane.md)
- [ADR-0019 Control plane architecture and ScrapeJob CRD](../../docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md)
- [ADR-0001 Driver protocol as architectural primitive](../../docs/adr/0001-driver-protocol-as-architectural-primitive.md)
- [ADR-0002 Polyglot language selection](../../docs/adr/0002-polyglot-language-selection.md)
- [Roadmap — Phase 3](../../docs/roadmap.md#phase-3--distributed-execution)
