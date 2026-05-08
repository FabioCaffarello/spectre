# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Spectre engine binary.
#
# - chef stage: extract dependency metadata into recipe.json so the
#   builder can compile dependencies in a layer that is cached across
#   source-only edits (cargo-chef pattern).
# - builder stage: cook deps, then compile the engine for the
#   appropriate musl target so the result is a fully static binary
#   the distroless runtime can run with no shared libraries.
# - runtime stage: gcr.io/distroless/static:nonroot. No shell, no
#   package manager, no userland. The engine spawns adapter
#   subprocesses but never needs root itself.
#
# Build context is the repository root (so build.rs can resolve
# proto/ at the same relative path it uses for native cargo builds).
# See ADR-0018 for the decisions, ADR-0013 §5 for the static-binary
# distribution choice, and ADR-0007 for the protoc dependency.
#
# R6.1 routes every per-image build through `docker buildx bake`
# (docker-bake.hcl). Bake supplies RUST_VERSION + PROTOC_VERSION
# from build/docker/versions.env and injects the OCI label schema
# uniformly across the five images, so this Dockerfile carries no
# inline ARG defaults and no LABEL directives — both are bake's
# job.
#
# Multi-arch (W2.1, 2026-05-08 — ADR-0018 §5 W2.1 update). Builds
# `linux/amd64 + linux/arm64`. The builder stage runs natively on
# `$BUILDPLATFORM` (typically amd64 in CI) and cross-compiles to
# `$TARGETPLATFORM` — avoids the ~10x slowdown of running the
# full Rust+CMake+protoc toolchain under QEMU emulation. Multi-arch
# is opted into at publish time via per-target bake `--set`
# platform overrides — see `.github/workflows/publish.yml`. CI's
# verify-only matrix stays single-arch (`--load` cannot represent
# a manifest list).
#
# Usage: docker buildx bake engine

ARG RUST_VERSION

# Note: BUILDPLATFORM, TARGETPLATFORM, and TARGETARCH are
# auto-injected by BuildKit into FROM directives without explicit
# top-level ARG declarations. Top-level ARGs would *override* the
# auto-injection with an empty default (resolved at parse time
# before BuildKit's per-stage injection runs). Each stage that
# needs them in RUN bodies declares them inside its own FROM block.

# ---------------------------------------------------------------------------
# Stage 1 — cargo-chef recipe (dependency metadata only)
# ---------------------------------------------------------------------------
FROM rust:${RUST_VERSION}-bookworm AS chef
WORKDIR /app
RUN cargo install cargo-chef --locked --version 0.1.71
# proto/ is copied so `cargo metadata` (invoked by chef prepare) can
# resolve the engine's build.rs reference; only recipe.json travels
# to the builder stage.
COPY proto/ ./proto/
COPY engines/engine/ ./engines/engine/
WORKDIR /app/engines/engine
RUN cargo chef prepare --recipe-path recipe.json

# ---------------------------------------------------------------------------
# Stage 2 — build the engine binary (musl static, cross-compile)
# ---------------------------------------------------------------------------
# `--platform=$BUILDPLATFORM` keeps the builder native (host arch)
# and uses `${TARGETARCH}`-conditional cross-toolchain selection
# below to produce a target-arch musl-static binary. Avoids running
# the full Rust + CMake + protoc toolchain under QEMU emulation.
FROM --platform=$BUILDPLATFORM rust:${RUST_VERSION}-bookworm AS builder
ARG TARGETARCH
ARG PROTOC_VERSION
WORKDIR /app

# Common host-side build deps shared by both target arches.
#  - g++ + cmake: librdkafka-sys is built via CMake (R4.4 — see
#    ADR-0023 §8). cmake's cross-compile path looks for a
#    `${TARGET}-g++` binary; the per-arch symlinks from the
#    musl.cc tarballs below satisfy it.
#  - libcurl4-openssl-dev: librdkafka 2.12+ unconditionally
#    `#include <curl/curl.h>` in `rdkafka_conf.c` even with
#    `-DWITH_CURL=0`. The header just needs to exist on the
#    include path; symbols are dropped by the preprocessor.
#  - unzip + curl: protoc release archive + musl.cc tarball.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      g++ \
      cmake \
      libcurl4-openssl-dev \
      unzip \
      curl \
      ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# Per-target musl toolchain — pre-built tarball from musl.cc.
