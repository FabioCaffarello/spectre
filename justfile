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

# Run the engine integration test. Requires the Playwright adapter
# build and Chromium; the test is `#[ignore]` by default. See ADR-0012.
engine-integration-test: pw-build pw-install-browsers
    cd core/engine && PLAYWRIGHT_AVAILABLE=1 cargo test --test integration -- --ignored --nocapture

# ---------------------------------------------------------------------------
# spectre CLI (core/engine/src/bin/spectre.rs)
# ---------------------------------------------------------------------------
# The CLI is the engine binary; see ADR-0013. These recipes drive the
# release build and the three subcommands.

# Build the release `spectre` binary at core/engine/target/release/spectre.
spectre-build:
    cd core/engine && cargo build --release --bin spectre

# Print the engine and protocol versions. Cheap; used as a CI smoke test.
spectre-version: spectre-build
    core/engine/target/release/spectre version

# Validate a job YAML file: parse, plan, and check declared
# capabilities without launching the driver. Prints the compiled plan.
spectre-validate JOB: spectre-build
    core/engine/target/release/spectre validate {{JOB}}

# Run a job end-to-end. Requires the Playwright adapter to be built
# and Chromium installed (run `just pw-install-browsers` first). Pass
# extra args after the job path, e.g. `just spectre-run job.yaml --verbose`.
spectre-run JOB *ARGS='': spectre-build pw-build
    core/engine/target/release/spectre run {{JOB}} {{ARGS}}

# Back-compat alias for the PR7 recipe: runs the hello-hackernews job
# via the spectre binary. Equivalent to
# `just spectre-run examples/hello-hackernews/job.yaml`.
engine-run-hello *ARGS='': spectre-build pw-build
    core/engine/target/release/spectre run examples/hello-hackernews/job.yaml {{ARGS}}

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
cp-lint:
    cd core/control-plane && go vet ./...
    cd core/control-plane && golangci-lint run

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

op-run:
    cd core/control-plane && make run

op-install-crds:
    cd core/control-plane && make install

op-uninstall-crds:
    cd core/control-plane && make uninstall

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

# Run the curl-impersonate adapter against a Unix domain socket.
# Pass --socket=<path> after `--`, or set SPECTRE_DRIVER_SOCKET. The
# server prints `ready unix:<path>` on stdout once it accepts
# connections; SIGTERM/Ctrl-C drains, unlinks the socket, and
# exits 0. Defaults to /tmp/spectre-curl.sock for ad-hoc local
# testing. Override the curl-impersonate variant by setting
# SPECTRE_CURL_VARIANT (default `curl_chrome116`). See ADR-0016 §3.
curl-imp-run *ARGS='--socket=/tmp/spectre-curl.sock': curl-imp-build
    cd adapters/curl-impersonate && \
    ./bin/adapter {{ARGS}}

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

# Run the SeleniumBase adapter against a Unix domain socket.
# Pass --socket=<path> after `--`, or set SPECTRE_DRIVER_SOCKET. The
# server prints `ready unix:<path>` on stdout once it accepts
# connections; SIGTERM/Ctrl-C stops the server, tears down any
# launched Chrome sessions, unlinks the socket, and exits 0.
# Defaults to /tmp/spectre-sb.sock for ad-hoc local testing. See
# ADR-0008.
sb-run *ARGS='--socket=/tmp/spectre-sb.sock': sb-bootstrap
    cd adapters/seleniumbase && \
    .venv/bin/python -m spectre_seleniumbase.adapter {{ARGS}}

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

# Smoke-test the image by printing the engine and protocol versions.
# Mirrors the CI engine-image job. The --platform flag is required on
# Apple Silicon hosts (Docker emulates linux/amd64 via QEMU there).
engine-image-run: engine-image
    docker run --rm --platform=linux/amd64 spectre-engine:dev version

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
