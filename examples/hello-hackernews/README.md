# hello-hackernews

A minimal Spectre job that fetches the Hacker News front page and
extracts the title and URL of each story.

> **Status:** runnable via `spectre run job.yaml`. ADR-0013 made
> the engine binary the `spectre` CLI; this example is the
> canonical input to `spectre run`. See the
> [roadmap](../../docs/roadmap.md) for the full Phase 1 picture.

## Run it

From the repository root:

```bash
just spectre-build
just spectre-run examples/hello-hackernews/job.yaml --verbose
```

Or, with the binary on `$PATH`:

```bash
spectre run examples/hello-hackernews/job.yaml --verbose
```

The first run installs the Chromium binary the Playwright adapter
needs (~150 MB; cached afterwards). Run `just pw-install-browsers`
ahead of time to avoid the wait on the first run.

The job writes one JSON object per story to `stories.jsonl`,
resolved relative to this directory (the directory containing
`job.yaml`). With `--verbose`, the engine prints the compiled
`Plan` to stderr before execution so you can see the protocol-level
RPC sequence. To pipe the output instead, set `output.path: '-'`
in the YAML or pass `--output=-` to override it for one run.

To inspect the plan without running anything (useful when editing
the YAML), use `spectre validate`:

```bash
spectre validate examples/hello-hackernews/job.yaml
```

Expected output (one line per story):

```json
{"title": "Show HN: A new ...", "url": "https://..."}
{"title": "Open-source ...",    "url": "https://..."}
```

## What it does

1. Engine parses `job.yaml` into a validated `Job`, then compiles it
   to a `Plan`: `Initialize → Navigate → Query → ExtractEach → Close`.
2. Engine launches the Playwright adapter as a subprocess (reading
   `adapters/playwright/driver.yaml`), polls the gRPC standard
   health check until SERVING (ADR-0021 §6), and dials the TCP
   listener over gRPC (ADR-0022). The engine-side TCP dial lands
   in R2.3; until then the `spectre run` flow against this example
   is broken — see `KNOWN_BREAKAGE.md` at the repo root.
3. Engine sends the RPC sequence. `Query(.titleline > a)` returns
   one `ElementRef` per story; `ExtractEach` reads `textContent` and
   the `href` attribute from each one.
4. Each result row is written to `stories.jsonl` as soon as its
   `Extract` returns — long-running jobs produce visible progress
   and a panic mid-job preserves prior rows.

## Why Hacker News

It is one of the few legitimate browsable targets with stable HTML,
no anti-automation friction, and a visible API contract. This
example is not a useful HN scraper — production HN access should
use the [official API](https://github.com/HackerNews/API). The
example exists to demonstrate the smallest interesting Spectre job.

The DOM selector `.titleline > a` has been stable on HN for years;
if HN changes their structure the example may break and this README
will need updating. The engine itself is unaffected.

## What this example does NOT exercise

- Pagination (planned for `parameterized-search`).
- JavaScript execution (`js_execution` capability).
- Network interception or screenshots.
- Distributed execution via the control plane.

See the [roadmap](../../docs/roadmap.md) for when each lands.
