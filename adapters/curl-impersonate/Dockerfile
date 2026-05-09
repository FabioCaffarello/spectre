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
# - runtime stage: debian:bookworm-slim + extracted upstream
#   curl-impersonate v0.6.1 tarballs (W2.3, 2026-05-08 — ADR-0018
#   §5 W2.3 update + ADR-0017 §3 W2.3 amendment).
#
# Multi-arch (W2.3, 2026-05-08). Builds `linux/amd64 + linux/arm64`.
# Replaces the pre-W2.3 runtime base `lwthiker/curl-impersonate:0.6-chrome`
# (amd64-only on Docker Hub) with a Debian bookworm-slim base into
# which we install the upstream curl-impersonate v0.6.1 tarballs
# matching the target architecture. Upstream publishes prebuilt
# `aarch64-linux-gnu.tar.gz` + `x86_64-linux-gnu.tar.gz` releases
# per [their v0.6.1 release page](https://github.com/lwthiker/curl-impersonate/releases/tag/v0.6.1);
# the binaries are dynamically linked against glibc (linux-gnu)
# so debian-slim is the natural runtime, not Alpine (musl) which
# the pre-W2.3 image used.
#
# Tarballs are mirrored in this repo's `curl-impersonate-v0.6.1`
# GitHub Release with SHA256 verification (same supply-chain
# pattern as `musl-cross-toolchains-v1` from W2.1) so a build-
# time outage on github.com/lwthiker/curl-impersonate releases
# does not break our publish flow.
#
# Build context: repository root.
#
# Usage: docker buildx bake curl-impersonate

ARG GO_VERSION=1.26
ARG BUF_VERSION=1.55.1
ARG CURL_IMPERSONATE_VERSION=0.6.1

# Note: TARGETPLATFORM, TARGETOS, and TARGETARCH are auto-injected
# by BuildKit into FROM directives without explicit top-level ARG
# declarations. Each stage that needs them in RUN bodies declares
# them inside its own FROM block (see W2.1 commit 0f70209's
# similar pattern in engines/engine/Dockerfile).

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
# Stage 3 — runtime: debian:bookworm-slim + curl-impersonate tarballs
# ---------------------------------------------------------------------------
# Debian-slim is the runtime base because:
#   1. Upstream's curl-impersonate-v0.6.1 binaries are
#      `linux-gnu` (glibc), not musl. They won't run on Alpine.
#   2. The variant binaries (`curl_chrome116`, `curl_ff117`, …)
#      are POSIX shell wrappers that exec the underlying
#      `curl-impersonate-chrome` binary. Debian-slim ships
#      `dash` at `/bin/sh` so the wrappers work — we sed-replace
#      their `#!/usr/bin/env ash` shebangs to `#!/usr/bin/env sh`
#      below since `ash` (busybox's Almquist shell) is not on
#      Debian.
#   3. The shared library (`libcurl-impersonate-chrome.so.4`)
#      links against glibc + the standard NSS/SSL surfaces; no
#      additional userland needs to ship.
FROM debian:bookworm-slim AS runtime
ARG TARGETARCH
ARG CURL_IMPERSONATE_VERSION

# Per-target tarball SHA256 pins. Upstream publishes prebuilt
# binaries for both arches; we mirror the tarballs in this repo's
# `curl-impersonate-v${CURL_IMPERSONATE_VERSION}` GitHub Release
# (W2.3 supply-chain pattern, 2026-05-08). Bumping the upstream
# version requires: download the new tarballs from upstream,
# upload to a new release tag, update CURL_IMPERSONATE_VERSION
# above + the four SHA256 ARGs below + (likely) the
# CURL_IMPERSONATE_VERSION pin in build/docker/versions.env in
# lockstep.
ARG CURL_IMPERSONATE_TARBALLS_RELEASE=https://github.com/FabioCaffarello/spectre/releases/download/curl-impersonate-v${CURL_IMPERSONATE_VERSION}
ARG CI_X86_64_SHA256=fa1e1614f7ba69ccc66721a0f38be457a3647eb64c75d66974b56186e3316b12
ARG CI_AARCH64_SHA256=b45f14366e4766fbd70a54a07182d2ffaf1e8aed0f0d85aa4e0183fa9d041694
ARG LIBCI_X86_64_SHA256=cf68ce8d4fd5a848cb62fd782c6eb6c8a17d473adba658198e2528ce48923c12
ARG LIBCI_AARCH64_SHA256=1e34628a06e857704ac1f1689019e4e2ac7884c9a5142f32fb043ae07b29fc73

