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

# Install dependencies for every component
bootstrap: proto-bootstrap engine-bootstrap cp-bootstrap curl-imp-bootstrap pw-bootstrap sb-bootstrap conf-bootstrap

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

# Generate language bindings into proto/gen/ (gitignored)
proto-generate:
    cd proto && buf generate

# ---------------------------------------------------------------------------
# Rust engine (core/engine)
# ---------------------------------------------------------------------------

engine-bootstrap:
    cd core/engine && cargo fetch

engine-fmt:
    cd core/engine && cargo fmt --all

engine-lint:
    cd core/engine
    cargo fmt --all -- --check
    cargo clippy --all-targets --all-features -- -D warnings

engine-test:
    cd core/engine && cargo test --all-features

engine-build:
    cd core/engine && cargo build --release

# ---------------------------------------------------------------------------
# Go control plane (core/control-plane)
# ---------------------------------------------------------------------------

cp-bootstrap:
    cd core/control-plane && go mod download

cp-fmt:
    cd core/control-plane
    gofmt -l -w .
    goimports -l -w .

cp-lint:
    cd core/control-plane
    go vet ./...
    golangci-lint run

cp-test:
    cd core/control-plane && go test ./...

cp-build:
    cd core/control-plane && go build -o bin/controller ./cmd/controller

# ---------------------------------------------------------------------------
# Go curl-impersonate adapter (adapters/curl-impersonate)
# ---------------------------------------------------------------------------

curl-imp-bootstrap:
    cd adapters/curl-impersonate && go mod download

curl-imp-fmt:
    cd adapters/curl-impersonate
    gofmt -l -w .
    goimports -l -w .

curl-imp-lint:
    cd adapters/curl-impersonate
    go vet ./...
    golangci-lint run

curl-imp-test:
    cd adapters/curl-impersonate && go test ./...

curl-imp-build:
    cd adapters/curl-impersonate && go build -o bin/adapter ./cmd/adapter

# ---------------------------------------------------------------------------
# TypeScript Playwright adapter (adapters/playwright)
# ---------------------------------------------------------------------------

pw-bootstrap:
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

# ---------------------------------------------------------------------------
# Python SeleniumBase adapter (adapters/seleniumbase)
# ---------------------------------------------------------------------------

sb-bootstrap:
    cd adapters/seleniumbase && uv sync --all-extras --dev

sb-fmt:
    cd adapters/seleniumbase && uv run ruff format .

sb-lint:
    cd adapters/seleniumbase
    uv run ruff check .
    uv run ruff format --check .
    uv run mypy .

sb-test:
    cd adapters/seleniumbase && uv run pytest

# ---------------------------------------------------------------------------
# Python conformance suite (tools/conformance)
# ---------------------------------------------------------------------------

conf-bootstrap:
    cd tools/conformance && uv sync --all-extras --dev

conf-fmt:
    cd tools/conformance && uv run ruff format .

conf-lint:
    cd tools/conformance
    uv run ruff check .
    uv run ruff format --check .
    uv run mypy .

conf-test:
    cd tools/conformance && uv run pytest

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

# Remove build artifacts and language caches
clean:
    rm -rf core/engine/target
    rm -rf core/control-plane/bin adapters/curl-impersonate/bin
    rm -rf adapters/playwright/{node_modules,dist}
    rm -rf adapters/seleniumbase/{.venv,dist} adapters/seleniumbase/**/__pycache__
    rm -rf tools/conformance/{.venv,dist} tools/conformance/**/__pycache__
    find . -type d -name '.pytest_cache' -prune -exec rm -rf {} +
    find . -type d -name '.ruff_cache' -prune -exec rm -rf {} +
    find . -type d -name '.mypy_cache' -prune -exec rm -rf {} +
