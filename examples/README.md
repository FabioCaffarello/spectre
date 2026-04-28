# Examples

Sample jobs for Spectre. Each example lives in its own directory
with a `job.yaml` and a `README.md`. The `job.yaml` files are the
canonical DSL artefacts; their semantics are unchanged across the
microservices refactor.

## Status (R2.3 era)

The engine is a gRPC service after R2.3 (ADR-0020 §3); the
`spectre run` CLI subcommand is gone. End-to-end invocation
against the running stack is currently a manual flow: start the
engine, start the relevant adapter, send the DSL via `grpcurl`.
Cleaner ergonomics arrive in two later phases:

- **R3.1** lands `kubectl apply -f scrapejob.yaml` against a
  Helm-installed cluster. The control plane becomes a gRPC
  client of the engine.
- **R6.2** lands `docker compose up` plus a
  `just example-<name>` recipe per sample. The Compose stack
  becomes the canonical local-development workflow.

Until then, the per-example READMEs document the manual
`grpcurl` flow honestly.

## Index

- [hello-hackernews](hello-hackernews) — Playwright (Node).
  Extract titles and URLs from the Hacker News front page.
  Phase 1 reference example.
- [seleniumbase-extract](seleniumbase-extract) — SeleniumBase
  (Python). Same DSL shape as `hello-hackernews`, different
  driver and runtime.
- [curl-impersonate-extract](curl-impersonate-extract) —
  curl-impersonate (Go). Mirrors `seleniumbase-extract`
  byte-for-byte except for the `driver:` field; the third leg
  of the cross-driver equivalence demo.

R2.3 retired the older navigate-only demos
(`seleniumbase-navigate`, `curl-impersonate-fetch`) — they
existed to demonstrate the legacy CLI's "minimum viable adapter
run" and the `*-extract` examples cover the same adapters with
richer functionality.

## Cross-driver equivalence demo

The three surviving examples — `hello-hackernews`,
`seleniumbase-extract`, and `curl-impersonate-extract` — use the
same DSL shape against three runtimes in three languages. The
shared shape exercises only the four-capability intersection of
all three drivers: `navigation`, `query_css`, `extract_text`,
`extract_attribute`. Running the three jobs against the same
engine service is the visible artefact for Phase 2's exit
criterion: *the same `job.yaml` runs unchanged across all three
adapters where their capabilities allow*.

`hello-hackernews` targets a richer page (Hacker News) and
demonstrates the full DSL; `seleniumbase-extract` and
`curl-impersonate-extract` target example.com so the reader can
diff them and confirm only the `driver:` field changes.

## Conventions

- Each example lives in its own directory.
- Each example ships a `job.yaml` and a `README.md`.
- The README states which capabilities the job depends on, which
  adapters it has been verified against, and any caveats.
