# Examples

Sample jobs and walkthroughs for Spectre.

> **Status:** `hello-hackernews` runs end-to-end via `spectre run`
> (Phase 1 complete). The other examples below remain aspirational
> until the relevant capabilities land — see the
> [roadmap](../docs/roadmap.md). The job specs are committed early
> so the user-facing contract is visible during design and so
> contributors have a target shape to code against.

## Index

- [hello-hackernews](hello-hackernews) — extract titles and URLs from
  the Hacker News front page using the Playwright adapter.

## Future examples

These will be added as the relevant capabilities land:

- `parameterized-search` — paginated search that uses `js_execution`
  for next-page handling.
- `auth-flow` — session reuse across pages with `cookies_persist`.
- `kubernetes-fleet` — distributed run via the control plane.

## Conventions

- Each example lives in its own directory.
- Each example ships a `job.yaml` and a `README.md`.
- The README states clearly which capabilities the job depends on,
  which adapters it has been verified against, and any caveats.
