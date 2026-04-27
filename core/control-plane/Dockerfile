# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Spectre control-plane operator. The
# resulting image bundles two binaries:
#
# - /manager — the kubebuilder-generated controller manager (Go).
# - /usr/local/bin/spectre — the spectre engine binary that
#   SubprocessRunner shells out to (Rust, musl-static).
#
# The engine binary is pulled from a previously built engine image
# (default: spectre-engine:dev) rather than rebuilt here. This
# matches ADR-0019 §3 (subprocess-in-pod execution model): the
# operator and the engine ship in one Pod, but the engine is its
# own artifact built by core/engine/Dockerfile (ADR-0018 §3).
#
# Adapters are deliberately *not* bundled in this image. End-to-end
# in-cluster execution against real sites requires the Playwright
# adapter + Chromium (or another adapter); bundling those is PR16+
# work. v1alpha1 ships an operator image that runs the engine
# subprocess and works against external adapter mounts or
# adapters-path overrides.
#
# Usage:
#   docker buildx build \
#     --build-arg ENGINE_IMAGE=spectre-engine:dev \
#     -t spectre-control-plane:dev \
#     -f core/control-plane/Dockerfile core/control-plane/

ARG ENGINE_IMAGE=spectre-engine:dev

# ---------------------------------------------------------------------------
# Stage 1 — pull the engine binary out of the engine image.
# ---------------------------------------------------------------------------
FROM ${ENGINE_IMAGE} AS engine

# ---------------------------------------------------------------------------
# Stage 2 — build the manager binary.
# ---------------------------------------------------------------------------
FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# ---------------------------------------------------------------------------
# Stage 3 — distroless runtime that bundles both binaries.
# ---------------------------------------------------------------------------
# Use distroless as minimal base image to package the manager binary.
# The engine binary is musl-static (ADR-0018 §3) so it runs on
# distroless without a libc.
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=engine  /usr/local/bin/spectre /usr/local/bin/spectre
USER 65532:65532

ENTRYPOINT ["/manager"]
