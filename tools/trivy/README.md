# Trivy per-image override files (W1.3)

> Per-image `.trivyignore` files referenced by
> [`.github/workflows/scan.yml`](../../.github/workflows/scan.yml).
> Each file's presence is checked at scan time; the workflow
> instructs Trivy to ignore the listed CVE IDs for that image
> only.

## When to use a `.trivyignore` entry

Per-image overrides are **last resort**. The default response
to a HIGH/CRITICAL finding is:

1. **Fix the upstream dependency** — bump the affected
   package version in the image's go.mod / Cargo.toml /
   pyproject.toml / package.json.
2. **Wait for an upstream fix** — Trivy's
   `ignore-unfixed: true` already skips findings without a
   fix; if the finding is reported, a fix exists.
3. **Replace the dependency** — switch to an alternative
   package without the vulnerability.

A `.trivyignore` entry is appropriate only when:

- The CVE applies to a code path the image does not exercise
  (false-positive for the deployment shape), AND
- The upstream fix is not available or its adoption requires
  substantial work, AND
- The finding has been reviewed by a maintainer.

## Format

Each file follows the
[Trivy ignore file format](https://aquasecurity.github.io/trivy/v0.50/docs/configuration/filtering/#trivyignore)
— one CVE ID per line, `#`-prefixed comments allowed:

```
# CVE-2024-XXXX — affects example/library@v1.2.3 → fixed in v1.2.4.
# Adapter does not exercise the affected `Foo()` code path; we
# can't bump because v1.2.4 drops support for x86_32 which an
# upstream consumer requires. Reviewed: 2026-MM-DD by <maintainer>.
CVE-2024-XXXX
```

## Files

| File | Image | Content |
|---|---|---|
| `engine.trivyignore` | `spectre-engine` | Empty (no overrides) |
| `control-plane.trivyignore` | `spectre-control-plane` | Empty |
| `curl-impersonate.trivyignore` | `spectre-curl-impersonate` | Empty |
| `playwright.trivyignore` | `spectre-playwright` | Empty |
| `seleniumbase.trivyignore` | `spectre-seleniumbase` | Empty |

## Workflow integration

The W1.3 scan workflow's Trivy step references
`tools/trivy/<target>.trivyignore` per matrix target. Trivy
loads the file (if present) and excludes listed CVE IDs from
findings before applying severity gates.

Adding a `.trivyignore` entry is a **PR-side** change with a
`docs(scan):` Conventional Commit prefix; the PR description
documents the rationale per the "When to use" criteria above.
