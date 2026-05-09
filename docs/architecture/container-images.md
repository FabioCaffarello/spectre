# Container images

> Reference for the five Spectre container images, the orchestration
> that builds them, and the development workflow around them. R6.1
> introduced the per-service Dockerfile set; R6.2 wires them into
> the Compose stack; R6.3 revisits the Devcontainer; R6.5.3 ships
> Docker Hub publishing (`fabiocaffarello/spectre-<name>`) with
> multi-arch for control-plane + playwright, three deferrals
> documented. Wave 1 added Trivy scanning (W1.3),
> tag-triggered auto-publish (W1.2), and cosign keyless signing
> (W1.4); SBOMs and the `:edge` rolling tag remain deferred to
> v1beta1.

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
| `spectre-seleniumbase` | `python:3.12-slim-bookworm` (uv) | `python:3.12-slim-bookworm` + Chromium + chromium-driver | 1000 (`seluser`) | ~450 MB |

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

### `spectre-engine` (`engines/engine/Dockerfile`)

Cargo-chef pattern caches dependency compilation across source-only
edits. Builds for the `x86_64-unknown-linux-musl` target so the
result is a fully static binary the distroless runtime runs without
shared libraries. R6.1 lifted the `RUST_VERSION` pin from 1.85 to
1.88 because aws-sdk-sts 1.94 (a transitive dep added by R5.1's
aws-sdk-s3) requires rustc 1.88.

Runtime is `gcr.io/distroless/static:nonroot` — no shell, no
package manager. ENTRYPOINT is `/usr/local/bin/spectre`; the binary
is the gRPC service entry point per R2.3.

### `spectre-control-plane` (`operators/control-plane/Dockerfile`)

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

Runtime is `python:3.12-slim-bookworm` + Debian's `chromium` +
`chromium-driver` (W2.2, 2026-05-08 — Chrome → Chromium swap
unblocked multi-arch; ADR-0014 §6 amendment + ADR-0018 §5 W2.2
update). Both apt packages ship for amd64 + arm64 in
bookworm-main and are released as a single Debian source package
so the chromedriver binary is version-locked to the chromium
binary — eliminates the runtime `seleniumbase install chromedriver`
step that the prior Google Chrome flow required.
`SPECTRE_SELENIUMBASE_CONTAINER=1` is baked as a default ENV so
the adapter's container-aware browser flag path
(`--no-sandbox` / `--disable-dev-shm-usage`, plus
`binary_location=/usr/bin/chromium`) activates without Compose /
Helm having to remember.

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
- `REGISTRY` (default empty = local-only) — registry prefix.
  R6.5.3's publish workflow sets this to `fabiocaffarello`
  (Docker Hub flat namespace) for push, producing
  `fabiocaffarello/spectre-<name>:<tag>`. The variable is
  registry-agnostic — any non-empty value prefixes the image
  reference.
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
`docker build -f engines/engine/Dockerfile .` directly (without bake)
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

The smoke recipes accept a `TAG='dev'` positional parameter (R6.5.2)
so CI's matrix images job — which builds with `TAG=ci` to keep CI
artefacts distinct from local `:dev` images — can reuse the same
recipe bodies without re-tagging.

## CI shape

R6.5.2 routes every CI image build through the same `docker buildx
bake` invocation that `just images` runs locally. CI and local
share one orchestrator byte-for-byte; toolchain pins flow from
`build/docker/versions.env` in both flows.

### Matrix `images` job

A single matrix job replaces the two ad-hoc `engine-image` and
`operator-image` jobs that pre-R6.5.2 CI shipped. Each matrix entry
maps a bake target to its smoke recipe:

| Matrix entry | Bake target | Smoke recipe |
|---|---|---|
| `engine` | `engine` | `engine-image-run` |
| `control-plane` | `control-plane` | `op-image-smoke` |
| `curl-impersonate` | `curl-impersonate` | `curl-imp-image-smoke` |
| `playwright` | `playwright` | `pw-image-smoke` |
| `seleniumbase` | `seleniumbase` | `sb-image-smoke` |

