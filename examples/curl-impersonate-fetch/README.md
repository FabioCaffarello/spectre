# curl-impersonate-fetch

A minimal Spectre job that drives the **curl-impersonate** adapter
against [example.com](https://example.com) using only the
`navigation` capability.

> **Status:** runnable via `spectre run job.yaml` once the
> curl-impersonate adapter has been built (`just curl-imp-build`)
> and a `curl_chrome116` binary is on PATH (install from
> [the curl-impersonate releases page](https://github.com/lwthiker/curl-impersonate/releases)).
> See ADR-0016 for the PR11 scope; see the
> [roadmap](../../docs/roadmap.md) for Phase 2.

## Why a separate example

The `hello-hackernews` example needs `Query` and `Extract`
capabilities, which the curl-impersonate adapter does not
implement yet (PR12 lands those). PR11's curl-impersonate adapter
only declares `navigation`, so this example exercises exactly that
— `Initialize → Navigate → Close` against a single URL.

The example is the visible evidence that the same `spectre` CLI
runs the same DSL against three different runtimes in three
different languages. After PR12, the curl-impersonate adapter
will declare roughly five or six capabilities — a strict subset
of SeleniumBase's twelve, which is itself a strict subset of
Playwright's thirteen. The protocol's planning surface scales
honestly across drastically different runtime models.

## Run it

From the repository root:

```bash
just curl-imp-build                                       # go build the adapter
# Install curl_chrome116 from the curl-impersonate releases page
# https://github.com/lwthiker/curl-impersonate/releases
just spectre-build
just spectre-run examples/curl-impersonate-fetch/job.yaml --verbose
```

Or, with the binary on `$PATH`:

```bash
spectre run examples/curl-impersonate-fetch/job.yaml --verbose
```

The adapter spawns one `curl_chrome116` subprocess per Navigate
(ADR-0016 §1). The job writes nothing to `output.jsonl` — there
is no extract step in PR11. The exit code (zero on success) is
the demonstration. With `--verbose`, the engine prints the
compiled `Plan` to stderr so you can see the protocol-level RPC
sequence: `Initialize → Navigate → Close`.

To inspect the plan without running anything:

```bash
spectre validate examples/curl-impersonate-fetch/job.yaml
```

## Operator notes

- **`curl_chrome116` (or your chosen variant) must be on PATH.**
  Override the variant by setting `SPECTRE_CURL_VARIANT` before
  launching the engine. ADR-0016 §3 records the default and the
  override mechanism.
- **No JSON output.** The job has no `extract` step, so
  `output.jsonl` is created empty. v1alpha1 has no
  "metadata-only" output mode; PR12's first curl-impersonate
  extract example will exercise the JSONL path.
- **Honest `WaitCondition` no-op.** This adapter accepts every
  `WaitCondition` value but cannot honour `DOMContentLoaded` or
  `NetworkIdle` — the response *is* the load event for an
  HTTP-only request. ADR-0016 §2.

## What it does NOT exercise

- Extraction (`query_*`, `extract_*` capabilities) — PR12.
- JavaScript execution — never, by design.
- Screenshots — never, by design (no rendering pipeline).
- Network interception, captcha handling, proxy rotation —
  Phase 2 follow-ups.
- The control plane — Phase 3.

See the [roadmap](../../docs/roadmap.md) for when each lands.
