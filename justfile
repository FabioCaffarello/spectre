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

# Verify versions.env pins agree with each Dockerfile's ARG defaults
# and that bake's labels schema is intact. See build/docker/README.md.
check-versions:
    @bash tools/build/check-versions-coherent.sh

# Lint + test + version-coherence (CI-equivalent local run)
check: check-versions lint test

# ---------------------------------------------------------------------------
# Compose stack (R6.2 + R6.3 — see ADR-0025; ADR-0023 §9)
# ---------------------------------------------------------------------------
# R6.2 brought the application services (engine + three adapters)
# into `docker-compose.yml` alongside the stateful services from
# R4.x and R5.1. R6.3 added the control-plane operator as the
# eleventh service (ADR-0025 §6 R6.3 update); `just compose-up`
# brings up the full development graph: operator + engine on 8090,
# adapters on 8091/8092/8093, Postgres / Redis / Kafka / MinIO +
# Console / minio-bootstrap. The operator joins both the Compose
# default network and the `kind` Docker network (managed via
# `just kind-up`); `just kind-up` must run before `compose-up`
# the first time (post-create.sh handles that automatically on
# devcontainer first-build).
#
# Profiles (ADR-0025 §4): infra (stateful only — pre-R6.2 default),
# core (engine + stateful deps), adapters (three adapters + redis),
# app (headless full stack incl. operator), full (default —
# everything).

# Bring up the full development graph (eleven services across the
# `full` profile — six stateful + engine + three adapters +
# control-plane operator). Requires images built first via
# `just images` — Compose uses `pull_policy: never` per ADR-0025
# §8 — and the `kind` Docker network present (via `just kind-up`).
compose-up:
    docker compose --profile full up -d

# Bring up `app` profile — headless full stack (no kafka-console).
# Aimed at CI runs where the observability UI adds no value.
compose-up-app:
    docker compose --profile app up -d

# Bring up `core` profile — postgres + kafka + minio (+ bootstrap)
# + engine. Engine integration tests (engine-db-test /
# engine-kafka-test / engine-s3-test) target this profile.
compose-up-core:
    docker compose --profile core up -d

# Bring up `adapters` profile — redis + the three adapters. Useful
# for adapter-only experimentation via grpcurl against the host
# port mappings (8091 / 8092 / 8093).
compose-up-adapters:
    docker compose --profile adapters up -d

# Bring up `infra` profile — six stateful services only (the
# pre-R6.2 default shape). Used by the conformance suite (which
# spawns adapter subprocesses, ADR-0025 §5) and by contributors
# running native-binary application services via
# `just engine-run-native`.
compose-up-infra:
    docker compose --profile infra up -d

# Stop every running service; preserve volumes.
compose-down:
    docker compose down

# Tail the stack logs. Pass a service name to scope.
#   just compose-logs                 # all services
#   just compose-logs engine          # engine only
compose-logs SERVICE='':
    @if [ -z "{{SERVICE}}" ]; then \
        docker compose logs -f; \
    else \
        docker compose logs -f "{{SERVICE}}"; \
    fi

# Restart a running service in place — useful for picking up
# image changes after `just compose-rebuild SERVICE`.
compose-restart SERVICE:
    docker compose restart {{SERVICE}}

# Rebuild SERVICE's image via bake then recreate just that
# service in the running stack. Usage:
#   just compose-rebuild engine
#   just compose-rebuild playwright       # bake target name
# The bake target name and the Compose service name diverge for
# adapters: bake builds `playwright`, `seleniumbase`,
# `curl-impersonate`; Compose runs `playwright-adapter`,
# `seleniumbase-adapter`, `curl-impersonate-adapter`. Pass the
# bake target name; the recipe maps it to the matching Compose
# service.
compose-rebuild SERVICE:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load {{SERVICE}}
    @case "{{SERVICE}}" in \
        playwright|seleniumbase|curl-impersonate) \
            docker compose up -d --no-deps "{{SERVICE}}-adapter" ;; \
        *) \
            docker compose up -d --no-deps "{{SERVICE}}" ;; \
    esac

