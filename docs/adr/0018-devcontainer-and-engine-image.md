---
status: accepted (partially superseded; see status notes in §3, §4 and §5)
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

#### R6.5.4 update — shared codegen base

The four buf-consuming Dockerfiles
(`core/control-plane`, `adapters/curl-impersonate`,
`adapters/playwright`, `adapters/seleniumbase`) extract their
buf install into a single `build/docker/buf-base.Dockerfile`
stage consumed via bake's `contexts:` feature. Each consumer
carries one
`COPY --from=buf-base /usr/local/bin/buf /usr/local/bin/buf`
line in place of the previous ~10-line install RUN. The
engine remains independent — Rust uses `prost-build`, not
`buf`. Image sizes are unchanged: buf lives only in the
codegen stage, never reaches runtime. The bumping procedure
and the deeper rationale live in `build/docker/README.md`'s
"Shared codegen base" section. ADR-0020 §4's R6.5 row already
covers this PR alongside R6.5.1–R6.5.3.

### 3a. R6.3 evolution: Docker-in-Docker for the devcontainer

> **Status — added in R6.3 (2026-04-29).** Decision §1 chose a
> single VS Code Devcontainer; decision §2 chose Ubuntu 22.04 LTS
> as the base. Both stand. R6.3 amends the *content* of the
> devcontainer rather than the choice itself: the
> `docker-in-docker` feature
> (`ghcr.io/devcontainers/features/docker-in-docker:2`, Moby
> variant, `dockerDashComposeVersion: "v2"`) is added via
> `.devcontainer/devcontainer.json`, and `kind` (0.24.0) plus
> `kubectl` (1.31.0 — already pinned in PR13) are installed in the
> Dockerfile. ADR-0025 §6 R6.3 update is the authoritative
> reference for the post-R6.3 shape; this status note is the
> audit trail.

PR13's devcontainer was single-Docker-daemon — the host's daemon
was invisible from inside the devcontainer; the contributor's
local-dev path mixed host-side `docker compose up` with native
binaries running inside the devcontainer. R6.2 brought the
application services into the Compose stack; R6.3 (ADR-0025 §6
R6.3 update) closes the loop by placing both the Compose stack
and a `kind` cluster inside the devcontainer's own Docker
daemon (DinD). The `docker` CLI inside the devcontainer talks to
that inner daemon; tearing down the devcontainer destroys the
DinD volumes, the Compose stack, and the kind cluster — clean
slate.

Master plan ADR-0020 §85–91 commits to DinD over the
socket-mount pattern (`docker-outside-of-docker`):

> **The Devcontainer ships with Docker-in-Docker enabled.** The
> contribution barrier rises slightly; the architectural coherence
> of "what runs in development equals what runs in production" is
> the offsetting benefit.

DinD is the architectural risk in this evolution (cgroup
permissions, privileged-mode requirements, Docker Desktop vs
native-Linux differences). The official feature handles those
uniformly across hosts; the failure modes are documented in
[`docs/architecture/development-environment.md`](../architecture/development-environment.md)
under the "DinD model" subsection.

Operational characteristics:

- **First-time devcontainer build** rises from ~5–10 minutes
  (PR13) to ~10–15 minutes — the DinD feature install adds ~30s,
  the kind binary download adds ~10s, the post-create script's
  `kind-up` step adds ~30s on first run. Subsequent reopens
  remain instant.
- **kind kubeconfig has two server URLs.** `kind get kubeconfig
  --internal` writes `https://spectre-dev-control-plane:6443`
  (in-network URL, used by the operator container's mounted
  kubeconfig at `build/kind/kubeconfig`); kind also writes
  `~/.kube/config` automatically with the host-port URL for
  `kubectl` from the devcontainer terminal. The two-config dance
  is standard kind practice and not project-specific.
- **`kind` Docker network is shared.** `kind create cluster`
  creates a Docker network named `kind`; `docker-compose.yml`
  attaches the operator service to it via `external: true`. If
  the network is missing (`compose-up` before `kind-up`),
  Compose errors with "network kind not found" — the designed
  behaviour.

