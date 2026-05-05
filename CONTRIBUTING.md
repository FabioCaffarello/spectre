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
   `proto/spectre/driver/v1alpha1/`. The transport is gRPC over
   TCP; the adapter runs as a long-running service exposing
   `Driver` plus `grpc.health.v1.Health`
   (see [ADR-0022](docs/adr/0022-tcp-grpc-transport.md)).
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

## Architectural commitments

The microservices refactor (R1 → R8.1, completed 2026-05-03) was
guided by seven non-negotiable principles. These commitments outlast
the refactor and bind v1alpha2 work too. Reading them is reading the
project's architectural conscience.

### The driver protocol stays frozen

The wire contract between engine and adapters — the gRPC service
definitions in
[`proto/spectre/driver/v1alpha1/`](proto/spectre/driver/v1alpha1/) —
does not change without an explicit ADR amendment to
[ADR-0001](docs/adr/0001-driver-protocol-as-architectural-primitive.md).
Capability lists per adapter (Playwright 13, SeleniumBase 12,
curl-impersonate 6) are preserved byte-for-byte through every PR; the
conformance test
`test_<adapter>_initialize::test_capabilities_match_manifest_byte_for_byte`
catches regressions. The protocol is the project's most distinctive
asset; touching it conflates concerns and corrupts the audit trail.

### No legacy paths survive

When a PR replaces a code path, the old path is **deleted in the same
PR**. No "temporary" fallbacks. No `runner.SubprocessRunner` left
behind once `runner.EngineClientRunner` lands. No UDS fallback once
TCP is in. No `spectre run` CLI once Compose is canonical. Legacy
paths during refactor become permanent; each "temporary" fallback
adds maintenance burden, dilutes the architecture, and undermines
narrative coherence.

After each PR merges, a grep for the retired pattern returns zero
hits in source code (allowed in ADRs documenting the retirement, in
CHANGELOG entries, in
[`docs/refactor-audit.md`](docs/refactor-audit.md) historical
entries).

### Capability divergence is preserved exactly

The capability lists declared by each adapter — Playwright 13,
SeleniumBase 12, curl-impersonate 6 — remain unchanged. The
`driver.yaml` files, the byte-for-byte conformance assertions, the
runtime declarations — all identical from the project's beginning.
The strict-subset chain is the project's most architecturally
consequential narrative artifact (see
[ADR-0017 §1](docs/adr/0017-curl-impersonate-extraction-strategy.md)).
Capability surface changes require an ADR.

### Each PR is independently reviewable

PRs are not bundled. Each one is opened, reviewed, merged before the
next begins. No "mega-PR" with 50 file changes across all components.
PR diffs typically fit within 500–2000 lines of substantive change.
PRs that exceed this should be split into a sequence of smaller,
individually-merged PRs with their own ADRs and acceptance criteria.

### Compose is the development environment

`docker compose up` brings up the full stack and is the supported
local development workflow. There is no "run engine standalone" path,
no "run adapter as subprocess" path, no Devcontainer-without-Compose
path. Microservices are validated by running the full graph locally;
allowing alternative dev paths reintroduces the monolithic mental
model the refactor exists to retire. See
[ADR-0025](docs/adr/0025-compose-stack.md) and
[`docs/architecture/development-environment.md`](docs/architecture/development-environment.md).

### ADR supersession is explicit and recorded

When a PR supersedes an existing ADR, the supersession is recorded
**both** in the new ADR (referencing what it supersedes) and in the
old ADR (status update pointing to the superseder). Partial
supersessions are allowed (a section of an old ADR superseded, the
rest preserved) and documented per the convention established by
ADR-0008's status field. ADRs 0001–0030 are immutable text once
accepted; in-place evolution notes (the precedent established by
ADR-0018's R6.3 / R6.5.4 updates and ADR-0007's R6.6 evolution notes)
are the only allowed amendments.

### Tests are not weakened

CI gates that catch regressions are preserved through every PR. New
tests are added when new surfaces emerge (the `helm-lint` gate in
R7.1, the `production-smoke` gate in R7.2, the version-coherence
script in R6.5.1). Tests are never weakened to accommodate refactor
convenience. If a test fails because of a refactor change, the
refactor is wrong.

