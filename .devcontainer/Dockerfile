# Spectre Devcontainer image.
#
# Base: Microsoft-curated Ubuntu 22.04 (LTS through April 2027) with a
# pre-configured non-root `vscode` user that has passwordless sudo.
# All four language toolchains plus the browser stack live in this
# single image so the conformance suite (which needs Python + Node +
# Chrome + Go + curl-impersonate together) works out of the box.
# ADR-0018 records the decisions.
#
# Pinned versions for reproducibility:
#   Go          1.25.3 (matches the kubebuilder-scaffolded
#                       core/control-plane go.mod; backwards-compatible
#                       with adapters that pin go 1.24)
#   Node        20 LTS (Active LTS through April 2026)
#   Python      3.12   (apt deadsnakes PPA)
#   protoc      27.2   (matches engine Dockerfile)
#   curl-impersonate 0.5.4 (matches CI; ADR-0016 §1)
#   kubebuilder 4.13.1 (PR14: scaffolding for `kubebuilder create api`
#                       on Phase 3 follow-up CRDs; ADR-0019 §1)
#   kubectl     1.31.0 (PR14: apply ScrapeJob CRs against a local
#                       cluster; the operator's `make install/run`
#                       targets call kubectl directly)
#
# Chrome / ChromeDriver track the apt stable channel (auto-update on
# image rebuild). PR14+ may pin them if conformance flakes.
FROM mcr.microsoft.com/devcontainers/base:ubuntu-22.04

ARG GO_VERSION=1.25.3
ARG NODE_MAJOR=20
ARG PROTOC_VERSION=27.2
ARG CURL_IMPERSONATE_VERSION=0.5.4
ARG BUF_VERSION=1.45.0
ARG JUST_VERSION=1.36.0
ARG ACTIONLINT_VERSION=1.7.4
ARG GITLEAKS_VERSION=8.21.2
ARG KUBEBUILDER_VERSION=4.13.1
ARG KUBECTL_VERSION=1.31.0

ENV DEBIAN_FRONTEND=noninteractive

