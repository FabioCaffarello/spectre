# Spectre — top-level build orchestration.
# Run `just --list` to see available recipes.
#
# Recipes are grouped by component. Top-level aggregates (`bootstrap`,
# `fmt`, `lint`, `test`, `build`, `check`) fan out to per-component
# recipes so you can also work on a single component (e.g. `just engine-test`).

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

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
proto-generate: proto-bootstrap
    cd proto && buf generate
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

# Run the hello-hackernews example end-to-end. Builds the Playwright
# adapter on first run and assumes Chromium is already installed (run
# `just pw-install-browsers` first if not). Pass `--verbose` for the
# compiled plan; pass `--job=<path>` to point at a different YAML.
engine-run-hello *ARGS='': pw-build
    cd core/engine && cargo run --example hello-hackernews -- {{ARGS}}

# Run the engine integration test. Requires the Playwright adapter
# build and Chromium; the test is `#[ignore]` by default. See ADR-0012.
engine-integration-test: pw-build pw-install-browsers
    cd core/engine && PLAYWRIGHT_AVAILABLE=1 cargo test --test integration -- --ignored --nocapture

# ---------------------------------------------------------------------------
# Go control plane (core/control-plane)
# ---------------------------------------------------------------------------

cp-bootstrap: proto-generate
    cd core/control-plane && go mod download

cp-fmt:
    cd core/control-plane && gofmt -l -w .
    cd core/control-plane && goimports -l -w .

cp-lint:
    cd core/control-plane && go vet ./...
    cd core/control-plane && golangci-lint run

cp-test:
    cd core/control-plane && go test ./...

cp-build:
    cd core/control-plane && go build -o bin/controller ./cmd/controller

# ---------------------------------------------------------------------------
# Go curl-impersonate adapter (adapters/curl-impersonate)
# ---------------------------------------------------------------------------

curl-imp-bootstrap: proto-generate
    cd adapters/curl-impersonate && go mod download

curl-imp-fmt:
    cd adapters/curl-impersonate && gofmt -l -w .
    cd adapters/curl-impersonate && goimports -l -w .

curl-imp-lint:
    cd adapters/curl-impersonate && go vet ./...
    cd adapters/curl-impersonate && golangci-lint run

curl-imp-test:
    cd adapters/curl-impersonate && go test ./...

curl-imp-build:
    cd adapters/curl-impersonate && go build -o bin/adapter ./cmd/adapter

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

# Run the Playwright adapter against a Unix domain socket.
# Pass --socket=<path> after `--`, or set SPECTRE_DRIVER_SOCKET. The
# server prints `ready unix:<path>` on stdout once it accepts
# connections; SIGTERM/Ctrl-C drains, unlinks the socket, and exits 0.
# Defaults to /tmp/spectre-pw.sock for ad-hoc local testing.
pw-run *ARGS='--socket=/tmp/spectre-pw.sock': pw-build
    cd adapters/playwright && node dist/index.js {{ARGS}}

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

# Conformance test depends on the Playwright build artifact and on
# Chromium being installed: tests launch `dist/index.js` as a
# subprocess, and `Navigate` requires the browser binary. See
# ADR-0008, ADR-0009.
conf-test: pw-build pw-install-browsers
    cd tools/conformance && uv run pytest

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
