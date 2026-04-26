---
status: accepted
date: 2026-04-26
deciders: [Fabio Caffarello]
---

# Devcontainer and engine image (Phase 2.5 kickoff)

## Context and Problem Statement

Phase 2 closed with three reference adapters implementing the
v1alpha1 unary surface across three runtime models (PR8 / PR10 /
PR12). ADR-0014 §5 deferred all container infrastructure to a
*Phase 2.5 — Container infrastructure* work block on the grounds
that containers add an inter-process boundary with no protocol
benefit until Phase 3's control plane consumes them. Phase 2.5 is
now in scope. This PR ships its smallest defensible slice: a
Devcontainer that reduces contributor onboarding from a four-
language toolchain install to "Reopen in Container", and a
distroless engine image that lays the groundwork for Phase 3
without claiming any control-plane consumer today. Per-adapter
Dockerfiles, a Compose stack, and CI image variants stay deferred
to PR14+.

## Decision Drivers

- **Contributor onboarding is the load-bearing reason to ship a
  Devcontainer now.** A first-time reviewer needs Rust + Go +
  Node + Python + Chrome + ChromeDriver + curl-impersonate to run
  `just check`. That is enough friction to lose interested
  reviewers before they read the code.
- **The engine image must be Phase-3-ready, not Phase-3-complete.**
  Nothing in this PR or the next several PRs runs the engine from
  a container. The image exists so that when the control plane
  ships, the artifact already does.
- **Phase 2.5 has a long tail** (multi-arch, registry publishing,
  signing, SBOMs). Conservative slicing preserves the option to
  add each piece in its own PR with its own ADR.
- **Per-adapter Docker packaging is per-adapter work.** Playwright
  + Chromium, SeleniumBase + Chrome, and curl-impersonate static
  binary each have distinct toolchain quirks. Bundling all three
  into one PR alongside the engine and the Devcontainer would
  create four parallel surface areas to review.

## Decisions

### 1. Devcontainer over Nix flake or per-language images

Chosen: **a single VS Code Devcontainer (Ubuntu 22.04 base) with
all four language toolchains plus Chrome / ChromeDriver / curl-
impersonate.** The decisive reason is "Reopen in Container" being
the lowest-friction onboarding flow for the audience — engineers
evaluating the architecture, not yet deploying it. GitHub
Codespaces compatibility comes for free from the same config. A
single image keeps cross-language conformance (which needs
Python + Node + Chrome + Go + curl-impersonate together) trivial
to wire.

Rejected:

- **Nix flake.** Powerful and reproducible but high learning curve
  for Nix-unfamiliar contributors. A future ADR if reproducibility
  becomes the dominant concern.
- **Per-language Devcontainers.** Conformance needs the toolchains
  combined; splitting forces a Compose-in-Devcontainer pattern
  that is premature.

### 2. Ubuntu 22.04 LTS as the Devcontainer base image

Chosen: **`mcr.microsoft.com/devcontainers/base:ubuntu-22.04`**.
LTS through April 2027, well-supported Chrome / ChromeDriver via
Google's apt repo, and the canonical Microsoft-curated
Devcontainer base image (pre-configured non-root `vscode` user
with passwordless sudo). Rejected: Debian Bookworm (defensible
but the Microsoft Ubuntu image is more battle-tested for
Devcontainer workflows); Alpine (musl-libc compatibility issues
with ChromeDriver outweigh the size savings — the Devcontainer is
a build environment, not a runtime image).

### 3. Distroless static for the engine runtime image

