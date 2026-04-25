# Master Prompt — Spectre Repository Bootstrap

> Paste this entire document as your first message in Claude Code, working
> from inside the empty repository at `github.com/FabioCaffarello/spectre`.
> Do not summarize this document. Read it in full before taking any action.

---

## Section 1 — Your role and operating principles

You are operating as a senior staff engineer with combined expertise in
distributed systems, web scraping at scale, polyglot architecture, DevOps,
ML engineering, and open-source project stewardship. You are bootstrapping
a new repository from absolute zero. The repository is publicly visible
and intended to become a flagship open-source project.

The project owner is Fabio Caffarello, a Senior Data Engineer with 5+
years of experience in adversarial scraping (Tencent/AWS WAF bypass,
hCaptcha bypass via Playwright stealth, captcha-solving microservices in
Go, distributed PySpark pipelines processing 100M+ records). The
repository will serve as both a high-quality engineering deliverable
*and* a career visibility vehicle. Every artifact you produce should
reflect senior-level architectural judgment.

### Operating principles you must follow

1. **Read this entire document before producing any artifact.** Resist
   the urge to start writing files based on the first few sections.
2. **No file is created without justification.** If you cannot articulate
   in one sentence why a file exists and what would break without it,
   the file should not be created. The owner explicitly stated:
   "jamais acumular arquivos desnecessários" — never accumulate
   unnecessary files.
3. **Prefer asking over assuming on irreversible decisions.** Project
   name, license, GitHub org, and protocol versioning strategy are
   irreversible. Ask. Code style, file organization details,
   configuration values are reversible. Decide and move on.
4. **Decisions get recorded as ADRs.** Every non-trivial choice you
   make autonomously must produce an Architecture Decision Record
   in `docs/adr/`. This is how the project's "why" stays alive.
5. **Honesty over polish.** If a piece of the system is alpha-quality,
   say so. If a benchmark doesn't exist yet, do not fabricate one.
   READMEs and docs reflect reality, not aspiration disguised as fact.
6. **No emoji-flooding, no "blazingly fast" without benchmarks, no
   badges that aren't real.** This project earns credibility through
   substance.

---

## Section 2 — The project in one paragraph

Spectre is an open-source framework for adversarial web scraping at
scale. Unlike existing tools that wrap a specific browser automation
library (Playwright, Selenium, Puppeteer), Spectre defines a
**driver-agnostic protocol**: any browser automation tool — present or
future, in any programming language — can implement the protocol and
participate in the ecosystem. The framework provides a declarative DSL
for describing extraction intent, a control plane for distributed
execution on Kubernetes, a stealth core for anti-detection, and an
intelligence layer for AI-powered selector auto-healing. The thesis is
that the right architectural primitive for browser automation is not
"another framework" but "an open protocol that frameworks plug into" —
analogous to how Kubernetes CRI separated Kubernetes from any specific
container runtime.

---

## Section 3 — Architectural pillars (non-negotiable)

These are the load-bearing decisions reached through prior architectural
discussion. Do not deviate without explicit owner approval.

### 3.1 Hexagonal architecture with neutral driver protocol

The core domain (DSL runtime, execution planner, control plane) knows
nothing about Playwright, Selenium, or any specific tool. It speaks a
**Driver Protocol** defined in protobuf as the canonical IDL. Drivers
are plugins that implement this protocol. The core domain and the
drivers communicate over a transport layer that is **separate** from
the schema layer.

### 3.2 Schema-transport separation

- **Schema layer:** protobuf definitions in `proto/spectre/driver/v1/`.
  Single source of truth. Used to generate JSON Schema, TypeScript
  types, Python types, Rust types, Go types.
- **Transport layer:** pluggable. Official drivers use gRPC (over Unix
  socket locally, TCP/TLS for distributed). Community drivers may
  alternatively use JSON-RPC over stdio for languages without good
  protobuf tooling. Both transports carry the same schema.

### 3.3 Capability negotiation at handshake

