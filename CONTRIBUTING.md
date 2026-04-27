# Contributing to Spectre

Thanks for your interest in contributing.

This document covers three contributor paths, ordered from lowest to
highest barrier to entry. By participating, you agree to follow our
[Code of Conduct](CODE_OF_CONDUCT.md).

---

## Development environment

Two paths to a working environment. Pick whichever fits your workflow.

- **Devcontainer (recommended).** Open the repo in VS Code, then
  *Dev Containers: Reopen in Container*. The first build is ~5–10
  minutes; everything you need (Rust, Go, Node, Python, Chrome,
  ChromeDriver, curl-impersonate, buf, just) is included. Works
  identically in GitHub Codespaces. See
  [docs/architecture/development-environment.md](docs/architecture/development-environment.md)
  for details and the deferred Phase 2.5 work (per-adapter images,
  Compose stack).
- **Native install.** Install the toolchains listed under [Local
  development setup](#local-development-setup) and run `just bootstrap
  && just check`. Preferred if you already have the toolchains on your
  machine.

---

## Path 1 — Bug fixes and documentation improvements

The lowest-barrier way to contribute.

1. Fork the repository and create a topic branch from `main`.
2. Make your change.
3. Run `just check` (or `make check`) locally to verify formatting,
   lint, and tests pass.
4. Open a pull request. Use a [Conventional Commits][cc] message
   format for the PR title (for example `fix(playwright-adapter):
   handle navigation timeout`).
5. The maintainer will review. Most doc fixes and small bug fixes are
   merged within a few days.

[cc]: https://www.conventionalcommits.org/

## Path 2 — Writing a new driver

This is the most impactful contribution path. Spectre's value grows
with each driver added to the ecosystem.

A new driver must:

1. Implement the Driver Protocol defined in
   `proto/spectre/driver/v1alpha1/`. You can use either gRPC or
   JSON-RPC over stdio as the transport.
2. Declare its capabilities in a `driver.yaml` manifest. See existing
   adapters in `adapters/` for reference.
3. Pass the conformance test suite at `tools/conformance/`. The suite
   exercises the contract; your driver does not need to support every
   capability, but the capabilities it declares must work.
4. Include a README documenting the driver, its capabilities, runtime
   requirements, and any caveats.

Read [docs/guides/writing-a-driver.md](docs/guides/writing-a-driver.md)
for a step-by-step walkthrough.

Open a draft PR early. The maintainer will help you iterate before
final review.

## Path 3 — Core engine, control plane, or protocol changes

Higher bar. Changes here affect every driver and every user.

For non-trivial changes (anything beyond a bug fix), an
[Architecture Decision Record](docs/adr/) is required before
implementation. The flow is:

1. Open an issue describing the proposed change and the problem it
   solves.
2. After discussion, draft an ADR using
   [the template](docs/adr/0000-template.md).
3. Submit the ADR as a pull request. The ADR is reviewed and either
   accepted, rejected, or sent back for revision.
4. Once the ADR is accepted, implementation can proceed in a separate
   PR that references the ADR.

This process exists to protect the project's long-term coherence. It
is not bureaucracy for its own sake. ADR review is typically completed
within one week.

---

## Local development setup

### Required tooling

| Tool                          | Used for                                                  |
|-------------------------------|-----------------------------------------------------------|
| Rust (stable)                 | `core/engine`                                             |
| Go (1.24+ for adapters; 1.25+ for `core/control-plane`) | `core/control-plane`, `adapters/curl-impersonate` |
| `goimports`                   | Pre-commit Go imports hook                                |
| `golangci-lint`               | Go lint aggregator (used by `just cp-lint`)               |
| `kubectl` (optional)          | Apply ScrapeJob CRs against a local cluster               |
| `kind` or `minikube` (optional) | Local Kubernetes cluster for operator smoke tests       |
| Node.js (20+) and `pnpm` (9+) | `adapters/playwright`                                     |
| Python (3.10+) and `uv`       | `adapters/seleniumbase`, `tools/conformance`              |
| `buf`                         | Protobuf lint, format, breaking-change detection          |
| `just`                        | Build orchestration (`just check`, `just bootstrap`, ...) |
| `pre-commit`                  | Local Git hooks                                           |
| `actionlint` (optional)       | Validates `.github/workflows/*.yml` locally               |

#### Install on macOS

```bash
brew install just buf actionlint pre-commit uv golangci-lint
go install golang.org/x/tools/cmd/goimports@latest
# pnpm: corepack is no longer bundled with recent Node releases. Pick one:
npm install -g corepack && corepack enable pnpm
# or:
npm install -g pnpm
```

#### Install on Linux

Use the upstream installation guides for `just`, `buf`, `actionlint`, and
`uv` (each provides a one-line installer). `goimports` and `pnpm` follow
the same commands as macOS.

### First-time setup

```bash
git clone https://github.com/FabioCaffarello/spectre.git
cd spectre
pre-commit install            # commit hooks
pre-commit install --hook-type commit-msg   # Conventional Commits check
just bootstrap                # installs all per-language dependencies
just check                    # runs lint and tests across all components
```

### Per-language work

Each component directory has its own README with build and test
instructions specific to that language.

### Operator development (`core/control-plane`)

The control plane is a kubebuilder v4 operator. Its tests run against
a real apiserver and etcd via the controller-runtime `envtest`
package — no external Kubernetes cluster is required for unit tests.

```bash
just op-test           # envtest reconciler suite (downloads apiserver
                       # binaries on first run; ~2 minutes)
just op-build          # build bin/manager
just op-build-image    # multi-stage operator image. Depends on the
                       # engine image — `just engine-image` runs first
                       # if `spectre-engine:dev` is missing locally.
```

The operator image bundles the spectre engine binary at
`/usr/local/bin/spectre` (PR15 §4.3); the multi-stage Dockerfile
copies it out of `spectre-engine:dev`, so the engine image must be
built first. `just op-build-image` chains `engine-image` → operator
image automatically.

For local-cluster smoke testing (kind / minikube / Docker Desktop):

```bash
just op-install-crds   # apply the ScrapeJob CRD to the current
                       # kubectl context
just op-run            # run the operator in the foreground; uses
                       # the workspace's release-build spectre binary
                       # and the workspace adapters/ directory
# Apply a sample CR in another terminal:
kubectl apply -f core/control-plane/config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml
kubectl get scrapejob -w
just op-uninstall-crds # tear down
```

PR15 wired `SubprocessRunner`: a `ScrapeJob` whose `outputSink` is
`stdout` produces real JSONL rows on the operator's stdout (or the
foreground `op-run` terminal) and `RowsExtracted` reflects the
engine's row count. See
[docs/architecture/control-plane.md](docs/architecture/control-plane.md)
and [ADR-0019](docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md).

---

## Pull request expectations

- **One logical change per PR.** Smaller PRs review faster.
- **Tests for new behavior.** Bug fixes include a regression test.
  New features include unit tests at minimum.
- **Documentation updates.** If you change behavior, update the
  relevant README or guide in the same PR.
- **Conventional Commits** for the PR title. The squash-merge commit
  message is generated from the title.
- **CI must pass.** All language-specific lint, format, type, and
  test jobs must be green before merge.

## License of contributions

By contributing, you agree that your contributions will be licensed
under the [Apache License 2.0](LICENSE), the same license as the
project.
