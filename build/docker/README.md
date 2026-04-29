# `build/docker/`

Build-time configuration shared by every Spectre image: the
toolchain pins, and (post-R6.5.4) the shared codegen base
Dockerfile.

R6.5.1 ships only the `versions.env` documentation and the
coherence check. Future R6.5.x PRs will extend this directory:

- R6.5.4 (Dockerfile deduplication) introduces a shared
  `codegen/Dockerfile` stage so the buf install does not repeat
  in four Dockerfiles.

---

## What lives here

| File | Purpose |
|------|---------|
| `versions.env` | Canonical toolchain pins. Single source of truth. |

---

## The `versions.env` contract

`versions.env` is a flat, POSIX shell-sourceable file
(`KEY=VALUE`, `#` comments). Every consumer reads it without a
YAML/TOML parser:

```bash
set -a
source build/docker/versions.env
set +a
```

After that, `RUST_VERSION`, `GO_VERSION`, … are ordinary
environment variables.

### Two consumers, one file

1. **`docker buildx bake`** — `docker-bake.hcl` declares
   `variable "FOO" { default = "..." }` blocks for every pin.
   Bake reads `FOO` from the environment if set, falling back
   to the block's `default`. The `just images` umbrella sources
   `versions.env` before invoking bake, so contributor-driven
   image builds use the canonical pins.
2. **Direct `docker build` invocations** — each Dockerfile
   independently carries `ARG FOO=<value>` defaults *matching*
   the `versions.env` value. A contributor running
   `docker build -f adapters/playwright/Dockerfile .` directly
   (without bake) gets the same toolchain by default.

The duplication is intentional and load-bearing: the Dockerfile
stays buildable in isolation, while bake runs orchestrate the
whole matrix from one shell variable.

### Why two of the five Dockerfiles omit the ARG default

`core/engine/Dockerfile` (`RUST_VERSION`, `PROTOC_VERSION`) and
`core/control-plane/Dockerfile` (`GO_VERSION`, `BUF_VERSION`)
declare `ARG <NAME>` *without* a default. They rely on bake (or
an explicit `--build-arg`) to inject the value. The intent: the
core images are not expected to be built directly outside the
bake pipeline — bumping them is a coordinated maintainer-side
operation, not a contributor experiment. The three adapter
Dockerfiles favour direct-build ergonomics and carry the
defaults inline.

The coherence script (below) checks both styles where they
apply.

### Enforcement: `tools/build/check-versions-coherent.sh`

The script is the executable contract that `versions.env`,
`docker-bake.hcl`, and the Dockerfile `ARG` defaults all agree.
It runs as the first step of CI's `proto` job and is wired into
`just check` via `just check-versions`.

If a contributor edits `versions.env` without updating the
matching bake variable default and Dockerfile ARG, the script
fails with a per-mismatch error line and the build does not
proceed.

---

## How to bump a toolchain version

Three places to edit, plus two verifications:

1. Edit `build/docker/versions.env` — change the value.
2. Edit `docker-bake.hcl` — update the `default` of the matching
   `variable` block.
3. Edit every Dockerfile that carries an `ARG <NAME>=<value>`
   default for this pin (consult the inventory table below).
4. Run `just check-versions` — must exit zero.
5. Run `just images` to rebuild; spot-check with
   `just compose-up` or component-specific tests.

The script is the safety net for steps 2 and 3.

---

## Pin inventory

Every variable in `versions.env`, what it pins, where the
matching bake variable default lives, and which Dockerfile ARGs
it backs.

| Variable | Default | Bake default | Dockerfile ARG defaults | Cadence |
|----------|---------|--------------|-------------------------|---------|
| `RUST_VERSION` | `1.88` | `docker-bake.hcl` | `core/engine/Dockerfile` (no default; bake-injected) | quarterly |
| `GO_VERSION` | `1.25` | `docker-bake.hcl` | `core/control-plane/Dockerfile` (no default; bake-injected); `adapters/curl-impersonate/Dockerfile` (default present) | semiannual |
| `NODE_VERSION` | `20` | `docker-bake.hcl` | `adapters/playwright/Dockerfile` (default present) | yearly LTS |
| `PYTHON_VERSION` | `3.12` | `docker-bake.hcl` | `adapters/seleniumbase/Dockerfile` (default present) | yearly |
| `PROTOC_VERSION` | `27.2` | `docker-bake.hcl` | `core/engine/Dockerfile` (no default; bake-injected) | quarterly |
| `BUF_VERSION` | `1.55.1` | `docker-bake.hcl` | `core/control-plane/Dockerfile` (no default; bake-injected); `adapters/curl-impersonate/Dockerfile`, `adapters/playwright/Dockerfile`, `adapters/seleniumbase/Dockerfile` (defaults present) | weekly |
| `UV_VERSION` | `0.5.11` | `docker-bake.hcl` | `adapters/seleniumbase/Dockerfile` (default present) | weekly |
| `PLAYWRIGHT_VERSION` | `1.49.0` | `docker-bake.hcl` | `adapters/playwright/Dockerfile` (default present); also matches the `playwright` npm dependency | minor monthly |
| `CURL_IMPERSONATE_IMAGE` | `lwthiker/curl-impersonate:0.6-chrome` | `docker-bake.hcl` | `adapters/curl-impersonate/Dockerfile` (default present) | as upstream |
| `CHROME_VERSION` | `131.0.6778.85` | _(informational)_ | _(none — Chrome is installed at runtime by the SeleniumBase adapter image via Google's apt repo)_ | as upstream |

### Notes

- `CHROME_VERSION` is documented for reference only. The
  SeleniumBase adapter image installs `google-chrome-stable`
  from Google's apt repo; the runtime version drifts with
  upstream and the adapter calls `seleniumbase install
  chromedriver` at image build time so the bundled driver
  matches whichever Chrome ended up installed. The pin exists
  to give contributors a baseline expectation, not to gate the
  build.
- The Devcontainer (`.devcontainer/Dockerfile`) carries its own
  ARG block. The pins are duplicated there so the devcontainer
  image is buildable in offline / pre-versions.env context, and
  the values are kept in lockstep with `versions.env` by code
  review (R6.3 introduced this duplication; ADR-0018 §3a R6.3
  evolution).
- The `playwright` npm dep in `adapters/playwright/package.json`
  must match `PLAYWRIGHT_VERSION`. Bumping one without the
  other causes Chromium / API drift; the `playwright` package
  upgrade and the `versions.env` edit go in the same commit.
- CI consumes the same pins via the same bake invocation. R6.5.2
  routed every CI image build through `set -a; source
  build/docker/versions.env; set +a; docker buildx bake --load
  <target>` — local and CI are byte-for-byte aligned, so a
  toolchain bump merged here flows through to CI without a
  workflow edit.

---

## Cross-references

- ADR-0018 §3 — engine image build (the precedent for
  versioned multi-stage builds).
- ADR-0018 §3a R6.3 evolution — devcontainer adopting matching
  pins.
- ADR-0025 §7 — port migration touched the same Dockerfiles.
- `docker-bake.hcl` header comment — the variable resolution
  rules and the bake invocation pattern.
- `docs/architecture/container-images.md` — the high-level
  architecture document that introduces this directory.
