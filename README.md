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

> **This example does not run yet.** It shows the intended experience
> once the engine and at least one driver are functional.

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
spectre run job.yaml
```

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

**Phase 0 — Foundation** (current)

- Repository structure established
- Driver Protocol defined at v1alpha1
- Three reference adapter skeletons: Playwright, SeleniumBase, curl-impersonate
- CI/CD pipelines, pre-commit hooks, build orchestration

See the full [roadmap](docs/roadmap.md) for Phases 1–5.

## Contributing

Spectre welcomes contributions. The most impactful path is **writing a
new driver** — see [the driver guide](docs/guides/writing-a-driver.md).
For everything else, read [CONTRIBUTING.md](CONTRIBUTING.md).

## Documentation

- [Architecture overview](docs/architecture/overview.md)
- [Driver Protocol deep dive](docs/architecture/driver-protocol.md)
- [Writing a driver](docs/guides/writing-a-driver.md)
- [Architecture Decision Records](docs/adr/README.md)
- [Roadmap](docs/roadmap.md)

## License

Apache 2.0 — see [LICENSE](LICENSE).