Per-entry `if: matrix.changed == 'true'` reads the corresponding
`changes.outputs.image_<name>` filter; unchanged targets list in
the run UI but skip their build / smoke / size steps. The build
step is the canonical `set -a; source build/docker/versions.env;
set +a; docker buildx bake --load <target>` — identical to `just
images`. CI additionally sets `VCS_REF` (commit SHA), `BUILD_DATE`
(repository updated_at), and `TAG=ci` so the OCI labels are
populated and the resulting image is tagged distinctly from local
artefacts.

### Full-stack gate

The `full-stack` job exercises the post-R6.3 unified Compose flow
end-to-end on every relevant change. Eight steps:

1. Bake builds the five images at `TAG=dev` so
   `docker-compose.yml`'s `image: spectre-<name>:dev` references
   resolve without re-tagging.
2. `docker compose --profile full up -d` brings up the eleven
   services on the runner's Docker daemon.
3. `helm/kind-action@v1` creates a kind cluster
   (`cluster_name: spectre-ci`) using `build/kind/cluster.yaml`.
4. `kind get kubeconfig --internal` regenerates
   `build/kind/kubeconfig` so the operator container — which joins
   the `kind` Docker network — can dial
   `spectre-ci-control-plane:6443`.
5. `make install` (kubebuilder) applies the v1alpha2 CRD.
6. `kubectl apply -f
   operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_hello-hackernews.yaml`
   submits the canonical sample.
7. A 5-minute polling loop asserts
   `kubectl get scrapejob hello-hackernews -o
   jsonpath={.status.phase}` reads `Completed`.
8. Always-run debug steps tail operator + engine + adapter logs;
   `compose down -v` cleans up.

The hello-hackernews sample exercises Playwright (the most common
driver), the engine, the control plane, Postgres (status writes),
and the gRPC dial chain — `Completed` is the strongest possible
signal that the unified flow is intact.

### `changes` filter map

The `changes` job's `dorny/paths-filter` config emits per-target
outputs that the matrix job consumes, plus a single `full_stack`
output that gates the heavy gate:

| Output | Triggers |
|---|---|
| `image_engine` | `engines/engine/**`, `engines/engine/.dockerignore`, `proto/**`, `docker-bake.hcl`, `build/docker/**`, the workflow |
| `image_control_plane` | `operators/control-plane/**`, …`/.dockerignore`, `proto/**`, `docker-bake.hcl`, `build/docker/**`, the workflow |
| `image_curl_impersonate` | `adapters/curl-impersonate/**`, …`/.dockerignore`, `proto/**`, `docker-bake.hcl`, `build/docker/**`, the workflow |
| `image_playwright` | `adapters/playwright/**`, …`/.dockerignore`, `proto/**`, `docker-bake.hcl`, `build/docker/**`, the workflow |
| `image_seleniumbase` | `adapters/seleniumbase/**`, …`/.dockerignore`, `proto/**`, `docker-bake.hcl`, `build/docker/**`, the workflow |
| `full_stack` | `core/**`, `adapters/**`, `proto/**`, `docker-compose.yml`, `docker-bake.hcl`, `build/docker/**`, `build/kind/**`, sample CRs / CRDs, the workflow |

Selectivity is preserved: a change in `engines/engine/src/lib.rs`
rebuilds only the engine image; a change in
`build/docker/versions.env` rebuilds every image (toolchain pins
are shared); a change in `proto/**` rebuilds every image **and**
fires the full-stack gate.

### When each job runs

| Job | Fires when | Cost |
|---|---|---|
| `proto`, `rust`, `go`, `typescript`, `python`, `operator` | Per-language source changes (existing) | Cheap |
| `engine-integration` | engine or playwright source | Medium (~3–5 min) |
| `images` matrix | Any image filter matches | Per-entry ~2–4 min, parallel |
| `full-stack` | `full_stack` filter matches | Sequential after `images`; ~5–8 min |