# Full reset: stop, drop volumes, restart under `--profile full`.
# Useful when the schema advances or you want a clean Postgres /
# Redis / Kafka / MinIO state. The kind cluster is independent of
# Compose's lifecycle — `compose-reset` does NOT recreate it. Run
# `just kind-down && just kind-up` separately for a clean cluster.
compose-reset:
    docker compose down -v && docker compose --profile full up -d

# Open the Redpanda Console UI for the local Kafka broker. Linux
# uses `xdg-open`; macOS uses `open`. Falls back to printing the
# URL when no opener is available (e.g. headless dev container).
kafka-console:
    @if command -v open >/dev/null; then open http://localhost:8080; \
    elif command -v xdg-open >/dev/null; then xdg-open http://localhost:8080; \
    else echo "open http://localhost:8080 in your browser"; fi

# List topics on the local Kafka broker via docker exec — useful
# for sanity-checking the engine's published topic.
kafka-topics:
    docker exec spectre-kafka /opt/kafka/bin/kafka-topics.sh \
        --bootstrap-server kafka:9092 --list

# Tail messages from a topic on the local broker. Reads from the
# beginning so a freshly-started consumer sees historical rows.
# Stop with Ctrl-C.
kafka-consume TOPIC:
    docker exec -it spectre-kafka /opt/kafka/bin/kafka-console-consumer.sh \
        --bootstrap-server kafka:9092 \
        --topic {{TOPIC}} \
        --from-beginning \
        --property print.headers=true

# Open the MinIO Console UI for the local S3-compatible object
# store. Linux uses `xdg-open`; macOS uses `open`. Falls back to
# printing the URL when no opener is available (e.g. headless
# dev container). Default credentials are
# spectre_dev_access / spectre_dev_secret_key (visible in
# docker-compose.yml; dev-only).
minio-console:
    @if command -v open >/dev/null; then open http://localhost:9001; \
    elif command -v xdg-open >/dev/null; then xdg-open http://localhost:9001; \
    else echo "open http://localhost:9001 in your browser"; fi

# List objects in the spectre-rows bucket via docker exec —
# useful for sanity-checking the engine's S3 uploads.
minio-ls:
    docker exec spectre-minio mc ls --recursive local/spectre-rows

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
# bindings are produced lazily by engines/engine/build.rs at cargo
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
# Rust engine (engines/engine)
# ---------------------------------------------------------------------------

engine-bootstrap: proto-generate
    cd engines/engine && cargo fetch

engine-fmt:
    cd engines/engine && cargo fmt --all

engine-lint:
    cd engines/engine && cargo fmt --all -- --check
    cd engines/engine && cargo clippy --all-targets --all-features -- -D warnings

engine-test:
    cd engines/engine && cargo test --all-features

engine-build:
    cd engines/engine && cargo build --release

# Run the engine integration test. Requires the Playwright adapter
# build and Chromium; the test is `#[ignore]` by default. See ADR-0012.
engine-integration-test: pw-build pw-install-browsers
    cd engines/engine && PLAYWRIGHT_AVAILABLE=1 cargo test --test integration -- --ignored --nocapture

# Run the engine's database integration tests. Requires a Postgres
# reachable at SPECTRE_POSTGRES_URL (the same env var the engine
# binary reads at startup, ADR-0023 §12). Bring one up via
# `just compose-up` (R4.2). Tests are `#[ignore]` by default so
# `just engine-test` stays DB-free.
engine-db-test:
    cd engines/engine && SQLX_OFFLINE=true cargo test --test db_integration -- --ignored --nocapture

