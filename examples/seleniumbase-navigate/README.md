# seleniumbase-navigate

A minimal Spectre job that drives the SeleniumBase adapter against
[example.com](https://example.com) using only the `navigation`
capability.

> **Status:** runnable via `spectre run job.yaml` once the
> SeleniumBase adapter has been bootstrapped (`just sb-bootstrap`)
> and Chrome plus ChromeDriver are installed locally
> (`seleniumbase install chromedriver`). See ADR-0014 for the PR9
> scope; see the [roadmap](../../docs/roadmap.md) for Phase 2.

## Why a separate example

The `hello-hackernews` example needs `Query` and `Extract`
capabilities, which the SeleniumBase adapter does not implement
yet (PR10 lands those). PR9's SeleniumBase adapter only declares
`navigation`, so this example exercises exactly that — `Initialize
→ Navigate → Close` against a single URL. PR10 will either extend
this job or rotate the SeleniumBase examples toward extraction
once the broader RPC surface lands.

The example is the visible evidence that the same `spectre` CLI
runs the same DSL against two different runtimes in two different
languages.

## Run it

From the repository root:

```bash
just sb-bootstrap                                          # uv sync the adapter
seleniumbase install chromedriver                          # one-time, see notes below
just spectre-build
just spectre-run examples/seleniumbase-navigate/job.yaml --verbose
```

Or, with the binary on `$PATH`:

```bash
spectre run examples/seleniumbase-navigate/job.yaml --verbose
```

The adapter launches Chrome in headless mode on the first
`Navigate` call (lazy launch; ADR-0009 §1, ADR-0014 §2). The job
writes nothing to `output.jsonl` — there is no extract step in
PR9. The exit code (zero on success) is the demonstration. With
`--verbose`, the engine prints the compiled `Plan` to stderr so
you can see the protocol-level RPC sequence:
`Initialize → Navigate → Close`.

To inspect the plan without running anything:

```bash
spectre validate examples/seleniumbase-navigate/job.yaml
```

## Operator notes

- **Chrome and ChromeDriver are required.** SeleniumBase's
  ``install chromedriver`` recipe fetches the matching driver for
  the local Chrome install. If either is missing, `Navigate`
  surfaces `CODE_INTERNAL` with a hint pointing at the recipe.
- **Headless mode is the default.** PR9 does not expose a config
  knob for headed runs; tweak the factory in
  `adapters/seleniumbase/src/spectre_seleniumbase/server.py` if
  you need a window.
- **No JSONL output.** The job has no `extract` step, so
  `output.jsonl` is created empty. v1alpha1 has no "metadata-only"
  output mode; PR10's first SeleniumBase extract example will
  exercise the JSONL path.

## What it does NOT exercise

- Extraction (`query_*`, `extract_*` capabilities) — PR10.
- Screenshots (`screenshot_*` capabilities) — PR11.
- Network interception, cookies, header overrides — Phase 2 follow-ups.
- The control plane — Phase 3.

See the [roadmap](../../docs/roadmap.md) for when each lands.