R6.3 also harmonises devcontainer toolchain ARGs with
`build/docker/versions.env` (BUF 1.45.0 → 1.55.1; Go / Node /
Python / protoc were already aligned). The single source of
truth lives in `versions.env`; the Dockerfile's ARG values
mirror it for clarity.

### 4. Per-adapter Dockerfiles deferred to PR14

> **Status — retired by R6.1 (2026-04-29).** Per-adapter
> Dockerfiles for Playwright, SeleniumBase, and curl-impersonate
> landed in R6.1 alongside `docker-bake.hcl` orchestration and the
> `build/docker/versions.env` single-source-of-truth. The PR14-era
> deferral is closed. R6.1's Dockerfiles diverge slightly from this
> §4's sketch — see
> [`docs/architecture/container-images.md`](../architecture/container-images.md)
> for the runtime-base matrix and the curl-impersonate deviation
> (Alpine upstream image rather than distroless, because the variant
> binaries are POSIX shell wrappers).

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

> **Status — reaffirmed for R6.1; updated in R6.5.3 (2026-04-29).**
> ghcr.io is replaced by Docker Hub `fabiocaffarello/spectre-<name>`
> as the publish target; multi-arch ships for control-plane +
> playwright; three deferrals (engine, seleniumbase,
> curl-impersonate) are recorded with explicit unblock criteria.
> See "R6.5.3 update — Docker Hub registry + multi-arch reality"
> below.

> **Status (R6.1) — reaffirmed.**
> Single-arch (linux/amd64) builds and no-registry-publish remained
> in effect through R6.1: every target in `docker-bake.hcl` pinned
> `platforms = ["linux/amd64"]` and the `REGISTRY` bake variable
> defaulted to empty (local-only). Multi-arch matrix, registry
> publishing, image signing (`cosign`), and SBOMs (`syft`) were
> deferred — the bake variables (`TAG`, `REGISTRY`, `VCS_REF`,
> `BUILD_DATE`) are the hooks; nothing in R6.1 foreclosed any of it.

Chosen: **the new CI job builds the engine image locally on the
runner and runs it; no `docker push` to any registry.** Image
publishing belongs to a release-engineering workflow that fires
on tagged releases, not on every PR. PR13's CI proves the image
*builds and runs*. Multi-arch buildx and registry publishing
stayed out of scope until the artifact had versioned releases to
attach to.

#### R6.5.3 update — Docker Hub registry + multi-arch reality

R6.5.3 makes the publish flow operational. Two pivots from the
R6.1 reaffirmation, plus three explicit deferrals.

**Pivot 1: Docker Hub, not ghcr.io.** The maintainer's decision
is to publish to **Docker Hub** under the personal account
`fabiocaffarello` (flat namespace — Docker Hub does not support
nested paths under a personal account). The five image
references become:

- `fabiocaffarello/spectre-engine`
- `fabiocaffarello/spectre-control-plane`
- `fabiocaffarello/spectre-playwright`
- `fabiocaffarello/spectre-seleniumbase`
- `fabiocaffarello/spectre-curl-impersonate`

The bake `image()` function is registry-agnostic — it produces
`<registry>/spectre-<name>:<tag>` for any non-empty `REGISTRY`,
including `fabiocaffarello`, `ghcr.io/<owner>`, or a private
registry. Only the documented value of `REGISTRY` changes; no
bake function or target structure is touched.

**Pivot 2: Multi-arch where it is achievable today.** Of the
five images, only two can practically ship multi-arch
(`linux/amd64,linux/arm64`) without engineering work that would
exceed the scope of a hygiene-phase PR:

| Image | Multi-arch ready? | Blocker / unblock criteria |
|-------|-------------------|----------------------------|
| **control-plane** | ✅ today | None. Pure Go cross-compile (CGO_ENABLED=0 + GOARCH=${TARGETARCH}). Multi-arch from R6.5.3 publish. |
| **playwright** | ✅ today | None. Microsoft Playwright runtime image is multi-arch; Node `pnpm install` runs under QEMU emulation on amd64 runners (~3-5x slowdown — acceptable for v1alpha1; native arm64 runners are a v1alpha2 optimisation). Multi-arch from R6.5.3 publish. |
| **engine** | ❌ deferred | `MUSL_TARGET` hardcoded to `x86_64-unknown-linux-musl`; the apt-installed `musl-tools` + the `x86_64-linux-musl-g++` symlink + the `cp -r .../x86_64-linux-gnu/curl .../x86_64-linux-musl/curl` step are all amd64-specific. Unblock: select `MUSL_TARGET` per `${TARGETARCH}` (`aarch64-unknown-linux-musl` on arm64), install matching cross-compiler (`gcc-aarch64-linux-gnu` + `g++-aarch64-linux-gnu`), create `aarch64-linux-musl-g++` symlink, select libcurl include-path source per `${TARGETARCH}`. ~30-line Dockerfile change in a focused PR. |
| **seleniumbase** | ❌ deferred | Google Chrome stable for Linux is published amd64-only as of R6.5.3 (Chromium has arm64 builds; Chrome stable does not). The `[arch=amd64]` apt-source pin and the `seleniumbase install chromedriver` runtime step both depend on the amd64-only `google-chrome-stable` package. Unblock paths: (a) wait for Google to publish a Linux/arm64 stable channel; (b) switch the adapter from Chrome to Chromium — multi-arch but changes the project's tested driver surface, an ADR-level decision deferred to v1alpha2. |
| **curl-impersonate** | ❌ deferred | Runtime base `lwthiker/curl-impersonate:0.6-chrome` is published amd64-only on Docker Hub. Unblock paths: (a) wait for upstream to publish a multi-arch manifest list; (b) fork the upstream image build and publish our own multi-arch tag; (c) cross-compile curl-impersonate from source per [their `INSTALL.md`](https://github.com/lwthiker/curl-impersonate/blob/main/INSTALL.md)'s ARM64 instructions and skip the upstream base entirely. |

The forward-readiness work in R6.5.3's Dockerfiles
(`ARG TARGETPLATFORM` / `ARG TARGETARCH` declarations in the
deferred three; `R7.x: multi-arch unblock` comment blocks
referencing this subsection) narrows v1alpha2 work to the
specific blocker per image, not wholesale Dockerfile rewrites.

**Per-target platform set declared at publish time, not in
`docker-bake.hcl`.** The bake targets keep
`platforms = ["linux/amd64"]` as their default; the publish
workflow declares multi-arch via per-target bake `--set`
overrides (one `--set <target>.platform=<plat>` per platform
per target). The platform set is a deployment concern, not a
build concern; CI's verify-only matrix doesn't need overrides,
and adding new multi-arch targets later is a few lines in
`publish.yml` rather than a bake structural change.

**`workflow_dispatch` only in R6.5.3; tag-triggered and `:edge`
deferred.** The repo has no tags as of R6.5.3 merge (`VERSION =
0.1.0-alpha.0`); auto-trigger on tag pushes before any tags
exist is theoretical. Manual dispatch + manifest verification
builds maintainer confidence before automation. Tag-triggered
publish (`v*.*.*` → `:<tag>` and `:latest`) and main-branch
publish (`:edge`) are ~5-line additions to `publish.yml`'s `on:`
block when the project is ready (R7.x or v1alpha2). The R6.5.3
design doesn't preempt that future step.

**Deliverables of R6.5.3:**

- `.github/workflows/publish.yml` (workflow_dispatch only;
  three inputs: `tag`, `targets`, `multi_arch`)
- `.github/workflows/ci.yml` `publish-dry-run` job (multi-arch
  build path validation, no push)
- `docker-bake.hcl` comment block updated for Docker Hub
- five Dockerfiles annotated with multi-arch posture
  (control-plane + playwright as ready; engine, seleniumbase,
  curl-impersonate as deferred with R7.x comments)
- this ADR-0018 §5 amendment
- new `docs/architecture/releases.md` (operator-facing)
- `docs/architecture/container-images.md` updated with the
  Multi-arch status subsection

**What stays unchanged:**

- ADR-0007 (proto codegen at build time)
- ADR-0016 §1 (subprocess-over-cgo)
- The `image()` function in bake (registry-agnostic from R6.1)
- The `images` matrix and `full-stack` jobs in CI (verify-only,
  single-arch — `--load` cannot represent a manifest list)
- All five service binaries' source trees (R6.5.3 is packaging
  only — capability invariant 13/12/6 holds byte-for-byte)
