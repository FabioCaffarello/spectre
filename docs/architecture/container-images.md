# Container images

> Reference for the five Spectre container images, the orchestration
> that builds them, and the development workflow around them. R6.1
> introduced the per-service Dockerfile set; R6.2 wires them into
> the Compose stack; R6.3 revisits the Devcontainer; R7.1 adds
> release-engineering (multi-arch, registry publishing, signing).

This page is the operator's reference for "how is this project
containerised". It is a *reference*, not an architectural decision
record — the patterns it describes are conventional industry
practice; the architectural surface (which services exist, how they
connect) was settled in earlier ADRs (ADR-0019, ADR-0020, ADR-0023).

## Overview

Five images, each multi-stage, each shipped from this repo:

| Service | Builder base | Runtime base | UID:GID | Size (R6.1 measured) |
|---|---|---|---|---|
| `spectre-engine` | `rust:1.88-bookworm` (musl target) | `gcr.io/distroless/static:nonroot` | 65532 | 11.4 MB |
| `spectre-control-plane` | `golang:1.25` | `gcr.io/distroless/static:nonroot` | 65532 | 17.6 MB |
| `spectre-curl-impersonate` | `golang:1.25` (codegen + builder) | `lwthiker/curl-impersonate:0.6-chrome` (Alpine) | 65534 (`nobody`) | 20.5 MB |
| `spectre-playwright` | `node:20-bookworm-slim` | `mcr.microsoft.com/playwright:v1.49.0-noble` | 1000 (`pwuser`) | 836 MB |
| `spectre-seleniumbase` | `python:3.12-slim-bookworm` (uv) | `python:3.12-slim-bookworm` + Chrome + ChromeDriver | 1000 (`seluser`) | 308 MB |

> Sizes via `docker image inspect --format '{{.Size}}'`. The
> `docker images` listing reports a larger value because it
> aggregates manifest-list entries (incl. attestations); inspect
> reads the per-platform layer total.

The orchestration entry point is `docker buildx bake` driven by
`docker-bake.hcl` at the repo root. Toolchain pins live in
`build/docker/versions.env` and flow through bake into every image.

## Build context

Every Dockerfile uses the **repository root** as its build context.
This is non-negotiable: `proto/` (gitignored — ADR-0007) is
regenerated *inside each builder stage* via `buf generate` +
`tools/codegen/post-generate.sh`, and that codegen needs `proto/`
visible at the same path the contributor's host workflow uses
(`/workspace/proto/`).

The repo-root `.dockerignore` keeps the context efficient. Its
shape (R6.1 §4.5):

1. **Deny everything by default** (`*`).
2. **Negate-include** the directories any image's stages need:
   `!proto/`, `!core/`, `!adapters/`, `!tools/codegen/`,
   `!build/docker/`.
3. **Re-deny generated trees** within the include-set:
   `proto/gen/`, `**/node_modules/`, `**/target/`, `**/.venv/`,
   `**/__pycache__/`, etc.

A contributor adding a new top-level directory that any image needs
must add a corresponding negation entry — otherwise the build will
fail with a "file not found" inside a `COPY` step.

## Toolchain pinning — `build/docker/versions.env`

Single source of truth. Bumping a line here propagates to every
consumer the next time `just images` runs:

| Variable | Purpose |
|---|---|
| `RUST_VERSION` | Engine builder stage. Tracks the engine's transitive MSRV. |
| `GO_VERSION` | Control-plane + curl-impersonate builder stages. |
| `NODE_VERSION` | Playwright builder stage. |
| `PYTHON_VERSION` | SeleniumBase builder + runtime stages. |
| `PROTOC_VERSION` | Engine builder stage's prost-build. |
| `BUF_VERSION` | Every codegen stage. |
| `UV_VERSION` | SeleniumBase builder stage's venv resolver. |
| `PLAYWRIGHT_VERSION` | Playwright runtime image tag. **Must match** the npm `playwright` dep in `adapters/playwright/package.json` — bumping one without the other causes Chromium / API drift. |
| `CURL_IMPERSONATE_IMAGE` | curl-impersonate runtime base image reference. |
| `CHROME_VERSION` | Reference pin (informational) for the SeleniumBase Chrome install. |

The file is POSIX shell-sourceable (`KEY=VALUE`) so every consumer
(bake, justfile, R6.3 Devcontainer, R7-era CI) can read it without
a parser:

```bash
set -a; source build/docker/versions.env; set +a
docker buildx bake [--load] [<group-or-target>]
```

The `just images` recipe wraps that flow.

## Per-service notes

### `spectre-engine` (`core/engine/Dockerfile`)

