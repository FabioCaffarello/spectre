# Known breakage: engine ↔ adapter transport mismatch

> **Window:** R2.2 merge → R2.3 merge.
> **Resolution:** R2.3 lands within the next sprint cycle. This file
> is deleted in the same PR.

The R2.2 refactor switched all three reference adapters
(Playwright, SeleniumBase, curl-impersonate) and the conformance
harness from Unix-domain-socket gRPC to TCP gRPC plus the gRPC
standard health check. ADR-0021 + ADR-0022 record the contract;
`docs/refactor-audit.md` enumerates the touched files.

The engine binary at `core/engine/` was deliberately left out of
R2.2: its UDS dial path (`Client::dial(socket: &Path)`,
`launcher::launch`) and its CLI binary (`spectre run …`) all
land in R2.3. The adapter and engine sides are therefore out of
sync between R2.2 and R2.3 merges.

## What is broken

- `just spectre-build && just spectre-run examples/<name>/job.yaml`
  for any of the three example jobs
  (`hello-hackernews`, `seleniumbase-extract`,
  `curl-impersonate-extract`, and the navigate-only and fetch-only
  variants under `examples/`). The engine spawns the adapter,
  waits for the pre-R2.2 `ready unix:<path>` readiness banner the
  adapter no longer emits, and either times out on readiness or
  fails to dial UDS once readiness is bypassed.
- The `internal/runner/SubprocessRunner` lane in the control plane
  (`core/control-plane/internal/runner/subprocess.go`) inherits
  the same break — it shells out to the same engine binary. The
  control plane is itself scheduled for refactor in R3.1
  (`EngineClientRunner` replaces `SubprocessRunner`); the R2.2
  → R2.3 window is the first time the existing runner produces
  visibly broken behaviour rather than passing tests.

## What still works

- The conformance suite (`just conf-test`) — the harness was
  rewritten end-to-end in R2.2 and exercises every adapter over
  TCP with the new health-check readiness signal. All 56 tests
  pass three consecutive times.
- Each adapter on its own: `just pw-run`, `just sb-run`, and
  `just curl-imp-run` start the adapter on a TCP port; manual
  probing with `grpc_health_probe -addr=127.0.0.1:<port>` returns
  `status: SERVING`.
- Per-adapter unit/integration test suites: `just pw-test`,
  `just sb-test`, `just curl-imp-test`.

## Workaround

To exercise an adapter end-to-end during the R2.2 → R2.3 window,
use the conformance suite — it covers the full
`Initialize → Navigate → Query → Extract → Screenshot → Close`
cycle against deterministic fixtures and is the project's quality
backbone:

```bash
just conf-test                 # all three adapters
just sb-conf-test              # SeleniumBase only
just curl-imp-conf-test        # curl-impersonate only
```

For ad-hoc probes, dial each adapter's TCP endpoint directly:

```bash
just pw-run 19091 &
grpc_health_probe -addr=127.0.0.1:19091
# … any gRPC client speaking spectre.driver.v1alpha1
```

## Why we accept the break

The strategy prompt's no-legacy principle (master-strategy §2.2)
forbids fallback paths during refactor: every "temporary" UDS
fallback adds maintenance burden, dilutes the architecture, and
undermines the audit trail R2.2 — R2.3 — R3 is establishing. The
phase boundary discipline (master-strategy §4) makes the engine
work an explicit R2.3 deliverable, not an R2.2 one. Splitting the
adapter and engine sides across two PRs keeps each diff
independently reviewable (master-strategy §2.4).

The break is bounded — one PR, expected within a single sprint
cycle — and surfaced honestly here so reviewers and contributors
discover the gap before reaching for `spectre run`. R2.3's first
commit deletes this file.
