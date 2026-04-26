# seleniumbase-extract

A minimal Spectre job that drives the SeleniumBase adapter
against [example.com](https://example.com), queries every link
on the page, and extracts each link's visible text and `href`.

> **Status:** runnable via `spectre run job.yaml` once the
> SeleniumBase adapter has been bootstrapped (`just sb-bootstrap`)
> and Chrome plus ChromeDriver are installed locally
> (`seleniumbase install chromedriver`). PR10 closes the
> SeleniumBase adapter at the v1alpha1 unary surface; see
> [ADR-0015](../../docs/adr/0015-seleniumbase-element-lifecycle-and-screenshot-coverage.md)
> for the deviations from the Playwright contract and the
> [roadmap](../../docs/roadmap.md) for the full Phase 2 picture.

## Why a separate example

`hello-hackernews` exercises the same DSL against the Playwright
adapter. Running this example side by side validates the
project's central thesis — *one CLI, one protocol, two
runtimes in two languages* — without changing the job shape:
only the `driver:` field differs.

## Run it

From the repository root:

```bash
just sb-bootstrap                                      # uv sync the adapter
seleniumbase install chromedriver                      # one-time, see notes below
just spectre-build
just spectre-run examples/seleniumbase-extract/job.yaml --verbose
```

Or, with the binary on `$PATH`:

```bash
spectre run examples/seleniumbase-extract/job.yaml --verbose
```

The adapter launches Chrome in headless mode on the first
`Navigate` call (lazy launch; ADR-0009 §1, ADR-0014 §2). The
job writes one JSON object per link to `links.jsonl`, resolved
relative to this directory. With `--verbose`, the engine prints
the compiled `Plan` to stderr so you can see the protocol-level
RPC sequence: `Initialize → Navigate → Query → ExtractEach →
Close`.

To inspect the plan without running anything:

```bash
spectre validate examples/seleniumbase-extract/job.yaml
```

Expected output (one line per link):

```json
{"text":"Learn more","url":"https://iana.org/domains/example"}
```

(example.com currently has a single link; the job is structured
so adding a richer target — your own page, a documentation site —
is one YAML edit away.)

## What it does

1. Engine parses `job.yaml` into a validated `Job`, then compiles
   it to a `Plan`: `Initialize → Navigate → Query → ExtractEach →
   Close`. The plan is identical to the one the engine produces
   for `hello-hackernews`; only the driver name differs.
2. Engine launches the SeleniumBase adapter as a subprocess
   (reading `adapters/seleniumbase/driver.yaml`), waits for the
   `ready unix:<path>` readiness line, and dials the socket
   over gRPC.
3. Engine sends the RPC sequence. `Query(a)` returns one
   `ElementRef` per link; `ExtractEach` reads `textContent` and
   the `href` attribute from each one. The handlers go through
   Selenium's `WebElement.get_attribute("textContent")` /
   `get_attribute("href")` — see ADR-0015 §4 for the
   mode-by-mode mapping.
4. Each result row is written to `links.jsonl` as soon as its
   `Extract` returns.

## Operator notes

- **Chrome and ChromeDriver are required.** SeleniumBase's
  `install chromedriver` recipe fetches the matching driver for
  the local Chrome install. If either is missing, `Navigate`
  surfaces `CODE_INTERNAL` with a hint pointing at the recipe.
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
- Distributed execution via the control plane — Phase 3.

See the [roadmap](../../docs/roadmap.md) for when each lands.