---

## Local development setup

### Required tooling

| Tool                          | Used for                                                  |
|-------------------------------|-----------------------------------------------------------|
| Rust (stable)                 | `engines/engine`                                             |
| Go (1.24+ for adapters; 1.25+ for `operators/control-plane`) | `operators/control-plane`, `adapters/curl-impersonate` |
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

### Operator development (`operators/control-plane`)

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
                       # First run also pulls ~600 MB of Microsoft
                       # Playwright base layers from mcr.microsoft.com.
just op-smoke-kind     # full in-cluster end-to-end smoke (linux/amd64
                       # hosts only; Apple Silicon falls back to CI).
```

The operator image bundles the spectre engine binary at
`/usr/local/bin/spectre` (PR15 §4.3), the Playwright adapter at
`/opt/spectre/adapters/playwright/` (PR16), the SeleniumBase
adapter at `/opt/spectre/adapters/seleniumbase/` (PR17), and the
curl-impersonate adapter at
`/opt/spectre/adapters/curl-impersonate/` (PR18). The runtime
base is the official Microsoft Playwright image pinned by digest
in
[`adapters/playwright/.playwright-base-image`](adapters/playwright/.playwright-base-image);
the first build needs network access to `mcr.microsoft.com`. The
`playwright-builder`, `seleniumbase-builder`, and
`curl-impersonate-builder` stages each fetch the `buf` CLI from
GitHub releases to regenerate their language's protocol bindings
inside the build context; the SeleniumBase stage additionally
fetches `uv` from `astral.sh`. The runtime stage fetches
`google-chrome-stable` from `dl.google.com`, Python wheels for
SeleniumBase from `pypi.org`, and the curl-impersonate release
tarball from `github.com/lwthiker/curl-impersonate/releases` —
the latter pinned by version + SHA-256 in
[`adapters/curl-impersonate/.curl-impersonate-version`](adapters/curl-impersonate/.curl-impersonate-version)
so a bump touches one file.

For local-cluster smoke testing (kind / minikube / Docker Desktop):

```bash
just op-install-crds   # apply the ScrapeJob CRD to the current
                       # kubectl context
just op-run            # run the operator in the foreground; uses
                       # the workspace's release-build spectre binary
                       # and the workspace adapters/ directory
# Apply a sample CR in another terminal:
kubectl apply -f operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_endpoint.yaml
kubectl get scrapejob -w
just op-uninstall-crds # tear down
```

R3.1's `EngineClientRunner` dials the engine over gRPC; a v1alpha2
`ScrapeJob` whose `outputSink.stdout` is set produces real JSONL
rows on the operator's stdout (or the foreground `op-run`
terminal) and `RowsExtracted` reflects the engine's row count.
v1alpha2 added `EngineRef` (Service or Endpoint) and the
`OutputSink` discriminated union; see
[docs/architecture/control-plane.md](docs/architecture/control-plane.md)
and [ADR-0019](docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md).

---

## Pull request expectations

### v1alpha2 process rigor matrix

*Different v1alpha2 PRs warrant different process rigor. The matrix
below maps PR scale to documentation overhead so the post-refactor
velocity matches the work's actual scope.*

| Work scale | Master phase prompt? | New ADR? | Multi-cluster commits? | Acceptance criteria? |
|------------|----------------------|----------|------------------------|----------------------|
| Transformational change (e.g., first SDK migration, first infra-service, observability framework, engine orchestrator refactor) | YES — full §1-§12 prompt | YES | YES (5–9 clusters) | YES — exhaustive list |
| Single architectural decision (e.g., one multi-arch unblock, one image-scan addition) | NO — execution checklist | YES if it warrants | OPTIONAL — single commit OK | YES — focused list |
| Incremental change without architectural commitment (e.g., chart helper improvement, doc fix, dependency bump) | NO | NO | NO — single commit | OPTIONAL — Conventional Commit + CHANGELOG |
| Doc-only change | NO | NO | NO | NO |

*The tell for which level applies: ask "would a future contributor
reading the code in 2 years need an ADR to understand why this
decision was made?" If yes, ADR. If no, just CHANGELOG entry.*

### Baseline expectations

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
