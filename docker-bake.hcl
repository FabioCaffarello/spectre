// docker-bake.hcl — single orchestrator for the five Spectre images.
//
// Replaces the ad-hoc `just engine-image && just op-build-image && …`
// chaining that grew through R3.1 / R5.1. Five targets (engine,
// control-plane, curl-impersonate, playwright, seleniumbase), three
// groups (default, core, adapters), ten variables, two functions.
//
// Build context for every target is the repository root because every
// service's build regenerates protocol bindings inside its codegen
// stage from `proto/` (gitignored — ADR-0007). The repo-root
// `.dockerignore` keeps the context efficient.
//
// Variable resolution: bake reads variables from the environment at
// invocation time (`${env.RUST_VERSION}`-style references not needed —
// `variable {}` blocks pick up env vars of the same name
// automatically). Source `build/docker/versions.env` before invoking
// bake so toolchain pins flow uniformly:
//
//   set -a; source build/docker/versions.env; set +a
//   docker buildx bake [--load] [<group-or-target>]
//
// The `just images` umbrella recipe wraps that flow.
//
// Per-target `platforms = ["linux/amd64"]` is the default. R6.5.3
// adds Docker Hub publishing via `.github/workflows/publish.yml`
// (`workflow_dispatch` only); the publish workflow opts the two
// multi-arch-ready images into `linux/amd64,linux/arm64` per-target
// via bake's `--set <target>.platform=...` overrides — bake stays
// minimal-by-default, deployment chooses the platform set
// (ADR-0018 §5 R6.5.3 update §4.3). The other three images
// (engine, seleniumbase, curl-impersonate) carry forward-readiness
// comments in their Dockerfiles plus per-image deferral analysis
// in ADR-0018 §5 R6.5.3 update.
//
// CI sets `VCS_REF` and `BUILD_DATE`; local builds leave them empty
// (resulting in empty `org.opencontainers.image.{revision,created}`
// label values, which is acceptable for local artefacts).
//
// Refs: R6.1 phase prompt §4.2 (bake as orchestrator), §4.3 (runtime
// base matrix), §4.4 (OCI labels via bake); ADR-0018 §5 R6.5.3
// update (Docker Hub registry + multi-arch reality).

// ---------------------------------------------------------------------------
// Variables
// ---------------------------------------------------------------------------

variable "TAG" {
  default = "dev"
}

// REGISTRY = "" (default) produces local-only image names like
// `spectre-engine:dev`. R6.5.3 set the publish target to Docker
// Hub flat namespace: `REGISTRY=fabiocaffarello` produces
// `fabiocaffarello/spectre-engine:<tag>` (Docker resolves the bare
// owner reference to `docker.io/fabiocaffarello/...`
// automatically). The `image()` function below is registry-
// agnostic — any non-empty value works (alternative public
// registries, a private registry, etc.). See ADR-0018 §5 R6.5.3
// update for the pivot rationale.
variable "REGISTRY" {
  default = ""
}

variable "RUST_VERSION" {
  default = "1.88"
}

variable "GO_VERSION" {
  default = "1.25"
}

variable "NODE_VERSION" {
  default = "20"
}

variable "PYTHON_VERSION" {
  default = "3.12"
}

variable "PROTOC_VERSION" {
  default = "27.2"
}

variable "BUF_VERSION" {
  default = "1.55.1"
}

variable "UV_VERSION" {
  default = "0.5.11"
}

variable "PLAYWRIGHT_VERSION" {
  default = "1.59.1"
}

variable "CURL_IMPERSONATE_IMAGE" {
  default = "lwthiker/curl-impersonate:0.6-chrome"
}

// CI sets these; local builds leave them empty.
variable "VCS_REF" {
  default = ""
}

