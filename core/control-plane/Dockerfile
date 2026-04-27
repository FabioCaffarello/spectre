# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Spectre control-plane operator. The
# resulting image bundles four artifacts:
#
# - /manager — the kubebuilder controller manager (Go).
# - /usr/local/bin/spectre — the spectre engine binary copied out
#   of the engine image (Rust, musl-static; ADR-0018 §3).
# - /opt/spectre/adapters/playwright/ — the Playwright adapter
#   (TypeScript, compiled to JS) plus its production node_modules
#   and driver.yaml manifest. The engine resolves
#   `<adapters_path>/<driver>/driver.yaml` per ADR-0019 §3.
# - /opt/spectre/adapters/seleniumbase/ — the SeleniumBase adapter
#   (Python, uv-managed venv) plus driver.yaml. The shared
#   `[tool.uv.sources]` editable for the generated proto bindings
#   resolves at runtime via /opt/spectre/proto/gen/python/.
#
# The runtime base is the official Microsoft Playwright image, which
# ships Ubuntu 24.04, Node 24, Chromium pinned to the same patch
# release as the bundled `playwright` npm package, and every system
# dependency Chromium needs (libnss3, libgtk-3, libdrm, fonts, etc.).
# Building those by hand on top of distroless is ~300 lines of brittle
# apt invocations and a new Chromium-vs-driver skew surface; reusing
# Microsoft's published image keeps the supply chain auditable. See
# PR16 §4.1.
#
# PR17 §4.1 extends — does not replace — that base. SeleniumBase needs
# Google Chrome (not Chromium), Python 3.12, and a matching
# ChromeDriver. The runtime stage apt-installs all three on top of
# the Microsoft base; Chrome's runtime libraries (libnss3, libgtk-3,
# fonts) overlap with Chromium's so the marginal install footprint
# is small. Switching the base image would have rebuilt the whole
# Playwright supply-chain story from scratch — not worth it for one
# extra adapter.
#
# Adapter bundling for curl-impersonate replicates this pattern in
# its own stage and is deferred to PR18.
#
# Build context is the repository root because the playwright-builder
# stage regenerates the TypeScript protocol bindings from proto/
# (gitignored per ADR-0007) before `pnpm build`, and the
# seleniumbase-builder stage does the same for Python.
#
# Usage:
#   docker build \
#     --build-arg ENGINE_IMAGE=spectre-engine:dev \
#     --build-arg PLAYWRIGHT_BASE_IMAGE=$(cat adapters/playwright/.playwright-base-image) \
#     -t spectre-control-plane:dev \
#     -f core/control-plane/Dockerfile \
#     .

ARG ENGINE_IMAGE=spectre-engine:dev
# PLAYWRIGHT_BASE_IMAGE is sourced from
# adapters/playwright/.playwright-base-image so version bumps touch one
# file. The Makefile / justfile recipes pass `$(cat ...)` as the
# build-arg; the value is a fully qualified `<image>:<tag>@sha256:<digest>`
# pin so the operator image is reproducible.
ARG PLAYWRIGHT_BASE_IMAGE
# buf is required to regenerate the TypeScript protocol bindings inside
# the playwright-builder stage and the Python protocol bindings inside
# the seleniumbase-builder stage; pinning matches the version used in
# devcontainer + CI tooling. Bumping is a single-line change.
ARG BUF_VERSION=1.55.1
# uv resolves and installs the SeleniumBase adapter's Python
# environment in the seleniumbase-builder stage. Pinning matches
# the documented uv release used in CI; bumping is a single-line
# change.
ARG UV_VERSION=0.5.11

# ---------------------------------------------------------------------------
# Stage 1 — pull the engine binary out of the engine image.
# ---------------------------------------------------------------------------
FROM ${ENGINE_IMAGE} AS engine

# ---------------------------------------------------------------------------
# Stage 2 — build the manager binary (Go).
# ---------------------------------------------------------------------------
FROM golang:1.25 AS manager-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY core/control-plane/go.mod core/control-plane/go.sum ./
RUN go mod download
COPY core/control-plane/ ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -a -o /out/manager cmd/main.go

# ---------------------------------------------------------------------------
# Stage 3 — build the Playwright adapter assets.
# ---------------------------------------------------------------------------
# Regenerate the TypeScript protocol bindings (gitignored — ADR-0007),
# install build-time deps, compile to dist/, then prune to production
# deps for the runtime stage. PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD prevents
# the `playwright` npm package's postinstall from downloading Chromium
# again — the runtime base image already ships it under
# PLAYWRIGHT_BROWSERS_PATH.
FROM node:20-bookworm AS playwright-builder
ARG TARGETARCH
ARG BUF_VERSION
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
WORKDIR /workspace
RUN corepack enable && corepack prepare pnpm@10.33.2 --activate
RUN BUF_ARCH=$(case "${TARGETARCH:-amd64}" in \
        amd64|x86_64) echo "x86_64" ;; \
        arm64|aarch64) echo "aarch64" ;; \
        *) echo "unsupported BUF arch: ${TARGETARCH}"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
 && chmod +x /usr/local/bin/buf \
 && buf --version