# Run the engine's Kafka producer integration tests. Requires a
# Kafka broker reachable at SPECTRE_KAFKA_BROKERS (the same env
# var the engine binary reads at startup, ADR-0023 §12). Bring
# one up via `just compose-up` (R4.4). Tests are `#[ignore]` by
# default so `just engine-test` stays broker-free.
engine-kafka-test:
    cd engines/engine && cargo test --test kafka_integration -- --ignored --nocapture

# Run the engine's S3 uploader integration tests. Requires an
# S3-compatible endpoint reachable at SPECTRE_S3_ENDPOINT (the
# same env var the engine binary reads at startup, ADR-0024 §3).
# Bring up MinIO via `just compose-up` (R5.1). Tests are
# `#[ignore]` by default so `just engine-test` stays MinIO-free.
engine-s3-test:
    cd engines/engine && cargo test --test s3_integration -- --ignored --nocapture

# Run the engine's webhook client integration tests. No external
# dependency — the test server runs in-process via axum. ADR-0024
# §4. Tests run unconditionally as part of `just engine-test`;
# this recipe gives them a discoverable surface.
engine-webhook-test:
    cd engines/engine && cargo test --test webhook_integration -- --nocapture

# ---------------------------------------------------------------------------
# spectre engine binary (engines/engine/src/bin/spectre.rs)
# ---------------------------------------------------------------------------
# The binary is the gRPC service entry point — no subcommands. R2.3
# retired the CLI surface ADR-0013 introduced (`run`, `validate`,
# standalone `version`); ADR-0020 §3 records the supersession.

# Build the release `spectre` binary at engines/engine/target/release/spectre.
spectre-build:
    cd engines/engine && cargo build --release --bin spectre

# Run the native engine binary as a gRPC service. Debugging escape
# hatch — the canonical local-dev path post-R6.2 is the Compose
# stack (`just compose-up`), which runs the engine container on
# `127.0.0.1:8090` with stateful deps already wired. This recipe
# survives because live-coding the engine without rebuilding the
# image is genuinely faster for tight inner loops; for everyday
# end-to-end runs, prefer `compose-up`.
#
# Listens on `0.0.0.0:8090` by default; override via
# `SPECTRE_ENGINE_PORT`. Adapter endpoints resolve from
# `SPECTRE_PLAYWRIGHT_ENDPOINT` / `SPECTRE_SELENIUMBASE_ENDPOINT` /
# `SPECTRE_CURL_IMPERSONATE_ENDPOINT` with `127.0.0.1:809{1,2,3}`
# defaults — the Compose stack's host-port mappings. SIGTERM/Ctrl-C
# drains in-flight RPCs and exits 0.
engine-run-native *ARGS='': spectre-build
    engines/engine/target/release/spectre {{ARGS}}

# gRPC health-probe the running engine on its default port. Requires
# `grpc_health_probe` on PATH (https://github.com/grpc-ecosystem/grpc-health-probe).
# Override the port with `PORT=...` if the engine is bound elsewhere.
engine-grpc-test PORT='8090':
    grpc_health_probe -addr=127.0.0.1:{{PORT}}

# ---------------------------------------------------------------------------
# Go control plane / Kubernetes operator (operators/control-plane)
# ---------------------------------------------------------------------------
# operators/control-plane is a kubebuilder v4 scaffold; the cp-* aggregates
# wrap the kubebuilder Makefile so top-level just
# bootstrap/fmt/lint/test/build keep working; the op-* aliases below
# are the discoverable surface for operator-specific workflows
# (install/uninstall CRDs, run the manager locally). See ADR-0019.

# The operator does not import the protocol bindings — it dials the
# engine over gRPC (R3.1), so cp-bootstrap drops the proto-generate
# dependency the placeholder carried.
cp-bootstrap:
    cd operators/control-plane && go mod download

cp-fmt:
    cd operators/control-plane && gofmt -l -w .
    cd operators/control-plane && goimports -l -w .

