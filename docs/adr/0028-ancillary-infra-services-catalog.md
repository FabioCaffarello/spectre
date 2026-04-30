---
status: accepted
date: 2026-04-30
deciders: [Fabio Caffarello]
---

# Ancillary infra services catalog

## §1 — Context and Problem Statement

ADR-0026 §3.5 reserves `infra-services/` as the home for
ancillary services that the engine and adapters consume to
solve cross-cutting infrastructure concerns. The category is
*defined* by ADR-0026 — purpose, location, dependency posture,
admission criteria — but its inhabitants are unnamed there. ADR-0026
deliberately defers the catalog to this ADR so that the platform
taxonomy and the catalog of slots can evolve independently.

Web scraping at the platform scale Spectre is targeting needs
more than browser-automation orchestration. The Driver Protocol
(ADR-0001) handles the *what* — given a session, navigate, click,
extract. The wider operational surface — how to source IP
addresses without burning rate limits, how to clear CAPTCHAs that
gate access, how to rotate browser fingerprints without
detection, how to share session state across adapter restarts,
how to coordinate request budgets across tenants — sits outside
the Driver Protocol's scope. Today's three adapters handle these
concerns ad-hoc or not at all:

- **Proxy management.** None today. A real production adapter
  needs proxies; a research-grade adapter sometimes uses
  hard-coded proxies in env vars; either way no shared
  abstraction exists.
- **CAPTCHA solving.** None today. SeleniumBase's
  undetected-chromedriver mitigates some bot-detection but does
  not solve image / hCaptcha / reCAPTCHA challenges. A real
  production deployment needs a solver service.
- **Fingerprint rotation.** Limited today. SeleniumBase's
  stealth defaults are baked into the adapter; Playwright relies
  on whatever fingerprint Chromium chooses; curl-impersonate's
  fingerprint is its raison d'être but is fixed per binary.
  Cross-adapter fingerprint coordination is absent.
- **Session persistence.** None today. Each adapter's session
  state lives in the adapter's process; a restart loses cookies
  and authenticated state. ADR-0023 §5's `adapter_instance_id`
  invalidates sessions on adapter restart by design — the
  contract is correct but leaves "persisted sessions across
  restarts" as a separate problem.
- **Rate limiting and budgeting.** None today. A single adapter
  can hammer one origin without internal coordination; the
  engine has no visibility into per-domain budgets across
  parallel jobs.

Each of the above is a candidate service. Each is also a place
where the wrong abstraction at the wrong time wastes effort:
building a "proxy service" with one provider integration is a
glorified config wrapper; building a "fingerprint service"
without a concrete consumer is speculative architecture.

The catalog approach — *name the slots, gate the build* —
balances those risks. ADR-0028 lists the services that
Spectre's platform is committed to having available, defines
their canonical shape, and specifies the admission criteria
that turn a named slot into built code. Naming a slot is cheap;
building one is not. The catalog separates the cheap step from
the expensive one.

This ADR does not land any service code. R6.6's restructure PR
materialises `infra-services/` as a placeholder directory with
a `README.md` that points to this ADR. Catalogued services
land one at a time in R7.x and later phases, each gated by the
admission criteria in §5.

## §2 — Decision summary

R6.6 commits to:

- **Five named slots.** `proxy-broker`, `captcha-solver`,
  `fingerprint-broker`, `session-store`, `rate-limit-broker`.
  Each slot has a reserved proto package path
  (`spectre.<slot>.v1alpha1`) and a reserved directory
  (`infra-services/<slot>/`).
- **Tiered conviction.** Two slots (`proxy-broker`,
  `captcha-solver`) are *high-conviction* — Spectre will need
  them for any production deployment beyond research-grade.
  Three slots (`fingerprint-broker`, `session-store`,
  `rate-limit-broker`) are *probable* — the slot is named to
  prevent ad-hoc placement when the need surfaces, but the
  conviction is lower; admission may end up routing the
  concern elsewhere (a shared-lib, a feature inside an
  adapter, a Redis-backed module of an existing service).