COPY proto/ /workspace/proto/
COPY adapters/playwright/ /workspace/adapters/playwright/
RUN cd /workspace/proto && buf generate
WORKDIR /workspace/adapters/playwright
RUN pnpm install --frozen-lockfile
RUN pnpm build
# Prune to production deps so the runtime image carries the smallest
# node_modules that still resolves the adapter's import graph
# (`playwright`, `@bufbuild/protobuf`, `@connectrpc/connect`,
# `@connectrpc/connect-node`).
RUN pnpm install --prod --frozen-lockfile --ignore-scripts

# ---------------------------------------------------------------------------
# Stage 4 — build the SeleniumBase adapter assets (PR17 §4.2).
# ---------------------------------------------------------------------------
# uv hard-codes absolute paths into venv shebangs and pyvenv.cfg, so
# re-rooting a venv after build is brittle (`uv venv --relocatable` is
# young and has documented gaps around pip-installed scripts). We
# sidestep the problem by setting WORKDIR — and the proto-bindings
# location — to the same paths the runtime stage will see at
# /opt/spectre/..., then COPYing the trees over verbatim.
#
# Ubuntu 24.04 (noble) matches the Microsoft Playwright base's
# distribution so the venv's interpreter built here is byte-compatible
# with the apt-installed python3.12 in the runtime stage.
FROM ubuntu:24.04 AS seleniumbase-builder
ARG TARGETARCH
ARG BUF_VERSION
ARG UV_VERSION
ENV DEBIAN_FRONTEND=noninteractive
# uv must use the apt-installed system Python so the venv's shebangs
# and python symlink resolve in the runtime stage (which apt-installs
# the same python3.12 onto the Microsoft base). Without this, uv
# would happily download its own managed CPython into ~/.local/share,
# which the runtime image does not carry.
ENV UV_PYTHON_PREFERENCE=only-system
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        python3.12 python3.12-venv python3-pip \
        ca-certificates curl gnupg \
 && rm -rf /var/lib/apt/lists/*
# Install uv from the upstream pinned installer.
RUN curl -fsSL "https://astral.sh/uv/${UV_VERSION}/install.sh" | sh \
 && mv /root/.local/bin/uv /usr/local/bin/uv \
 && uv --version
# buf regenerates the gitignored Python protocol bindings (ADR-0007).
RUN BUF_ARCH=$(case "${TARGETARCH:-amd64}" in \
        amd64|x86_64) echo "x86_64" ;; \
        arm64|aarch64) echo "aarch64" ;; \
        *) echo "unsupported BUF arch: ${TARGETARCH}"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
 && chmod +x /usr/local/bin/buf \
 && buf --version
# Generate the Python protocol bindings into the canonical
# proto/gen/python tree, then run post-generate.sh which writes the
# package __init__.py markers and the editable-source pyproject.toml
# (matches what `just proto-generate` does on a developer host —
# ADR-0007).
COPY proto/                          /workspace/proto/
COPY tools/codegen/post-generate.sh  /workspace/tools/codegen/post-generate.sh
RUN cd /workspace/proto && buf generate
RUN bash /workspace/tools/codegen/post-generate.sh
# Stage the proto python bindings at the absolute path that
# `[tool.uv.sources] path = "../../proto/gen/python"` resolves to
# from /opt/spectre/adapters/seleniumbase/. Two segments up from
# that directory is /opt/spectre/, so the editable source must live
# at /opt/spectre/proto/gen/python/. uv pins the absolute path it
# resolves at install time into a .pth file in site-packages, so
# the same tree must exist at the same path in the runtime stage —
# we COPY both over below.
RUN mkdir -p /opt/spectre/proto/gen \
 && cp -r /workspace/proto/gen/python /opt/spectre/proto/gen/python
# Build the venv at the FINAL runtime path. WORKDIR + uv sync writes
# /opt/spectre/adapters/seleniumbase/.venv with shebangs and
# pyvenv.cfg pinning that exact path. The runtime stage COPYs the
# whole tree across; nothing needs relocating.
WORKDIR /opt/spectre/adapters/seleniumbase
COPY adapters/seleniumbase/pyproject.toml ./pyproject.toml
COPY adapters/seleniumbase/uv.lock        ./uv.lock
COPY adapters/seleniumbase/README.md      ./README.md
COPY adapters/seleniumbase/src/           ./src/
COPY adapters/seleniumbase/driver.yaml    ./driver.yaml
RUN uv sync --frozen --no-dev
# Sanity check: the venv's python resolves, the adapter package is
# importable, and the proto bindings load through the editable
# source. A failure here is cheaper to debug than at runtime.
RUN ./.venv/bin/python -c "import spectre_seleniumbase; print(spectre_seleniumbase.__file__)" \
 && ./.venv/bin/python -c "from spectre.driver.v1alpha1 import driver_pb2; print(driver_pb2.__file__)"

# ---------------------------------------------------------------------------
# Stage 5 — runtime: Microsoft Playwright base + manager + engine + both adapters.
# ---------------------------------------------------------------------------
# Resulting image is roughly 1.0–1.4 GB compressed (Microsoft base ships
# Chromium + Firefox + WebKit; we only use Chromium, but stripping the
# others would diverge from the supported base — image-size optimisation
# is a separate PR). PR16 §4.1 records the trade-off.
FROM ${PLAYWRIGHT_BASE_IMAGE}
USER root
RUN mkdir -p /opt/spectre/adapters/playwright
COPY --from=manager-builder /out/manager /manager
COPY --from=engine          /usr/local/bin/spectre /usr/local/bin/spectre
COPY --from=playwright-builder \
     /workspace/adapters/playwright/dist          /opt/spectre/adapters/playwright/dist
COPY --from=playwright-builder \
     /workspace/adapters/playwright/node_modules  /opt/spectre/adapters/playwright/node_modules
COPY --from=playwright-builder \
     /workspace/adapters/playwright/driver.yaml   /opt/spectre/adapters/playwright/driver.yaml
COPY --from=playwright-builder \
     /workspace/adapters/playwright/package.json  /opt/spectre/adapters/playwright/package.json

# PR17 §4.1: extend (do not swap) the Microsoft Playwright base with
# the SeleniumBase runtime requirements. Python 3.12 matches the
# version the seleniumbase-builder used so the COPYed venv's
# /usr/bin/python3.12 symlink resolves; google-chrome-stable comes
# from Google's apt repo (the only source that publishes the same
# branded Chrome that SeleniumBase's `Driver(browser="chrome")`
# expects). Chrome's runtime libs (libnss3, libgtk-3, fonts, …) are
# already on the Microsoft base for Chromium, which keeps the
# marginal install footprint to ~80 MiB. ChromeDriver is provisioned
# below in §4.3 once Chrome is on disk.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        python3.12 python3.12-venv ca-certificates curl gnupg \
 && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
        | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg \
 && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
        > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends google-chrome-stable \
 && rm -rf /var/lib/apt/lists/*

# COPY the seleniumbase venv + sources + the proto bindings tree at
# the SAME absolute paths the builder used. The venv's pyvenv.cfg
# and shebangs resolve under /opt/spectre/adapters/seleniumbase/.venv
# unchanged; the editable-source .pth file in site-packages resolves
# /opt/spectre/proto/gen/python/ to the tree we COPY here.
COPY --from=seleniumbase-builder /opt/spectre/adapters/seleniumbase /opt/spectre/adapters/seleniumbase
COPY --from=seleniumbase-builder /opt/spectre/proto                 /opt/spectre/proto

# PR17 §4.3: ChromeDriver matching the apt-installed Chrome. Run
# upstream's `seleniumbase install chromedriver` from the bundled
# venv (the source of truth for version-matching), locate the
# resulting binary, copy it onto $PATH, and assert majors agree.
# We do this in the runtime stage — not the builder — so the
# version-matching logic inspects the actual google-chrome shipped
# here (the builder image has no Chrome).
RUN /opt/spectre/adapters/seleniumbase/.venv/bin/python -m seleniumbase install chromedriver \
 && DRIVER=$(find /root/.seleniumbase /opt/spectre/adapters/seleniumbase/.venv \
        -type f -name chromedriver 2>/dev/null | head -n 1) \
 && test -n "${DRIVER}" \
 && cp "${DRIVER}" /usr/local/bin/chromedriver \
 && chmod 0755 /usr/local/bin/chromedriver \
 && rm -rf /root/.seleniumbase \
 && CHROME_MAJOR=$(google-chrome --version | grep -oE '[0-9]+' | head -n 1) \
 && DRIVER_MAJOR=$(chromedriver --version | grep -oE '[0-9]+' | head -n 1) \
 && test "${CHROME_MAJOR}" = "${DRIVER_MAJOR}" \
 && echo "chrome=${CHROME_MAJOR} chromedriver=${DRIVER_MAJOR}"

RUN chown -R 1001:1001 /opt/spectre /manager
USER 1001:1001
ENTRYPOINT ["/manager"]