For a typical PR (one language change), most matrix entries skip
their build steps and the full-stack gate may not fire; runtime
stays close to the pre-R6.5.2 baseline. The heaviest case (a
proto-schema change) rebuilds all five images and runs the gate.

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
- **R6.5.3** added Docker Hub publishing
  (`fabiocaffarello/spectre-<name>`) via
  [`.github/workflows/publish.yml`](../../.github/workflows/publish.yml)
  (`workflow_dispatch` only). Multi-arch ships for control-plane
  + playwright + engine (W2.1) + seleniumbase (W2.2);
  curl-impersonate remains amd64-only with documented unblock
  criteria — see ADR-0018 §5 R6.5.3 / W2.1 / W2.2 updates and
  the Multi-arch status subsection above. Operator-facing
  reference: [`docs/architecture/releases.md`](releases.md).
- **v1alpha2 Wave 1** added the production-hardening pieces
  R7.x left out: tag-triggered publish auto-trigger
  (W1.2 shipped 2026-05-07, `v*.*.*` tag push triggers the
  publish workflow), Trivy image vulnerability scanning
  (W1.3 shipped 2026-05-07, every image-affecting PR fails on
  HIGH/CRITICAL findings), and cosign keyless signing via
  GitHub OIDC (W1.4 shipped 2026-05-07, every published image
  is signed and verifiable per the recipe in
  [`releases.md`](releases.md) "Image signing"). SBOMs (`syft`)
  and the `:edge` rolling tag remain v1beta1 territory.

## Multi-arch status

R6.5.3 ships multi-arch where it is achievable today; the three
deferrals carry explicit unblock criteria so v1alpha2
contributors face a small surface per image, not wholesale
rework.