- **Canonical shape per service.** Each infra service is one
  protocol in `proto/spectre/<slot>/v1alpha1/`, one binary
  per service (language picked per service), N pluggable
  provider implementations behind the protocol, per-language
  SDK clients per ADR-0027, one Compose service entry per
  ADR-0025 (when the service materialises), one Helm chart
  fragment per future Helm work.
- **Admission gate.** A slot becomes built when (a) at least
  one consumer in `engines/`, `adapters/`, `data-platform/`,
  or another `infra-services/` has a concrete need landing in
  the same or a directly preceding PR; (b) at least two
  provider implementations are designed (otherwise the
  abstraction is premature); (c) the service's protocol lands
  in `proto/` in the same PR; (d) the SDK clients land per
  ADR-0027 in the same PR; (e) a deployment posture (Compose
  service entry; Helm placeholder) is recorded in the same
  PR.
- **Catalog amendments require ADR amendments.** Adding a
  sixth slot, removing a slot, or changing the canonical
  shape is a successor ADR. Per-slot details — RPC surfaces,
  provider rosters, admission gates — may evolve without an
  ADR amendment as long as they don't change the canonical
  shape.

## §3 — Canonical service shape

Every infra service in the catalog has the same structural
shape. The shape is normative; per-service specifics fill in
the cells.

### §3.1 — Protocol contract

Each service exposes one gRPC service definition under
`proto/spectre/<slot>/v1alpha1/<slot>.proto`:

```
proto/spectre/proxy/v1alpha1/proxy.proto
proto/spectre/captcha/v1alpha1/captcha.proto
proto/spectre/fingerprint/v1alpha1/fingerprint.proto
proto/spectre/session/v1alpha1/session.proto
proto/spectre/rate_limit/v1alpha1/rate_limit.proto
```

Naming follows ADR-0021's existing `Driver` convention: the
service name is the slot's noun (`Proxy`, `Captcha`,
`Fingerprint`, `Session`, `RateLimit`); the lint-exempted
`SERVICE_SUFFIX` rule (per `buf.yaml`) applies uniformly.

Each protocol's wire contract is provider-agnostic. A consumer
calling `Proxy.Acquire` does not care whether the implementation
is BrightData, Oxylabs, Smartproxy, or a self-hosted
datacenter pool. Provider-specific options surface as
opaque-string fields where unavoidable.

Versioning follows ADR-0004's path-based scheme; today's
catalog is `v1alpha1` across the board.

### §3.2 — Service binary and language

Each service is a single binary process. The implementation
language is chosen per-service for fit, not standardised across
the catalog:

- **Latency-sensitive services with high-fanout external
  calls** (proxy-broker, captcha-solver) are good fits for Rust
  or Go.
- **State-heavy services** (session-store, rate-limit-broker)
  are good fits for Go or Rust with Redis/Postgres as the state
  store.
- **Computation-heavy services** (fingerprint-broker doing
  cryptographic-grade randomness or fingerprint generation) are
  good fits for Rust.

The language choice is recorded in the slot's per-section
entry (§4) when known; for slots not yet built, the language
remains "TBD per build PR".

### §3.3 — Provider implementations

Provider integrations live inside the service's source tree at
`infra-services/<slot>/internal/providers/<vendor>/`. Vendor
SDKs (BrightData client SDK, CapMonster client SDK, ...) are
private to the service: they are dependencies of the service
binary, not of the service's SDK clients. ADR-0026 §3.5
explicitly excludes vendor SDKs from cross-category exposure.

Provider selection is runtime configuration: the service binary
reads a config file or env vars listing enabled providers and
their credentials, picks one per request via the routing policy
the service exposes, and falls through to alternates on
failure.

The "two provider implementations" gate (§5 below) ensures the
abstraction earns its keep. A service with one provider is a
glorified config-and-retry wrapper; a service with two or more
providers proves the abstraction is doing real work.

### §3.4 — SDK clients