# Use vanilla golangci-lint rather than the kubebuilder Makefile's
# custom-gcl path: we have no plugins to add, and the custom build
# step is fragile on paths containing spaces.
#
# GOTOOLCHAIN is pinned to match go.mod so go's auto-toolchain
# resolution does not bump to a newer Go than the project intends.
# The operator's go.mod requires Go 1.26 (controller-runtime 0.24
# transitive). Contributors with Go 1.27+ on their host hit this;
# CI's setup-go installs the pinned version directly.
cp-lint:
    cd operators/control-plane && GOTOOLCHAIN=go1.26.0 go vet ./...
    cd operators/control-plane && GOTOOLCHAIN=go1.26.0 golangci-lint run

# Defer to the kubebuilder Makefile so envtest binaries are downloaded
# and KUBEBUILDER_ASSETS is set automatically.
cp-test:
    cd operators/control-plane && make test

# Defer to the kubebuilder Makefile; produces bin/manager.
cp-build:
    cd operators/control-plane && make build

# Operator development: discoverable aliases over the kubebuilder
# Makefile targets. op-test and op-build mirror cp-test and cp-build.
# The R6.2 `op-run` host-process recipe was retired in R6.3 (ADR-0025
# §6 R6.3 update): the operator now runs as a Compose service
# (`control-plane`) under the `app`/`full` profiles, joining both the
# Compose default network and the kind Docker network. Local CRD
# management lives in the `crds-install` / `crds-uninstall` recipes
# below; kind cluster lifecycle in `kind-up` / `kind-down` /
# `kind-status`.

op-test: cp-test
op-build: cp-build

# Build the operator image. R3.1 retired the bundled-image
# execution model: the image carries only the kubebuilder manager
# binary on top of a distroless static base. R6.1 routed this
# through `docker buildx bake control-plane` so toolchain pins,
# OCI labels, and platform args stay uniform across the five
# images. R6.2 renamed the recipe `op-image` to match the
# `<service>-image` pattern (engine-image / pw-image / sb-image /
# curl-imp-image). Build context is the repository root because
# the operator's go.mod has a local `replace` for the proto Go
# bindings.
op-image:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load control-plane

# Smoke-test the operator image. The image is now a distroless Go
# binary — no /usr/local/bin/spectre, no /opt/spectre/adapters/*.
# The only meaningful smoke at this layer is "the manager binary
# exists at /manager and runs". Deeper end-to-end smoke requires
# the engine and adapter services to be running alongside; that
# is what `just compose-up` (R6.2) provides for app-level
# smoke and what production smoke (R7.2) covers in-cluster.
op-image-smoke TAG='dev': op-image
    docker run --rm --platform=linux/amd64 \
        --entrypoint=/manager \
        spectre-control-plane:{{TAG}} --help

# ---------------------------------------------------------------------------
# Kubernetes-in-Docker (kind) — ADR-0025 §6 R6.3 update
# ---------------------------------------------------------------------------
# R6.3 places the control-plane operator inside the Compose stack
# alongside a `kind` cluster running in the same Docker daemon
# (Docker-in-Docker, devcontainer-managed). The operator container
# joins kind's Docker network and dials
# `spectre-dev-control-plane:6443` directly (kind names the node
# after the cluster, `<cluster>-control-plane`); `kubectl` from the
# devcontainer terminal reaches the same cluster via kind's
# host-side kubeconfig at `~/.kube/config` (kind writes that
# automatically on cluster creation).
#
# Lifecycle is independent of Compose: `kind-up` is idempotent and
# the post-create script runs it once per devcontainer build;
# `compose-up` / `compose-down` do not touch the kind cluster.