Cargo-chef pattern caches dependency compilation across source-only
edits. Builds for the `x86_64-unknown-linux-musl` target so the
result is a fully static binary the distroless runtime runs without
shared libraries. R6.1 lifted the `RUST_VERSION` pin from 1.85 to
1.88 because aws-sdk-sts 1.94 (a transitive dep added by R5.1's
aws-sdk-s3) requires rustc 1.88.

Runtime is `gcr.io/distroless/static:nonroot` — no shell, no
package manager. ENTRYPOINT is `/usr/local/bin/spectre`; the binary
is the gRPC service entry point per R2.3.

### `spectre-control-plane` (`core/control-plane/Dockerfile`)

R3.1 retired the bundled-image execution model — the operator
image now carries only the kubebuilder manager binary on
distroless-static. The builder stage regenerates the Go protocol
bindings via `buf generate` so the operator's `replace` directive
in `go.mod` resolves at link time.

### `spectre-curl-impersonate` (`adapters/curl-impersonate/Dockerfile`)

**Runtime-base deviation from R6.1 §4.3 sketch.** The phase prompt
proposed `gcr.io/distroless/base-debian12:nonroot` with a multi-
stage `COPY --from=` extracting `curl_chrome116` and its shared
library. That sketch is infeasible: the variant binaries shipped
upstream (`curl_chrome116`, `curl_chrome110`, …) are POSIX shell
wrappers (`#!/usr/bin/env ash`) that exec the underlying
`curl-impersonate-chrome` binary with a Chrome-116-specific TLS
cipher list and header set. Distroless ships no shell, so the
wrappers cannot execute.

The minimal viable runtime is the upstream Alpine image itself
(`lwthiker/curl-impersonate:0.6-chrome`, 14 MB compressed),
providing `/bin/ash`, every variant in `/usr/local/bin/`, and
`libcurl-impersonate-chrome.so.4` in `/usr/local/lib/`. We add
`ca-certificates` and copy the adapter binary in. Final image is
~22 MB — well under the 80 MB target. The trade-off (deeper
supply-chain coupling to upstream) is the better deal versus
forking the variant scripts or modifying the adapter to call
`curl-impersonate-chrome` directly. ADR-0016 §1's
subprocess-over-cgo contract holds byte-for-byte.

### `spectre-playwright` (`adapters/playwright/Dockerfile`)

Builder uses `node:20-bookworm-slim` + pnpm (corepack-managed,
version pinned by the adapter's `package.json` `packageManager`
field), regenerates the TS proto bindings via buf, runs `pnpm
install --frozen-lockfile` + `pnpm run build`, and prunes
devDependencies before the runtime stage copies `node_modules`.

Runtime is `mcr.microsoft.com/playwright:v<version>-noble` — the
canonical Playwright runtime image kept current by the Playwright
team with the matching Chromium pre-baked. The version is pinned
in lock-step with the npm `playwright` dep via
`build/docker/versions.env::PLAYWRIGHT_VERSION`. The image already
ships a non-root `pwuser` (UID 1000) — `USER pwuser` works without
a `useradd`.

**Image size (~836 MB) is above the 650 MB target in the R6.1
phase prompt.** The Microsoft image carries Chromium + Firefox +
WebKit and the system deps for all three; we only use Chromium but
stripping the others would mean forking the supply chain (and is
the wrong trade-off for v1alpha1 — the maintenance burden of
matching Chromium / API versions outweighs the size win). The
target is updated to ~900 MB for R6.1.

### `spectre-seleniumbase` (`adapters/seleniumbase/Dockerfile`)

The most invasive of the three new adapters because there's no
Microsoft equivalent for SeleniumBase. The builder stage uses
`python:3.12-slim-bookworm` + uv. Critical detail: uv hard-codes
absolute paths into venv shebangs and `pyvenv.cfg`, so the venv is
built at the **final** runtime path
(`/opt/spectre/adapters/seleniumbase/.venv`); the proto python
bindings are staged at `/opt/spectre/proto/gen/python` so the
adapter's `[tool.uv.sources] path = "../../proto/gen/python"`
resolves identically in builder and runtime.

Runtime is `python:3.12-slim-bookworm` + Google Chrome stable
(from Google's apt repo — the only source for the branded Chrome
that `Driver(browser="chrome")` expects) + matching ChromeDriver
fetched via `seleniumbase install chromedriver` at image build
time. `SPECTRE_SELENIUMBASE_CONTAINER=1` is baked as a default
ENV so the adapter's container-aware Chrome flag path
(`--no-sandbox` / `--disable-dev-shm-usage`) activates without
Compose / Helm having to remember.

## Bake orchestration

`docker-bake.hcl` declares five targets, three groups, ten
variables, and two functions:

```bash
# Build all five
just images
# = set -a; source build/docker/versions.env; set +a; \
#     docker buildx bake --load default

# Build core only (engine + control-plane)
docker buildx bake --load core

# Build the three adapters only
docker buildx bake --load adapters

# Build a single image
docker buildx bake --load engine
```

