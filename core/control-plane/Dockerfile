# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the Spectre control-plane operator. R3.1
# retired the bundled-image execution model — the engine binary and
# the three reference adapters used to land under /usr/local/bin/ and
# /opt/spectre/adapters/, materialising the single-Pod, three-process
# tree from ADR-0019 §3. ADR-0020 §5 supersedes that model with
# service-per-component: the engine is a gRPC service the operator
# dials, adapters are gRPC services the engine dials, and the
# operator image carries only the kubebuilder manager binary.
#
# The runtime stage is `gcr.io/distroless/static:nonroot` per the
# kubebuilder convention. The Go binary is statically linked
# (`CGO_ENABLED=0`), strips out symbols, and runs as uid:gid
# 65532:65532 — no Chrome / Chromium / chromedriver / Python /
# Node / curl-impersonate runtime to keep up to date.
#
# Final image size target: < 50 MB (was ~1.95 GB pre-refactor).
#
# The builder stage regenerates the Go protocol bindings from
# `proto/` (gitignored — ADR-0007) before `go build`, so the
# operator's go.mod `replace` for
# `github.com/FabioCaffarello/spectre/proto/gen/go` resolves. Build
# context is the repository root; the docker-bake target sets the
# context accordingly.
#
# R6.1 routes every per-image build through `docker buildx bake`
# (docker-bake.hcl). Bake supplies GO_VERSION + BUF_VERSION from
# build/docker/versions.env and injects the OCI label schema
# uniformly across the five images, so this Dockerfile carries no
# inline ARG defaults and no LABEL directives — both are bake's
# job.
#
# Usage: docker buildx bake control-plane
#
# Multi-arch ready (R6.5.3): linux/amd64 + linux/arm64. The builder
# stage already declares TARGETOS / TARGETARCH (R6.1) and the
# CGO_ENABLED=0 Go cross-compile produces an arm64 binary from an
# amd64 builder transparently. The buf-arch case-statement below
# covers both architectures. Multi-arch is opted into at publish
# time via `--set control-plane.platform=linux/amd64,linux/arm64`
# (see `.github/workflows/publish.yml`); CI's verify-only matrix
# stays single-arch (`--load` cannot represent a manifest list).

ARG GO_VERSION
ARG BUF_VERSION

FROM golang:${GO_VERSION} AS builder
ARG TARGETOS
ARG TARGETARCH
ARG BUF_VERSION

WORKDIR /workspace

# Install buf (Go and Python bindings come from buf-managed remote
# plugins). The arch case mirrors the curl-impersonate-builder stage's
# pattern.
RUN BUF_ARCH=$(case "${TARGETARCH:-amd64}" in \
        amd64|x86_64) echo "x86_64" ;; \
        arm64|aarch64) echo "aarch64" ;; \
        *) echo "unsupported BUF arch: ${TARGETARCH}"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
 && chmod +x /usr/local/bin/buf \
 && buf --version

# Regenerate the Go bindings for the driver protocol (default
# template) and the internal engine protocol (engine template). Both
# land in /workspace/proto/gen/go/. post-generate.sh runs `go mod
# init` + `go mod tidy` so the operator's `replace` directive resolves.
COPY proto/                          /workspace/proto/
COPY tools/codegen/post-generate.sh  /workspace/tools/codegen/post-generate.sh
RUN cd /workspace/proto && buf generate \
 && cd /workspace/proto && buf generate --template buf.gen.engine.yaml
RUN bash /workspace/tools/codegen/post-generate.sh

# Stage the operator module at the path the proto replace resolves
# from (../../proto/gen/go relative to core/control-plane).
WORKDIR /workspace/core/control-plane
COPY core/control-plane/go.mod core/control-plane/go.sum ./
RUN go mod download

COPY core/control-plane/cmd/      ./cmd/
COPY core/control-plane/api/      ./api/
COPY core/control-plane/internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/manager cmd/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
