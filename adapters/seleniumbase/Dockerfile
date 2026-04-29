# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the SeleniumBase adapter.
#
# - builder stage: python:3.12-slim-bookworm + uv. Regenerates the
#   gitignored Python proto bindings via buf, runs
#   tools/codegen/post-generate.sh (writes the package __init__.py
#   markers + the editable-source pyproject.toml), and `uv sync
#   --frozen --no-dev` builds the venv. Critical detail: uv hard-
#   codes absolute paths into the venv's shebangs and pyvenv.cfg, so
#   the venv is built at the FINAL runtime path
#   (/opt/spectre/adapters/seleniumbase/.venv) and the proto python
#   bindings are staged at /opt/spectre/proto/gen/python so the
#   adapter's `[tool.uv.sources] path = "../../proto/gen/python"`
#   resolves identically in builder and runtime.
# - runtime stage: python:3.12-slim-bookworm + Google Chrome stable
#   (from Google's apt repo, the only source for the branded Chrome
#   build that SeleniumBase's `Driver(browser="chrome")` expects) +
#   matching ChromeDriver fetched via `seleniumbase install
#   chromedriver` at image build time so the bundled driver is on
#   disk before the first runtime request. SPECTRE_SELENIUMBASE_
#   CONTAINER=1 baked as a default ENV so the adapter's container-
#   aware Chrome flag path
#   (adapters/seleniumbase/src/spectre_seleniumbase/server.py:325)
#   activates without Compose / Helm having to remember.
#
# Build context: repository root.
#
# Refs: R6.1 phase prompt §2 (SeleniumBase Dockerfile sketch),
# §4.3 (runtime base — python:3.12-slim + Chrome from Google's
# repo), §9.4 (SPECTRE_SELENIUMBASE_CONTAINER=1 baked).
#
# Usage: docker buildx bake seleniumbase

ARG PYTHON_VERSION=3.12
ARG BUF_VERSION=1.55.1
ARG UV_VERSION=0.5.11

# Forward-readiness for multi-arch (R6.5.3). TARGETARCH is
# auto-populated by BuildKit per stage when declared inside the
# stage; the buf-arch case-statement below already handles
# aarch64 via $(uname -m). The runtime stage ships amd64-only —
# see the R7.x deferral comment above the google-chrome apt-source
# pin further down.
ARG TARGETARCH

