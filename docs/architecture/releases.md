# Releases

> Operator-facing reference for publishing Spectre's container
> images to Docker Hub. R6.5.3 made this flow operational; this
> document is what to read when you (the maintainer) want to
> ship a release.

## Overview

Spectre publishes its five container images to Docker Hub under
the `fabiocaffarello` account, flat namespace. As of R6.5.3:

- The publish flow has **two triggers** (W1.2 update,
  2026-05-07): pushing a `v*.*.*` tag triggers an automatic
  publish, and `workflow_dispatch` remains for exotic flows
  (overriding tag, partial target sets, multi-arch off). A
  tag-vs-VERSION consistency check fails fast on operator
  error.
- Two of five images publish multi-arch (`linux/amd64` +
  `linux/arm64`); three are amd64-only with documented unblock
  criteria.
- `:edge` rolling tag, image signing (cosign), and SBOM
  generation remain **deferred** — W1.4 (cosign keyless via
  GitHub OIDC) lands in Wave 1; `:edge` and SBOM defer to
  v1beta1.

The architectural rationale lives in
[ADR-0018 §5 R6.5.3 update](../adr/0018-devcontainer-and-engine-image.md);
this page is the runbook.

## Image registry

**Account.** Personal Docker Hub account `fabiocaffarello`. Flat
namespace — Docker Hub does not support nested paths under
personal accounts.

**Image references.** Five images, one per service:

| Image | Reference |
|-------|-----------|
| Engine | `fabiocaffarello/spectre-engine` |
| Control plane | `fabiocaffarello/spectre-control-plane` |
| Playwright adapter | `fabiocaffarello/spectre-playwright` |
| SeleniumBase adapter | `fabiocaffarello/spectre-seleniumbase` |
| curl-impersonate adapter | `fabiocaffarello/spectre-curl-impersonate` |

The bare `fabiocaffarello/...` form is the canonical Docker Hub
reference shape; Docker resolves it to
`docker.io/fabiocaffarello/...` automatically.

**Tag conventions:**

| Tag | Source | Purpose |
|-----|--------|---------|
| `:dev` | local `just images` | Contributor builds |
| `:ci` | CI matrix `images` job | CI verify-only artefacts |
| `:ci-dry-run` | CI `publish-dry-run` job | Multi-arch path validation (not pushed) |
| `:<version>` | `publish.yml` workflow_dispatch | Released artefact (e.g. `:0.1.0-alpha.0`) |
| `:edge` | (deferred) | Rolling main-branch publish — post-refactor |
| `:latest` | (deferred) | Stable release alias — post-refactor |

The `VERSION` file at the repo root is the default release tag.
At R6.5.3 merge: `VERSION = 0.1.0-alpha.0`.

## Multi-arch status

R6.5.3 ships multi-arch where it is achievable today. The three
deferrals carry explicit unblock criteria.

