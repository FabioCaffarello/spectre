# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Spectre control-plane operator. The
# resulting image bundles three artifacts:
#
# - /manager — the kubebuilder controller manager (Go).
# - /usr/local/bin/spectre — the spectre engine binary copied out
#   of the engine image (Rust, musl-static; ADR-0018 §3).
# - /opt/spectre/adapters/playwright/ — the Playwright adapter
#   (TypeScript, compiled to JS) plus its production node_modules
#   and driver.yaml manifest. The engine resolves
#   `<adapters_path>/<driver>/driver.yaml` per ADR-0019 §3.
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
# Adapter bundling for SeleniumBase and curl-impersonate replicates
# this pattern in their own stages and is deferred to PR17 / PR18.
#
# Build context is the repository root because the playwright-builder
# stage regenerates the TypeScript protocol bindings from proto/
# (gitignored per ADR-0007) before `pnpm build`. The engine Dockerfile
# uses the same convention (ADR-0018 §5).
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
# the playwright-builder stage; pinning matches the version used in
# devcontainer + CI tooling. Bumping is a single-line change.
ARG BUF_VERSION=1.55.1

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
# Stage 4 — runtime: Microsoft Playwright base + manager + engine + adapter.
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
RUN chown -R 1001:1001 /opt/spectre /manager
USER 1001:1001
ENTRYPOINT ["/manager"]
