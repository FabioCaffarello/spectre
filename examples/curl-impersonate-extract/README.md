# curl-impersonate-extract

A minimal Spectre job that drives the curl-impersonate adapter
against [example.com](https://example.com), queries every link
on the page, and extracts each link's visible text and `href`.

> **Status:** runnable via `spectre run job.yaml` once the
> curl-impersonate adapter has been built (`just curl-imp-build`)
> and a curl-impersonate variant (default `curl_chrome116`) is on
> `$PATH`. PR12 closes the v1alpha1 unary surface for the
> curl-impersonate adapter; see
> [ADR-0017](../../docs/adr/0017-curl-impersonate-extraction-and-final-capability-divergence.md)
> for the deviations from the browser drivers and the
> [roadmap](../../docs/roadmap.md) for the full Phase 2 picture.

## Why a separate example

`hello-hackernews` exercises the same DSL against the Playwright
adapter; `seleniumbase-extract` exercises it against the
SeleniumBase adapter. This example completes the trio against the
curl-impersonate adapter — the same `job.yaml` shape, the same
RPC sequence (`Initialize → Navigate → Query → ExtractEach →
Close`), three honestly different runtime models. **One CLI, one
protocol, three runtimes.**

## Run it

From the repository root:

```bash
just curl-imp-build                                    # go build -o bin/adapter ./cmd/adapter
# Install a curl-impersonate release if needed (Linux: tarball into /usr/local/bin;
# macOS: download from https://github.com/lwthiker/curl-impersonate/releases).
just spectre-build
just spectre-run examples/curl-impersonate-extract/job.yaml --verbose
```

Or, with the binary on `$PATH`:

```bash
spectre run examples/curl-impersonate-extract/job.yaml --verbose
```

The adapter spawns one `curl_chrome116` subprocess per `Navigate`
call (no browser, no rendering). The job writes one JSON object
per link to `links.jsonl`, resolved relative to this directory.
With `--verbose`, the engine prints the compiled `Plan` to stderr
so you can see the protocol-level RPC sequence.

To inspect the plan without running anything:

```bash
spectre validate examples/curl-impersonate-extract/job.yaml
```

Expected output (one line per link):

```json
{"text":"More information...","url":"https://www.iana.org/domains/example"}
```

(example.com currently has a single link; the job is structured
so adding a richer target — your own page, a documentation site —
is one YAML edit away.)

## What it does

1. Engine parses `job.yaml` into a validated `Job`, then compiles
   it to a `Plan`: `Initialize → Navigate → Query → ExtractEach →
   Close`. The plan is identical to the one produced for
   `hello-hackernews` and `seleniumbase-extract`; only the driver
   name differs.
2. Engine launches the curl-impersonate adapter as a subprocess
   (reading `adapters/curl-impersonate/driver.yaml`), polls the
   gRPC standard health check until SERVING (ADR-0021 §6), and
   dials the TCP listener over gRPC (ADR-0022). The engine-side
   TCP dial lands in R2.3; until then the `spectre run` flow
   against this example is broken — see `KNOWN_BREAKAGE.md` at
   the repo root.
3. Engine sends the RPC sequence. `Navigate` shells out to
   `curl_chrome116`, parses the response body via goquery
   (ADR-0017 §2), and caches the `*goquery.Document` on the
   session. `Query(a)` resolves the CSS selector against the
   cached document; `ExtractEach` reads `textContent` (a goquery
   `Selection.Text()` call) and the `href` attribute (a goquery
   `Selection.Attr` call) from each match.
4. Each result row is written to `links.jsonl` as soon as its
   `Extract` returns.

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
  field. A job with `eval:` would fail
  `spectre validate` against this driver.
- **Screenshots.** No `screenshot_*` capability declared, ever.
  ADR-0016 §5.
- **Text and attribute selectors as separate capabilities.** The
  adapter accepts CSS and XPath only. `SELECTOR_KIND_TEXT` and
  `SELECTOR_KIND_ATTRIBUTE` reject with `CODE_INVALID_ARGUMENT`
  and a message pointing at ADR-0017 §1 — the cross-driver
  semantic-equivalence contract that justifies the omission.
- **Visibility-aware text extraction.** `MODE_INNER_TEXT` falls
  back to `MODE_TEXT_CONTENT` because there is no layout engine
  to evaluate visibility. ADR-0017 §5 documents the
  approximation; clients who need true visible-text semantics
  should use `driver: playwright` or `driver: seleniumbase`.

See the [roadmap](../../docs/roadmap.md) for what each Phase
unblocks.