- Phase R6.5's no-new-ADR posture (R6.5.1 §9.8) — this is an
  amendment, not a new record

**Maintainer prerequisite (operator action, not contributor
action).** The first publish requires a Docker Hub Personal
Access Token added to the repo as the `DOCKERHUB_TOKEN` secret.
Token generation: Docker Hub → Account Settings → Security →
New Access Token, scoped Read/Write/Delete on
`fabiocaffarello/spectre-*`. R6.5.3 ships the workflow that
consumes the secret; the secret is added separately by the
maintainer before the first manual dispatch.

#### W1.2 update — tag-triggered publish enabled

> **W1.2 evolution note (2026-05-07).** This subsection is
> added in-place per the precedent set by §3a (R6.3 update),
> §3 R6.5.4 update, and §5 R6.5.3 update above. ADR-0018
> §5's R6.5.3 deferral text ("Tag-triggered (`v*.*.*`) and
> main-branch (`:edge`) auto-publish are deferred ... ~5-line
> additions to `publish.yml`'s `on:` block when the project
> is ready (R7.x or v1alpha2). The R6.5.3 design doesn't
> preempt that future step.") is now resolved for the
> tag-triggered half. The `:edge` half remains deferred to
> v1beta1.

**What W1.2 lands** (Wave 1, production hardening foundation
per `docs/roadmap.md` §4.1):

- `.github/workflows/publish.yml` gains an `on.push.tags`
  trigger matching `v*.*.*`. Pushing a semver-prefixed tag
  triggers a complete publish — same outputs as a
  maintainer-dispatched run with default inputs.
- The `Resolve image tag` step branches on
  `github.event_name` to handle three trigger paths
  (tag push / workflow_dispatch with input / workflow_dispatch
  without input). For tag pushes, the step strips the
  leading `v` from `github.ref_name` and produces the image
  tag.
- A **tag-vs-VERSION consistency check** fails the workflow
  fast when `github.ref_name` (minus `v` prefix) does not
  match the committed `VERSION` file content. This prevents
  publishing images tagged with one value while the source
  represents another — operator error surfaces immediately
  rather than as silent inconsistency.
- The `multi_arch` + `targets` env vars use bash
  `${VAR:-default}` substitution to apply the
  workflow_dispatch input defaults (`true` + `"default"`)
  on tag-triggered runs where inputs are absent. Tag-triggered
  publishes behave identically to a maintainer-dispatched
  publish with no overrides.

**What stays unchanged from R6.5.3:**

- The `Verify pushed manifests` step asserting per-image
  multi-arch posture.
- Manifest list output (`linux/amd64 + linux/arm64` for
  `control-plane` and `playwright`; `linux/amd64`-only for
  `engine`, `seleniumbase`, `curl-impersonate` per the §5
  R6.5.3 update deferral table).
- The `DOCKERHUB_TOKEN` + `DOCKERHUB_USERNAME` environment
  resolution from the `ci` GitHub Actions environment.
- The `publish-dry-run` job in `.github/workflows/ci.yml`
  (still validates the multi-arch build path on every PR
  without pushing).