# Install the bare-minimum runtime userland: curl/ca-certs for
# the tarball download + TLS chain verification, and dash (already
# present on slim, listed for documentation). The wrappers' POSIX
# shell shape is satisfied by /bin/sh which dash provides.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
 && rm -rf /var/lib/apt/lists/*

# Per-target tarball selection. Both the binaries tarball
# (curl-impersonate-*) and the shared-library tarball
# (libcurl-impersonate-*) ship per-arch upstream and need to be
# installed together: the wrappers under /usr/local/bin/ exec the
# `curl-impersonate-chrome` binary (binaries tarball), which
# dynamically links `libcurl-impersonate-chrome.so.4` (libs
# tarball). Both tarballs unpack flat — files land directly under
# the cwd — so we extract into /usr/local/bin and /usr/local/lib
# respectively.
#
# After extraction, the wrappers' shebangs are rewritten from
# `#!/usr/bin/env ash` to `#!/usr/bin/env sh` so they run under
# Debian's dash (POSIX-compatible). The wrappers are simple
# wrappers — they exec the curl binary with a per-variant flag
# set — and the substitution is shell-shape-preserving.
#
# `ldconfig` rebuilds the dynamic linker cache so
# `curl-impersonate-chrome` finds `libcurl-impersonate-chrome.so.4`
# under /usr/local/lib without an LD_LIBRARY_PATH override at
# runtime.
RUN CI_TARBALL=$(case "${TARGETARCH:-amd64}" in \
        amd64) echo "curl-impersonate-v${CURL_IMPERSONATE_VERSION}.x86_64-linux-gnu.tar.gz" ;; \
        arm64) echo "curl-impersonate-v${CURL_IMPERSONATE_VERSION}.aarch64-linux-gnu.tar.gz" ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}"; exit 1 ;; \
    esac) \
 && LIBCI_TARBALL=$(case "${TARGETARCH:-amd64}" in \
        amd64) echo "libcurl-impersonate-v${CURL_IMPERSONATE_VERSION}.x86_64-linux-gnu.tar.gz" ;; \
        arm64) echo "libcurl-impersonate-v${CURL_IMPERSONATE_VERSION}.aarch64-linux-gnu.tar.gz" ;; \
    esac) \
 && CI_SHA256=$(case "${TARGETARCH:-amd64}" in \
        amd64) echo "${CI_X86_64_SHA256}" ;; \
        arm64) echo "${CI_AARCH64_SHA256}" ;; \
    esac) \
 && LIBCI_SHA256=$(case "${TARGETARCH:-amd64}" in \
        amd64) echo "${LIBCI_X86_64_SHA256}" ;; \
        arm64) echo "${LIBCI_AARCH64_SHA256}" ;; \
    esac) \
 && curl -fsSL "${CURL_IMPERSONATE_TARBALLS_RELEASE}/${CI_TARBALL}" -o /tmp/ci.tar.gz \
 && echo "${CI_SHA256}  /tmp/ci.tar.gz" | sha256sum -c - \
 && curl -fsSL "${CURL_IMPERSONATE_TARBALLS_RELEASE}/${LIBCI_TARBALL}" -o /tmp/libci.tar.gz \
 && echo "${LIBCI_SHA256}  /tmp/libci.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/ci.tar.gz    -C /usr/local/bin \
 && tar -xzf /tmp/libci.tar.gz -C /usr/local/lib \
 && rm /tmp/ci.tar.gz /tmp/libci.tar.gz \
 && sed -i 's|^#!/usr/bin/env ash$|#!/usr/bin/env sh|' /usr/local/bin/curl_* \
 && chmod +x /usr/local/bin/curl_* /usr/local/bin/curl-impersonate-* \
 && ldconfig

# Drop privileges. Debian's `nobody` user (UID 65534) is
# preinstalled; reuse it rather than creating a redundant
# account. This satisfies Kubernetes runAsNonRoot security
# contexts.
COPY --from=builder /out/adapter /usr/local/bin/adapter
USER 65534:65534

# ADR-0021 §4 reserves 8093 for the curl-impersonate adapter. The
# adapter requires SPECTRE_ADAPTER_GRPC_PORT to be set explicitly at
# run time (the env var has no Dockerfile-side default — Compose /
# Helm provide it).
EXPOSE 8093

ENTRYPOINT ["/usr/local/bin/adapter"]
