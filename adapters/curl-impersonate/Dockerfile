# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the curl-impersonate adapter.
#
# - codegen stage: regenerates Go protocol bindings via buf and runs
#   tools/codegen/post-generate.sh so the adapter's go.mod `replace`
#   for github.com/FabioCaffarello/spectre/proto/gen/go resolves.
# - builder stage: compiles the adapter as a static Go binary
#   (CGO_ENABLED=0). ADR-0016 §1 chose subprocess invocation over
#   cgo, so the adapter does not link libcurl-impersonate — it
#   shells out to a `curl_<variant>` wrapper per Navigate.
# - runtime stage: the upstream `lwthiker/curl-impersonate:<tag>`
#   image used as a base, not extracted from. Rationale below.
#
# Runtime-base choice
# -------------------
# The phase prompt's R6.1 §4.3 sketched
# `gcr.io/distroless/base-debian12:nonroot` with a multi-stage
# COPY --from=upstream extracting `curl_chrome116` and its shared
# library into a distroless rootfs. That sketch is infeasible: the
# variant binaries shipped by upstream (`curl_chrome116`,
# `curl_chrome110`, `curl_firefox117`, …) are POSIX shell wrappers
# (`#!/usr/bin/env ash`) that exec the actual `curl-impersonate-chrome`
# binary with a Chrome-116-specific TLS cipher list and header set.
# Distroless ships no shell — the wrappers cannot execute. The
# pre-R3.1 operator image bundled the variants by downloading the
# upstream release tarball into a Microsoft Playwright base which
# happens to ship `bash`; that base is not available here.
#
# The minimal viable runtime is therefore the upstream Alpine image
# itself (`lwthiker/curl-impersonate:0.6-chrome`, 14 MB compressed),
# which provides:
#
#   * `/bin/ash` and `/bin/sh` for the wrapper shebang,
#   * every chrome / firefox / edge / safari variant in
#     `/usr/local/bin/`,
#   * `libcurl-impersonate-chrome.so.4` in `/usr/local/lib/`,
#   * a working dynamic linker for the underlying glibc-style
#     binary (Alpine's musl is API-compatible enough — the
#     upstream binaries are built specifically for this image).
#
# We add `ca-certificates` (TLS verification needs the trust store)
# and a non-root user; the adapter binary lands in /usr/local/bin/.
# Final image size: ~25–30 MB, well under the 80 MB target. Using
# the upstream image as the base trades a deeper supply-chain
# coupling for an order-of-magnitude smaller image and a working
# runtime — the better trade for v1alpha1.
#
# The image tag is pinned via `build/docker/versions.env` (key
# CURL_IMPERSONATE_IMAGE) so a bump is one edit. ADR-0016 §3 records
# the variant-default rationale; ADR-0017 §3 covers the extraction
# approach.
#
# Build context: repository root.
#
# Usage: docker buildx bake curl-impersonate

ARG GO_VERSION=1.26
ARG BUF_VERSION=1.55.1
ARG CURL_IMPERSONATE_IMAGE=lwthiker/curl-impersonate:0.6-chrome

# ---------------------------------------------------------------------------
# Stage 1 — regenerate Go protocol bindings (gitignored — ADR-0007)
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION} AS codegen
# Cache-key only — buf binary itself comes from buf-base; ARG
# declared so per-target args invalidates the codegen stage when
# version changes.
ARG BUF_VERSION
WORKDIR /workspace
# buf comes from the shared codegen base — built by bake's
# `buf-base` target and injected as a build context. See
# `build/docker/buf-base.Dockerfile` and `docker-bake.hcl`.
COPY --from=buf-base /usr/local/bin/buf /usr/local/bin/buf
COPY proto/                          /workspace/proto/
COPY tools/codegen/post-generate.sh  /workspace/tools/codegen/post-generate.sh
RUN cd /workspace/proto && buf generate
RUN bash /workspace/tools/codegen/post-generate.sh

# ---------------------------------------------------------------------------
# Stage 2 — compile the adapter as a static Go binary
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION} AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY --from=codegen /workspace/proto/gen/go /workspace/proto/gen/go
WORKDIR /workspace/adapters/curl-impersonate
COPY adapters/curl-impersonate/go.mod adapters/curl-impersonate/go.sum ./
RUN go mod download
COPY adapters/curl-impersonate/cmd/      ./cmd/
COPY adapters/curl-impersonate/internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/adapter ./cmd/adapter

# ---------------------------------------------------------------------------
# Stage 3 — runtime: upstream curl-impersonate image (Alpine)
# ---------------------------------------------------------------------------
# R7.x: multi-arch unblock — the runtime base
# `lwthiker/curl-impersonate:0.6-chrome` is published amd64-only on
# Docker Hub as of R6.5.3. See ADR-0018 §5 R6.5.3 update for unblock
# options: (a) wait for upstream to publish a multi-arch manifest
# list, (b) fork the upstream image build and publish our own
# multi-arch tag, (c) cross-compile curl-impersonate from source per
# https://github.com/lwthiker/curl-impersonate/blob/main/INSTALL.md's
# ARM64 instructions and skip the upstream base entirely. Option (a)
# is the lowest-effort path; (b) and (c) trade engineering cost for
# release-cadence independence from upstream.
FROM ${CURL_IMPERSONATE_IMAGE} AS runtime

# ca-certificates is required for TLS chain verification on outbound
# HTTPS requests the curl-impersonate variants make. Alpine 3.18's
# `ca-certificates-bundle` is preinstalled but the explicit package
# adds the update-ca-certificates symlinks the variants expect under
# /etc/ssl/. apk add --no-cache keeps the package index out of the
# image layer.
RUN apk add --no-cache ca-certificates \
 && update-ca-certificates 2>/dev/null || true

# Drop privileges. Upstream ships `nobody` (UID 65534, GID 65534) in
# /etc/passwd; reuse it rather than creating a redundant account.
# This satisfies Kubernetes runAsNonRoot security contexts.
COPY --from=builder /out/adapter /usr/local/bin/adapter
USER 65534:65534

# ADR-0021 §4 reserves 8093 for the curl-impersonate adapter. The
# adapter requires SPECTRE_ADAPTER_GRPC_PORT to be set explicitly at
# run time (the env var has no Dockerfile-side default — Compose /
# Helm provide it).
EXPOSE 8093

ENTRYPOINT ["/usr/local/bin/adapter"]
