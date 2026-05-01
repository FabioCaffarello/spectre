# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Spectre engine binary.
#
# - chef stage: extract dependency metadata into recipe.json so the
#   builder can compile dependencies in a layer that is cached across
#   source-only edits (cargo-chef pattern).
# - builder stage: cook deps, then compile the engine for the
#   x86_64-unknown-linux-musl target so the result is a fully static
#   binary the distroless runtime can run with no shared libraries.
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
# Usage: docker buildx bake engine

ARG RUST_VERSION

# Forward-readiness for multi-arch (R6.5.3). TARGETPLATFORM /
# TARGETARCH are auto-populated by BuildKit per stage when declared
# inside the stage; declaring them at the top documents the
# build-arg surface contributors will use once the cross-compile
# blocker below is unblocked.
ARG TARGETPLATFORM
ARG TARGETARCH

# R7.x: multi-arch unblock — see ADR-0018 §5 R6.5.3 update for the
# cross-compile plan. The engine ships amd64-only today: MUSL_TARGET
# is hardcoded to x86_64-unknown-linux-musl; the apt-installed
# musl-tools + the manual `x86_64-linux-musl-g++` symlink + the
# `cp -r /usr/include/x86_64-linux-gnu/curl /usr/include/x86_64-linux-musl/curl`
# step are all amd64-specific. Unblock requires (a) selecting
# MUSL_TARGET per ${TARGETARCH} (aarch64-unknown-linux-musl on
# arm64), (b) installing the matching cross-compiler
# (gcc-aarch64-linux-gnu + g++-aarch64-linux-gnu) and creating an
# `aarch64-linux-musl-g++` symlink, (c) selecting the libcurl
# include-path source per ${TARGETARCH}.
ARG MUSL_TARGET=x86_64-unknown-linux-musl

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
# Stage 2 — build the engine binary (musl static)
# ---------------------------------------------------------------------------
FROM rust:${RUST_VERSION}-bookworm AS builder
ARG MUSL_TARGET
ARG PROTOC_VERSION
WORKDIR /app
# musl-tools provides the musl-gcc linker the cargo build invokes for
# the static target. unzip is needed to unpack the protoc release.
# cmake + g++ + a x86_64-linux-musl-g++ symlink + libcurl headers
# cover R4.4's rdkafka-sys transitive dep: librdkafka is built via
# CMake (so cmake is required) and CMake's cross-compile path invokes
# `${TARGET}-g++` (so g++ is required and the symlink lets cmake
# find a C++ compiler under the musl-cross name); librdkafka 2.12+
# unconditionally includes <curl/curl.h> in `rdkafka_conf.c` even
# with `-DWITH_CURL=0`, so we provide the header via
# libcurl4-openssl-dev. The C++ code in librdkafka is small (the
# bulk is C); compiling its C++ surface with the host g++ still
# produces a musl-compatible static library when the C side uses
# musl-gcc. ADR-0023 §8 (rdkafka library pin).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      musl-tools \
      g++ \
      cmake \
      libcurl4-openssl-dev \
      unzip \
 && ln -s /usr/bin/g++ /usr/local/bin/x86_64-linux-musl-g++ \
 && cp -r /usr/include/x86_64-linux-gnu/curl /usr/include/x86_64-linux-musl/curl \
 && rm -rf /var/lib/apt/lists/*
# protoc from the official protocolbuffers/protobuf release. The Debian
# package's protoc does not ship the well-known .proto files in a
# location its default include resolution finds, so prost-build (via
# the engine's build.rs) cannot resolve `google/protobuf/duration.proto`.
# The release archive places `bin/protoc` and `include/google/protobuf/*`
# under the same prefix, which protoc auto-discovers. ADR-0007.
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
RUN rustup target add ${MUSL_TARGET}
RUN cargo install cargo-chef --locked --version 0.1.71

# Cache dependency compilation. The recipe is the only input here, so
# this layer is reused across source-only changes.
COPY --from=chef /app/engines/engine/recipe.json ./engines/engine/recipe.json
WORKDIR /app/engines/engine
RUN cargo chef cook --release \
      --target ${MUSL_TARGET} \
      --recipe-path recipe.json

# Real source. Dependency artifacts from `cook` above stay cached in
# target/. build.rs runs from this stage and reads ../../proto.
WORKDIR /app
COPY proto/ ./proto/
COPY engines/engine/ ./engines/engine/
WORKDIR /app/engines/engine
RUN cargo build --release \
      --target ${MUSL_TARGET} \
      --bin spectre \
 && strip target/${MUSL_TARGET}/release/spectre

# ---------------------------------------------------------------------------
# Stage 3 — distroless runtime
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot AS runtime
ARG MUSL_TARGET
COPY --from=builder \
     /app/engines/engine/target/${MUSL_TARGET}/release/spectre \
     /usr/local/bin/spectre
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/spectre"]
CMD ["--help"]