| Image | Multi-arch | Unblock criteria |
|-------|------------|------------------|
| `spectre-control-plane` | ✅ amd64 + arm64 | None — already multi-arch. |
| `spectre-playwright` | ✅ amd64 + arm64 | None — already multi-arch. |
| `spectre-engine` | ❌ amd64 only | Cross-compile to `aarch64-unknown-linux-musl`. ~30-line Dockerfile change: select `MUSL_TARGET` per `${TARGETARCH}`, install `gcc-aarch64-linux-gnu` + `g++-aarch64-linux-gnu`, mirror libcurl include-path step per arch. v1alpha2. |
| `spectre-seleniumbase` | ❌ amd64 only | Google Chrome stable for Linux is amd64-only as of R6.5.3. Two paths: (a) wait for Google to publish a Linux/arm64 stable channel, (b) switch the adapter to Chromium (multi-arch — but changes the project's tested driver surface, ADR-level decision). v1alpha2. |
| `spectre-curl-impersonate` | ❌ amd64 only | Runtime base `lwthiker/curl-impersonate:0.6-chrome` is amd64-only on Docker Hub. Three paths: (a) upstream multi-arch publish, (b) fork upstream's image build, (c) cross-compile from source per their [`INSTALL.md`](https://github.com/lwthiker/curl-impersonate/blob/main/INSTALL.md). v1alpha2. |

The forward-readiness changes in R6.5.3's deferred Dockerfiles
(`ARG TARGETPLATFORM` / `ARG TARGETARCH` declarations, plus
`R7.x: multi-arch unblock` comment blocks) narrow each unblock
to its specific blocker. Authoritative record in ADR-0018 §5
R6.5.3 update.

## Publish flow

The publish workflow is
[`.github/workflows/publish.yml`](../../.github/workflows/publish.yml).
Two triggers (W1.2 update, 2026-05-07):

- **`push` to a `v*.*.*` tag** — triggers an automatic publish.
  The workflow's `Resolve image tag` step strips the leading `v`
  from the tag name and asserts the result matches the
  committed `VERSION` file content (mismatch fails fast). The
  `multi_arch` and `targets` env vars use bash `${VAR:-default}`
  substitution to apply `true` + `default` as the
  workflow_dispatch input defaults — tag-triggered publishes
  behave identically to a manually dispatched publish with no
  overrides.
- **`workflow_dispatch`** — remains for exotic flows
  (overriding tag, partial target sets, multi-arch off). The
  three inputs below remain available; the same multi-arch
  + manifest-verification semantics apply.

Per [ADR-0018 §5 W1.2 update](../adr/0018-devcontainer-and-engine-image.md).

**Three inputs:**

| Input | Default | Purpose |
|-------|---------|---------|
| `tag` | (empty — reads VERSION file) | Image tag to publish. Override for one-off pre-release builds (e.g. `rc1`). |
| `targets` | `default` (all five) | Bake targets to publish. Override for partial publishes. |
| `multi_arch` | `true` | Build linux/amd64,linux/arm64 for control-plane + playwright. |

**The flow:**

1. Checkout the repo.
2. Register QEMU for binfmt_misc (cross-arch RUN-time emulation).
3. Set up `docker buildx` and `just`.
4. Resolve the image tag (input `tag` if set, else `cat VERSION`).
5. Log in to Docker Hub via the `DOCKERHUB_TOKEN` secret.
6. Source `build/docker/versions.env` and call
   `docker buildx bake --push` with the resolved tag,
   `REGISTRY=fabiocaffarello`, and per-target platform
   overrides for the two multi-arch-ready images.
7. Verify pushed manifests via `docker buildx imagetools
   inspect` for each image.

The bake invocation is **byte-for-byte aligned** with `just
images`, except for `--push` (the workflow pushes to Docker
Hub) and the platform overrides (the workflow opts the two
ready images into multi-arch). The toolchain pins flow from
`build/docker/versions.env` in both flows — same versions for
contributors and consumers.

## What's deferred

Each is non-blocking for v1alpha1 release engineering:

- **`:edge` rolling tag from main.** Main-branch push trigger
  for an always-current rolling artefact. Same `on.push.<filter>`
  shape as the W1.2-shipped tag trigger; deferred to v1beta1.
- **`:latest` from stable releases.** Adds a tag-aliasing step
  to the publish flow.
- **Image signing (`cosign`).** Adds a `cosign sign` step after
  the `--push`. Post-refactor.
- **SBOM generation (`syft`).** Adds a syft scan + `--attest`
  flag. Post-refactor.
- **Registry-side cache (`--cache-to type=registry`).** Build
  performance optimisation for repeated multi-arch publishes.
  Post-refactor.
- **Multi-arch builds for engine, seleniumbase,
  curl-impersonate.** v1alpha2 per the per-image unblock
  criteria above.
- **Native arm64 runners (`ubuntu-24.04-arm`).** Eliminates
  QEMU emulation overhead (~3-5x slowdown for the playwright
  build's `pnpm install` step). v1alpha2 if publish runtime
  becomes painful.
- **Pre-flight check that errors if the tag is already
  published.** R6.5.3 echoes the resolved tag in the run log
  so the maintainer can verify before push; v1alpha2 may add
  an explicit pre-check.

## Operator runbook

### First publish setup (one-time)

The publish workflow consumes a Docker Hub Personal Access
Token from the `DOCKERHUB_TOKEN` repo secret. Adding the
secret is operator action — done once, before the first
manual dispatch:

1. Generate a Personal Access Token: Docker Hub → Account
   Settings → Security → New Access Token. Scope: **Read,
   Write, Delete** for the `fabiocaffarello/spectre-*`
   repositories. Name the token something like
   `github-actions-publish`.
2. Add the secret to the GitHub repo: Settings → Secrets and
   variables → Actions → New repository secret. Name:
   `DOCKERHUB_TOKEN`. Value: the token from step 1.
3. (Optional) If the maintainer ever changes Docker Hub
   account, override the username via the repository variable
   `DOCKERHUB_USERNAME`. The default in `publish.yml` is
   `fabiocaffarello`.

### Trigger a publish

The maintainer's typical flow:

1. Bump `VERSION` if the publish corresponds to a version
   change (`0.1.0-alpha.0` → `0.1.0-alpha.1`). PR + merge.
2. GitHub → Actions → "Publish images to Docker Hub" → Run
   workflow.
3. Leave inputs at defaults (the workflow reads VERSION on
   the freshly-merged main; multi-arch is on by default; all
   five targets are published) and click Run.
4. For one-off pre-release builds, override `tag` (e.g. `rc1`)
   without bumping VERSION.

The workflow takes ~15-30 minutes (multi-arch playwright is
the long pole — `pnpm install` under QEMU emulation).

### Verify a publish

The workflow's final step calls `docker buildx imagetools
inspect` for each image. The run log surfaces the manifest
list per image:

- `control-plane` and `playwright` should report two entries
  (linux/amd64 + linux/arm64).
- `engine`, `seleniumbase`, and `curl-impersonate` should
  report one entry (linux/amd64).

The workflow exits non-zero if any inspection fails. **Caveat:
the `--push` happens before the inspection** — a partial
success is possible (images pushed, manifest verification
fails). R6.5.3 documents this as a known limitation;
v1alpha2 may add pre-push validation. In practice the
manifest-inspection check has been reliable in upstream
testing of the underlying actions.

To verify a published image manually:

```bash
# Multi-arch (control-plane or playwright)
docker buildx imagetools inspect \
  fabiocaffarello/spectre-control-plane:0.1.0-alpha.0
# Expect: Manifests: linux/amd64 + linux/arm64

# Single-arch (engine, seleniumbase, curl-impersonate)
docker buildx imagetools inspect \
  fabiocaffarello/spectre-engine:0.1.0-alpha.0
# Expect: Manifests: linux/amd64

# Pull and run an arm64 manifest on amd64 (Docker Desktop
# emulation) for a quick smoke
docker run --rm --platform linux/arm64 \
  fabiocaffarello/spectre-control-plane:0.1.0-alpha.0 \
  --help
```

## CI dry-run

Every push that touches a Dockerfile, `docker-bake.hcl`,
`build/docker/**`, the publish workflow, or the CI workflow
runs the `publish-dry-run` job in `.github/workflows/ci.yml`.
The dry-run builds all five images at the publish workflow's
multi-arch shape but **without** authenticating to Docker Hub
or pushing.

The dry-run validates: bake variables resolve correctly with
`REGISTRY=fabiocaffarello`; multi-arch builds succeed for the
two ready images; the three single-arch images build
unchanged. Build artefacts live in BuildKit's local cache and
are discarded with the runner.

## R7.1 — Helm chart as a release artifact

R7.1 added the Helm chart at `build/helm/spectre/` (ADR-0030)
as a release artifact alongside the published images. The
chart's `appVersion` tracks the repository's `VERSION` file,
so a release cadence bumps both in the same commit. The chart's
default image references resolve to
`docker.io/fabiocaffarello/spectre-<name>:<chart appVersion>`,
making the publish flow described above the prerequisite for
any chart consumer running the defaults.

R7.1 included the **first real publish** (`0.1.0-alpha.0`) of
all five images, exercising the publish workflow end-to-end and
confirming the manifest list shape recorded in the Multi-arch
status table above.

The chart's structural CI gate (`helm-lint` job) runs on every
PR that touches `build/helm/**` or the operator's CRD source.
R7.2 added an end-to-end production-smoke CI gate that installs
the chart into a kind cluster and asserts row events arrive at
the three sinks (kafka, s3, webhook); see
[production-smoke.md](production-smoke.md). See
[helm-chart.md](helm-chart.md) for the chart-level details.

OCI-registry chart publish
(`oci://docker.io/fabiocaffarello/charts/spectre`) is deferred
post-refactor; consumers install from a cloned repo.

## Forward references

- **R6.5.4** deduplicates Dockerfile codegen via a shared base
  image stage. Bake's structure is unchanged; the publish flow
  in this document is unaffected.
- **Post-refactor** wires auto-trigger (tag-triggered +
  `:edge`), signing (cosign), and SBOM (syft) once the project
  has a versioned release cadence. (R7.x closed without picking
  these up — its territory was Helm chart packaging + production
  smoke.)
- **v1alpha2** unblocks the three multi-arch deferrals per the
  per-image criteria in the Multi-arch status table.

## v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe the
> R7.1-landed release shape — manual `git tag` + GitHub
> Actions publish workflow; first real publish at
> `0.1.0-alpha.0` for the five v1alpha1 service images.
> Phase R9 commits to expanding the release surface across
> the v1alpha2 catalog; this subsection forwards readers to
> the v1alpha2 release plan.*

The five existing v1alpha1 service images (engine + 3
adapters + control-plane) continue releasing per the §
"Release process" model — manual `git tag` triggers the
publish workflow. v1alpha2 adds:

- **9 new service images** as catalog services materialise
  across Waves 5 – 10 — each ships under
  `docker.io/fabiocaffarello/spectre-<slot>:<tag>`
  following the canonical service shape per
  [ADR-0036 §5](../adr/0036-microservices-catalog-expansion.md);
  the 14th lands when the driver-router decision resolves
  per [ADR-0035 §6](../adr/0035-dsl-evolution-driver-abstraction.md).
- **Wave 1 production hardening** (per
  [`docs/roadmap.md`](../roadmap.md) §4.1) lands four
  release-side improvements:
  - ✅ Auto-trigger publish on tag push (W1.2 shipped
    2026-05-07 per ADR-0018 §5 W1.2 update)
  - ✅ CRD upgrade procedure documentation (W1.5 shipped
    2026-05-07 per ADR-0030 §8.4 – §8.9)
  - Trivy vulnerability scanning (W1.3, every image scans
    before publish)
  - cosign keyless signing via GitHub OIDC (W1.4, every
    published image signed)

The version-coherence script + `Chart.lock` + appVersion
tracking continue unchanged. The v1alpha2 release cadence
post-W1.2 is **maintainer-triggered tags** that auto-publish
(`git tag v<x.y.z>` → `git push --tags` → workflow runs).
Auto-trigger from main (`:edge` rolling tag) remains deferred
to v1beta1.

The `:edge` floating tag and SBOM (syft) generation remain
deferred per the existing "Post-refactor" deferral; v1beta1
revisits.

For the full Wave 1 – 12 release plan see
[`docs/roadmap.md`](../roadmap.md) §4.