- Manifest verification semantics (a manifest-list mismatch
  fails the workflow per the partial-success caveat in
  `docs/architecture/releases.md`).
- The `:edge` (main-branch) auto-publish deferral (v1beta1
  territory).

**Why this is safe at v1alpha2 maturity** — three observations:

- The repo already has one tag (`v0.1.0-alpha.0`) and a
  proven publish flow from R7.1's first real Docker Hub
  publish. The R6.5.3 framing's "no tags exist yet" hesitation
  is no longer applicable.
- The tag-vs-VERSION consistency check makes operator error
  loud: a mistaken tag push fails fast at job setup time, not
  silently after a complete multi-arch build cycle.
- The `Verify pushed manifests` step at the end of the
  workflow continues to assert per-image manifest list
  posture, catching any silent regression in the multi-arch
  story regardless of trigger.

W1.2 is **single architectural decision** scope per
CONTRIBUTING.md "v1alpha2 process rigor matrix" (R9.0).
Single commit; no master phase prompt; no new ADR (this
in-place §5 amendment + the publish.yml change + the
releases.md operator-facing update + the CHANGELOG entry).
The other Wave 1 PRs (W1.3 Trivy scanning; W1.4 cosign
signing) follow per the roadmap §4.1 sequence.

#### W2.1 update — engine multi-arch unblock

