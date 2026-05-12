---
status: accepted
date: 2026-05-12
deciders: [Fabio Caffarello]
---

# Provider picks for W5.1: BrightData (wired) + stub (placeholder)

## §1 — Context and problem statement

[ADR-0028 §5 criterion #2](../../../docs/adr/0028-ancillary-infra-services-catalog.md)
mandates a **two-provider design** for any infra-service: the
protocol abstraction must absorb vendor variation, and the
proof is two concrete providers exercising the same
`Provider` interface.

[ADR-0028 §4.1](../../../docs/adr/0028-ancillary-infra-services-catalog.md)
names the known proxy provider candidates: BrightData,
Oxylabs, Smartproxy, IPRoyal, NetNut, ScraperAPI, datacenter
pools, self-hosted residential pools, Tor (research-grade).
Three of those (BrightData, Oxylabs, Smartproxy) dominate
production use; the rest are niche.

[ADR-0028 §5 criterion #6](../../../docs/adr/0028-ancillary-infra-services-catalog.md)
permits the second provider to be **stubbed with an explicit
TODO and a follow-up PR reference** so long as the abstraction
is exercised by both. This is the escape hatch the build PR
uses when scope or cost rules out wiring two real providers in
one PR.

W5.1's scope is already substantial (proto + Go service + Rust
SDK + engine orchestrator scaffolding + Helm + Compose + bake
+ CI + docs). Wiring two real providers would add ~500-1000
lines of vendor-integration code without proving anything new
about the abstraction — the test matrix already runs against
both BrightData and stub, and the matrix passing is the
admission gate's actual signal.

## §2 — Decision

W5.1 ships **BrightData as the first real provider** and
**stub as the second provider satisfying the admission gate**.
A real second provider lands in **W5.1b** — a follow-up PR
that names the vendor based on
[Wave 4 pilot questionnaire §1](../../../docs/pilot/v1alpha2-questionnaire.md)
data (currently unavailable; questionnaire merged
2026-05-12).

## §3 — Rationale

### Why BrightData first

- **Market presence.** The largest residential proxy
  provider; widely used in production scraping; well-
  understood failure modes; substantial community
  documentation outside vendor channels.
- **API simplicity.** Super Proxy pattern derives the
  proxy URL from credentials + session string; no per-
  acquire API call needed. Acquire-path latency stays
  cheap. Release is a no-op (sessions expire
  vendor-side without an API call).
- **Sticky session support.** First-class — embedded in
  the username field via `-session-<id>` suffix. Maps
  cleanly to `AcquireRequest.sticky`.
- **Geo targeting via username.** `-country-<cc>` suffix
  in the username field handles per-acquire region
  constraints without a per-region zone configuration.

### Why stub as second

- **The abstraction proof doesn't require a second real
  provider** — it requires two implementations of the
  same interface. Stub satisfies this gate at near-zero
  cost.
- **Local-dev convenience.** Cluster operators without
  vendor credentials can still bring up the broker
  (`SPECTRE_PROXY_BROKER_STUB_ENABLED=true` +
  `SPECTRE_PROXY_BROKER_STUB_URLS=...`) — the same dial
  path as production, just routed at hardcoded URLs.
- **CI viability.** `mtls-smoke` and `production-smoke`
  run without vendor credentials; stub enables the
  smoke gates to exercise the broker end-to-end without
  paid traffic.

### Why not Oxylabs / Smartproxy in W5.1

Both are real candidates for W5.1b — neither was selected
in W5.1 because:

- The W5.1 PR is already at the upper bound of single-PR
  scope (~3500-4500 lines per the master phase prompt
  estimate). Adding a second vendor integration pushes
  past the ceiling.
- The pilot data informing the choice is not yet
  available; picking a vendor blind risks shipping the
  wrong second provider. Better to defer 4-8 weeks for
  Wave 4 pilot answers from §1 of the questionnaire
  (proxy provider choice + spend + friction) than
  commit prematurely.

## §4 — Consequences

- W5.1 satisfies ADR-0028 §5 criteria #1 – #5 fully and
  criterion #6 via the documented escape hatch.
- W5.1b is the named follow-up:
  - Replace `internal/providers/stub/` with the real
    second provider's subpackage.
  - Update `adr/0001` (this file) marking the second
    provider as `wired`.
  - Update `CHANGELOG.md` with the W5.1b entry.
  - Production-smoke + mtls-smoke gain a configuration
    matrix exercising both real providers (currently the
    matrix runs against {BrightData, stub} only).
- The `Provider` interface is frozen by the matrix —
  W5.1b's vendor must implement it as-is; any
  interface change is an architectural amendment, not
  a vendor-integration concern.

## §5 — Confirmation (acceptance criteria)

W5.1b's PR is correct when:

1. The provider matrix in `internal/providers/providers_test.go`
   includes the new vendor alongside BrightData (stub may
   stay as a third entry for local-dev / CI use).
2. Every matrix scenario passes against the new vendor
   without code-level exceptions.
3. The chart's `proxyBroker.providers` block exposes the
   new vendor's configuration knobs.
4. `production-smoke.yml` + `mtls-smoke.yml` exercise the
   new vendor when CI provides credentials (gated by
   `vars.PROXY_VENDOR_2_USERNAME` presence).
5. This ADR's `§2` is updated to reflect the second
   provider as `wired`; `§4` is updated to mark W5.1b as
   merged.

## §6 — Reference materials

- [ADR-0028 §4.1](../../../docs/adr/0028-ancillary-infra-services-catalog.md)
  — proxy-broker catalog entry + known providers list
- [ADR-0028 §5](../../../docs/adr/0028-ancillary-infra-services-catalog.md)
  — admission criteria including §5.2 two-provider design
  + §5.6 escape hatch
- [`docs/pilot/v1alpha2-questionnaire.md`](../../../docs/pilot/v1alpha2-questionnaire.md)
  §1 — acquisition layer questions feeding W5.1b
  vendor pick
- BrightData docs:
  <https://docs.brightdata.com/proxy-networks/residential-network/super-proxy>
- Oxylabs docs (candidate):
  <https://developers.oxylabs.io/residential-proxies>
- Smartproxy docs (candidate):
  <https://help.smartproxy.com/docs/getting-started>