# Create the local kind cluster used by the operator container
# (ADR-0025 §6 resolution / R6.3). Idempotent — skips creation if
# the cluster exists. Writes the in-DinD-rewritten kubeconfig
# (server URL `https://spectre-dev-control-plane:6443` — kind
# names the node after the cluster) to `build/kind/kubeconfig`,
# which `docker-compose.yml`'s `control-plane` service mounts
# read-only at `/home/nonroot/.kube/config`.
kind-up:
    #!/usr/bin/env bash
    set -euo pipefail
    if kind get clusters 2>/dev/null | grep -q '^spectre-dev$'; then
        echo "kind cluster 'spectre-dev' already exists"
    else
        kind create cluster --config build/kind/cluster.yaml
    fi
    mkdir -p build/kind
    kind get kubeconfig --name spectre-dev --internal \
        > build/kind/kubeconfig
    echo "wrote build/kind/kubeconfig (server: https://spectre-dev-control-plane:6443)"

# Tear down the kind cluster. Used by the contributor when they
# want a clean reset; not run by `compose-down` (the cluster is
# independent of Compose's service lifecycle).
kind-down:
    kind delete cluster --name spectre-dev || true
    rm -f build/kind/kubeconfig

# Print the kind clusters known to this devcontainer's Docker
# daemon. Useful for sanity-checking the post-create script's
# idempotence.
kind-status:
    @kind get clusters

# Apply the v1alpha2 ScrapeJob CRD to the local kind cluster.
# Idempotent — `kubectl apply -f` is the canonical way to bring
# CRDs into a cluster. Targets `build/kind/kubeconfig` so
# re-running outside of the `kind-up` flow still works.
crds-install:
    cd operators/control-plane && \
        KUBECONFIG=$(realpath ../../build/kind/kubeconfig) make install

crds-uninstall:
    cd operators/control-plane && \
        KUBECONFIG=$(realpath ../../build/kind/kubeconfig) make uninstall

# Deprecated R6.2 names. R6.3 renames `op-install-crds` →
# `crds-install` and `op-uninstall-crds` → `crds-uninstall`; the old
# names remain as one-cycle aliases for muscle memory. Removed in
# R7.1.
op-install-crds:
    @echo 'note: `op-install-crds` is deprecated; use `crds-install` (removed in R7.1)' >&2
    @just crds-install

op-uninstall-crds:
    @echo 'note: `op-uninstall-crds` is deprecated; use `crds-uninstall` (removed in R7.1)' >&2
    @just crds-uninstall

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

# Run only the curl-impersonate conformance tests. Useful for
# iterating on the Go adapter without re-running the Playwright +
# SeleniumBase suites.
curl-imp-conf-test: curl-imp-build conf-bootstrap
    cd tools/conformance && \
    uv run pytest \
        tests/test_curl_impersonate_initialize.py \
        tests/test_curl_impersonate_navigate.py

# Build the curl-impersonate adapter image as
# `spectre-curl-impersonate:dev` via docker buildx bake. R6.1 §4.2
# routes every per-image build through bake so toolchain pins,
# OCI labels, and platform args stay uniform across the five
# images. Sources build/docker/versions.env first so RUST/GO/etc.
# pins flow through.
curl-imp-image:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load curl-impersonate

# Smoke-test the curl-impersonate adapter image. Per R6.1 §4.10:
# the adapter requires SPECTRE_ADAPTER_GRPC_PORT at runtime (no
# Compose / Helm machinery in the smoke), so a bare `docker run`
# exits 1 with the canonical "SPECTRE_ADAPTER_GRPC_PORT is
# required" message. The smoke greps for that exact text so a
# regression where the binary fails to load (missing proto
# bindings, wrong arch) surfaces as a different error.
#
# `docker run` exits 1 here on purpose — the justfile's pipefail
# shell would propagate that as failure, so we capture the output
# first and grep against the captured log.
curl-imp-image-smoke TAG='dev': curl-imp-image
    set +e; \
      out=$(docker run --rm --platform=linux/amd64 spectre-curl-impersonate:{{TAG}} 2>&1); \
      echo "$out"; \
      echo "$out" | grep -q 'SPECTRE_ADAPTER_GRPC_PORT is required'

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