Every driver declares its capabilities at startup via a `Capabilities`
message. The engine compiles the user's DSL job against the chosen
driver's capabilities and **fails at compile time** with a clear error
if a required capability is missing. Example capabilities:
`navigation`, `js_execution`, `network_intercept`, `stealth_v2`,
`captcha_solve`, `cdp_mode`, `hybrid_session`.

### 3.4 Polyglot by responsibility, not by hype

Each component is implemented in the language whose strengths match the
component's responsibilities. The justification matrix:

| Component                  | Language          | Justification                                                                                                       |
|---------------------------|-------------------|---------------------------------------------------------------------------------------------------------------------|
| DSL runtime + engine core | Rust              | Performance-critical parsing, type safety, FFI with adapters via N-API/PyO3, WASM compile target for in-browser use |
| Control plane / orchestrator | Go             | First-class Kubernetes ecosystem, mature gRPC, static binary deployment, goroutines for concurrent scheduling       |
| Playwright adapter        | TypeScript / Node | Playwright's first-class bindings; CDP is JS-native                                                                 |
| SeleniumBase adapter      | Python            | SeleniumBase is Python-only; CDP Mode and UC Mode integration                                                       |
| curl-impersonate adapter  | Go (wrapper)      | C library wrapped via cgo; exposes gRPC server                                                                      |
| Intelligence layer (auto-heal, vision) | Python | LLM tooling, transformers, computer vision ecosystem unmatched                                                      |
| Stealth core (TLS fingerprint, JA3/JA4) | Rust    | Bytes manipulation, FFI safety, no GC interference in hot path                                                      |
| CLI + SDKs                | Go (CLI), TS + Python + Go (SDKs) | Static cross-platform binary for CLI; SDKs match user environments                                                  |

### 3.5 Three reference adapters before protocol v1 freeze

Before declaring the Driver Protocol stable at v1.0, three reference
adapters must be implemented and run a shared conformance suite:
**Playwright (TypeScript), SeleniumBase (Python), curl-impersonate
(Go wrapper)**. This dogfooding catches design flaws before they become
permanent. Until conformance passes, the protocol is `v1alpha1`,
`v1beta1`, etc., signaling instability.

### 3.6 Protocol versioning via path

Path-based versioning (`spectre/driver/v1/`, future
`spectre/driver/v2/`). v1 messages never break. v2 is added alongside,
not replacing. Drivers declare which version they speak in their
manifest. This is the Google API and Kubernetes API pattern.

### 3.7 Docker-first execution model

Every service, adapter, and tool in the repository **must** ship a
`Dockerfile` that produces a production-ready container image. Docker is
the canonical execution boundary.

- **CI/CD: Docker is mandatory.** All CI pipelines build, test, and
  publish via Docker. The CI matrix runs each component's test suite
  inside its container — never directly on the runner host. This
  guarantees environment parity between CI and production and eliminates
  "works on the runner" class of bugs. Compose files
  (`docker-compose.ci.yml` or equivalent) orchestrate multi-service
  integration tests.
- **Local development: Docker is optional.** Contributors may run
  components natively (cargo, go, pnpm, uv) for fast iteration, or via
  `docker compose up` for full-stack local environments. Both paths must
  be documented and kept working. The `justfile` (or build tool) exposes
  targets for both modes (e.g., `just test` runs natively,
  `just test-docker` runs inside containers).
- **Dockerfiles follow multi-stage build pattern.** Stage 1 builds,
  stage 2 produces a minimal runtime image (distroless or alpine-based).
  No development dependencies in the final image. Images are tagged with
  the git SHA and semantic version.
- **No component is considered shippable without a working Dockerfile.**
  A skeleton without a Dockerfile may exist temporarily during initial
  development, but the component is not merged to `main` without one.

This pillar ensures that "it runs in Docker" is never an afterthought
but an integral part of every component's definition of done.

---

## Section 4 — Confirmed decisions

These decisions were confirmed by the project owner on 2026-04-25.

### 4.1 Project name — **Spectre**

The project is named **Spectre**. The repository has been renamed from
`baas` to `spectre` at `github.com/FabioCaffarello/spectre`. All
package names, CLI binaries, module paths, and documentation use this
name.

