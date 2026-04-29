# hello-hackernews

A minimal Spectre job that fetches the Hacker News front page and
extracts the title and URL of each story. The Phase 1 reference
example.

> **Status (R2.3 era).** The `spectre run` CLI surface this
> example documented in PR8 was retired by R2.3 (ADR-0020 §3).
> End-to-end invocation is currently a manual `grpcurl` flow:
> start the engine, start the Playwright adapter, send the
> inline DSL. R3.1 replaces this with `kubectl apply -f
> scrapejob.yaml` against a Helm-installed cluster; R6.2 replaces
> it with `docker compose up` plus a `just example-hello-hackernews`
> recipe.

## Run it

The pieces — bring up the unified Compose stack (R6.2,
ADR-0025) and submit the job from the host:

```bash
# Build the five service images, then start the full
# development graph (engine on 8090, Playwright adapter on 8091,
# stateful deps on their canonical ports).
just images
just compose-up

# Submit the job.
grpcurl -plaintext \
    -import-path proto -proto spectre/engine/v1alpha1/engine.proto \
    -d "$(jq -n --arg dsl "$(cat examples/hello-hackernews/job.yaml)" '{job_dsl: $dsl}')" \
    127.0.0.1:8090 \
    spectre.engine.v1alpha1.Engine/RunJob
```

The Compose stack ships the Chromium runtime baked into the
Playwright adapter image; no separate
`just pw-install-browsers` step is needed for the Compose flow.

The engine streams `RunJobResponse` events back: one
`row.json_line` per story, then a terminal `completed.rows_extracted`
with the total count. With `RUST_LOG=info` set on the engine, the
compiled `Plan` is logged to stderr before execution.

Tools required for the manual flow: `grpcurl`, `jq`. Both are
available via Homebrew (`brew install grpcurl jq`) and standard
Linux package managers.

## What it does

1. The engine parses `job.yaml` into a validated `Job`, then
   compiles it to a `Plan`:
   `Initialize → Navigate → Query → ExtractEach → Close`.
2. The engine resolves `driver: playwright` to
   `SPECTRE_PLAYWRIGHT_ENDPOINT` (defaults to
   `grpc://playwright-adapter:8091` inside Compose, or
   `127.0.0.1:8091` from the host) via `AdapterRegistry`, dials
   the TCP listener over gRPC
   (ADR-0022), and waits for the `grpc.health.v1.Health` check
   to return `SERVING` (ADR-0021 §6).
3. The engine sends the RPC sequence. `Query(.titleline > a)`
   returns one `ElementRef` per story; `ExtractEach` reads
   `textContent` and the `href` attribute from each one.
4. Each `Extract` response becomes a `RunJobResponse.Row` event
   on the wire — long-running jobs produce visible progress and
   a stream cancellation preserves rows already delivered.

## Why Hacker News

It is one of the few legitimate browsable targets with stable
HTML, no anti-automation friction, and a visible API contract.
This example is not a useful HN scraper — production HN access
should use the [official API](https://github.com/HackerNews/API).
The example exists to demonstrate the smallest interesting
Spectre job.

The DOM selector `.titleline > a` has been stable on HN for
years; if HN changes their structure the example may break and
this README will need updating. The engine itself is unaffected.

## What this example does NOT exercise

- Pagination.
- JavaScript execution (`js_execution` capability).
- Network interception or screenshots.
- Distributed execution via the control plane (R3.1).

See the [roadmap](../../docs/roadmap.md) for when each lands.
