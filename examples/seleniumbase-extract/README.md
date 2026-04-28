# seleniumbase-extract

A minimal Spectre job that drives the SeleniumBase adapter
against [example.com](https://example.com), queries every link
on the page, and extracts each link's visible text and `href`.

> **Status (R2.3 era).** The `spectre run` CLI surface this
> example documented in PR10 was retired by R2.3 (ADR-0020 §3).
> End-to-end invocation is currently a manual `grpcurl` flow:
> start the engine, start the SeleniumBase adapter, send the
> inline DSL. R3.1 replaces this with `kubectl apply -f
> scrapejob.yaml` against a Helm-installed cluster; R6.2
> replaces it with `docker compose up` plus a
> `just example-seleniumbase-extract` recipe.

## Why a separate example

`hello-hackernews` exercises the same DSL shape against the
Playwright adapter. Running this example against the same
engine validates the project's central thesis — *one engine,
one protocol, two runtimes in two languages* — without
changing the job shape: only the `driver:` field differs.

## Run it

The pieces, three terminals (or three `tmux` panes — the engine
and adapter are both long-running services):

```bash
# Terminal 1 — engine gRPC service on 127.0.0.1:9090
just engine-run

# Terminal 2 — SeleniumBase adapter gRPC service on 127.0.0.1:9092
just sb-install-chromedriver   # one-time, see notes below
just sb-run 9092

# Terminal 3 — submit the job
grpcurl -plaintext \
    -import-path proto -proto spectre/engine/v1alpha1/engine.proto \
    -d "$(jq -n --arg dsl "$(cat examples/seleniumbase-extract/job.yaml)" '{job_dsl: $dsl}')" \
    127.0.0.1:9090 \
    spectre.engine.v1alpha1.Engine/RunJob
```

The adapter launches Chrome in headless mode on the first
`Navigate` call (lazy launch; ADR-0009 §1, ADR-0014 §2). The
engine streams `RunJobResponse` events back: one `row.json_line`
per link on example.com, then a terminal `completed.rows_extracted`.

Expected `row.json_line` (one line; example.com currently has a
single link):

```json
{"text":"Learn more","url":"https://iana.org/domains/example"}
```

The job is structured so adding a richer target — your own
page, a documentation site — is one YAML edit away.

## What it does

1. The engine parses `job.yaml` into a validated `Job`, then
   compiles it to a `Plan`:
   `Initialize → Navigate → Query → ExtractEach → Close`. The
   plan is identical to the one the engine produces for
   `hello-hackernews`; only the driver name differs.
2. The engine resolves `driver: seleniumbase` to
   `SPECTRE_SELENIUMBASE_ENDPOINT` (default `127.0.0.1:9092`)
   via `AdapterRegistry`, dials the TCP listener over gRPC
   (ADR-0022), and waits for `grpc.health.v1.Health.Check` to
   return `SERVING` (ADR-0021 §6).
3. The engine sends the RPC sequence. `Query(a)` returns one
   `ElementRef` per link; `ExtractEach` reads `textContent` and
   the `href` attribute from each one. The handlers go through
   Selenium's `WebElement.get_attribute("textContent")` /
   `get_attribute("href")` — see ADR-0015 §4 for the
   mode-by-mode mapping.
4. Each `Extract` response becomes a `RunJobResponse.Row` event
   on the wire.

## Operator notes

- **Chrome and ChromeDriver are required.** SeleniumBase's
  `install chromedriver` recipe (wrapped by
  `just sb-install-chromedriver`) fetches the matching driver
  for the local Chrome install. If either is missing,
  `Navigate` surfaces `CODE_INTERNAL` with a hint pointing at
  the recipe.
- **Headless mode is the default.** PR10 keeps PR9's headless
  factory; tweak the factory in
  `adapters/seleniumbase/src/spectre_seleniumbase/server.py` if
  you need a window.
- **Public-internet dependency.** example.com is one of the
  most stable hosts on the public web, but a network outage,
  DNS failure, or upstream change would break this example.
  The deterministic counterpart lives in the conformance suite
  (`tools/conformance/tests/test_seleniumbase_*.py`), which
  drives the adapter against an in-process HTTP fixture and is
  the load-bearing test signal.

## What this example does NOT exercise

- Screenshots (`screenshot_viewport` / `screenshot_element`) —
  the conformance suite covers these; the DSL does not yet
  expose them.
- `screenshot_full_page` — *intentionally* not declared by the
  SeleniumBase adapter. ADR-0015 §5 records the rationale.
- Network interception, cookies, header overrides — Phase 2
  follow-ups.
- Distributed execution via the control plane (R3.1).

See the [roadmap](../../docs/roadmap.md) for when each lands.
