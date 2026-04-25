# hello-hackernews

A minimal Spectre job that fetches the Hacker News front page and
extracts the title and URL of each story.

> **Status:** aspirational. The job spec is the intended user
> contract; it cannot run end-to-end until Phase 1 lands the engine,
> the Playwright adapter, and the conformance suite.

## What it does (when it runs)

1. Spawn the Playwright reference adapter as a child process.
2. Engine compiles `job.yaml`: one `navigate` step and one `extract`
   step. The required capabilities are `navigation` and the default
   `MODE_ATTR` extraction. Both are declared by the Playwright adapter
   once it ships.
3. Engine sends the corresponding RPC sequence to the driver:
   `Initialize` → `Navigate` → `Query` → `Extract` → `Close`.
4. Driver returns the queried elements and their fields.
5. Engine writes one JSON object per story to `stories.jsonl`.

## How to (eventually) run it

```bash
cd examples/hello-hackernews
spectre run job.yaml
```

Expected output (one line per story):

```json
{"title": "Show HN: A new ...", "url": "https://..."}
{"title": "Open-source ...",    "url": "https://..."}
```

## Why Hacker News

It is one of the few legitimate browsable targets with stable HTML,
no anti-automation friction, and a visible API contract. This example
is not meant to be a useful HN scraper — production HN access should
use the [official API](https://github.com/HackerNews/API). The
example exists to demonstrate the smallest interesting Spectre job.

## What this example does NOT exercise

- Pagination (planned for `parameterized-search`).
- JavaScript execution (`js_execution` capability).
- Network interception or screenshots.
- Distributed execution via the control plane.

See the [roadmap](../../docs/roadmap.md) for when each lands.