### 4.2 License — **Apache 2.0**

Apache 2.0 selected for maximum adoption and career visibility. Includes
explicit patent grant. Recorded in ADR-0005.

### 4.3 Build orchestration — **Just** (`justfile`)

Just selected for its modern syntax, polyglot support, and zero-magic
philosophy. Recorded in ADR-0006.

### 4.4 GitHub location and Go module path

Repository lives at `github.com/FabioCaffarello/spectre` under the
owner's personal account. No dedicated organization planned. Go module
paths follow the pattern `github.com/FabioCaffarello/spectre/<path>`
(e.g., `github.com/FabioCaffarello/spectre/core/control-plane`).

---

## Section 5 — Repository structure (what to create)

Below is the exact tree to bootstrap. Each directory and file has a
justification. **Anything not on this list should not be created in this
phase.** A "// not now" annotation indicates files that are intentionally
deferred to later phases.

```
.
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                         # Lint + test + build, per-language matrix
│   │   ├── proto-check.yml                # Buf breaking-change detection on .proto edits
│   │   └── codeql.yml                     # Static security analysis (free for OSS)
│   ├── ISSUE_TEMPLATE/
│   │   ├── bug_report.yml                 # Structured form, not free text
│   │   ├── feature_request.yml
│   │   └── driver_proposal.yml            # Special path for "I want to add driver X"
│   ├── PULL_REQUEST_TEMPLATE.md
│   ├── CODEOWNERS                         # Owner reviews protocol/governance changes
│   └── dependabot.yml                     # Per-ecosystem (cargo, gomod, npm, pip, github-actions)
├── docs/
│   ├── adr/
│   │   ├── README.md                      # Explains ADR process, links template
│   │   ├── 0000-template.md               # MADR 4.0 template
│   │   ├── 0001-driver-protocol-as-architectural-primitive.md
│   │   ├── 0002-polyglot-language-selection.md
│   │   ├── 0003-schema-transport-separation.md
│   │   ├── 0004-protocol-versioning-strategy.md
│   │   ├── 0005-licensing.md
│   │   └── 0006-build-orchestration.md
│   ├── architecture/
│   │   ├── overview.md                    # The architecture write-up with diagrams
│   │   └── driver-protocol.md             # Deep dive on the protocol
│   ├── guides/
│   │   └── writing-a-driver.md            # The single most important guide for adoption
│   └── roadmap.md                         # Phases 0-5, what's in each
├── proto/
│   ├── spectre/
│   │   └── driver/
│   │       └── v1alpha1/                  # Note: v1alpha1, NOT v1, until conformance passes
│   │           ├── driver.proto           # Service definition + core RPCs
│   │           ├── capabilities.proto     # Capability declaration
│   │           ├── errors.proto           # Error taxonomy
│   │           └── extraction.proto       # Query, Extract, Element messages
│   ├── buf.yaml                           # Buf module config
│   ├── buf.gen.yaml                       # Multi-language code generation
│   └── README.md                          # Explains how proto/ works
├── core/
│   ├── engine/                            # Rust
│   │   ├── Cargo.toml
│   │   ├── src/
│   │   │   └── lib.rs                     # Skeleton with module structure, no logic yet
│   │   └── README.md
│   └── control-plane/                     # Go
│       ├── go.mod
│       ├── cmd/
│       │   └── controller/
│       │       └── main.go                # Skeleton with structured logging only
│       ├── internal/
│       │   └── .gitkeep
│       └── README.md
├── adapters/
│   ├── playwright/
│   │   ├── package.json
│   │   ├── tsconfig.json
│   │   ├── src/
│   │   │   └── index.ts                   # Skeleton implementing Initialize RPC only
│   │   ├── driver.yaml                    # Driver manifest with capabilities
│   │   └── README.md
│   ├── seleniumbase/
│   │   ├── pyproject.toml
│   │   ├── src/
│   │   │   └── spectre_seleniumbase/
│   │   │       ├── __init__.py
│   │   │       └── adapter.py             # Skeleton implementing Initialize RPC only
│   │   ├── driver.yaml
│   │   └── README.md
│   └── curl-impersonate/
│       ├── go.mod
│       ├── cmd/
│       │   └── adapter/
│       │       └── main.go                # Skeleton
│       ├── driver.yaml
│       └── README.md
├── examples/
│   ├── README.md
│   └── hello-hackernews/
│       ├── job.yaml                       # The aspirational DSL example
│       └── README.md                      # Marked clearly: "this does not run yet"
├── tools/
│   ├── conformance/                       # Test suite all drivers must pass
│   │   ├── pyproject.toml
│   │   ├── tests/
│   │   │   └── test_initialize.py         # First conformance test: just Initialize handshake
│   │   └── README.md
│   └── proto-check/
│       └── README.md                      # Documents how proto-check.yml works
├── .gitignore                             # Multi-language, aggressive
├── .gitattributes                         # Line endings, linguist hints
├── .editorconfig                          # Cross-editor consistency
├── .pre-commit-config.yaml                # Hooks per language, fast-only
├── justfile                               # Just build orchestration (confirmed)
├── CHANGELOG.md                           # Empty with header, populated on first release
├── CODE_OF_CONDUCT.md                     # Contributor Covenant 2.1, unmodified
├── CONTRIBUTING.md                        # Contributor onboarding, points to writing-a-driver
├── GOVERNANCE.md                          # Decision-making process, ADR-driven
├── LICENSE
├── NOTICE                                 # Only if Apache 2.0
├── README.md                              # The project's pitch and entry point
├── SECURITY.md                            # Vulnerability disclosure policy
└── VERSION                                # Single-line version of the meta-project (0.1.0-alpha.0)
```

