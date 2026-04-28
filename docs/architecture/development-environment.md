# Development environment

This guide describes the two paths to a working Spectre development
environment and the engine Docker image. Phase 2.5 began with PR13;
the per-adapter images and Compose stack arrive in PR14+.

## Two paths to a working dev environment

Spectre is polyglot — Rust (engine), Go (control plane,
curl-impersonate adapter), TypeScript (Playwright adapter), and
Python (SeleniumBase adapter, conformance suite) all participate
in `just check`. Two installation paths cover this:

1. **Devcontainer (recommended).** A single image with all four
   toolchains plus Chrome, ChromeDriver, and curl-impersonate.
   Onboarding is "open the repo in VS Code → Reopen in Container",
   then wait for the first build.
2. **Native install.** The instructions in
   [CONTRIBUTING.md](../../CONTRIBUTING.md#local-development-setup)
   that pre-date the Devcontainer. Still supported; preferred by
   contributors who already have the toolchains installed locally.

Both paths produce the same environment for `just check` and
`just conf-test`. Choose whichever fits your workflow.

## Devcontainer (recommended)

### Prerequisites

- Docker (any recent version; Apple Silicon, Linux, and Windows-WSL2
  all work).
- VS Code with the **Dev Containers** extension
  (`ms-vscode-remote.remote-containers`).

### First-time setup

1. Clone the repository and open it in VS Code.
2. Command palette → **Dev Containers: Reopen in Container**.
3. Wait for the first build. Expect **5–10 minutes**: Docker pulls
   the Ubuntu base image, installs four language toolchains, fetches
   Chrome and curl-impersonate, then runs `just bootstrap` plus
   per-adapter bootstraps via the post-create script. This is normal;
   subsequent reopens reuse the image and are near-instant.
4. When the post-create script finishes, the integrated terminal is
   ready. Run `just check` to verify lint + tests across every
   component.

### What the Devcontainer provides

| Toolchain          | Version                  | Source                           |
|--------------------|--------------------------|----------------------------------|
| Rust               | stable (system-wide)     | rustup                           |
| Go                 | 1.25.3                   | Official tarball                 |
| Node.js            | 20 LTS                   | NodeSource apt                   |
| pnpm               | 9 (via corepack)         | corepack                         |
| Python             | 3.12                     | deadsnakes PPA                   |
| uv                 | latest                   | astral.sh installer              |
| Chrome             | stable                   | Google apt repo                  |
| ChromeDriver       | matched to Chrome        | SeleniumBase installer (post-create) |
| curl-impersonate   | 0.6.1                    | upstream release tarball         |
| protoc             | 27.2                     | upstream release zip             |
| buf                | 1.45.0                   | upstream binary                  |
| just               | 1.36.0                   | upstream binary                  |
| actionlint         | 1.7.4                    | upstream binary                  |
| gitleaks           | 8.21.2                   | upstream binary                  |
| pre-commit         | latest                   | pip3                             |
| kubebuilder        | 4.13.1                   | upstream binary                  |
| kubectl            | 1.31.0                   | upstream binary                  |

VS Code extensions installed automatically: rust-analyzer,
even-better-toml, Go, Python, mypy-type-checker, ruff, ESLint,
Prettier, YAML, Markdown All in One, Buf.

### GitHub Codespaces

The same `.devcontainer/devcontainer.json` works in Codespaces. Open
the repo on github.com → Code → **Codespaces: Create codespace** to
get the same environment without installing Docker locally. First
build takes the same ~5–10 minutes; subsequent opens of an existing
Codespace are instant.

### Caveats and trade-offs

- **First build is slow.** Multi-toolchain apt + rustup + tarball
  downloads add up. The image is reused across reopens, so the cost
  is paid once per clone.
- **Image cache is per-machine.** Devcontainer image registry
  pre-builds (via `devcontainers/ci`) are out of scope for PR13. A
  follow-up PR may add a published image so first-build downloads
  are smaller.
- **CI does not use the Devcontainer.** CI continues to install
  toolchains explicitly on `ubuntu-latest` runners
  (`.github/workflows/ci.yml`). The Devcontainer's value is
  contributor onboarding, not CI parity. ADR-0018 §1 records the
  rationale.

## Native install

The pre-PR13 path. Use this if you already have Rust, Go, Node, and
Python on your machine, or if you prefer not to use Docker.

See [CONTRIBUTING.md → Local development
setup](../../CONTRIBUTING.md#local-development-setup) for the
required tooling, the `brew install` line for macOS, and the
first-time `just bootstrap && just check` flow.

## Engine Docker image

PR13 ships a multi-stage `Dockerfile` for the `spectre` binary at
`core/engine/Dockerfile`. The result is a distroless image (~15 MB)
containing only the engine binary — no shell, no package manager,
no userland. ADR-0018 §3 records the design.

### Build and run

```bash
just engine-image          # docker buildx build … -t spectre-engine:dev
just engine-image-run      # docker run --rm spectre-engine:dev version
```

On Apple Silicon, the build uses Docker's QEMU emulation to produce a
linux/amd64 image (ADR-0018 §5 — single-arch in PR13; multi-arch
later). On linux/amd64 hosts (CI, most contributor laptops on Linux),
the build runs natively without emulation.

### Why the image exists today

The image lays groundwork for the microservices stack the
refactor (R1-R8) is delivering: Phase 3's control plane will
dial the engine as a gRPC service, and R6.2's Compose stack
will run the engine and adapters as separate services backed by
this same image. Building the image now (and exercising it in
CI on every PR) keeps it healthy until those consumers arrive.

After R2.3, the engine binary is itself a gRPC service entry
point (ADR-0020 §3); it is no longer a CLI. End-to-end local
runs against a built image are deferred to R6.2's
`docker compose up` flow.

## Why per-adapter images and Compose are deferred

Phase 2.5 has more than two deliverables; PR13 ships only the two
that have a current consumer:

1. **Devcontainer** — consumed today by every contributor opening
   the repo for the first time.
2. **Engine image** — consumed by Phase 3's control plane (forward
   investment).

The remaining Phase 2.5 deliverables stay deferred:

- **Per-adapter Dockerfiles** (Playwright + Chromium, SeleniumBase +
  Chrome, curl-impersonate static binary). Each has distinct
  toolchain quirks worth a focused PR. Tracked for **PR14**.
- **Compose stack** orchestrating the engine plus a chosen adapter
  for local multi-service development. Tracked for **PR15**.
- **CI image variants** that exercise the per-adapter images.
  Tracked alongside PR14.
- **Multi-arch builds, registry publishing, signing, SBOMs.** A
  release-engineering workflow that fires on tagged releases, not
  every PR.

The phasing is not arbitrary. ADR-0014 §5 (the original Phase 2.5
deferral) and ADR-0018 §4 record the reasoning: per-adapter Docker
packaging is per-adapter work, and combining four parallel surface
areas in one PR optimises for size of diff at the cost of
reviewability.

## Related ADRs

- [ADR-0013](../adr/0013-cli-as-engine-binary.md) — the engine
  binary that the image distributes.
- [ADR-0014 §5](../adr/0014-seleniumbase-adapter-and-cross-language-conformance.md)
  — Phase 2.5 deferral rationale.
- [ADR-0018](../adr/0018-devcontainer-and-engine-image.md) — the
  decisions PR13 records.
