# Examples

Sample jobs and walkthroughs for Spectre. Each example lives in
its own directory with a `job.yaml` and a `README.md`. Job specs
are committed early so the user-facing contract is visible during
design and so contributors have a target shape to code against.

## Index

- [hello-hackernews](hello-hackernews) — Playwright (Node).
  Extract titles and URLs from the Hacker News front page.
  Phase 1 reference example.
- [seleniumbase-extract](seleniumbase-extract) — SeleniumBase
  (Python). Same DSL shape as `hello-hackernews`, different
  driver and runtime; first cross-language proof of the protocol
  thesis.
- [curl-impersonate-fetch](curl-impersonate-fetch) —
  curl-impersonate (Go). Minimal navigate-only example shipped
  with PR11.
- [curl-impersonate-extract](curl-impersonate-extract) —
  curl-impersonate (Go). Mirrors `seleniumbase-extract`
  byte-for-byte except for the `driver:` field; the third leg
  of the cross-driver equivalence demo (PR12).

## Cross-driver equivalence demo (Phase 2 exit-criterion proof)

Three of the examples — `hello-hackernews`,
`seleniumbase-extract`, and `curl-impersonate-extract` — use the
same DSL shape against three runtimes in three languages. The
shared shape exercises only the four-capability intersection of
all three drivers: `navigation`, `query_css`, `extract_text`,
`extract_attribute`. Running the three jobs in sequence is the
visible artefact for Phase 2's exit criterion: *the same
`job.yaml` runs unchanged across all three adapters where their
capabilities allow*.

`hello-hackernews` targets a richer page (Hacker News) and
demonstrates the full DSL; `seleniumbase-extract` and
`curl-impersonate-extract` target example.com so the reader can
diff them and confirm only the `driver:` field changes.

```bash
spectre run examples/hello-hackernews/job.yaml
spectre run examples/seleniumbase-extract/job.yaml
spectre run examples/curl-impersonate-extract/job.yaml
```

The output of the latter two examples is structurally
equivalent: one JSONL record per `<a>` link, each carrying a
`text` and `url` field. The text values match modulo
browser-vs-static differences (browsers normalise whitespace
slightly more aggressively than `golang.org/x/net/html`'s text
extraction; the values are equivalent in content).

## Future examples

These will be added as the relevant capabilities land:

- `parameterized-search` — paginated search that uses
  `js_execution` for next-page handling.
- `auth-flow` — session reuse across pages with
  `cookies_persist`.
- `kubernetes-fleet` — distributed run via the control plane
  (Phase 3).

## Conventions

- Each example lives in its own directory.
- Each example ships a `job.yaml` and a `README.md`.
- The README states clearly which capabilities the job depends
  on, which adapters it has been verified against, and any
  caveats.