# Build the Playwright adapter image as `spectre-playwright:dev` via
# docker buildx bake. R6.1 §4.2.
pw-image:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load playwright

# Smoke-test the Playwright adapter image. Per R6.1 §4.10: the
# image bakes SPECTRE_ADAPTER_GRPC_PORT=8091 as a default ENV so
# the adapter passes port resolution and proceeds to the Redis
# ping; that ping fails with the canonical "redis ping failed"
# message because no Redis is reachable in a bare `docker run`.
# The smoke greps for that exact text. Capture-then-grep avoids
# the justfile shell's pipefail propagating Node's exit-1.
pw-image-smoke TAG='dev': pw-image
    set +e; \
      out=$(docker run --rm --platform=linux/amd64 spectre-playwright:{{TAG}} 2>&1); \
      echo "$out" | tail -3; \
      echo "$out" | grep -q 'redis ping failed'

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

# Install ChromeDriver for SeleniumBase. Idempotent: SeleniumBase's
# `install chromedriver` recipe matches the local Chrome version
# and skips when an up-to-date driver is already in PATH. The
# conformance tests that exercise `Navigate` need this. See
# ADR-0014.
sb-install-chromedriver: sb-bootstrap
    cd adapters/seleniumbase && \
    .venv/bin/python -m seleniumbase install chromedriver

# Run only the SeleniumBase conformance tests. Useful for
# iterating on the Python adapter without re-running the
# Playwright suite.
sb-conf-test: sb-bootstrap sb-install-chromedriver conf-bootstrap
    cd tools/conformance && \
    uv run pytest \
        tests/test_seleniumbase_initialize.py \
        tests/test_seleniumbase_navigate.py \
        tests/test_seleniumbase_close.py \
        tests/test_seleniumbase_query.py \
        tests/test_seleniumbase_extract.py \
        tests/test_seleniumbase_screenshot.py

# Build the SeleniumBase adapter image as `spectre-seleniumbase:dev`
# via docker buildx bake. Slowest of the three adapter image builds
# (Chrome apt install + ChromeDriver download); buildx layer cache
# amortises subsequent builds. R6.1 §4.2.
sb-image:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load seleniumbase

# Smoke-test the SeleniumBase adapter image. Per R6.1 §4.10: the
# image bakes SPECTRE_ADAPTER_GRPC_PORT=8092 as a default ENV so
# the adapter passes port resolution and proceeds to the Redis
# ping; that ping fails with the canonical "redis ping" /
# "Connection refused" message because no Redis is reachable in
# a bare `docker run`. Capture-then-grep avoids the justfile
# shell's pipefail propagating Python's exit-1.
sb-image-smoke TAG='dev': sb-image
    set +e; \
      out=$(docker run --rm --platform=linux/amd64 spectre-seleniumbase:{{TAG}} 2>&1); \
      echo "$out" | tail -5; \
      echo "$out" | grep -qE 'redis ping|Connection refused|ConnectionError'

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
#   the suite exercises the Python adapter in parallel with
#   Playwright (ADR-0014).
# - the curl-impersonate adapter binary — the suite exercises the
#   Go adapter in parallel; the curl-impersonate
#   tests skip when the curl_chrome116 binary is not on PATH so
#   developers without the release tarball still get green
#   Playwright + SeleniumBase results (ADR-0016).
conf-test: pw-build pw-install-browsers sb-bootstrap sb-install-chromedriver curl-imp-build
    cd tools/conformance && uv run pytest

# ---------------------------------------------------------------------------
# Container images (R6.1 — see ADR-0018, docker-bake.hcl,
# build/docker/versions.env, docs/architecture/container-images.md)
# ---------------------------------------------------------------------------
# R6.1 routes every image build through `docker buildx bake`
# (docker-bake.hcl). The umbrella `images` / `images-smoke` /
# `images-clean` / `images-list` recipes act on the full set of
# five images (engine + control-plane + three adapters). Per-image
# recipes (`engine-image`, `op-image`, `pw-image`, `sb-image`,
# `curl-imp-image`) are thin bake-target wrappers preserved for
# muscle-memory + CI compatibility. R6.2 renamed the operator
# image recipe `op-build-image` → `op-image` for naming
# consistency; the old name is gone. Every recipe sources
# build/docker/versions.env first so toolchain pins flow
# uniformly.