Per ADR-0027, each protocol has per-language SDK clients at
`sdks/<lang>/<slot>/v1alpha1/`. Clients ship in whichever
languages have known consumers at the time the slot
materialises. Adding a language later (a Java consumer in some
future phase) is an ADR-0027-governed addition, not an ADR-0028
amendment.

The SDK's wrapper surface (deadlines, retries, error mapping —
ADR-0027 §5.1) is per-protocol. A `Proxy.Acquire` SDK retry
policy may differ from a `RateLimit.Reserve` retry policy; both
follow the same wrapper conventions.

### §3.5 — Deployment posture

When a slot materialises:

- **Compose** (per ADR-0025) — the service is added as a
  Compose service block. Profile assignment matches the
  service's role: `core` (always-on) for proxy/captcha
  /session/rate-limit; `app` for fingerprint (less critical
  in dev). Adapter-only experimentation profile (`adapters`)
  may include or exclude the service depending on whether
  the adapter being explored uses it.
- **Helm** (deferred to R7.1+) — each service ships with a
  Helm chart fragment that mirrors its Compose service. The
  port and DNS conventions established in ADR-0025 §3 carry
  forward.
- **Stateful dependencies** (per ADR-0023) — services that
  need persistent state (session-store, rate-limit-broker,
  proxy-broker for cooldown tracking) declare their stateful
  dependencies explicitly. Today's `postgres` and `redis`
  services are the candidate state backends; per-service
  decision lives in the slot's build PR.

### §3.6 — Observability

Every service exposes:

- **gRPC reflection** (per ADR-0021's existing pattern).
- **Health probe** — gRPC `Health` service following ADR-0025's
  per-base healthcheck convention.
- **Structured logging** to stdout — JSON lines, fields aligned
  with the engine's logging convention (job-id correlation,
  request-id, latency).
- **Metrics** — Prometheus scrape endpoint or OpenTelemetry
  push. The choice is per-service-build-PR.
- **Tracing** — OpenTelemetry traces with trace context
  propagation from the consumer SDK.

The observability surface is normative as a category of
concerns; the specific wire format (Prometheus vs. OTel) is per
build PR.

## §4 — The catalog

Five slots. Two high-conviction (§4.1, §4.2); three probable
(§4.3 – §4.5). Per-slot fields:

- **Status** — `named slot`, `built`, or `superseded`.
- **Conviction** — `high` or `probable`.
- **Purpose** — one paragraph.
- **Indicative RPC surface** — illustrative, not normative.
  The build PR designs the actual surface.
- **Known providers** — non-exhaustive roster.
- **Admission gate notes** — slot-specific notes on when the
  slot becomes built, beyond the generic gates in §5.

### §4.1 — `proxy-broker`

- **Status:** named slot
- **Conviction:** high
- **Proto path:** `spectre.proxy.v1alpha1`
- **Directory:** `infra-services/proxy-broker/`
- **Likely language:** Rust or Go
- **Purpose.** Manage proxy IP addresses for adapter requests.
  Centralises proxy acquisition, rotation, cooldown tracking,
  and budget accounting across all adapters and engines. Without
  this service, every adapter handles proxy state in its own
  process — duplicating logic and losing coordination
  (e.g., two parallel adapter instances unknowingly use the
  same proxy and burn the rate limit twice).
- **Indicative RPC surface.**
  - `Acquire(req: AcquireRequest) -> Lease` — request a proxy
    matching constraints (region, type: residential / datacenter
    / mobile / ISP, sticky-vs-rotating). Returns a lease the
    consumer holds for the duration of its session.
  - `Release(lease: Lease) -> Empty` — return a proxy when done.
  - `ReportFailure(lease: Lease, kind: FailureKind) -> Empty` —
    report a proxy as bad (banned, slow, returning errors)
    for cooldown tracking.
  - `BudgetStatus(scope: BudgetScope) -> Budget` — query
    remaining budget per provider, per region, per tenant.
- **Known providers.** BrightData, Oxylabs, Smartproxy,
  IPRoyal, NetNut, ScraperAPI, datacenter pools, self-hosted
  residential pools, Tor (research-grade).