#
# Why musl.cc instead of Debian's `musl-tools`? `musl-tools` is
# arch-bound to the host (apt installs the host-arch binary), so
# on an amd64 host it provides x86_64-linux-musl-gcc only, not
# aarch64-linux-musl-gcc. Building musl-cross-make from source
# would add 20+ minutes to every build. musl.cc publishes
# pre-built cross-toolchains for every (host, target) pair — the
# standard industry path for Rust musl cross-compile. Trade-off:
# trust an HTTPS fetch the same way we trust crates.io / npm.
# Mitigation: pin the URL (no version drift); the tarballs are
# small (~50 MB) so the layer cache amortises the download
# across rebuilds. Per ADR-0018 §5 W2.1 update.
#
# Both arches are handled with the same logic — download the
# matching tarball, extract under /opt/, symlink binaries into
# /usr/local/bin/ so cargo's musl target finds the linker by
# the standard `${TARGET_TRIPLE}-gcc` lookup. The libcurl include
# directory is host-arch-dependent (Debian multiarch path);
# `dpkg --print-architecture` resolves it.
#
# `MUSL_TARGET` is computed inline per stage rather than a
# Dockerfile-level ARG because bake invokes the builder once per
# platform with the matching `${TARGETARCH}`; a single ARG would
# need bake-level per-platform args plumbing that buildx does not
# support directly.
RUN HOST_TRIPLE=$(case "$(dpkg --print-architecture)" in \
        amd64) echo "x86_64-linux-gnu" ;; \
        arm64) echo "aarch64-linux-gnu" ;; \
        *) echo "unsupported host arch"; exit 1 ;; \
    esac) \
 && MUSL_CROSS=$(case "${TARGETARCH:-amd64}" in \
        amd64) echo "x86_64-linux-musl-cross" ;; \
        arm64) echo "aarch64-linux-musl-cross" ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}"; exit 1 ;; \
    esac) \
 && MUSL_TRIPLE="${MUSL_CROSS%-cross}" \
 && curl -fsSL "https://musl.cc/${MUSL_CROSS}.tgz" -o /tmp/musl-cross.tgz \
 && tar -xzf /tmp/musl-cross.tgz -C /opt/ \
 && rm /tmp/musl-cross.tgz \
 && for bin in /opt/${MUSL_CROSS}/bin/*; do \
        ln -sf "$bin" "/usr/local/bin/$(basename "$bin")"; \
    done \
 && cp -r "/usr/include/${HOST_TRIPLE}/curl" "/opt/${MUSL_CROSS}/${MUSL_TRIPLE}/include/curl"

# Tell cargo + the `cc` crate to use the musl cross-compilers
# instead of the default system `cc` (which doesn't understand
# the target triple's flags). Both target triples are listed
# unconditionally — cargo only reads the env var matching the
# active target, so this is host-arch-independent. Without these,
# cargo's link step invokes plain `cc` and fails with errors like
# `unrecognized command-line option '-m64'` on cross-compile
# builds.
ENV CARGO_TARGET_X86_64_UNKNOWN_LINUX_MUSL_LINKER=x86_64-linux-musl-gcc \
    CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER=aarch64-linux-musl-gcc \
    CC_x86_64_unknown_linux_musl=x86_64-linux-musl-gcc \
    CC_aarch64_unknown_linux_musl=aarch64-linux-musl-gcc \
    CXX_x86_64_unknown_linux_musl=x86_64-linux-musl-g++ \
    CXX_aarch64_unknown_linux_musl=aarch64-linux-musl-g++

# protoc from the official protocolbuffers/protobuf release. The Debian
# package's protoc does not ship the well-known .proto files in a
# location its default include resolution finds, so prost-build (via
# the engine's build.rs) cannot resolve `google/protobuf/duration.proto`.
# The release archive places `bin/protoc` and `include/google/protobuf/*`
# under the same prefix, which protoc auto-discovers. ADR-0007.
#
# protoc runs on BUILDPLATFORM (the builder's native arch) so the
# `uname -m`-based arch detection is correct regardless of
# TARGETPLATFORM. The host-arch protoc emits target-arch-agnostic
# .rs files.
RUN PROTOC_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x86_64" ;; \
        aarch64) echo "aarch_64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /tmp/protoc.zip \
      "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${PROTOC_ARCH}.zip" \
 && unzip -q /tmp/protoc.zip -d /usr/local \
 && rm /tmp/protoc.zip \
 && chmod +x /usr/local/bin/protoc \
 && protoc --version

# Compute MUSL_TARGET once and persist it for downstream RUNs to
# source. RUNs in a Dockerfile don't share shell variables across
# instructions, so the file-based hand-off is the simplest pattern.
RUN case "${TARGETARCH:-amd64}" in \
        amd64) echo "MUSL_TARGET=x86_64-unknown-linux-musl" > /musl-target.env ;; \
        arm64) echo "MUSL_TARGET=aarch64-unknown-linux-musl" > /musl-target.env ;; \
    esac

RUN . /musl-target.env && rustup target add "$MUSL_TARGET"
RUN cargo install cargo-chef --locked --version 0.1.71

# Cache dependency compilation. The recipe is the only input here, so
# this layer is reused across source-only changes. cargo-chef cooks
# the deps for the requested target; per-platform builds get
# separate cached layers automatically (BuildKit per-platform cache).
COPY --from=chef /app/engines/engine/recipe.json ./engines/engine/recipe.json
WORKDIR /app/engines/engine
RUN . /musl-target.env \
 && cargo chef cook --release \
      --target "$MUSL_TARGET" \
      --recipe-path recipe.json

# Real source. Dependency artifacts from `cook` above stay cached in
# target/. build.rs runs from this stage and reads ../../proto.
WORKDIR /app
COPY proto/ ./proto/
COPY engines/engine/ ./engines/engine/
WORKDIR /app/engines/engine
RUN . /musl-target.env \
 && cargo build --release \
      --target "$MUSL_TARGET" \
      --bin spectre \
 && "${MUSL_TARGET%-unknown-linux-musl}-linux-musl-strip" \
      "target/${MUSL_TARGET}/release/spectre" \
 && mkdir -p /out \
 && cp "target/${MUSL_TARGET}/release/spectre" /out/spectre

# ---------------------------------------------------------------------------
# Stage 3 — distroless runtime
# ---------------------------------------------------------------------------
# Runtime FROM is platform-agnostic — distroless/static publishes a
# multi-arch manifest and BuildKit picks the matching arch per
# TARGETPLATFORM. The COPY uses the fixed /out/spectre path the
# builder above writes to so the runtime stage doesn't need to
# reason about MUSL_TARGET.
FROM gcr.io/distroless/static:nonroot AS runtime
COPY --from=builder /out/spectre /usr/local/bin/spectre
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/spectre"]
CMD ["--help"]
