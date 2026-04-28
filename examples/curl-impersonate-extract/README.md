# curl-impersonate-extract

A minimal Spectre job that drives the curl-impersonate adapter
against [example.com](https://example.com), queries every link
on the page, and extracts each link's visible text and `href`.

> **Status (R2.3 era).** The `spectre run` CLI surface this
> example documented in PR12 was retired by R2.3 (ADR-0020 §3).
> End-to-end invocation is currently a manual `grpcurl` flow:
> start the engine, start the curl-impersonate adapter, send the
> inline DSL. R3.1 replaces this with `kubectl apply -f
> scrapejob.yaml` against a Helm-installed cluster; R6.2 replaces
> it with `docker compose up` plus a
> `just example-curl-impersonate-extract` recipe.

## Why a separate example

`hello-hackernews` exercises the same DSL against the Playwright
adapter; `seleniumbase-extract` exercises it against the
SeleniumBase adapter. This example completes the trio against
the curl-impersonate adapter — the same `job.yaml` shape, the
same RPC sequence (`Initialize → Navigate → Query → ExtractEach
→ Close`), three honestly different runtime models. **One
engine, one protocol, three runtimes.**

## Run it

The pieces, three terminals (or three `tmux` panes — the engine
and adapter are both long-running services):

```bash
# Terminal 1 — engine gRPC service on 127.0.0.1:9090
just engine-run

# Terminal 2 — curl-impersonate adapter on 127.0.0.1:9093
just curl-imp-build
# Install a curl-impersonate release if needed:
#   https://github.com/lwthiker/curl-impersonate/releases
just curl-imp-run 9093

# Terminal 3 — submit the job
grpcurl -plaintext \
    -import-path proto -proto spectre/engine/v1alpha1/engine.proto \
    -d "$(jq -n --arg dsl "$(cat examples/curl-impersonate-extract/job.yaml)" '{job_dsl: $dsl}')" \
    127.0.0.1:9090 \
    spectre.engine.v1alpha1.Engine/RunJob
```

The adapter spawns one `curl_chrome116` subprocess per
`Navigate` call (no browser, no rendering). The engine streams
`RunJobResponse` events back: one `row.json_line` per link on
example.com, then a terminal `completed.rows_extracted`.

Expected `row.json_line` (one line; example.com currently has a
single link):

```json
{"text":"More information...","url":"https://www.iana.org/domains/example"}
```

The job is structured so adding a richer target — your own
page, a documentation site — is one YAML edit away.

## What it does

1. The engine parses `job.yaml` into a validated `Job`, then
   compiles it to a `Plan`:
   `Initialize → Navigate → Query → ExtractEach → Close`. The
   plan is identical to the one produced for `hello-hackernews`
   and `seleniumbase-extract`; only the driver name differs.
2. The engine resolves `driver: curl-impersonate` to
   `SPECTRE_CURL_IMPERSONATE_ENDPOINT` (default
   `127.0.0.1:9093`) via `AdapterRegistry`, dials the TCP
   listener over gRPC (ADR-0022), and waits for
   `grpc.health.v1.Health.Check` to return `SERVING`
   (ADR-0021 §6).
3. The engine sends the RPC sequence. `Navigate` shells out to
   `curl_chrome116`, parses the response body via goquery
   (ADR-0017 §2), and caches the `*goquery.Document` on the
   session. `Query(a)` resolves the CSS selector against the
   cached document; `ExtractEach` reads `textContent` (a
   goquery `Selection.Text()` call) and the `href` attribute (a
   goquery `Selection.Attr` call) from each match.
4. Each `Extract` response becomes a `RunJobResponse.Row` event
   on the wire.

## Operator notes

- **A curl-impersonate variant must be on `$PATH`.** The adapter
  invokes `curl_chrome116` by default. Override with
  `SPECTRE_CURL_VARIANT` if your release ships a different name
  (e.g. `curl_firefox117`).
- **One subprocess per `Navigate`.** Each Navigate spawns the
  curl binary; ~5-15ms of process-spawn overhead per call on
  Linux. ADR-0016 §1 records why this is acceptable for
  v1alpha1 and what the v1alpha2 throughput escape hatch looks
  like.
- **Public-internet dependency.** example.com is one of the
  most stable hosts on the public web, but a network outage or
  DNS failure would break this example. The deterministic
  counterpart lives in the conformance suite
  (`tools/conformance/tests/test_curl_impersonate_*.py`), which
  drives the adapter against an in-process HTTP fixture.

## What this driver cannot do

- **JavaScript execution.** No `js_execution`; no `MODE_EVAL`
  field. A job with `eval:` would fail at plan time against
  this driver.
- **Screenshots.** No `screenshot_*` capability declared, ever.
  ADR-0016 §5.
- **Text and attribute selectors as separate capabilities.**
  The adapter accepts CSS and XPath only. `SELECTOR_KIND_TEXT`
  and `SELECTOR_KIND_ATTRIBUTE` reject with
  `CODE_INVALID_ARGUMENT` and a message pointing at ADR-0017
  §1 — the cross-driver semantic-equivalence contract that
  justifies the omission.
- **Visibility-aware text extraction.** `MODE_INNER_TEXT` falls
  back to `MODE_TEXT_CONTENT` because there is no layout
  engine to evaluate visibility. ADR-0017 §5 documents the
  approximation; clients who need true visible-text semantics
  should use `driver: playwright` or `driver: seleniumbase`.

See the [roadmap](../../docs/roadmap.md) for what each Phase
unblocks.
