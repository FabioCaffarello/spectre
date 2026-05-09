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
# - runtime stage: python:3.12-slim-bookworm + Debian `chromium` +
#   `chromium-driver` (W2.2 — see below). chromium and
#   chromium-driver are released as a single Debian source package,
#   so the chromedriver binary at /usr/bin/chromedriver always
#   matches the chromium binary at /usr/bin/chromium — no need for
#   the `seleniumbase install chromedriver` runtime step that the
#   Google Chrome path required. SPECTRE_SELENIUMBASE_CONTAINER=1
#   baked as a default ENV so the adapter's container-aware browser
#   flag path (server.py: `_default_driver_factory`) activates
#   without Compose / Helm having to remember.
#
# Multi-arch (W2.2, 2026-05-08 — ADR-0018 §5 W2.2 update). Builds
# `linux/amd64 + linux/arm64`. Debian's `chromium` package ships
# for both architectures in bookworm-main, where Google Chrome
# stable ships amd64-only. SeleniumBase's `Driver(browser="chrome")`
# accepts a `binary_location` kwarg pointing at any Chromium-family
# binary; the adapter passes `/usr/bin/chromium` when
# SPECTRE_SELENIUMBASE_CONTAINER=1 (preserving dev-host fallback
# to whatever chrome is on PATH).
#
# Build context: repository root.
#
# Refs: R6.1 phase prompt §2 (SeleniumBase Dockerfile sketch),
# §4.3 (runtime base — python:3.12-slim), §9.4
# (SPECTRE_SELENIUMBASE_CONTAINER=1 baked).
#
# Usage: docker buildx bake seleniumbase

ARG PYTHON_VERSION=3.12
ARG BUF_VERSION=1.55.1
ARG UV_VERSION=0.5.11

# ---------------------------------------------------------------------------
# Stage 1 — builder: uv-managed venv at the final runtime path
# ---------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim-bookworm AS builder
# Cache-key only — buf binary itself comes from buf-base; ARG
# declared so per-target args invalidates the codegen stage when
# version changes.
ARG BUF_VERSION
ARG UV_VERSION
WORKDIR /workspace

# buf comes from the shared codegen base — built by bake's
# `buf-base` target and injected as a build context. See
# `build/docker/buf-base.Dockerfile` and `docker-bake.hcl`.
COPY --from=buf-base /usr/local/bin/buf /usr/local/bin/buf

# curl + ca-certificates are needed for the uv install script and
# for `buf generate` to reach the Buf Schema Registry over TLS;
# unzip is needed later by SeleniumBase's ChromeDriver download
# step in the runtime stage's seluser flow (kept here so the
# codegen → runtime layering stays predictable).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        curl unzip ca-certificates \
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
# Stage 2 — runtime: python:3.12-slim + Chromium + chromium-driver
# ---------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim-bookworm AS runtime

# Browser runtime: Debian bookworm's `chromium` (the browser) +
# `chromium-driver` (matching chromedriver, released as the same
# Debian source package). Multi-arch (W2.2, 2026-05-08): both
# packages ship for amd64 + arm64 in bookworm main. The adapter
# points SeleniumBase at /usr/bin/chromium via `binary_location`
# when SPECTRE_SELENIUMBASE_CONTAINER=1. The runtime libs
# (libnss3, libxss1, fonts, …) come in via the chromium package's
# Depends graph automatically; the explicit list below is kept
# for documentation and as a safety net in case Debian unbundles
# in a future release.
#
# Replaces R6.1's Google Chrome flow (`google-chrome-stable` from
# Google's amd64-only apt repo + `seleniumbase install
# chromedriver`). The ADR-0014 amendment + ADR-0018 §5 W2.2
# update record the Chrome → Chromium decision rationale.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        chromium chromium-driver \
        fonts-liberation libnss3 libxss1 libasound2 \
        libgbm1 libdrm2 libxcomposite1 libxdamage1 libxrandr2 \
        libatk-bridge2.0-0 libatk1.0-0 libgtk-3-0 \
        xdg-utils \
 && rm -rf /var/lib/apt/lists/*

# Provision the seluser (UID 1000). SeleniumBase + Chromium run
# as this user so /home/seluser is writable for the
# user-data-dir at runtime.
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