| Image | Multi-arch ready? | Blocker / unblock |
|-------|-------------------|-------------------|
| `spectre-control-plane` | ✅ today | None — pure Go cross-compile (`CGO_ENABLED=0` + `GOARCH=${TARGETARCH}`). |
| `spectre-playwright` | ✅ today | None — Microsoft Playwright runtime image is multi-arch; Node `pnpm install` runs under QEMU emulation on amd64 runners. |
| `spectre-engine` | ✅ today | Multi-arch from W2.1 (2026-05-08) — Rust musl cross-compile via pre-built `aarch64-linux-musl-cross` toolchain from `musl.cc`; builder runs natively on `$BUILDPLATFORM` and cross-compiles to `$TARGETPLATFORM`. See ADR-0018 §5 W2.1 update. |
| `spectre-seleniumbase` | ✅ today | Multi-arch from W2.2 (2026-05-08) — Chrome → Chromium runtime swap (Debian's `chromium` + `chromium-driver` ship for both arches in bookworm-main). See ADR-0018 §5 W2.2 update + ADR-0014 §6 amendment. |
| `spectre-curl-impersonate` | ❌ deferred | Runtime base `lwthiker/curl-impersonate:0.6-chrome` is published amd64-only on Docker Hub. Unblock paths: (a) upstream multi-arch publish; (b) fork upstream's image build; (c) cross-compile from source per [`INSTALL.md`](https://github.com/lwthiker/curl-impersonate/blob/main/INSTALL.md). |

The forward-readiness changes in R6.5.3 (`ARG TARGETPLATFORM` /
`ARG TARGETARCH` declarations in the deferred Dockerfiles, plus
`R7.x: multi-arch unblock` comment blocks above each blocker)
narrow future work to the specific blocker per image. ADR-0018
§5 R6.5.3 update is the authoritative record.

## Things consciously *not* taken in R6.1

- **Multi-arch** (linux/arm64). QEMU-emulated arm64 in CI
  roughly doubles build time and adds flakiness; native arm64
  runners need explicit job matrix. ~~R7.1.~~ R6.5.3 ships
  multi-arch for control-plane + playwright; the other three
  are deferred — see "Multi-arch status" above.
- **Registry publishing.** ~~R7.1.~~ R6.5.3 (Docker Hub) — see
  [`releases.md`](releases.md).
- **Image signing.** ~~Post-refactor.~~ W1.4 shipped
  2026-05-07 — cosign keyless via GitHub OIDC, integrated as a
  post-bake step in `publish.yml` per ADR-0036 §5.8 W1.4
  update.
- **SBOMs.** Post-refactor (v1beta1).
- **`HEALTHCHECK` instructions in Dockerfiles.** Healthchecks live
  in Compose / Helm because the bound port is deployment config,
  not image config; `gcr.io/distroless/static` doesn't even have
  `wget` / `curl` to run a `HEALTHCHECK` command. R6.2 wires
  `grpc_health_probe` into Compose `healthcheck:` stanzas.
- **`grpc_health_probe` in runtime stages.** Added in R6.2
  alongside the Compose stanzas that consume it.
- **CI image-build job rewrite.** R6.1 left the existing
  `engine-image` / `op-image-smoke` jobs alone. R6.5.2 swept them
  into a single matrix `images` job that calls bake — see the
  "CI shape" section above. Layer caching across runs is still a
  follow-up if CI runtime becomes painful. R6.5.3 added a
  `publish-dry-run` job that validates the multi-arch publish
  path on every relevant change without pushing to Docker Hub.
- **Image-size optimisation beyond "good enough".** Each image
  could be smaller with aggressive optimisation (Chrome →
  Chromium for SeleniumBase, custom Chromium build for
  Playwright, `FROM scratch` for the static binaries) — none are
  worth the maintenance burden for v1alpha1.

## v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe
> v1alpha1's five published service images
> (engine + 3 adapters + control-plane). Phase R9 commits
> the catalog expansion to 14 v1alpha2 services + 1 v1beta1
> service; this subsection forwards readers to the new
> image surface.*

v1alpha2 expands the published image set from 5 (today) to
**14 + future v1beta1 services** as catalog services
materialise per ADR-0036's wave assignment. Each new
infra-service ships:

- A `Dockerfile` at `infra-services/<slot>/Dockerfile`
  following the canonical service shape per
  [ADR-0036 §5.1](../adr/0036-microservices-catalog-expansion.md)
- A `docker-bake.hcl` target auto-extending the bake matrix
  per [`service-shape.md` §5](service-shape.md)
- A Docker Hub manifest at
  `docker.io/fabiocaffarello/spectre-<slot>:<tag>` per the
  existing R6.5.3 publish workflow
- A multi-arch posture per the per-image criteria — Go
  services default to `linux/amd64 + linux/arm64`; the Rust
  `fingerprint-broker` (slot 3) follows the engine's
  multi-arch unblock when it materialises (per ADR-0018 §5
  R6.5.3 update)

Image scanning (Trivy) and signing (cosign keyless via
GitHub OIDC) land as part of **Wave 1** production
hardening per [`docs/roadmap.md`](../roadmap.md) §4.1.
**W1.3 shipped 2026-05-07** — every PR touching image-affecting
paths runs `.github/workflows/scan.yml` per ADR-0036 §5.8;
HIGH/CRITICAL findings fail the workflow; per-image overrides
live at [`tools/trivy/<target>.trivyignore`](../../tools/trivy/);
unfixed CVEs ignored to keep the gate actionable.
**W1.4 shipped 2026-05-07** — `publish.yml` integrates a
post-bake cosign signing step (per ADR-0036 §5.8 W1.4 update);
every published image is signed by manifest-list digest under
GitHub OIDC keyless attestation; verification recipe lives in
[`releases.md`](releases.md) "Image signing". From Wave 5
onward, every new infra-service image scans + signs
automatically as its bake target lands per the canonical
service shape.

The five existing v1alpha1 images are **unchanged** in shape
or publishing path. The engine image gains the
orchestrator-pattern code paths per ADR-0037 incrementally
across Waves 5 – 10; image identity (name + tag scheme)
preserved.

For the canonical service shape that governs new images see
[`service-shape.md`](service-shape.md) +
[ADR-0036 §5](../adr/0036-microservices-catalog-expansion.md).