# ---------------------------------------------------------------------------
# Stage 1 — builder: uv-managed venv at the final runtime path
# ---------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim-bookworm AS builder
ARG BUF_VERSION
ARG UV_VERSION
WORKDIR /workspace

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        curl unzip ca-certificates \
 && BUF_ARCH=$(case "$(uname -m)" in \
        x86_64) echo "x86_64" ;; \
        aarch64) echo "aarch_64" ;; \
        *) echo "unsupported BUF arch: $(uname -m)"; exit 1 ;; \
    esac) \
 && curl -fsSL -o /usr/local/bin/buf \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
 && chmod +x /usr/local/bin/buf \
 && curl -fsSL "https://astral.sh/uv/${UV_VERSION}/install.sh" | sh \
 && mv /root/.local/bin/uv /usr/local/bin/uv \
 && uv --version \
 && rm -rf /var/lib/apt/lists/*

# Regenerate the gitignored Python protocol bindings (ADR-0007).
COPY proto/                          /workspace/proto/
COPY tools/codegen/post-generate.sh  /workspace/tools/codegen/post-generate.sh
RUN cd /workspace/proto && buf generate
RUN bash /workspace/tools/codegen/post-generate.sh

# Stage the proto python bindings at the absolute path that
# `[tool.uv.sources] path = "../../proto/gen/python"` resolves to
# from /opt/spectre/adapters/seleniumbase/. uv pins the absolute
# path it resolves at install time into a .pth file in
# site-packages, so the same tree must exist at the same path in
# the runtime stage — we COPY both over below.
RUN mkdir -p /opt/spectre/proto/gen \
 && cp -r /workspace/proto/gen/python /opt/spectre/proto/gen/python

# Build the venv at the FINAL runtime path. WORKDIR + uv sync
# writes /opt/spectre/adapters/seleniumbase/.venv with shebangs and
# pyvenv.cfg pinning that exact path. The runtime stage COPYs the
# whole tree across; nothing needs relocating. UV_PYTHON_PREFERENCE
# pins to the system python so the venv's shebangs point at
# /usr/local/bin/python — which exists on the runtime base too.
ENV UV_PYTHON_PREFERENCE=only-system
WORKDIR /opt/spectre/adapters/seleniumbase
COPY adapters/seleniumbase/pyproject.toml ./pyproject.toml
COPY adapters/seleniumbase/uv.lock        ./uv.lock
COPY adapters/seleniumbase/README.md      ./README.md
COPY adapters/seleniumbase/src/           ./src/
COPY adapters/seleniumbase/driver.yaml    ./driver.yaml
RUN uv sync --frozen --no-dev

# Sanity check: the venv's python resolves, the adapter package is
# importable, and the proto bindings load through the editable
# source. A failure here is cheaper to debug than at runtime.
RUN ./.venv/bin/python -c "import spectre_seleniumbase; print(spectre_seleniumbase.__file__)" \
 && ./.venv/bin/python -c "from spectre.driver.v1alpha1 import driver_pb2; print(driver_pb2.__file__)"

# ---------------------------------------------------------------------------
# Stage 2 — runtime: python:3.12-slim + Chrome + ChromeDriver
# ---------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim-bookworm AS runtime

# Chrome runtime libraries (libnss3, libatk-bridge2.0-0,
# libxcomposite1, fonts, …) plus Google's apt repo for
# google-chrome-stable. The adapter's `Driver(browser="chrome")`
# expects the branded Chrome build, not Chromium — Google's repo is
# the only source.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg \
        fonts-liberation libnss3 libxss1 libasound2 \
        libgbm1 libdrm2 libxcomposite1 libxdamage1 libxrandr2 \
        libatk-bridge2.0-0 libatk1.0-0 libgtk-3-0 \
        xdg-utils \
 && install -d -m 0755 /etc/apt/keyrings \
 && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
        | gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg \
 # R7.x: multi-arch unblock — Google Chrome stable for Linux is
 # published only for amd64 as of R6.5.3 (Chromium has arm64 builds;
 # Chrome stable does not). The `[arch=amd64]` apt-source pin and
 # the `seleniumbase install chromedriver` runtime step both depend
 # on the amd64-only google-chrome-stable package. See
 # ADR-0018 §5 R6.5.3 update for unblock options: (a) wait for
 # Google to publish a Linux/arm64 stable channel, or (b) switch
 # the adapter from Chrome to Chromium (which IS multi-arch — but
 # changes the project's tested driver surface, an ADR-level
 # decision deferred to v1alpha2).
 && echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
        > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends google-chrome-stable \
 && rm -rf /var/lib/apt/lists/*

# Provision the seluser (UID 1000). SeleniumBase + Chrome run as
# this user so /home/seluser is writable for ChromeDriver caching
# and Chrome's user-data-dir.
RUN useradd -m -u 1000 -s /bin/bash seluser

# COPY the seleniumbase venv + sources + the proto bindings tree at
# the SAME absolute paths the builder used. The venv's pyvenv.cfg
# and shebangs resolve under /opt/spectre/adapters/seleniumbase/.venv
# unchanged; the editable-source .pth file in site-packages resolves
# /opt/spectre/proto/gen/python/ to the tree we COPY here.
COPY --from=builder --chown=seluser:seluser /opt/spectre/adapters/seleniumbase /opt/spectre/adapters/seleniumbase
COPY --from=builder --chown=seluser:seluser /opt/spectre/proto                 /opt/spectre/proto

USER seluser
WORKDIR /opt/spectre/adapters/seleniumbase

# Provision ChromeDriver matching the apt-installed Chrome. Run as
# seluser so the cached driver lands under
# /home/seluser/.seleniumbase/drivers/chromedriver — where
# SeleniumBase looks at runtime. We do this here (runtime stage) so
# the version-matching logic inspects the actual google-chrome
# shipped in this image; the builder has no Chrome installed.
RUN /opt/spectre/adapters/seleniumbase/.venv/bin/python -m seleniumbase install chromedriver

# ADR-0021 §4 reserves 8092 for the SeleniumBase adapter; the env
# var is the canonical port input. SPECTRE_SELENIUMBASE_CONTAINER=1
# activates the adapter's container-aware Chrome flag path
# (--no-sandbox / --disable-dev-shm-usage) — server.py:325.
ENV SPECTRE_ADAPTER_GRPC_PORT=8092 \
    SPECTRE_SELENIUMBASE_CONTAINER=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/opt/spectre/adapters/seleniumbase/.venv/bin:${PATH}"
EXPOSE 8092

ENTRYPOINT ["python", "-m", "spectre_seleniumbase.adapter"]
