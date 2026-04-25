---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Build orchestration (Just)

## Context and Problem Statement

Spectre is a polyglot project (ADR-0002) with at least five top-level
toolchains (Rust, Go, TypeScript, Python, protobuf). Contributors and
CI jobs need a single entry point for the common verbs — bootstrap,
format, lint, test, build — that fans out to per-language tooling.

## Decision Drivers

- One command surface across languages: `just bootstrap`, `just check`.
- Trivial install on every developer platform.
- Recipes readable at a glance; no implicit shell quoting traps.
- Friendly to per-component invocation
  (`just engine-test`, `just pw-build`).
- Survives the project's growth without becoming a build system in
  its own right.

## Considered Options

- **Option A — `make`** with a recursive Makefile.
- **Option B — `just`** (justfile).
- **Option C — Bazel**.
- **Option D — Nx**.
- **Option E — Plain shell scripts in `scripts/`**.

## Decision Outcome

Chosen option: **Option B — Just (`justfile`)**.

Just is a modern command runner that fits the project's needs:

- Recipes are first-class; sub-recipes and aggregates compose cleanly.
- Single static binary; install is one line on every platform.
- No tab/whitespace fragility (Make's classic foot-gun).
- Recipes use bash by default with a configurable shebang. Spectre's
  `justfile` opts in to `bash -eu -o pipefail` for fail-fast behaviour.
- Per-component recipes (`engine-*`, `cp-*`, `pw-*`, `sb-*`,
  `curl-imp-*`, `conf-*`, `proto-*`) so contributors iterate locally
  on a single area.

### Consequences

- Good, because the entry-point surface is uniform across languages.
- Good, because `just --list` is a self-documenting menu of available
  recipes.
- Good, because per-component recipes keep CI and local development
  in sync (CI runs the same commands a contributor runs locally).
- Bad, because `just` is one more tool to install. Mitigated by the
  trivial install (one-line `brew install just` or upstream installer)
  and by documenting it in CONTRIBUTING.md as a required tool.
- Neutral, because if the project later outgrows a flat justfile, Just
  supports modules (`mod foo 'path/to/justfile'`) so per-component
  justfiles can be introduced without a rewrite.

### Confirmation

- `just --list` produces the expected recipe menu.
- The CI workflows invoke the same recipes a contributor uses locally
  (no command-line drift).
- `just check` reproduces the CI quality gate locally.

## Pros and Cons of the Options

### Option A — Make

- Good, because installed everywhere.
- Bad, because POSIX Make has well-known foot-guns (tab vs. space,
  implicit rules) that confuse new contributors.
- Bad, because variable scoping and conditional logic are awkward in
  recursive Makefiles.

### Option C — Bazel

- Good, because excellent for large polyglot monorepos with strict
  hermeticity requirements.
- Bad, because the upfront cost (BUILD files for every package, custom
  rules for niche tooling like `buf`) is excessive for current scale.
- Neutral, because Bazel can be revisited at the 50-component scale.

### Option D — Nx

- Good, because strong support for TypeScript monorepos.
- Bad, because TS-centric; Rust, Go, and Python integration is
  community-driven and lags upstream tooling.

### Option E — Plain shell scripts

- Good, because zero new dependencies.
- Bad, because there is no convention for listing or composing
  scripts; new contributors will not know what is available.
- Bad, because aggregates (running format across all components)
  have to be re-implemented in each script.

## More Information

- Just: <https://github.com/casey/just>
- Bazel rules-the-world thread on small-vs-large polyglot tooling
  trade-offs: <https://bazel.build/concepts/build-files>
- Related: [ADR-0002 Polyglot language selection](0002-polyglot-language-selection.md).
