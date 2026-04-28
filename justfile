# Spectre — top-level build orchestration.
# Run `just --list` to see available recipes.
#
# Recipes are grouped by component. Top-level aggregates (`bootstrap`,
# `fmt`, `lint`, `test`, `build`, `check`) fan out to per-component
# recipes so you can also work on a single component (e.g. `just engine-test`).

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Load `.env` (gitignored) at recipe-evaluation time so SPECTRE_*
# env vars (Postgres URL, endpoint defaults) reach the recipes
# without each contributor needing to source it manually. R4.2 ships
# `.env.example` as the template; missing `.env` is silently ignored.
set dotenv-load := true

default:
    @just --list --unsorted

# ---------------------------------------------------------------------------
# Aggregates
# ---------------------------------------------------------------------------

# Install dependencies for every component (runs `proto-generate` first
# transitively via the per-component recipes — see ADR-0007).
bootstrap: engine-bootstrap cp-bootstrap curl-imp-bootstrap pw-bootstrap sb-bootstrap conf-bootstrap

# Format every component in place
fmt: proto-fmt engine-fmt cp-fmt curl-imp-fmt pw-fmt sb-fmt conf-fmt

# Lint every component (no tests)
lint: proto-lint engine-lint cp-lint curl-imp-lint pw-lint sb-lint conf-lint

# Run every test suite
test: engine-test cp-test curl-imp-test pw-test sb-test conf-test

# Build every component
build: engine-build cp-build curl-imp-build pw-build

# Lint + test (CI-equivalent local run)
check: lint test

# ---------------------------------------------------------------------------
# Compose stack (R4.2 — see ADR-0023 §9)
# ---------------------------------------------------------------------------
# v1alpha1's local-dev path runs application services as native
# binaries (`just engine-run`, `just pw-run`, `just op-run`) and the
# stateful services in Compose. R4.2 ships Postgres only; R4.3 adds
# Redis, R4.4 adds Kafka (Redpanda), R6.2 moves the application
# services into Compose too.

# Bring up the Compose stack in the background.
compose-up:
    docker compose up -d

# Stop the stack; preserve volumes.
compose-down:
    docker compose down

# Tail the stack logs.
compose-logs:
    docker compose logs -f

# Full reset: stop, drop volumes, restart. Useful when the schema
# advances and you want a clean migration apply.
compose-reset:
    docker compose down -v && docker compose up -d

# ---------------------------------------------------------------------------
# Repository hygiene
# ---------------------------------------------------------------------------

# Run pre-commit hooks against the whole repository
hooks:
    pre-commit run --all-files