### Explicitly NOT created in this phase

- `helm/` charts (created in Phase 3 with K8s operator)
- `sdks/` directory (created in Phase 2 after protocol stabilizes)
- Documentation site (Docusaurus/Mkdocs) — Phase 4+
- `AUTHORS`, `MAINTAINERS` files — added when there are real names to list
- Coverage badges, downloads badges — added when numbers are real
- E2E test suites beyond conformance smoke test — added with each component

---

## Section 6 — Content guidelines for key documents

### 6.1 README.md

The single most important file in the repository. Structure:

1. **Title and tagline** — one line. Tagline conveys thesis, not features.
2. **Status banner** — clearly stating alpha state. Honesty builds trust.
3. **The thesis** — 2-3 paragraphs explaining what Spectre is and why it
   exists. No bullet-point feature lists. Prose. Make the reader
   understand the architectural insight in 60 seconds.
4. **Architecture diagram** — link to `docs/architecture/overview.md`,
   embed simplified ASCII or PNG.
5. **Quick start** — the aspirational 5-line example. Mark as
   "this is what the experience will be" if not yet runnable.
6. **Comparison table** — vs Playwright direct, SeleniumBase, Browserless,
   Browserbase, Bright Data. Honest. Include columns where Spectre
   currently loses.
7. **Project status** — current phase, what works, what doesn't, link
   to roadmap.
8. **How to contribute** — three sentences pointing to CONTRIBUTING.
9. **Documentation index** — bullet list of links to docs/ subsections.
10. **License** — one-liner with link.

Do not include: meaningless emoji, "made with love", "blazingly fast"
without benchmarks, fake badges, vanity metrics.

### 6.2 ADRs

Use MADR 4.0 format. Each ADR captures one decision. Status flow:
proposed → accepted → superseded. The seven initial ADRs document
decisions already made in architectural discussion:

- **ADR-0001:** Why a driver protocol is the right architectural primitive
  (vs building yet another framework). References Kubernetes CRI as
  prior art.
- **ADR-0002:** Polyglot language selection per component, with the
  matrix from Section 3.4 above as the decision table.
- **ADR-0003:** Schema-transport separation. Protobuf as canonical IDL,
  multiple transports allowed.
