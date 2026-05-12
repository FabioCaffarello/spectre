# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the proxy-broker infra-service.
#
# - codegen stage: regenerates Go protocol bindings via buf and
#   runs `tools/codegen/post-generate.sh` so the broker's go.mod
#   `replace` for `github.com/FabioCaffarello/spectre/proto/gen/go`
#   resolves (mirrors the curl-impersonate adapter pattern).
# - builder stage: compiles the broker as a static Go binary
#   (CGO_ENABLED=0). Pure Go; no native deps.
# - runtime stage: distroless/static — minimal attack surface,
#   no shell, no package manager. Suitable for a pure-Go binary
#   that talks gRPC over TCP + Redis over TCP and nothing else.
#
# Multi-arch (W5.1, 2026-05-12). Builds `linux/amd64 + linux/arm64`
# per ADR-0018 — proxy-broker is the first infra-service and
# follows the multi-arch posture from the start.
#
# Build context: repository root.
#
# Usage: docker buildx bake proxy-broker

ARG GO_VERSION=1.26
ARG BUF_VERSION=1.55.1

# Note: TARGETPLATFORM, TARGETOS, and TARGETARCH are auto-injected
# by BuildKit into FROM directives. Stages that need them in RUN
# bodies declare them inside their own FROM block.

# ---------------------------------------------------------------------------
# Stage 1 — regenerate Go protocol bindings (gitignored — ADR-0007)
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION} AS codegen
ARG BUF_VERSION
WORKDIR /workspace
COPY --from=buf-base /usr/local/bin/buf /usr/local/bin/buf
COPY proto/                          /workspace/proto/
COPY tools/codegen/post-generate.sh  /workspace/tools/codegen/post-generate.sh
RUN cd /workspace/proto && buf generate
RUN bash /workspace/tools/codegen/post-generate.sh

# ---------------------------------------------------------------------------
# Stage 2 — compile the broker as a static Go binary
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION} AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY --from=codegen /workspace/proto/gen/go /workspace/proto/gen/go
WORKDIR /workspace/infra-services/proxy-broker
COPY infra-services/proxy-broker/go.mod infra-services/proxy-broker/go.sum* ./
RUN go mod download
COPY infra-services/proxy-broker/cmd/      ./cmd/
COPY infra-services/proxy-broker/internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/proxy-broker ./cmd/proxy-broker

# ---------------------------------------------------------------------------
# Stage 3 — runtime: distroless/static (no shell, no package manager)
# ---------------------------------------------------------------------------
# distroless/static is the natural fit for a CGO_ENABLED=0 static Go
# binary that needs nothing from the host userland beyond:
# - The static binary itself
# - CA certificates (for outbound TLS to providers + Redis when
#   `rediss://` is configured)
# - /etc/passwd entries for the non-root user
# distroless/static-debian12 provides all three.
#
# Multi-arch: the distroless `:nonroot` tag is a multi-arch manifest
# list; BuildKit picks the matching arch per TARGETPLATFORM.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

LABEL org.opencontainers.image.title="spectre-proxy-broker"
LABEL org.opencontainers.image.description="Spectre proxy-broker infra-service (slot 1 per ADR-0036 §3.1)"
LABEL org.opencontainers.image.vendor="Fabio Caffarello"
LABEL org.opencontainers.image.source="https://github.com/FabioCaffarello/spectre"

COPY --from=builder /out/proxy-broker /usr/local/bin/proxy-broker

# 65532 = `nonroot` user baked into the distroless image. Already the
# default USER in the base, declared here for clarity.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/proxy-broker"]
