# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Playwright adapter.
#
# - builder stage: node:20-bookworm-slim. Installs pnpm via corepack
#   (version pinned by the adapter's package.json `packageManager`
#   field), regenerates the gitignored TypeScript proto bindings via
#   `buf generate` (proto/buf.gen.yaml emits to
#   adapters/playwright/src/proto/ — ADR-0007), runs
#   `pnpm install --frozen-lockfile` and `pnpm run build` (tsc), then
#   `pnpm prune --prod` so devDependencies are not copied into the
#   runtime stage.
# - runtime stage: mcr.microsoft.com/playwright:v<version>-noble. The
#   Microsoft-curated Playwright runtime ships Chromium pre-baked at
#   the matching version + every system dependency (libnss3, libatk-
#   bridge2.0-0, libxcomposite1, fonts, …). Image version is pinned
#   to the same value as the npm `playwright` dep
#   (build/docker/versions.env::PLAYWRIGHT_VERSION); bumping one
#   without the other causes Chromium / API drift.
#
# Build context: repository root (proto/ + adapters/playwright/ both
# need to be visible to the codegen + builder stages).
#
# The runtime image already ships a non-root `pwuser` (UID 1000)
# with the right shared-memory tunings for headless Chromium —
# `USER pwuser` works without a `useradd`.
#
# Refs: R6.1 phase prompt §2 (Playwright Dockerfile sketch),
# §4.3 (runtime base matrix — Microsoft image is the canonical
# Playwright runtime, kept current by the Playwright team).
#
# Usage: docker buildx bake playwright

ARG NODE_VERSION=20
ARG BUF_VERSION=1.55.1
ARG PLAYWRIGHT_VERSION=1.49.0

# ---------------------------------------------------------------------------
# Stage 1 — builder: Node + pnpm + buf, regenerate proto, tsc, prune
# ---------------------------------------------------------------------------
FROM node:${NODE_VERSION}-bookworm-slim AS builder
ARG BUF_VERSION
WORKDIR /workspace

# buf for the TS proto bindings. The TS plugin emits to
# /workspace/adapters/playwright/src/proto/ — see proto/buf.gen.yaml.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      curl \
      ca-certificates \
 && BUF_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x86_64" ;; \
        aarch64) echo "aarch_64" ;; \
        *) echo "unsupported BUF arch: $(uname -m)"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
 && chmod +x /usr/local/bin/buf \
 && rm -rf /var/lib/apt/lists/*

# corepack ships with node:20; `enable` provisions the pnpm shim.
# pnpm picks up the version from the adapter's package.json
# `packageManager` field, so no explicit `corepack prepare` is needed.
RUN corepack enable

COPY proto/ /workspace/proto/
# adapters/playwright must exist in the workspace before buf
# generate runs because the TS plugin's `out:` path
# (../adapters/playwright/src/proto) is resolved relative to proto/
# and writing under a missing parent fails.
RUN mkdir -p /workspace/adapters/playwright/src
RUN cd /workspace/proto && buf generate --path spectre/driver --path grpc

WORKDIR /workspace/adapters/playwright
COPY adapters/playwright/package.json adapters/playwright/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY adapters/playwright/tsconfig.json ./
COPY adapters/playwright/src/        ./src/
COPY adapters/playwright/driver.yaml ./
RUN pnpm run build
# Runtime needs production dependencies only; `pnpm prune --prod`
# trims devDependencies (ts, vitest, prettier, …) before the
# runtime stage copies node_modules across.
RUN pnpm prune --prod

# ---------------------------------------------------------------------------
# Stage 2 — runtime: Microsoft Playwright image
# ---------------------------------------------------------------------------
FROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble AS runtime
WORKDIR /app
COPY --from=builder --chown=pwuser:pwuser /workspace/adapters/playwright/dist         /app/dist
COPY --from=builder --chown=pwuser:pwuser /workspace/adapters/playwright/node_modules /app/node_modules
COPY --from=builder --chown=pwuser:pwuser /workspace/adapters/playwright/package.json /app/package.json
COPY --from=builder --chown=pwuser:pwuser /workspace/adapters/playwright/driver.yaml  /app/driver.yaml
USER pwuser

# ADR-0021 §4 reserves 9091 for the Playwright adapter; the env var
# is the canonical port input. NODE_ENV=production silences npm
# debug paths.
ENV SPECTRE_ADAPTER_GRPC_PORT=9091 \
    NODE_ENV=production
EXPOSE 9091

ENTRYPOINT ["node", "/app/dist/index.js"]
