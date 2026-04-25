# Contributing to Spectre

Thanks for your interest in contributing.

This document covers three contributor paths, ordered from lowest to
highest barrier to entry. By participating, you agree to follow our
[Code of Conduct](CODE_OF_CONDUCT.md).

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
| Go (1.22+)                    | `core/control-plane`, `adapters/curl-impersonate`         |
| `goimports`                   | Pre-commit Go imports hook                                |
| `golangci-lint`               | Go lint aggregator (used by `just cp-lint`)               |
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