Chosen: **`gcr.io/distroless/static:nonroot` runtime stage on top
of a `rust:1.85-bookworm` builder targeting
`x86_64-unknown-linux-musl`** with cargo-chef caching dependencies
between rebuilds. Reasons: minimal attack surface (no shell, no
package manager), ~2MB base, and the `nonroot` variant runs as
UID 65532 — sufficient for the engine, which spawns adapter
subprocesses but never needs root itself. The static-musl target
matches ADR-0013 §5's binary distribution decision. The engine
uses `tonic` over `rustls` (no `openssl-sys`), so the static
build is well-supported. Rejected: `alpine:3` (larger and bundles
busybox unnecessarily); `scratch` (same final size but loses
distroless's debug-image variant for triage).

### 4. Per-adapter Dockerfiles deferred to PR14

Each adapter's Docker packaging is a separate engineering
concern with its own quirks: Playwright bundles a ~400MB
Chromium and needs `--no-sandbox` / `--disable-dev-shm-usage`
flags surfaced; SeleniumBase needs Chrome + ChromeDriver and the
same shared-memory tuning; curl-impersonate is the smallest of
the three (Go static + curl-impersonate static binary). PR13
covers the engine image alone so the per-adapter quirks land in a
PR scoped to discuss them. PR14 ships the three adapter images
(or splits further if any one proves painful).

### 5. No image registry publishing in PR13

Chosen: **the new CI job builds the engine image locally on the
runner and runs it; no `docker push` to any registry.** Image
publishing belongs to a release-engineering workflow that fires
on tagged releases, not on every PR. PR13's CI proves the image
*builds and runs*. Multi-arch buildx and `ghcr.io` publishing
stay out of scope until the artifact has versioned releases to
attach to.

## Consequences

- Good, because contributor onboarding compresses from a multi-
  language native install to "Reopen in Container" → wait for the
  first build → `just check`. GitHub Codespaces support is free.
- Good, because the engine image exists and is exercised in CI
  before Phase 3 needs it. When the control plane ships, the
  artifact is already < 50MB and distroless.
- Good, because Phase 2.5's long tail (per-adapter images,
  Compose, multi-arch, registry, signing, SBOMs) stays sliced
  into reviewable PRs rather than collapsing into one.
- Bad, because the first Devcontainer build takes ~5–10 minutes
  on a contributor's machine (multi-toolchain apt + rustup +
  download tarballs). Subsequent reopens are instant. Documented
  in `docs/architecture/development-environment.md`.
- Bad, because the engine image's Rust build cache is rebuilt
  per CI run in PR13. cargo-chef caches *within* a build but the
  CI runner has no inter-run layer cache. A `actions/cache` step
  for the Docker layer cache is a PR14+ optimisation.
- Neutral, because the Devcontainer's value is contributor
  onboarding, not CI parity. CI continues to use ubuntu-latest
  with explicit toolchain setup; the Devcontainer is for humans
  opening the repo for the first time.

## Confirmation

- Acceptance criteria 1–12 of the PR13 master prompt are the
  verification checklist for this ADR.
- A clean clone followed by `just bootstrap && just check`
  succeeds on Linux and macOS unchanged from PR12.
- `docker build -t spectre-engine:dev -f core/engine/Dockerfile .`
  and `docker run --rm spectre-engine:dev version` exit zero. The
  built image is < 50MB.
- The new `engine-image` CI job builds and smoke-tests the image
  on every PR.
- "Reopen in Container" produces a working environment in which
  `just check` and `just conf-test` exit zero (manual verification
  by the maintainer; not automated in CI for PR13).

## More Information

- Devcontainer specification:
  <https://containers.dev/implementors/spec/>
- Microsoft Devcontainer base images:
  <https://github.com/devcontainers/images/tree/main/src/base-ubuntu>
- cargo-chef: <https://github.com/LukeMathWalker/cargo-chef>
- Distroless images:
  <https://github.com/GoogleContainerTools/distroless>
- Related ADRs:
  [ADR-0013 CLI as engine binary](0013-cli-as-engine-binary.md),
  [ADR-0014 §5 SeleniumBase adapter and cross-language conformance — Phase 2.5 deferral](0014-seleniumbase-adapter-and-cross-language-conformance.md).