# Validate every GitHub Actions workflow YAML
actionlint:
    actionlint .github/workflows/*.yml

# ---------------------------------------------------------------------------
# Protobuf (proto/)
# ---------------------------------------------------------------------------

proto-bootstrap:
    @command -v buf >/dev/null || { echo "buf not installed (https://buf.build/docs/installation)" >&2; exit 1; }

# Buf workspace lives at the repo root (see buf.yaml). All recipes run
# from the root and use the workspace config to resolve modules.

proto-fmt:
    buf format -w

proto-lint:
    buf lint
    buf format --diff --exit-code

# Compare the working tree against origin/main (push-ready check)
proto-breaking:
    buf breaking --against ".git#branch=main"

# Generate language bindings (gitignored). Writes Go to proto/gen/go,
# Python to proto/gen/python, and TypeScript to
# adapters/playwright/src/proto/. See ADR-0007 for rationale; Rust
# bindings are produced lazily by core/engine/build.rs at cargo
# invocation time and are not materialised by this recipe.
#
# Two invocations: the default template generates Go + Python + TS
# for the public driver protocol (and the vendored grpc.health.v1).
# The engine template generates Go only for the internal engine
# protocol — Python and TS bindings are intentionally not produced
# (R2.3; ADR-0020 §6).
proto-generate: proto-bootstrap
    cd proto && buf generate --path spectre/driver --path grpc
    cd proto && buf generate --template buf.gen.engine.yaml --path spectre/engine
    bash tools/codegen/post-generate.sh

# ---------------------------------------------------------------------------
# Rust engine (core/engine)
# ---------------------------------------------------------------------------

engine-bootstrap: proto-generate
    cd core/engine && cargo fetch

engine-fmt:
    cd core/engine && cargo fmt --all

engine-lint:
    cd core/engine && cargo fmt --all -- --check
    cd core/engine && cargo clippy --all-targets --all-features -- -D warnings

engine-test:
    cd core/engine && cargo test --all-features

engine-build:
    cd core/engine && cargo build --release

# Run the engine integration test. Requires the Playwright adapter
# build and Chromium; the test is `#[ignore]` by default. See ADR-0012.
engine-integration-test: pw-build pw-install-browsers
    cd core/engine && PLAYWRIGHT_AVAILABLE=1 cargo test --test integration -- --ignored --nocapture

# Run the engine's database integration tests. Requires a Postgres
# reachable at SPECTRE_POSTGRES_URL (the same env var the engine
# binary reads at startup, ADR-0023 §12). Bring one up via
# `just compose-up` (R4.2). Tests are `#[ignore]` by default so
# `just engine-test` stays DB-free.
engine-db-test:
    cd core/engine && SQLX_OFFLINE=true cargo test --test db_integration -- --ignored --nocapture

# ---------------------------------------------------------------------------
# spectre engine binary (core/engine/src/bin/spectre.rs)
# ---------------------------------------------------------------------------
# The binary is the gRPC service entry point — no subcommands. R2.3
# retired the CLI surface ADR-0013 introduced (`run`, `validate`,
# standalone `version`); ADR-0020 §3 records the supersession.

# Build the release `spectre` binary at core/engine/target/release/spectre.
spectre-build:
    cd core/engine && cargo build --release --bin spectre

# Run the engine as a gRPC service. Listens on `0.0.0.0:9090` by
# default; override via `SPECTRE_ENGINE_PORT`. Adapter endpoints
# resolve from `SPECTRE_PLAYWRIGHT_ENDPOINT`,
# `SPECTRE_SELENIUMBASE_ENDPOINT`, and
# `SPECTRE_CURL_IMPERSONATE_ENDPOINT` with `127.0.0.1:909{1,2,3}`
# defaults for local-development convenience. SIGTERM/Ctrl-C drains
# in-flight RPCs and exits 0.
engine-run *ARGS='': spectre-build
    core/engine/target/release/spectre {{ARGS}}

# gRPC health-probe the running engine on its default port. Requires
# `grpc_health_probe` on PATH (https://github.com/grpc-ecosystem/grpc-health-probe).
# Override the port with `PORT=...` if the engine is bound elsewhere.
engine-grpc-test PORT='9090':
    grpc_health_probe -addr=127.0.0.1:{{PORT}}

# ---------------------------------------------------------------------------
# Go control plane / Kubernetes operator (core/control-plane)
# ---------------------------------------------------------------------------
# PR14 replaced the placeholder binary with a kubebuilder v4 scaffold.
# The cp-* aggregates wrap the kubebuilder Makefile so top-level just
# bootstrap/fmt/lint/test/build keep working; the op-* aliases below
# are the discoverable surface for operator-specific workflows
# (install/uninstall CRDs, run the manager locally). See ADR-0019.

# The operator does not import the protocol bindings — it shells out to
# the engine binary in PR15+, so cp-bootstrap drops the proto-generate
# dependency the placeholder carried.
cp-bootstrap:
    cd core/control-plane && go mod download

cp-fmt:
    cd core/control-plane && gofmt -l -w .
    cd core/control-plane && goimports -l -w .

# Use vanilla golangci-lint rather than the kubebuilder Makefile's
# custom-gcl path: we have no plugins to add, and the custom build
# step is fragile on paths containing spaces.
#
# GOTOOLCHAIN is pinned to match go.mod so go's auto-toolchain
# resolution does not bump to a newer Go than golangci-lint v2.8.0
# (built with Go 1.25) can parse. Contributors with Go 1.26+ on
# their host hit this; CI's setup-go installs the pinned version
# directly.
cp-lint:
    cd core/control-plane && GOTOOLCHAIN=go1.25.3 go vet ./...
    cd core/control-plane && GOTOOLCHAIN=go1.25.3 golangci-lint run

# Defer to the kubebuilder Makefile so envtest binaries are downloaded
# and KUBEBUILDER_ASSETS is set automatically.
cp-test:
    cd core/control-plane && make test

# Defer to the kubebuilder Makefile; produces bin/manager.
cp-build:
    cd core/control-plane && make build

# Operator development: discoverable aliases over the kubebuilder
# Makefile targets. op-test and op-build mirror cp-test and cp-build;
# the install/uninstall/run trio operates against the current kubectl
# context (the equivalent of `make install/uninstall/run` from the
# kubebuilder convention).

op-test: cp-test
op-build: cp-build

# Run the operator from your host against the current kubectl
# context. R3.1's EngineClientRunner dials the engine over gRPC, so
# the local-dev flow needs the engine listening on a TCP port; the
# recipe does not spawn it. Bring up the engine and the adapters in
# separate terminals (`just engine-run`, `just pw-run 9091`, …)
# before invoking this recipe. The endpoint defaults to
# 127.0.0.1:9090 to match `just engine-run`'s default listener;
# override via `SPECTRE_ENGINE_ENDPOINT=...` in the environment.
op-run:
    cd core/control-plane && \
        GOTOOLCHAIN=go1.25.3 go run ./cmd/main.go \
            --engine-endpoint="${SPECTRE_ENGINE_ENDPOINT:-127.0.0.1:9090}"

op-install-crds:
    cd core/control-plane && make install

op-uninstall-crds:
    cd core/control-plane && make uninstall

# Build the operator image. Depends on the engine image because the
# multi-stage Dockerfile copies /usr/local/bin/spectre out of it
# (ADR-0019 §3 / PR15 §4.3). PR16 added a playwright-builder stage
# that bundles the Playwright adapter at /opt/spectre/adapters/playwright
# and switched the runtime base to the Microsoft Playwright image
# pinned in adapters/playwright/.playwright-base-image. PR17 added a
# seleniumbase-builder stage and an apt overlay for Google Chrome +
# ChromeDriver. PR18 added a curl-impersonate-builder stage and a
# runtime-stage tarball download whose version + SHA-256 come from
# adapters/curl-impersonate/.curl-impersonate-version (one line:
# "VERSION SHA256"). Build context is the repo root because each
# builder stage regenerates its language's proto bindings from
# proto/. Single-arch (linux/amd64) to match the engine image and the
# Microsoft base; multi-arch is release-engineering work.
# Build the operator image. R3.1 retired the bundled-image
# execution model: the image carries only the kubebuilder manager
# binary on top of a distroless static base. The engine and
# adapters now run as separate services (per ADR-0020 §5);
# per-service Dockerfiles for them are R6.1 work, the Compose stack
# is R6.2, and the Helm chart is R7.1. Build context is the
# repository root because the operator's go.mod has a local
# `replace` for the proto Go bindings.
op-build-image:
    docker buildx build \
        --platform=linux/amd64 \
        -t spectre-control-plane:dev \
        -f core/control-plane/Dockerfile \
        --load \
        .

# Smoke-test the operator image. The image is now a distroless Go
# binary — no /usr/local/bin/spectre, no /opt/spectre/adapters/*. The
# only meaningful smoke at this layer is "the manager binary exists
# at /manager and runs". Deeper end-to-end smoke requires the engine
# and adapter services to be running alongside; that comes back with
# the Compose stack (R6.2) and the production smoke (R7.2).
op-image-smoke: op-build-image
    docker run --rm --platform=linux/amd64 \
        --entrypoint=/manager \
        spectre-control-plane:dev --help

# ---------------------------------------------------------------------------
# Go curl-impersonate adapter (adapters/curl-impersonate)
# ---------------------------------------------------------------------------

curl-imp-bootstrap: proto-generate
    cd adapters/curl-impersonate && go mod download

curl-imp-fmt:
    cd adapters/curl-impersonate && gofmt -l -w .
    cd adapters/curl-impersonate && goimports -l -w .

# GOTOOLCHAIN is pinned to match cp-lint for the same reason (see
# the cp-lint comment): go's auto-toolchain resolution must not bump
# to a newer Go than golangci-lint v2.8.0 (built with Go 1.25.5) can
# parse.
curl-imp-lint:
    cd adapters/curl-impersonate && GOTOOLCHAIN=go1.25.3 go vet ./...
    cd adapters/curl-impersonate && GOTOOLCHAIN=go1.25.3 golangci-lint run

curl-imp-test:
    cd adapters/curl-impersonate && go test ./...

curl-imp-build:
    cd adapters/curl-impersonate && go build -o bin/adapter ./cmd/adapter

# Run the curl-impersonate adapter as a TCP gRPC service. ADR-0021
# §4 reserves 9093 as the canonical default port. Readiness is
# signalled by the gRPC standard health check returning SERVING;
# SIGTERM/Ctrl-C drains, removes session cookie-jars, and exits 0.
# Override the curl-impersonate variant by setting
# SPECTRE_CURL_VARIANT (default `curl_chrome116`). See ADR-0016 §3.
#
# This recipe survives R2.2 as a developer convenience but is
# scheduled for retirement in R6.2 when the Compose stack becomes
# the canonical local-dev path.
curl-imp-run PORT='9093': curl-imp-build
    cd adapters/curl-impersonate && \
    SPECTRE_ADAPTER_GRPC_PORT={{PORT}} ./bin/adapter

# Run only the curl-impersonate conformance tests. PR11 wired
# Initialize + Navigate; PR12 will add Close + Query + Extract.
# Useful for iterating on the Go adapter without re-running the
# Playwright + SeleniumBase suites.
curl-imp-conf-test: curl-imp-build conf-bootstrap
    cd tools/conformance && \
    uv run pytest \
        tests/test_curl_impersonate_initialize.py \
        tests/test_curl_impersonate_navigate.py

# ---------------------------------------------------------------------------
# TypeScript Playwright adapter (adapters/playwright)
# ---------------------------------------------------------------------------

pw-bootstrap: proto-generate
    cd adapters/playwright && pnpm install --frozen-lockfile

pw-fmt:
    cd adapters/playwright && pnpm exec prettier --write .

pw-lint:
    cd adapters/playwright && pnpm lint

pw-typecheck:
    cd adapters/playwright && pnpm typecheck

pw-test:
    cd adapters/playwright && pnpm test

pw-build:
    cd adapters/playwright && pnpm build

# Run the Playwright adapter as a TCP gRPC service. Set
# SPECTRE_ADAPTER_GRPC_PORT to the desired port (ADR-0021 §4 reserves
# 9091 as the canonical default). The server registers the gRPC
# standard health check; readiness is signalled by Health.Check
# returning SERVING. SIGTERM/Ctrl-C drains and exits 0.
#
# This recipe survives R2.2 as a developer convenience but is
# scheduled for retirement in R6.2 when the Compose stack becomes the
# canonical local-dev path.
pw-run PORT='9091': pw-build
    cd adapters/playwright && SPECTRE_ADAPTER_GRPC_PORT={{PORT}} node dist/index.js

# Install the Chromium browser Playwright drives. Idempotent: skips
# when the binary at the expected version is already present. On
# Linux the system-deps flag pulls the apt packages Chromium needs;
# on macOS it is omitted because `--with-deps` is Linux-only. See
# ADR-0009.
pw-install-browsers: pw-bootstrap
    cd adapters/playwright && \
    if [ "$(uname)" = "Linux" ]; then \
      pnpm exec playwright install --with-deps chromium; \
    else \
      pnpm exec playwright install chromium; \
    fi

# ---------------------------------------------------------------------------
# Python SeleniumBase adapter (adapters/seleniumbase)
# ---------------------------------------------------------------------------

sb-bootstrap: proto-generate
    cd adapters/seleniumbase && uv sync --all-extras --dev

sb-fmt:
    cd adapters/seleniumbase && uv run ruff format .

sb-lint:
    cd adapters/seleniumbase && uv run ruff check .
    cd adapters/seleniumbase && uv run ruff format --check .
    cd adapters/seleniumbase && uv run mypy .

sb-test:
    cd adapters/seleniumbase && uv run pytest

# Run the SeleniumBase adapter as a TCP gRPC service. ADR-0021 §4
# reserves 9092 as the canonical default port. Readiness is
# signalled by the gRPC standard health check returning SERVING;
# SIGTERM/Ctrl-C stops the server, tears down any launched Chrome
# sessions, and exits 0.
#
# This recipe survives R2.2 as a developer convenience but is
# scheduled for retirement in R6.2 when the Compose stack becomes
# the canonical local-dev path.
sb-run PORT='9092': sb-bootstrap
    cd adapters/seleniumbase && \
    SPECTRE_ADAPTER_GRPC_PORT={{PORT}} .venv/bin/python -m spectre_seleniumbase.adapter

# Install ChromeDriver for SeleniumBase. Idempotent: SeleniumBase's
# `install chromedriver` recipe matches the local Chrome version
# and skips when an up-to-date driver is already in PATH. The
# conformance tests that exercise `Navigate` need this. See
# ADR-0014.
sb-install-chromedriver: sb-bootstrap
    cd adapters/seleniumbase && \
    .venv/bin/python -m seleniumbase install chromedriver

# Run only the SeleniumBase conformance tests. PR9 wired
# Initialize + Navigate; PR10 added Close, Query, Extract, and
# Screenshot. Useful for iterating on the Python adapter without
# re-running the Playwright suite.
sb-conf-test: sb-bootstrap sb-install-chromedriver conf-bootstrap
    cd tools/conformance && \
    uv run pytest \
        tests/test_seleniumbase_initialize.py \
        tests/test_seleniumbase_navigate.py \
        tests/test_seleniumbase_close.py \
        tests/test_seleniumbase_query.py \
        tests/test_seleniumbase_extract.py \
        tests/test_seleniumbase_screenshot.py

# ---------------------------------------------------------------------------
# Python conformance suite (tools/conformance)
# ---------------------------------------------------------------------------

conf-bootstrap: proto-generate
    cd tools/conformance && uv sync --all-extras --dev

conf-fmt:
    cd tools/conformance && uv run ruff format .

conf-lint:
    cd tools/conformance && uv run ruff check .
    cd tools/conformance && uv run ruff format --check .
    cd tools/conformance && uv run mypy .

# Conformance test depends on:
# - the Playwright build artifact (`dist/index.js`) and Chromium —
#   tests launch the Node adapter as a subprocess and Navigate
#   needs the browser binary (ADR-0008, ADR-0009);
# - the SeleniumBase adapter's uv-managed venv and ChromeDriver —
#   PR9 added a Python adapter that the suite exercises in
#   parallel with Playwright (ADR-0014).
# - the curl-impersonate adapter binary — PR11 added a Go adapter
#   that the suite exercises in parallel; the curl-impersonate
#   tests skip when the curl_chrome116 binary is not on PATH so
#   developers without the release tarball still get green
#   Playwright + SeleniumBase results (ADR-0016).
conf-test: pw-build pw-install-browsers sb-bootstrap sb-install-chromedriver curl-imp-build
    cd tools/conformance && uv run pytest

# ---------------------------------------------------------------------------
# Container images (Phase 2.5 — see ADR-0018)
# ---------------------------------------------------------------------------

# Build the engine Docker image as `spectre-engine:dev`. Single-arch
# (linux/amd64) per ADR-0018 §5; multi-arch is release-engineering work.
# Build context is the repo root so build.rs can resolve proto/ at the
# same relative path it uses for native cargo builds.
engine-image:
    docker buildx build \
        --platform=linux/amd64 \
        -t spectre-engine:dev \
        -f core/engine/Dockerfile \
        --load \
        .

# Smoke-test the image by confirming the engine binary exists at the
# canonical path. Mirrors the CI engine-image job. R2.3 retired the
# `version` subcommand alongside the rest of the CLI surface; the
# binary is now a gRPC service entry point. A deeper start-and-probe
# smoke (bind a port, query `grpc.health.v1.Health`) belongs to the
# Compose stack landing in R6.2. The --platform flag is required on
# Apple Silicon hosts (Docker emulates linux/amd64 via QEMU there).
engine-image-run: engine-image
    docker run --rm --platform=linux/amd64 \
        --entrypoint=test \
        spectre-engine:dev -x /usr/local/bin/spectre

# Devcontainer post-create entry point. Equivalent to running
# `bash .devcontainer/post-create.sh`. Useful inside the container if
# the contributor needs to re-bootstrap after a lockfile change.
devcontainer-bootstrap:
    bash .devcontainer/post-create.sh

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

# Remove build artifacts, language caches, and generated protocol code.
clean:
    rm -rf core/engine/target
    rm -rf core/control-plane/bin adapters/curl-impersonate/bin
    rm -rf adapters/playwright/{node_modules,dist}
    rm -rf adapters/seleniumbase/{.venv,dist} adapters/seleniumbase/**/__pycache__
    rm -rf tools/conformance/{.venv,dist} tools/conformance/**/__pycache__
    rm -rf proto/gen
    rm -rf adapters/playwright/src/proto
    find . -type d -name '.pytest_cache' -prune -exec rm -rf {} +
    find . -type d -name '.ruff_cache' -prune -exec rm -rf {} +
    find . -type d -name '.mypy_cache' -prune -exec rm -rf {} +