> **W2.1 evolution note (2026-05-08).** This subsection is
> added in-place per the R6.3 / R6.5.3 / R6.5.4 / W1.2
> precedent above. The R6.5.3 update's deferral row for the
> engine ("`MUSL_TARGET` hardcoded to `x86_64-unknown-linux-musl`
> ... ~30-line Dockerfile change in a focused PR") is now
> resolved.

**What W2.1 lands** (Wave 2, multi-arch unblocks per
`docs/roadmap.md` §4.2):

- `engines/engine/Dockerfile` builder stage rewrites
  toolchain selection per `${TARGETARCH}`. Both target
  triples (`x86_64-unknown-linux-musl`,
  `aarch64-unknown-linux-musl`) are supported via a
  pre-built musl cross-compiler from `https://musl.cc/`
  (see "Why musl.cc" below). The `MUSL_TARGET` value is
  derived inline per build (`/musl-target.env` written once
  and sourced in subsequent RUNs) rather than via a
  Dockerfile-level ARG, which buildx cannot supply
  per-platform on a single bake invocation.
- The builder stage uses
  `FROM --platform=$BUILDPLATFORM rust:${RUST_VERSION}-bookworm`
  so it runs natively on the host arch and cross-compiles
  to `${TARGETPLATFORM}`. Avoids QEMU emulation for the
  full Rust + CMake + protoc toolchain (~10x slowdown
  observed on amd64 runners producing arm64 artifacts).
- Cargo and the `cc` crate are pointed at the cross-
  compilers via `CARGO_TARGET_<TRIPLE>_LINKER` and
  `CC_<TRIPLE>` / `CXX_<TRIPLE>` env vars in the builder.
  Without these, cargo's link step invokes plain `cc` (the
  host's default C compiler) and fails with
  `unrecognized command-line option '-m64'` on cross-arch
  builds.
- librdkafka's required `<curl/curl.h>` include is provided
  in the cross-toolchain's sysroot (`/opt/${MUSL_CROSS}/${MUSL_TRIPLE}/include/curl/`),
  copied from Debian's host-arch multiarch path
  (`/usr/include/${HOST_TRIPLE}/curl/`). Symbols are dropped
  by the preprocessor (`-DWITH_CURL=0`); the file just needs
  to exist on the search path.
- Strip uses the cross-toolchain's `${MUSL_TRIPLE}-strip`
  binary so an arm64 host can strip an x86_64 ELF (host
  `strip` would error with "Unable to recognise the format
  of the input file").
- `.github/workflows/publish.yml` adds `engine.platform=linux/amd64`
  + `engine.platform=linux/arm64` to the multi-arch
  `platform_overrides` array alongside control-plane and
  playwright. Tag-triggered publishes now produce a
  manifest list for engine.

**Why a pre-built musl-cross toolchain and not `musl-tools` from
Debian.** Debian's `musl-tools` package is host-arch-bound — apt
installs the `${HOST_ARCH}-linux-musl-gcc` binary, not a
cross-compiler. On an amd64 host that's `x86_64-linux-musl-gcc`
only; on an arm64 host it's `aarch64-linux-musl-gcc` only.
Building musl-cross-make from source for both targets adds 20+
minutes to every build cycle. The standard industry path for
Rust musl cross-compile is the pre-built tarballs at
`https://musl.cc/`, maintained by the musl-libc community as a
convenience layer over musl-cross-make.

**Why we mirror musl.cc in this repo's GitHub Releases.** The
W2.1 PR's first CI run (2026-05-08) hit
`curl: (28) Failed to connect to musl.cc port 443 after 134478
ms` from GitHub Actions runners — `musl.cc` is community-
maintained and intermittently unreachable from CI IP ranges.
The remediation predicted in the original W2.1 plan ("vendor
the tarballs into our own mirror") was applied immediately:
the two tarballs (`x86_64-linux-musl-cross.tgz`,
`aarch64-linux-musl-cross.tgz`) are now hosted in this repo's
`musl-cross-toolchains-v1` GitHub Release, with SHA256s pinned
in the Dockerfile so a tampered or replaced artifact fails the
build. The Dockerfile fetches from `https://github.com/<owner>/spectre/releases/download/musl-cross-toolchains-v<N>/`
instead of `https://musl.cc/`. Trade preserved: trust
`github.com/<owner>/spectre` HTTPS the same way we trust
`github.com/<owner>` for source tarballs in any other repo.
Bumping the toolchain is a manual operator step (download
fresh from musl.cc → upload to a new release tag → bump the
URL prefix and SHA256 lines in the Dockerfile in lockstep) but
toolchains are static so bumps are rare.

**What stays unchanged from R6.5.3 + W1.2:**

- The five-image set, the registry namespace
  (`docker.io/fabiocaffarello/spectre-*`), and the bake
  matrix structure.
- `docker-bake.hcl` continues to default targets to
  `platforms = ["linux/amd64"]`; multi-arch is a publish-
  time `--set` override (per-target, two `--set` per
  platform per the documented bake idiom).
- The chef stage, cargo-chef caching pattern, distroless
  runtime, and the `nonroot` USER directive — all preserved
  byte-for-byte from R6.5.3 plus the strict path-relative
  COPY contract that lets the runtime stage be MUSL_TARGET-
  agnostic.
- The `Verify pushed manifests` step asserting per-image
  multi-arch posture — automatically picks up the new
  engine multi-arch row without code change (loop over the
  five-image list).
- The cosign signing step from W1.4 — signs the engine
  image's manifest-list digest the same way control-plane
  and playwright are signed; `--recursive` extends the
  signature to the per-platform manifests.

**What still defers** (Wave 2 continues):

- W2.2 — seleniumbase multi-arch via Chrome → Chromium swap
  (per the R6.5.3 update's deferral row option (b)).
  ADR-0018 amendment for the runtime swap will land
  alongside the build PR.
- W2.3 — curl-impersonate multi-arch via build-from-source
  per the upstream INSTALL.md ARM64 instructions (R6.5.3
  update's deferral row option (c)).

After W2.1+W2.2+W2.3 close, all five published images ship
`linux/amd64 + linux/arm64` manifest lists; the
"Multi-arch status" subsection above moves all five rows to
✅ today.

W2.1 is **single architectural decision** scope per
CONTRIBUTING.md "v1alpha2 process rigor matrix" (R9.0).
Single commit; no master phase prompt; no new ADR (this
in-place §5 amendment + the engine Dockerfile change + the
publish.yml platform_overrides change + the releases.md /
container-images.md updates + the CHANGELOG entry).

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