- **Admission gate notes.** The two-providers gate is trivial
  to satisfy here — virtually any production deployment uses
  at least two providers (cost optimisation across providers is
  itself a real driver). The build PR's interesting design
  questions are around stickiness semantics, lease duration,
  and how cooldown state survives proxy-broker restarts (likely
  Redis or Postgres backing).

### §4.2 — `captcha-solver`

- **Status:** named slot
- **Conviction:** high
- **Proto path:** `spectre.captcha.v1alpha1`
- **Directory:** `infra-services/captcha-solver/`
- **Likely language:** Rust or Go
- **Purpose.** Solve CAPTCHAs encountered by adapters during
  job execution. Accepts a CAPTCHA challenge (image, sitekey
  for hCaptcha / reCAPTCHA / Turnstile), routes to a provider,
  returns a solution token. Adapters do not directly integrate
  with CAPTCHA-solving vendors.
- **Indicative RPC surface.**
  - `Solve(req: SolveRequest) -> SolveResponse` — submit a
    challenge; returns a solution token. The `req` discriminates
    by CAPTCHA type (image, hCaptcha sitekey, reCAPTCHA v2/v3,
    Turnstile). Streamed `SolveProgress` events for long-running
    solves.
  - `Quote(req: QuoteRequest) -> Quote` — get expected cost and
    latency before submitting (for budget-aware adapters).
  - `Cancel(handle: SolveHandle) -> Empty` — cancel an in-flight
    solve (rare; mostly for tests).
- **Known providers.** CapMonster, 2Captcha, AntiCaptcha,
  DeathByCaptcha, NopeCHA, ScrapingAnt's solver mode.
- **Admission gate notes.** The two-providers gate is also
  trivial here — provider availability and cost vary by CAPTCHA
  type, and a real deployment routes per-type. The build PR's
  interesting questions are around streaming-vs-polling for
  solve progress and how the service tracks per-provider error
  rates for adaptive routing.

### §4.3 — `fingerprint-broker`

- **Status:** named slot
- **Conviction:** probable
- **Proto path:** `spectre.fingerprint.v1alpha1`
- **Directory:** `infra-services/fingerprint-broker/`
- **Likely language:** Rust
- **Purpose.** Generate, store, and rotate browser fingerprints
  (User-Agent, Accept-Language, viewport, canvas/WebGL
  fingerprints, TLS JA3, HTTP/2 fingerprint) at a granularity
  shared across adapters. The "probable" conviction reflects
  uncertainty about whether this concern is best handled
  centrally or inside each adapter; the slot is reserved to
  avoid ad-hoc placement if it materialises.
- **Indicative RPC surface.**
  - `Acquire(req: FingerprintRequest) -> Fingerprint` — request
    a fingerprint matching a profile (region, device class,
    browser family). Returns a coherent set of attributes.
  - `Release(fp: Fingerprint) -> Empty` — return a fingerprint
    so it can be retired or rotated by policy.
  - `Generate(req: GenerateRequest) -> Fingerprint` —
    procedurally generate a new fingerprint (vs. drawing from a
    pool of recorded ones).
- **Known providers.** Internal generator (cryptographic
  randomness + curated attribute database), BrowserScan API,
  FingerprintSwitcher, integrations with proxy providers that
  bundle fingerprint rotation.