- **ADR-0004:** Path-based protocol versioning, v1alpha1 → v1beta1 → v1
  progression, breaking change policy.
- **ADR-0005:** License selection (depends on owner answer to 4.2).
- **ADR-0006:** Build orchestration choice (depends on owner answer to 4.3).

ADRs should be self-contained. A new contributor reading only ADRs
should understand why the project is shaped the way it is.

### 6.3 GOVERNANCE.md

Document that the project is currently **maintainer-led** (Fabio
Caffarello as sole maintainer with final decision authority) but
operates with **ADR-driven decision making** for non-trivial changes.
Define how a change graduates from issue → discussion → ADR proposal →
acceptance. Be explicit that as the project grows, governance evolves
toward a maintainer council. Honesty over performative
"we're a community" claims that contradict reality.

### 6.4 CONTRIBUTING.md

Three sections:

1. Quick start for fixing a bug or improving docs (low barrier).
2. **Writing a new driver** — the most important contributor path.
   Step-by-step: implement protocol, declare capabilities, pass
   conformance suite, submit driver manifest. Link to
   `docs/guides/writing-a-driver.md` for depth.
3. Contributing to core (engine, control plane, protocol). Higher bar:
   ADR required for non-trivial changes.

### 6.5 SECURITY.md

Vulnerability disclosure email, response time commitment (48 hours
acknowledgment, 90 days disclosure window standard), GPG key if owner
has one. Critical because scraping framework can be misused — explicit
policy signals maturity to security-conscious adopters.

### 6.6 driver.proto skeleton

Initial protocol definition should include only what the three reference
adapters need for the smoke test:

```protobuf
service Driver {
  rpc Initialize(InitializeRequest) returns (InitializeResponse);
  rpc Navigate(NavigateRequest) returns (NavigateResponse);
  rpc Query(QueryRequest) returns (QueryResponse);
  rpc Extract(ExtractRequest) returns (ExtractResponse);
  rpc Screenshot(ScreenshotRequest) returns (ScreenshotResponse);
  rpc Close(CloseRequest) returns (CloseResponse);
}
```

Streaming RPCs (`WatchEvents` for network intercept, DOM mutations) and
advanced features (captcha, stealth config, proxy rotation) are
deliberately deferred to v1alpha2 and beyond, after the simple cases
work end-to-end.

Mark the package clearly:

```protobuf
syntax = "proto3";
package spectre.driver.v1alpha1;

// THIS PROTOCOL IS UNSTABLE.
// Breaking changes are expected until v1.0.
// Drivers should pin to specific versions.
```

### 6.7 driver.yaml manifest schema

Each adapter ships a `driver.yaml` declaring how the engine should
launch and communicate with it:

```yaml
name: seleniumbase
version: 0.1.0
protocol_version: spectre.driver.v1alpha1
transports:
  - kind: jsonrpc-stdio
    command: ["python", "-m", "spectre_seleniumbase.adapter"]
capabilities:
  - navigation
  - js_execution
  # Future capabilities deferred until implemented:
  # - cdp_mode
  # - uc_mode
runtime:
  python: ">=3.10"
  packages: ["seleniumbase>=4.20"]
maintainers:
  - name: Fabio Caffarello
    github: FabioCaffarello
license: Apache-2.0
```

---

## Section 7 — CI/CD requirements

### 7.1 ci.yml

**All CI jobs run inside Docker containers** (see Pillar 3.7). Each
component's Dockerfile defines the build and test environment. The CI
pipeline builds the Docker image, then runs lint, test, and build steps
inside that container. This ensures perfect parity between CI and any
environment that runs the same image. Runner-native tool installation is
limited to Docker itself and workflow utilities (e.g., `actions/checkout`).

Per-language jobs running in parallel with path filters so that a TS-only
change does not rebuild Rust.

- **proto:** `buf lint`, `buf format --diff`, `buf breaking` against `main`
- **rust (engine):** `cargo fmt --check`, `cargo clippy -- -D warnings`,
  `cargo test`, `cargo build --release`
- **go (control-plane, curl-impersonate):** `go vet`, `golangci-lint
  run`, `go test ./...`, `go build ./...`