# Build all five images via `docker buildx bake default`. First
# clean build pulls upstream bases (~3 GB total: Microsoft Playwright
# ~700 MB, Chrome apt ~200 MB, distroless ~30 MB, language toolchains)
# plus toolchain compile time — expect 15–25 minutes total. Subsequent
# rebuilds amortise via buildx's per-stage cache (~1–2 min when only
# source changes locally).
images:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load default

# Smoke-test all five images in sequence. Each per-image smoke
# matches its adapter / service shape (binary-exists check for
# engine + control-plane; canonical "port required" / "redis ping
# failed" error for the three adapters). See R6.1 §4.10.
images-smoke: engine-image-run op-image-smoke curl-imp-image-smoke pw-image-smoke sb-image-smoke

# `docker rmi` the five spectre-* images. Useful after a
# Dockerfile / versions.env change to force a clean rebuild that
# exercises the layer ordering from scratch.
images-clean:
    docker rmi -f \
      spectre-engine:dev \
      spectre-control-plane:dev \
      spectre-curl-impersonate:dev \
      spectre-playwright:dev \
      spectre-seleniumbase:dev \
      2>/dev/null || true

# Pretty-print the local spectre image set. Useful for verifying
# the size targets after a clean build. Uses the default
# `docker images` output rather than `--format` because Docker's
# Go template `{{...}}` delimiters collide with just's recipe
# interpolation syntax (the four-brace `{{{{...}}}}` escape only
# round-trips cleanly for opening braces).
images-list:
    @docker images "spectre-*"

# Build the engine Docker image as `spectre-engine:dev` via bake.
# Single-arch (linux/amd64) per ADR-0018 §5 reaffirmed for R6.1;
# multi-arch is release-engineering work (R7.1).
engine-image:
    set -a; source build/docker/versions.env; set +a; \
        docker buildx bake --load engine

# Smoke-test the engine image. R2.3 retired the `version` subcommand
# alongside the rest of the CLI surface; the binary is now a gRPC
# service entry point that reads SPECTRE_POSTGRES_URL at startup.
# A bare `docker run` exits 1 with the canonical
# "SPECTRE_POSTGRES_URL must be set" message because no Postgres
# is reachable in a one-shot smoke (Compose / Helm provide the
# env var). Capture-then-grep avoids the justfile shell's pipefail
# propagating the engine's exit-1. Distroless ships no shell or
# `test` binary so an "exists" probe is not available; the
# canonical-error path is the closest equivalent. Deeper end-to-
# end start-and-probe lives in R6.2's Compose stack work.
engine-image-run TAG='dev': engine-image
    set +e; \
      out=$(docker run --rm --platform=linux/amd64 spectre-engine:{{TAG}} 2>&1); \
      echo "$out"; \
      echo "$out" | grep -q 'SPECTRE_POSTGRES_URL must be set'

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
    rm -rf engines/engine/target
    rm -rf operators/control-plane/bin adapters/curl-impersonate/bin
    rm -rf adapters/playwright/{node_modules,dist}
    rm -rf adapters/seleniumbase/{.venv,dist} adapters/seleniumbase/**/__pycache__
    rm -rf tools/conformance/{.venv,dist} tools/conformance/**/__pycache__
    rm -rf proto/gen
    rm -rf adapters/playwright/src/proto
    find . -type d -name '.pytest_cache' -prune -exec rm -rf {} +
    find . -type d -name '.ruff_cache' -prune -exec rm -rf {} +
    find . -type d -name '.mypy_cache' -prune -exec rm -rf {} +