- **Admission gate notes.** The slot may end up reframed during
  the build PR. Three plausible alternatives the build PR may
  choose:
  - Implement as a service per the canonical shape (this
    catalog's expectation).
  - Move to `shared-libs/<lang>/fingerprint/` if the concern
    turns out to be stateless and per-adapter (no need for a
    service round-trip).
  - Absorb into `proxy-broker` if proxies and fingerprints are
    procured together from the same providers.
  The two-providers gate plus a concrete consumer requirement
  forces the choice in the build PR.

### §4.4 — `session-store`

- **Status:** named slot
- **Conviction:** probable
- **Proto path:** `spectre.session.v1alpha1`
- **Directory:** `infra-services/session-store/`
- **Likely language:** Go
- **Purpose.** Persist browser session state (cookies, local
  storage, IndexedDB, extension data) across adapter restarts
  and across job boundaries. ADR-0023 §5's
  `adapter_instance_id` invalidates sessions on adapter
  restart — the wire-level contract. session-store complements
  it: a session can be *stored* explicitly and *restored* into
  a new adapter instance, decoupling persistence from instance
  lifecycle. Without this service, "log in once, scrape across
  many sessions" requires the engine to manage cookie state
  itself.
- **Indicative RPC surface.**
  - `Store(req: StoreRequest) -> SessionId` — capture an
    adapter's current session state into the store. Idempotent
    by `(adapter_kind, profile_id, session_key)`.
  - `Restore(session_id: SessionId) -> SessionBlob` — fetch a
    stored session for restoration into a new adapter session.
  - `Delete(session_id: SessionId) -> Empty` — explicit
    cleanup; otherwise TTL-based expiry.
  - `List(filter: ListFilter) -> SessionRefs` — administrative
    listing for ops tooling.
- **Known providers.** Redis-backed (default), Postgres-backed
  (durability-first), S3-backed (large blobs), Vault-backed
  (secret-bearing sessions). The "two providers" gate translates
  here to "at least two of these backends".
- **Admission gate notes.** The interesting design question is
  whether session encryption-at-rest is part of the service
  (key management) or a deployer concern (transparent storage
  encryption). The build PR resolves it. The "probable"
  conviction reflects the possibility that the engine's
  Postgres backing (per ADR-0023 §6) absorbs the responsibility
  without a separate service.

### §4.5 — `rate-limit-broker`

- **Status:** named slot
- **Conviction:** probable
- **Proto path:** `spectre.rate_limit.v1alpha1`
- **Directory:** `infra-services/rate-limit-broker/`
- **Likely language:** Go
- **Purpose.** Coordinate per-domain, per-tenant, per-job-class
  request budgets across all engines and adapters. Without this
  service, two parallel engines (or two parallel adapter pods of
  the same kind) can independently breach a per-domain rate
  limit; reconciliation is impossible after the fact.
- **Indicative RPC surface.**
  - `Reserve(req: ReserveRequest) -> Reservation` — request
    permission to make N requests against a scope (domain,
    tenant, job-class). Returns a reservation token; blocks or
    fails per the policy if budget is exhausted.
  - `Release(reservation: Reservation, used: int32) -> Empty` —
    return unused budget after the fact.
  - `Inspect(scope: Scope) -> Budget` — query current budget
    state for ops tooling.
- **Known providers.** Redis-backed token bucket (default),
  Postgres-backed (auditable), in-memory (single-instance dev
  only). The "two providers" gate is harder to satisfy here —
  most deployments pick one backend and stick with it. The
  build PR may relax the gate by treating "in-memory" as a
  legitimate dev backend that satisfies the multi-provider
  requirement; the rationale is recorded if so.
- **Admission gate notes.** The probable conviction reflects
  that for v1alpha1 single-engine deployments, a Redis-backed
  token bucket inside the engine could absorb this concern
  without a separate service. The slot is reserved against the
  multi-engine future.

## §5 — Admission criteria (slot → built)

A named slot becomes built code when **all** of the following
land in a single PR or in a directly-preceding-and-directly-
landing PR pair:

1. **Concrete consumer need.** At least one PR in `engines/`,
   `adapters/`, `data-platform/`, or another `infra-services/`
   has a concrete need that the service is designed to absorb.
   "We will probably need this" is not sufficient. "Job X
   requires proxies and the engine has nowhere to ask for them"
   is sufficient.
2. **Two-provider design.** At least two provider integrations
   are designed (not necessarily implemented in the same PR;
   the design proves the protocol surface absorbs the variation).
   The slot's catalog entry (§4) lists known providers; the
   build PR picks two and shows their fit.
3. **Protocol contract.** `proto/spectre/<slot>/v1alpha1/<slot>.proto`
   lands in the same PR. The protocol may be evolved across
   build PRs (additive RPCs, new message fields per ADR-0004)
   but the initial surface is a coherent commit.
4. **SDK clients.** Per ADR-0027, SDK packages for the
   consumer's language land in the same PR. Other languages
   may be deferred to follow-up PRs as their consumers
   emerge.
5. **Deployment posture.** Compose service block per ADR-0025;
   Helm placeholder per the future Helm work; healthcheck;
   stateful-dependency declaration. The build PR may not solve
   every deployment concern, but the posture is recorded.
6. **Two-provider implementation.** At least one provider is
   wired through end-to-end in the build PR; the second is
   either stubbed (with explicit TODO and a follow-up PR
   reference) or wired in the same PR. A stubbed second
   provider is acceptable but a single-provider build PR is
   not — it reduces the service to a config wrapper.

A slot whose admission criteria cannot be met is *not built*.
The catalog entry stays in §4 with status `named slot` until
the criteria align. There is no penalty for a long-lived named
slot — the cost of the slot is the catalog entry's prose.

A slot that turns out to be the wrong shape (the build PR
discovers the abstraction doesn't fit) **does not silently
mutate**. The build PR is rejected; an ADR amendment supersedes
the catalog entry; subsequent build PRs target the amended
shape. The catalog is a contract.

## §6 — What's not in the catalog (anti-patterns)

The following candidates are **not** infra services. Rejecting
them up-front prevents drift.

- **`secrets-broker`** (centralised provider-API-key service).
  Rejected. Secrets are deployment configuration; their
  management belongs in the deployment system (Kubernetes
  Secrets, Vault, AWS Secrets Manager) consumed via env vars or
  mounted files. A bespoke service would duplicate proven
  infrastructure without benefit.
- **`account-pool`** (managed login accounts for sites
  requiring auth). Rejected at v1alpha1 conviction level.
  Account pools are typically per-tenant, with significant
  legal and operational nuance (TOS compliance, account-bans).
  A future ADR may revisit when the use-case sharpens.
- **`telemetry-collector`** / **`log-aggregator`**. Rejected.
  Off-the-shelf observability infrastructure (Prometheus,
  OpenTelemetry collectors, Loki, Tempo) covers these concerns;
  building a service for them is reinvention.
- **`scheduler`** / **`job-queue`**. Rejected. The control-plane
  operator (per ADR-0019) is the scheduler; future job-queue
  needs evolve the operator's reconciliation logic, not a
  separate service. Adding a job-queue service alongside the
  operator would entangle scheduling responsibilities.
- **`vendor-shim`** (per-vendor adapter that translates
  vendor-specific protocols to a Spectre protocol). Rejected as
  a category. A vendor integration is an internal implementation
  of an existing service's provider layer (e.g., BrightData
  inside `proxy-broker`'s providers), not a sibling service.
- **`config-broker`** / **`feature-flag-service`**. Rejected.
  Feature flags belong in the protocol surface (ADR-0004
  versioning) for protocol-level features, or in the deployment
  system (env vars, K8s ConfigMaps) for deployment-level
  features. A bespoke service is over-engineering for the
  platform's current scale.

The anti-pattern list is not exhaustive; reviewers SHOULD push
back on candidate services that resemble any of the above
shapes. A future ADR can reverse a rejection if circumstances
change; silently building a rejected service is a violation of
this catalog.

## §7 — Confirmation

The catalog is working when:

- **A new infra-service PR** explicitly cites its slot's
  catalog entry in the PR description, demonstrates the
  admission gates in §5, and references the relevant SDK
  packages per ADR-0027.
- **No ad-hoc service** lands in `infra-services/` outside the
  catalog. Reviewers reject PRs introducing un-catalogued
  services and direct contributors to amend this ADR first.
- **Provider sprawl is bounded.** Adding a new provider to an
  existing service is a routine PR (no ADR), but the canonical
  shape — protocol-agnostic surface, vendor SDK private to the
  service — is preserved.
- **The slot/built distinction holds.** Named slots stay named
  until the admission criteria align; catalog entries don't
  silently flip to "built" without a PR satisfying §5.

A signal that the catalog needs revision: a real consumer need
emerges that doesn't fit any catalogued slot and doesn't fit any
anti-pattern in §6. That's evidence the catalog is incomplete;
the response is an ADR amendment adding the new slot, not an
ad-hoc placement.

## §8 — What's deferred / out of scope

R6.6 declines these deliberately. Each is a real concern; each
belongs to a later phase or to a sibling ADR.

- **Implementation of any catalogued slot.** This ADR is the
  catalog. Per-slot build PRs are R7.x and later phase work,
  individually gated by §5.
- **Per-slot RPC surface details.** §4's "Indicative RPC
  surface" entries are illustrative. The build PR designs the
  actual protocol; this ADR does not constrain the surface
  beyond the canonical shape (§3).
- **Per-slot provider rosters.** §4's "Known providers" lists
  are non-exhaustive snapshots. Provider additions/removals
  are not catalog amendments.
- **Multi-version protocol coexistence.** When a catalogued
  slot's protocol moves from `v1alpha1` to `v1alpha2`, ADR-0004's
  versioning posture and ADR-0027's SDK strategy govern. This
  catalog only names `v1alpha1` slots; future versions are
  per-slot evolution.
- **Inter-service composition.** "Engine acquires a proxy from
  proxy-broker, then a CAPTCHA solution from captcha-solver,
  using a fingerprint from fingerprint-broker" — composition
  lives in the engine (ADR-0026 §3.2); the catalog doesn't
  prescribe orchestration.
- **Tenant-isolation policies.** Multi-tenant deployments may
  need per-tenant budget isolation, per-tenant audit trails,
  per-tenant routing rules. The shape is per-service-build
  decision; the catalog doesn't constrain it.
- **The data-platform's relationship to infra-services.**
  Data-platform modules (per ADR-0029) may also consume
  infra-services (e.g., a parser that calls captcha-solver to
  decode a CAPTCHA-protected page). Cross-category consumption
  is allowed by the DAG (per ADR-0026 §5); this catalog
  doesn't add constraints.

## §9 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive. Infra-services are siblings of
  the Driver Protocol on the protocol surface; the canonical
  shape (§3) mirrors the Driver Protocol's posture.
- [ADR-0004](0004-protocol-versioning-strategy.md) — Path-based
  protocol versioning. Each catalogued slot follows this scheme
  for its protocol path.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — Control plane and ScrapeJob CRD. Future CRDs (e.g., a
  `ProxyPool` CRD) may interact with infra-services; the
  catalog reserves protocol slots, not CRD slots.
- [ADR-0021](0021-service-discovery.md) — Service discovery.
  Each infra service registers under its slot's DNS short
  name (`proxy-broker`, `captcha-solver`, etc.) per the
  ADR-0021 §3 pattern.
- [ADR-0022](0022-tcp-grpc-transport.md) — gRPC transport.
  Every infra service speaks gRPC; the §4 RPC surfaces are
  gRPC services.
- [ADR-0023](0023-stateful-services-architecture.md) — Stateful
  services. Infra-services that need persistent state declare
  their dependency on Postgres / Redis per this ADR.
- [ADR-0025](0025-compose-stack.md) — Compose stack. Each
  infra service joins the Compose topology when it
  materialises; the §3 ports / DNS / depends_on conventions
  apply.
- [ADR-0026](0026-platform-taxonomy.md) — Platform taxonomy.
  This ADR fills the `infra-services/` cell (§3.5 of ADR-0026).
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy. Each
  catalogued protocol gets per-language SDK clients per this
  ADR.
- Provider catalogues (referenced for context, not endorsed):
  BrightData, Oxylabs, CapMonster, 2Captcha, AntiCaptcha. The
  catalog includes them as evidence the abstraction has at
  least two providers in each high-conviction slot.
