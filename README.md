# Spectre

**An open protocol for driver-agnostic browser automation at scale.**

> **Status: Alpha** — The protocol is `v1alpha1` and will change. Nothing
> is production-ready. If you are here, you are early.

---

## The thesis

Browser automation today is framework-locked. Choose Playwright? Your
selectors, stealth logic, session management, and deployment tooling are
all Playwright-specific. Switch to SeleniumBase for its UC Mode? Rewrite
everything. A new tool appears with better fingerprint evasion? Start over.

Spectre argues that the right primitive is not another framework but an
**open protocol**. Any browser automation tool — Playwright, SeleniumBase,
curl-impersonate, or something that doesn't exist yet — implements the
Spectre Driver Protocol and plugs into the ecosystem. Your extraction
logic, orchestration, stealth configuration, and scaling infrastructure
stay the same regardless of which driver runs underneath.

This is the same architectural insight behind Kubernetes CRI (which
separated Kubernetes from Docker), or OpenTelemetry (which separated
observability from vendor SDKs). Spectre applies it to browser automation.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  User / DSL Job                 │
├─────────────────────────────────────────────────┤
│              Spectre Engine (Rust)              │
│         DSL parsing, planning, execution        │
├──────────────┬──────────────┬───────────────────┤
│   Driver Protocol (protobuf v1alpha1)           │
├──────────────┼──────────────┼───────────────────┤
│  Playwright  │ SeleniumBase │ curl-impersonate  │
│  (TypeScript)│   (Python)   │    (Go/cgo)       │
└──────────────┴──────────────┴───────────────────┘
```

The protocol defines the contract. Drivers implement it. The engine
compiles jobs against driver capabilities and fails at compile time —
not at runtime — if a driver can't do what a job requires.

For the full architecture, see [docs/architecture/overview.md](docs/architecture/overview.md).

## Quick start

```yaml
# job.yaml — extract top stories from Hacker News
spectre: v1alpha1
driver: playwright
steps:
  - navigate: https://news.ycombinator.com
  - extract:
      selector: .titleline > a
      fields:
        title: textContent
        url: href
output:
  format: jsonl
  path: ./stories.jsonl
```

```bash
git clone https://github.com/FabioCaffarello/spectre && cd spectre
just bootstrap
just pw-install-browsers
just spectre-build
just spectre-run examples/hello-hackernews/job.yaml
```

> Contributors who prefer not to install Rust + Go + Node + Python
> locally: see
> [docs/architecture/development-environment.md](docs/architecture/development-environment.md)
> for the recommended Devcontainer setup.

`just spectre-build` produces `core/engine/target/release/spectre`.
`spectre validate <job.yaml>` parses, plans, and checks declared
capabilities without launching the driver — handy when iterating on
YAML. See [ADR-0013](docs/adr/0013-cli-as-engine-binary.md) for why
the CLI lives in the engine crate.

## How Spectre compares

| Capability                  | Playwright | SeleniumBase | Browserless | Spectre    |
|-----------------------------|-----------|-------------|-------------|------------|
| Driver-agnostic protocol    | No        | No          | No          | **Yes**    |
| Declarative DSL             | No        | No          | Partial     | **Yes**    |
| Capability negotiation      | No        | No          | No          | **Yes**    |
| Distributed execution (K8s) | Manual    | Manual      | Yes         | **Planned**|
| Stealth / anti-detection    | Plugins   | UC Mode     | Limited     | **Core**   |
| AI selector auto-healing    | No        | No          | No          | **Planned**|
| Multi-language drivers      | JS only   | Python only | API only    | **Any**    |
| Production-ready            | Yes       | Yes         | Yes         | **No**     |
| Open source                 | Yes       | Yes         | Partial     | **Yes**    |

Honesty: Spectre is alpha software. Playwright and SeleniumBase work
today. Spectre's advantage is architectural — it pays off when you need
to swap drivers, scale across clusters, or integrate tools that don't
exist yet.

## Project status

> **Microservices refactor in progress.** Spectre is undergoing
> an architectural refactor toward a fully microservices topology
> (engine, control plane, and each adapter as standalone services
> backed by PostgreSQL, Kafka, and Redis). See
> [ADR-0020](docs/adr/0020-microservices-architecture-supersession.md)
> for the architectural commitment and
> [`docs/refactoring-status.md`](docs/refactoring-status.md) for
> live progress. The pre-refactor architecture below continues to
> function for existing examples; new contributions should align
> with the post-refactor direction.
>
> **R2.2 → R2.3 transitional break.** PR R2.2 switched all three
> reference adapters and the conformance suite from Unix-domain-
> socket gRPC to TCP gRPC plus the standard health check (ADR-0021,
> ADR-0022). The engine binary's UDS dial path lands in R2.3, so
> `spectre run` against the example jobs is broken in this window.
> [`KNOWN_BREAKAGE.md`](KNOWN_BREAKAGE.md) documents the gap and
> the workaround (use `just conf-test` to exercise the adapters
> end-to-end). R2.3's first commit deletes `KNOWN_BREAKAGE.md`.

**Phases 1 and 2 — complete.** The engine parses the DSL, plans
against driver capabilities, and runs `spectre run` against the
Playwright (PR8), SeleniumBase (PR10), and curl-impersonate (PR12)
adapters. The cross-driver equivalence demo in
[examples/](examples/README.md) shows one CLI executing one protocol
against three runtimes in three languages.

**Phase 2.5 — in progress.** PR13 added the
[Devcontainer](docs/architecture/development-environment.md) and a
distroless engine image. Per-adapter Dockerfiles and a Compose stack
are deferred (PR15.5+).

**Phase 3 — in progress.** PR14 began the control plane with a
kubebuilder v4 operator scaffold, the `ScrapeJob` Custom Resource
Definition, and a state-machine reconciler. PR15 wired
`SubprocessRunner` so the reconciler shells out to the spectre
engine binary the operator image bundles, captures JSONL on stdout,
and reports `RowsExtracted`. Adapter bundling and the in-cluster
smoke test against `hello-hackernews` are PR16 work. See
[docs/architecture/control-plane.md](docs/architecture/control-plane.md)
for the user-facing guide and
[ADR-0019](docs/adr/0019-control-plane-architecture-and-scrapejob-crd.md)
for the design decisions.

See the full [roadmap](docs/roadmap.md) for Phases 3–5.

## Contributing

Spectre welcomes contributions. The most impactful path is **writing a
new driver** — see [the driver guide](docs/guides/writing-a-driver.md).
For everything else, read [CONTRIBUTING.md](CONTRIBUTING.md).

## Documentation

- [Architecture overview](docs/architecture/overview.md)
- [Driver Protocol deep dive](docs/architecture/driver-protocol.md)
- [Control plane (Phase 3)](docs/architecture/control-plane.md)
- [Development environment (Devcontainer)](docs/architecture/development-environment.md)
- [Writing a driver](docs/guides/writing-a-driver.md)
- [Architecture Decision Records](docs/adr/README.md)
- [Roadmap](docs/roadmap.md)

## License

Apache 2.0 — see [LICENSE](LICENSE).