# ---------------------------------------------------------------------------
# System packages: build deps, curl/git/etc, repos for Chrome and Python 3.12
# ---------------------------------------------------------------------------
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential \
      ca-certificates \
      curl \
      wget \
      gnupg2 \
      git \
      jq \
      pkg-config \
      libssl-dev \
      software-properties-common \
      unzip \
      xz-utils \
 && add-apt-repository -y ppa:deadsnakes/ppa \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      python3.12 \
      python3.12-venv \
      python3.12-dev \
 && rm -rf /var/lib/apt/lists/*

# Make python3.12 available as `python3` for tools that hard-code it.
RUN update-alternatives --install /usr/bin/python3 python3 /usr/bin/python3.12 1 \
 && update-alternatives --install /usr/bin/python python /usr/bin/python3.12 1

# ---------------------------------------------------------------------------
# Rust (system-wide install under /usr/local). Owned by root, readable
# by all users; the vscode user inherits the same toolchain.
# ---------------------------------------------------------------------------
ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH=/usr/local/cargo/bin:$PATH
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
      | sh -s -- -y --default-toolchain stable --no-modify-path --profile minimal \
 && rustup component add rustfmt clippy \
 && chmod -R a+rwX "$RUSTUP_HOME" "$CARGO_HOME"

# ---------------------------------------------------------------------------
# Go (official tarball under /usr/local/go).
# ---------------------------------------------------------------------------
ENV GOROOT=/usr/local/go \
    GOPATH=/home/vscode/go \
    PATH=/usr/local/go/bin:/home/vscode/go/bin:$PATH
RUN GO_ARCH=$(dpkg --print-architecture) \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
      -o /tmp/go.tgz \
 && tar -C /usr/local -xzf /tmp/go.tgz \
 && rm /tmp/go.tgz \
 && go version
# goimports — used by `just cp-fmt` and the pre-commit Go hook.
RUN go install golang.org/x/tools/cmd/goimports@latest \
 && mv /root/go/bin/goimports /usr/local/bin/goimports \
 && rm -rf /root/go
# golangci-lint — used by `just cp-lint`.
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
      | sh -s -- -b /usr/local/bin

# ---------------------------------------------------------------------------
# Node.js (NodeSource apt repo) + pnpm via corepack.
# ---------------------------------------------------------------------------
RUN curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/* \
 && corepack enable \
 && corepack prepare pnpm@9 --activate

# ---------------------------------------------------------------------------
# uv (Python package manager).
# ---------------------------------------------------------------------------
RUN curl -LsSf https://astral.sh/uv/install.sh | UV_INSTALL_DIR=/usr/local/bin sh \
 && uv --version

# ---------------------------------------------------------------------------
# Chrome stable + matching ChromeDriver. SeleniumBase's `install
# chromedriver` step inside post-create handles version-matching, but
# we install Chrome here so it's present before the conformance suite
# runs.
# ---------------------------------------------------------------------------
RUN curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
      | gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg \
 && echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
      > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends google-chrome-stable \
 && rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# curl-impersonate v0.5.4 — pinned to the same release CI uses
# (ADR-0016 §1 explains why bumping is a PR-sized change).
# ---------------------------------------------------------------------------
RUN CURL_ARCH=$(case "$(dpkg --print-architecture)" in \
        amd64) echo "x86_64" ;; \
        arm64) echo "aarch64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL "https://github.com/lwthiker/curl-impersonate/releases/download/v${CURL_IMPERSONATE_VERSION}/curl-impersonate-v${CURL_IMPERSONATE_VERSION}.${CURL_ARCH}-linux-gnu.tar.gz" \
      -o /tmp/curl-impersonate.tgz \
 && mkdir -p /opt/curl-impersonate \
 && tar -C /opt/curl-impersonate -xzf /tmp/curl-impersonate.tgz \
 && rm /tmp/curl-impersonate.tgz \
 && ln -s /opt/curl-impersonate/curl_chrome116 /usr/local/bin/curl_chrome116

# ---------------------------------------------------------------------------
# Protocol toolchain: protoc + buf.
# ---------------------------------------------------------------------------
RUN PROTOC_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x86_64" ;; \
        aarch64) echo "aarch_64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /tmp/protoc.zip \
      "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${PROTOC_ARCH}.zip" \
 && unzip -q /tmp/protoc.zip -d /usr/local \
 && rm /tmp/protoc.zip \
 && chmod +x /usr/local/bin/protoc
RUN BUF_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x86_64" ;; \
        aarch64) echo "aarch64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
 && chmod +x /usr/local/bin/buf

# ---------------------------------------------------------------------------
# Build orchestration + lint helpers: just, actionlint, gitleaks.
# ---------------------------------------------------------------------------
RUN JUST_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x86_64-unknown-linux-musl" ;; \
        aarch64) echo "aarch64-unknown-linux-musl" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL "https://github.com/casey/just/releases/download/${JUST_VERSION}/just-${JUST_VERSION}-${JUST_ARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin just
RUN AL_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "amd64" ;; \
        aarch64) echo "arm64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL "https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/actionlint_${ACTIONLINT_VERSION}_linux_${AL_ARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin actionlint
RUN GL_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x64" ;; \
        aarch64) echo "arm64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${GL_ARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin gitleaks

# pre-commit (consumed by the project's hooks). Installed via the system
# pip3 since uv-managed venvs are per-project.
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3-pip \
 && pip3 install --no-cache-dir --break-system-packages pre-commit \
 && rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# Kubernetes operator tooling (PR14, ADR-0019). kubebuilder scaffolds
# CRDs and controllers; kubectl drives `make install/run` against the
# developer's current kubectl context. setup-envtest, controller-gen,
# kustomize, and golangci-lint are vendored on demand by the
# kubebuilder Makefile into core/control-plane/bin/, so the image
# itself only needs the two top-level binaries.
# ---------------------------------------------------------------------------
RUN KB_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "amd64" ;; \
        aarch64) echo "arm64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/kubebuilder \
      "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v${KUBEBUILDER_VERSION}/kubebuilder_linux_${KB_ARCH}" \
 && chmod +x /usr/local/bin/kubebuilder
RUN KC_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "amd64" ;; \
        aarch64) echo "arm64" ;; \
        *) echo "unsupported"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${KC_ARCH}/kubectl" \
 && chmod +x /usr/local/bin/kubectl

# ---------------------------------------------------------------------------
# Per-user PATH propagation. The vscode user inherits the system tools
# above; the GOPATH bin and per-user cargo bin are appended for any
# `cargo install`/`go install` they run themselves.
# ---------------------------------------------------------------------------
RUN echo 'export PATH="$HOME/.cargo/bin:$HOME/go/bin:/usr/local/cargo/bin:/usr/local/go/bin:$PATH"' \
      >> /home/vscode/.bashrc \
 && chown vscode:vscode /home/vscode/.bashrc