variable "BUILD_DATE" {
  default = ""
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

// image returns the fully-qualified image reference. Local builds
// (REGISTRY="") produce `spectre-<name>:<tag>`; pushed builds prefix
// the registry.
function "image" {
  params = [name]
  result = REGISTRY == "" ? "spectre-${name}:${TAG}" : "${REGISTRY}/spectre-${name}:${TAG}"
}

// labels returns the OCI image annotation map. Injected uniformly
// across every target so contributors don't repeat the schema in
// each Dockerfile (§4.4 + §9.3).
function "labels" {
  params = [title, description]
  result = {
    "org.opencontainers.image.title"       = title
    "org.opencontainers.image.description" = description
    "org.opencontainers.image.vendor"      = "Spectre"
    "org.opencontainers.image.licenses"    = "Apache-2.0"
    "org.opencontainers.image.source"      = "https://github.com/FabioCaffarello/spectre"
    "org.opencontainers.image.revision"    = VCS_REF
    "org.opencontainers.image.created"     = BUILD_DATE
    "org.opencontainers.image.version"     = TAG
  }
}

// ---------------------------------------------------------------------------
// Targets
// ---------------------------------------------------------------------------

// Shared codegen tooling target — consumed by the four buf-using
// images via `contexts:`. R6.5.4 extracts the buf install logic
// out of each consumer Dockerfile into a single source. The
// `output = ["type=cacheonly"]` clause keeps this image in
// BuildKit's cache without loading it to the daemon or pushing
// it; consumers reference it as a named build context.
//
// Platforms intentionally omitted: when called via `contexts:`,
// bake builds buf-base at the consumer's requested platform(s)
// transparently. When called standalone (debugging), bake uses
// the host platform (single-arch).
//
// Refs: ADR-0018 §3 R6.5.4 update; build/docker/README.md
// "Shared codegen base" section.
target "buf-base" {
  context    = "."
  dockerfile = "build/docker/buf-base.Dockerfile"
  args = {
    BUF_VERSION = BUF_VERSION
  }
  output = ["type=cacheonly"]
}

target "engine" {
  context    = "."
  dockerfile = "engines/engine/Dockerfile"
  tags       = [image("engine")]
  labels     = labels(
    "spectre-engine",
    "Spectre engine: DSL parsing, planning, gRPC service, and execution",
  )
  args = {
    RUST_VERSION   = RUST_VERSION
    PROTOC_VERSION = PROTOC_VERSION
  }
  platforms = ["linux/amd64"]
}

target "control-plane" {
  context    = "."
  dockerfile = "operators/control-plane/Dockerfile"
  contexts = {
    buf-base = "target:buf-base"
  }
  tags       = [image("control-plane")]
  labels     = labels(
    "spectre-control-plane",
    "Spectre control plane: kubebuilder operator reconciling ScrapeJob CRDs",
  )
  args = {
    GO_VERSION  = GO_VERSION
    BUF_VERSION = BUF_VERSION
  }
  platforms = ["linux/amd64"]
}

target "curl-impersonate" {
  context    = "."
  dockerfile = "adapters/curl-impersonate/Dockerfile"
  contexts = {
    buf-base = "target:buf-base"
  }
  tags       = [image("curl-impersonate")]
  labels     = labels(
    "spectre-curl-impersonate",
    "Spectre curl-impersonate adapter: TLS-fingerprint HTTP fetcher",
  )
  args = {
    GO_VERSION             = GO_VERSION
    BUF_VERSION            = BUF_VERSION
    CURL_IMPERSONATE_IMAGE = CURL_IMPERSONATE_IMAGE
  }
  platforms = ["linux/amd64"]
}

target "playwright" {
  context    = "."
  dockerfile = "adapters/playwright/Dockerfile"
  contexts = {
    buf-base = "target:buf-base"
  }
  tags       = [image("playwright")]
  labels     = labels(
    "spectre-playwright",
    "Spectre Playwright adapter: Chromium browser-automation driver",
  )
  args = {
    NODE_VERSION       = NODE_VERSION
    BUF_VERSION        = BUF_VERSION
    PLAYWRIGHT_VERSION = PLAYWRIGHT_VERSION
  }
  platforms = ["linux/amd64"]
}

target "seleniumbase" {
  context    = "."
  dockerfile = "adapters/seleniumbase/Dockerfile"
  contexts = {
    buf-base = "target:buf-base"
  }
  tags       = [image("seleniumbase")]
  labels     = labels(
    "spectre-seleniumbase",
    "Spectre SeleniumBase adapter: Chrome + ChromeDriver browser-automation driver",
  )
  args = {
    PYTHON_VERSION = PYTHON_VERSION
    BUF_VERSION    = BUF_VERSION
    UV_VERSION     = UV_VERSION
  }
  platforms = ["linux/amd64"]
}

// ---------------------------------------------------------------------------
// Groups — bake's grouping primitive lets R6.2 Compose rebuild only
// adapters when adapter source changes, and R7.1 Helm-publishing push
// every image at a release tag.
// ---------------------------------------------------------------------------

group "default" {
  targets = ["engine", "control-plane", "curl-impersonate", "playwright", "seleniumbase"]
}

group "core" {
  targets = ["engine", "control-plane"]
}

group "adapters" {
  targets = ["curl-impersonate", "playwright", "seleniumbase"]
}