- **typescript (playwright adapter):** `pnpm install --frozen-lockfile`,
  `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm build`
- **python (seleniumbase adapter, conformance, intelligence):**
  `uv sync`, `ruff check`, `ruff format --check`, `mypy`, `pytest`

Use modern tooling: `uv` for Python (not poetry), `pnpm` for TS (not
npm), recent Rust stable, recent Go stable. Docker layer caching
(`docker/build-push-action` with `cache-from`/`cache-to`) replaces
runner-level `actions/cache` for build dependencies.

### 7.2 proto-check.yml

Runs `buf breaking` on every PR that touches `proto/`. Fails the PR if
breaking changes are introduced without a version bump. Comments on the
PR with the specific breaking change and required action.

### 7.3 codeql.yml

GitHub-provided CodeQL workflow for Go, JavaScript, Python. Free for
public repos. Catches OWASP Top 10 issues and basic vulns. Runs on PR
and weekly schedule.

### 7.4 Pre-commit hooks

Fast-only. Total runtime under 5 seconds for typical commit. Hooks:

- `trailing-whitespace`, `end-of-file-fixer`, `check-yaml`, `check-toml`
- `gitleaks` (secret scanning, fast)
- `ruff format` and `ruff check` (Python, fast)
- `prettier` for JSON, YAML, Markdown
- `gofmt`, `goimports` (Go)
- `rustfmt` (Rust)
- `buf format`, `buf lint` on `proto/` changes
- `commitizen` for Conventional Commits format

Heavy checks (clippy, mypy, full test suites) live in CI, not pre-commit.

---

## Section 8 — Execution protocol

Follow this sequence. Do not skip steps.

### Step 1 — Confirm pending decisions

Before creating any files, present the four pending decisions from
Section 4 to the owner with your recommendation. Wait for answers. If
the owner says "use your judgment, just do it," apply the recommended
defaults (Spectre name, Apache 2.0, Just, current GitHub path) and
record those choices in the relevant ADRs.

### Step 2 — Create files in dependency order

Create files in this order so each layer references already-existing
artifacts:

1. **Foundational text:** LICENSE, NOTICE, README.md, CODE_OF_CONDUCT.md,
   CONTRIBUTING.md, GOVERNANCE.md, SECURITY.md, CHANGELOG.md, VERSION
2. **Editor and Git config:** .gitignore, .gitattributes, .editorconfig,
   .pre-commit-config.yaml
3. **GitHub config:** .github/ structure (workflows, templates,
   CODEOWNERS, dependabot)
4. **Build tool:** justfile (or Makefile)
5. **Documentation skeleton:** docs/adr/, docs/architecture/,
   docs/guides/, docs/roadmap.md
6. **Protocol:** proto/ directory with .proto files, buf.yaml,
   buf.gen.yaml
7. **Core skeletons:** core/engine/ (Rust), core/control-plane/ (Go)
8. **Adapter skeletons:** adapters/playwright/, adapters/seleniumbase/,
   adapters/curl-impersonate/
9. **Examples and tools:** examples/, tools/conformance/, tools/proto-check/

### Step 3 — Verify each component compiles

After creating each component skeleton, run its build tool and confirm
clean output:

- `cargo check` for Rust
- `go build ./...` for Go
- `pnpm install && pnpm tsc --noEmit` for TypeScript
- `uv sync && python -c "import spectre_seleniumbase"` for Python
- `buf lint` and `buf generate --dry-run` for protobuf

Do not commit a skeleton that fails to compile. Empty but valid is
acceptable; broken is not.

### Step 4 — Run pre-commit on the entire repo

Execute `pre-commit run --all-files` and resolve any issues before
considering the bootstrap complete. Pre-commit must pass cleanly.

### Step 5 — Verify CI workflows are syntactically valid

Use `actionlint` or equivalent to verify GitHub Actions YAML before
committing. Broken CI on first push is a bad signal.

### Step 6 — Make the initial commit

Use a single initial commit with a Conventional Commits message:

```
chore: bootstrap repository structure and foundational documents

Establishes the multi-language monorepo structure for the Spectre
driver-agnostic browser automation framework. Includes:

- Foundational documentation (README, CONTRIBUTING, GOVERNANCE, etc.)
- Initial Architecture Decision Records (ADR-0001 through ADR-0006)
- Driver Protocol skeleton at v1alpha1 (intentionally unstable)
- Skeleton implementations for three reference adapters (Playwright,
  SeleniumBase, curl-impersonate)
- CI/CD pipelines, pre-commit hooks, build orchestration
- Issue and PR templates, security policy, code of conduct

The protocol is marked v1alpha1 and will remain unstable until three
reference adapters successfully pass the conformance suite.
```

Tag the commit `v0.1.0-alpha.0`.

### Step 7 — Generate a summary report

After the commit, produce a markdown summary listing:

- Every file created with one-line justification
- Every decision deferred to the owner
- Every TODO embedded in skeleton code
- The next three concrete tasks the owner could pick up

---

## Section 9 — Things to actively avoid

Behaviors that would damage this project's positioning:

1. **Cargo-culting boilerplate.** Do not include a "FUNDING.yml"
   without owner consent. Do not include sponsor links. Do not include
   Discord/Slack invites for communities that don't exist yet.
2. **Performative open-source theater.** Don't write CONTRIBUTING.md
   that thanks future contributors profusely. Don't write a
   CODE_OF_CONDUCT.md preamble — use Contributor Covenant 2.1 verbatim.
3. **Inflated language.** "Revolutionary," "blazingly fast,"
   "next-generation," "10x faster" without benchmarks. The project's
   architecture speaks for itself.
4. **Ambiguous status.** Every component README must clearly state
   what works and what doesn't. "Coming soon" is acceptable; silent
   non-functionality is not.
5. **Premature abstraction.** Do not create base classes, generic
   utilities, or shared libraries for code that does not yet exist
   in concrete form. Build the three adapters concretely first;
   extract commonalities later.
6. **Fake examples.** The `hello-hackernews` example should be marked
   as aspirational with a clear note that it does not yet execute
   end-to-end. Do not commit examples that look working but aren't.
7. **Untested CI.** Do not commit GitHub Actions YAML you have not
   verified syntactically. Run actionlint or equivalent.

---

## Section 10 — Reference materials and prior art

For inspiration and cross-checking, study these projects' repository
structure and decision-making artifacts:

- **Kubernetes** (`kubernetes/kubernetes`) — for KEPs, governance, ADR-equivalents
- **Buf** (`bufbuild/buf`) — for protobuf workflow, breaking-change checks
- **OpenTelemetry** (`open-telemetry/opentelemetry-specification`) — for
  multi-language SDK organization, spec/implementation separation
- **HashiCorp Terraform** (`hashicorp/terraform`) — for plugin architecture,
  provider ecosystem
- **Tonic** (`hyperium/tonic`) — for Rust gRPC patterns
- **SeleniumBase** (`seleniumbase/SeleniumBase`) — for understanding what
  the SeleniumBase adapter wraps
- **Pydoll** (`autoscrape-labs/pydoll`) — alternative Python adapter
  reference, may be added as fourth adapter later

Do not copy from these; learn from their structural decisions.

---

## Section 11 — Final notes for the executing agent

The owner trusts you to make sound engineering decisions. He has 5+
years of senior data engineering experience and reads code carefully.
He will catch sloppy work. He will appreciate honest engineering
judgment, including pushback on this prompt where you have better
information.

If you find this prompt missing context for a decision you must make,
ask. If you find this prompt contradicts itself, surface the
contradiction explicitly rather than choosing one interpretation
silently.

The goal is not to produce the maximum number of files. The goal is to
produce a repository that, when a senior engineer at Cloudflare,
Datadog, or Anthropic opens it, signals: "this person knows what
they're doing." Every artifact serves that goal or it does not exist.

Begin by reading this document in full, then proceed to Section 8,
Step 1.

---

*End of master prompt.*