Variables:

- `TAG` (default `dev`) — image tag suffix.
- `REGISTRY` (default empty = local-only) — registry prefix; R7.1
  sets this to `ghcr.io/<owner>` for push.
- `RUST_VERSION` / `GO_VERSION` / `NODE_VERSION` / `PYTHON_VERSION` /
  `PROTOC_VERSION` / `BUF_VERSION` / `UV_VERSION` /
  `PLAYWRIGHT_VERSION` / `CURL_IMPERSONATE_IMAGE` — toolchain pins.
  Default to the values in `build/docker/versions.env`.
- `VCS_REF` / `BUILD_DATE` — populate the `org.opencontainers.image.{revision,created}`
  labels. CI sets them; local builds leave them empty.

Functions:

- `image(name)` — returns the fully-qualified image reference
  (`spectre-<name>:<tag>` or `<registry>/spectre-<name>:<tag>`).
- `labels(title, description)` — returns the OCI annotation map
  injected uniformly across every target. Keeps Dockerfiles
  label-free.

## OCI label schema

Every image carries the `org.opencontainers.image.*` set:
`title`, `description`, `vendor` (`Spectre`), `licenses`
(`Apache-2.0`), `source`, `revision`, `created`, `version`.
Labels are injected by bake (R6.1 §9.3) so contributors don't
repeat the schema in each Dockerfile. The contributor running
`docker build -f core/engine/Dockerfile .` directly (without bake)
won't get the labels — bake is the canonical entry point from R6.1
onward.

## Smoke testing

Per-image smoke recipes (`just <svc>-image-smoke`) verify the
layered images are self-consistent without Compose / Helm
machinery:

| Image | Smoke shape |
|---|---|
| `engine-image-run` | `docker run --entrypoint=test … -x /usr/local/bin/spectre` — binary exists |
| `op-image-smoke` | `docker run --entrypoint=/manager … --help` — manager runs |
| `curl-imp-image-smoke` | `docker run …` — adapter exits 1 with canonical "SPECTRE_ADAPTER_GRPC_PORT is required" |
| `pw-image-smoke` | `docker run …` — adapter exits 1 with canonical "redis ping failed" |
| `sb-image-smoke` | `docker run …` — adapter exits 1 with canonical "redis ping" / "Connection refused" |

Run all five in sequence with `just images-smoke`. Deeper end-to-end
smoke (start each service, poll gRPC health, fan a job through the
engine + an adapter pair) belongs to R6.2's Compose stack work and
R7.2's production-cluster smoke.

## Forward references

- **R6.2** will add Compose `services:` stanzas for every image and
  add `grpc_health_probe` to each runtime stage so Compose
  `healthcheck:` can exec it. R6.2 will also retire the
  `just <svc>-run` developer-convenience recipes in favour of
  `docker compose up` as the canonical local-dev path.
- **R6.3** will revisit `.devcontainer/` with Docker-in-Docker so
  contributors can run `docker compose up` from inside the
  container; the post-create script will source
  `build/docker/versions.env` to install matching toolchains
  natively in the Devcontainer.
- **R7.1** will add registry publishing (`ghcr.io`), a multi-arch
  matrix (linux/amd64 + linux/arm64), image signing (`cosign`), and
  SBOMs (`syft`). The `TAG` and `REGISTRY` bake variables are the
  hooks; nothing in R6.1 forecloses this.

## Things consciously *not* taken in R6.1

- **Multi-arch** (linux/arm64). QEMU-emulated arm64 in CI
  roughly doubles build time and adds flakiness; native arm64
  runners need explicit job matrix. R7.1.
- **Registry publishing.** R7.1 (release engineering).
- **Image signing + SBOMs.** R7.1.
- **`HEALTHCHECK` instructions in Dockerfiles.** Healthchecks live
  in Compose / Helm because the bound port is deployment config,
  not image config; `gcr.io/distroless/static` doesn't even have
  `wget` / `curl` to run a `HEALTHCHECK` command. R6.2 wires
  `grpc_health_probe` into Compose `healthcheck:` stanzas.
- **`grpc_health_probe` in runtime stages.** Added in R6.2
  alongside the Compose stanzas that consume it.
- **CI image-build job rewrite.** R6.1 leaves the existing
  `engine-image` / `op-image-smoke` jobs alone; R7.1 sweeps them
  up wholesale with layer caching.
- **Image-size optimisation beyond "good enough".** Each image
  could be smaller with aggressive optimisation (Chrome →
  Chromium for SeleniumBase, custom Chromium build for
  Playwright, `FROM scratch` for the static binaries) — none are
  worth the maintenance burden for v1alpha1.
