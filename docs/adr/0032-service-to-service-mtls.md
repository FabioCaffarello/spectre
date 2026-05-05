---
status: accepted
date: 2026-05-05
deciders: [Fabio Caffarello]
---

# Service-to-service authentication via mTLS

## §1 — Context and Problem Statement

The v1alpha1 platform's service-to-service authentication
posture is **none**. The engine accepts gRPC from any caller
that can reach its TCP port; adapters accept gRPC from any
caller that can reach theirs; the operator dials the engine
unauthenticated. The
[Compose stack](0025-compose-stack.md) and the
[Helm chart](0030-helm-chart-structure.md) rely on
**network-layer isolation** (Compose's user-defined network;
Kubernetes NetworkPolicies if the cluster operator
configures them) for any access control. Within the network,
every service trusts every caller.

The shape was defensible in v1alpha1 — single-tenant
deployments where the cluster boundary was the trust
boundary; the refactor's velocity prioritised the protocol
contract over auth scaffolding. The shape is **not
defensible** in v1alpha2:

- **Multi-tenant deployments** become real once the user
  pilot (Wave 4) closes and shared clusters host workloads
  for multiple tenants. Network-layer isolation is
  insufficient — a compromised adapter Pod inside the
  cluster could call any service in any tenant's namespace
  unauthenticated.
- **15 catalog services** ([ADR-0036](0036-microservices-catalog-expansion.md))
  multiply the trust surface. Each per-step call from the
  engine fans out to N services; without per-service
  authentication, any cluster-internal compromise has full
  lateral movement across the platform.
- **The audit-log service** (slot 8 per ADR-0036 §3.3)
  records per-job decisions; without authenticated
  emissions, the audit log cannot prove provenance — any
  cluster-internal caller could forge audit records
  attributed to any service.
- **The secret-broker service** (slot 13 per ADR-0036 §3.6)
  brokers credentials; without authenticated callers, it
  cannot enforce per-service access policies (proxy-broker
  may need BrightData credentials but should not access
  Postgres credentials, for example).

This ADR commits the v1alpha2 platform to **mutual TLS
(mTLS)** as the service-to-service authentication mechanism,
provisioned via **cert-manager** and the chart's
`_helpers.tpl` certificate templates. It is one of two
cross-cutting framework ADRs in R9.3 (the other is
[ADR-0031](0031-observability-framework.md) — observability
framework); together they make the canonical service shape
from ADR-0036 §5.4 + §5.5 normative for every Wave 3+ build
PR.

### §1.1 — What ADR-0036 §5.5 already commits

ADR-0036 §5.5 lists the canonical mTLS surface each
service exposes: a cert-manager Certificate via the chart's
`_helpers.tpl` template, gated by the chart's
`cert-manager.enabled` flag (default `false`); when the
flag is on, service-to-service gRPC traffic uses mTLS by
default.

That commitment is structural — it tells a build PR *what
the canonical shape looks like*, not *which CA, which
algorithm, which validity period, what rotation policy*.
ADR-0032 fills the *how*: certificate authority shape,
per-service certificate template, engine ↔ adapter ↔
operator wiring, the chart's `cert-manager.enabled` flag
semantics, and the per-service certificate-rotation policy.

### §1.2 — What this ADR does not yet land

No service code, no certificate generation, no chart
template files land in R9.3. This ADR is contract-only. The
first mTLS PR is **Wave 3** (per
[`docs/roadmap.md`](../roadmap.md) §4 in R9.7) — likely the
operator ↔ engine mTLS wiring (the simplest case: two
services already deployed in v1alpha1, one TLS dial). Wave
5+ engine ↔ infra-service mTLS wiring follows per the
canonical service shape ADR-0036 §5.5 mandates.

## §2 — Decision summary

R9.3 commits the platform to the following authentication
posture. Each commitment is **normative** for Wave 3+ build
PRs.

### §2.1 — mTLS as the authentication primitive

**Mutual TLS (mTLS)** authenticates every service-to-service
gRPC call. The choice over alternatives:

- **Bearer tokens (JWT, opaque)** would work but require a
  token-issuer service (a 16th service ADR-0036 doesn't
  catalogue) plus per-service token validation logic. mTLS
  delegates auth to the TLS layer where Go / Rust / Python
  / TypeScript all have mature implementations.
- **API keys** (per-caller static keys) are operationally
  simple but require manual key rotation and lack identity
  semantics — the called service knows "the caller had a
  valid key", not "the caller is service X".
- **mTLS** authenticates **identity** (the caller's
  certificate Common Name / Subject Alternative Names is
  the caller's service identity) and **transport** (the
  TLS layer encrypts in-flight; eavesdropping is
  cryptographically unsound).

mTLS is the cloud-native default for service-to-service
auth (Istio, Linkerd, Consul Connect — every service mesh
adopts it as the primitive). The choice aligns the platform
with existing Kubernetes-native patterns rather than
inventing a Spectre-specific scheme.

### §2.2 — cert-manager as the issuance primitive

[**cert-manager**](https://cert-manager.io/) provisions
certificates per service. The choice over alternatives:

- **Manual certificate management** (operators generating
  certs and mounting them as Secrets) is operationally
  brittle — rotations require manual coordination; expiry
  causes downtime; multi-cluster rollout is per-cluster
  toil.
- **Bespoke per-service issuance** (each service mints its
  own certificate) inverts the trust model — services
  cannot prove identity if they self-sign.
- **Service mesh integration** (Istio / Linkerd handle
  cert-manager-equivalent provisioning automatically)
  works but couples the platform to a specific mesh; the
  mesh adoption is a deployment-side decision the platform
  should not pre-decide.
- **cert-manager** is the Kubernetes-native standard for
  certificate provisioning, with mature ACME / Vault /
  internal-CA integrations and a CRD-driven declarative
  model that fits the chart's existing template-driven
  shape.

cert-manager is **not** an internal CA itself — it manages
certificates from any configured `Issuer` (the cluster's
choice). The chart's templates create the per-service
`Certificate` resources; cert-manager issues from whichever
`Issuer` the deployment configures.

### §2.3 — Per-service certificates

Each catalogued service receives its **own certificate**
provisioned by cert-manager. The certificate's identity:

- **Common Name (CN)**: `<slot>.<release-namespace>.svc`
  matching the Kubernetes Service DNS name per
  [ADR-0021](0021-service-discovery.md) §3
- **Subject Alternative Names (SANs)**:
  `<slot>.<release-namespace>.svc.cluster.local` (the
  cluster-local form);
  `<slot>.<release-namespace>` (the short form);
  `<slot>` (within-cluster short DNS form)
- **Validity period**: 90 days (the Let's Encrypt-aligned
  default; rotation per §5.4 prevents expiry-driven
  outages)
- **Renewal**: 30 days before expiry (cert-manager
  default)
- **Algorithm**: RSA 2048-bit (broad client compatibility)
  or ECDSA P-256 (when the `Issuer` supports it; preferred
  for performance)

The certificate's identity is **the service's identity** —
mTLS handshakes verify the caller's certificate against the
expected service identity, and the called service's
authorisation logic uses the verified identity for per-RPC
access decisions.

### §2.4 — Chart's `cert-manager.enabled` flag

The chart's `values.yaml` exposes a top-level
`cert-manager.enabled` flag (default **`false`**). When
`false`:

- Per-service `Certificate` resources are **not rendered**
  in the chart output.
- Services dial each other over **plaintext gRPC** (the
  v1alpha1 posture).
- Deployments without cert-manager installed continue to
  work without modification.

When `true`:

- Per-service `Certificate` resources render via the
  chart's `_helpers.tpl` template (`§3.4 below`).
- Services dial each other over **mTLS** (verified peer
  certificates).
- Deployments must have cert-manager installed in the
  cluster (the chart's `Chart.yaml` does not bundle
  cert-manager; it is a deployment-side prerequisite).

The default-off posture is **deliberate** — many existing
Kubernetes deployments do not run cert-manager; forcing it
as a hard prerequisite would block the chart from those
clusters. Operators with cert-manager already installed
flip the flag on; operators without it continue with the
v1alpha1 plaintext posture (acceptable for single-tenant
deployments per §1's framing).

The flag is **deployment-wide**, not per-service. Partial
mTLS deployments (some services on mTLS, others on
plaintext) introduce a confused trust boundary that this
ADR explicitly rejects.

## §3 — Certificate authority shape

### §3.1 — The Issuer is deployment-side

The chart **does not commit a specific `Issuer`** —
cert-manager supports many issuer types
(`SelfSigned`, `CA`, `Vault`, `ACME`, `Venafi`, ...) and
the right choice is deployment-side:

- **Single-cluster development**: `SelfSigned` Issuer
  (lowest setup overhead; no external dependency)
- **Single-cluster production**: internal `CA` Issuer
  rooted in the deployment's trust anchor
- **Multi-cluster production**: cluster-shared `Vault`
  Issuer (HashiCorp Vault-backed PKI mount)
- **Public-facing endpoints** (rare; the platform is
  internal-cluster-only): `ACME` Issuer (Let's Encrypt or
  equivalent)

The chart's `values.yaml` provides an `issuerRef` field
that the cluster operator wires to whichever `Issuer` /
`ClusterIssuer` is appropriate:

```yaml
cert-manager:
  enabled: true
  issuerRef:
    name: spectre-internal-ca
    kind: ClusterIssuer
```

The chart's `_helpers.tpl` references `issuerRef` from each
`Certificate` resource; the operator chooses the issuer.

### §3.2 — Trust bundle

When `cert-manager.enabled: true`, the chart references the
**cluster's trust bundle** (the CA certificate the `Issuer`
chains to) for peer verification. Two paths:

- **`trust-manager`** (cert-manager's companion controller):
  the chart consumes a `Bundle` resource that
  trust-manager renders as a ConfigMap mounted into each
  service Pod. The bundle is the source of truth for which
  CAs the services trust.
- **Manual bundle**: the cluster operator provides a
  ConfigMap directly via
  `cert-manager.trustBundleConfigMapName` in `values.yaml`.

The chart prefers `trust-manager` when available
(`trust-manager` is the cert-manager-blessed path); the
manual fallback exists for clusters that do not run
trust-manager.

### §3.3 — Certificate naming

Per-service `Certificate` resources follow a uniform naming
convention:

```
<release-name>-<slot>-cert
```

For a release named `spectre` deploying the
`proxy-broker` slot: `spectre-proxy-broker-cert`. The
certificate's resulting Secret has the same name; the
chart mounts the Secret as a volume into the service Pod
at `/etc/spectre/tls/`.

### §3.4 — Chart's `_helpers.tpl` certificate template

The chart's `templates/_helpers.tpl` gains a named template
that renders a per-service `Certificate`:

```
{{- define "spectre.certificate" -}}
{{- if .Values.cert-manager.enabled -}}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ .Release.Name }}-{{ .slot }}-cert
  namespace: {{ .Release.Namespace }}
spec:
  secretName: {{ .Release.Name }}-{{ .slot }}-cert
  issuerRef:
    {{- toYaml .Values.cert-manager.issuerRef | nindent 4 }}
  commonName: {{ .slot }}.{{ .Release.Namespace }}.svc
  dnsNames:
    - {{ .slot }}
    - {{ .slot }}.{{ .Release.Namespace }}
    - {{ .slot }}.{{ .Release.Namespace }}.svc
    - {{ .slot }}.{{ .Release.Namespace }}.svc.cluster.local
  duration: 2160h  # 90 days
  renewBefore: 720h  # 30 days
  privateKey:
    algorithm: ECDSA
    size: 256
    rotationPolicy: Always
{{- end -}}
{{- end -}}
```

Per-service template files (`templates/<slot>-cert.yaml`)
invoke the named template:

```
{{- include "spectre.certificate" (dict "slot" "proxy-broker"
                                        "Release" .Release
                                        "Values" .Values
                                        "Namespace" .Release.Namespace) }}
```

The pattern follows the existing chart template-helper
conventions ADR-0030 §3 commits. The first Wave 3 mTLS PR
materialises the template; subsequent services reference it.

## §4 — Engine ↔ adapter ↔ operator wiring

The v1alpha1 service set already deployed (engine, three
adapters, control-plane operator) gets mTLS wiring in
**Wave 3** as the first auth PR. The wiring expectations:

### §4.1 — Operator → engine

- Operator dials engine over gRPC per
  [ADR-0019 §5](0019-control-plane-architecture-and-scrapejob-crd.md)'s
  `EngineClientRunner`.
- When `cert-manager.enabled: true`, the operator's
  `EngineClientRunner` loads its own certificate from
  `/etc/spectre/tls/tls.crt` + `/etc/spectre/tls/tls.key`
  and the cluster's trust bundle from
  `/etc/spectre/tls/ca.crt`.
- The operator's gRPC client uses TLS credentials (Go's
  `credentials.NewTLS` with the loaded credentials) for
  the dial.
- The engine's gRPC server requires client certificates
  (`tls.RequireAndVerifyClientCert`).
- **Authenticated identity**: the engine receives the
  operator's verified identity (`spectre-control-plane.<ns>.svc`)
  and authorises per-RPC access (the `RunJob` RPC, for
  example, requires operator identity).

### §4.2 — Engine → adapters

- Engine dials adapters over gRPC per
  [ADR-0021 §3](0021-service-discovery.md)'s service
  discovery.
- When `cert-manager.enabled: true`, the engine's adapter
  client loads engine credentials and verifies the
  adapter's server certificate against the trust bundle.
- The adapter's gRPC server requires client certificates;
  it receives the engine's verified identity.
- **Authenticated identity**: each adapter receives engine
  identity (`spectre-engine.<ns>.svc`); only the engine is
  authorised to call adapter RPCs (no cluster-internal
  caller can dial an adapter directly without engine
  identity).

### §4.3 — Engine → infra-services (Wave 5+)

Each Wave 5+ infra-service ships its mTLS wiring per the
canonical service shape ADR-0036 §5.5. The wiring is
**uniform** across services — the same chart template
(§3.4) provisions the certificate; the same per-language
gRPC TLS wiring (Go's `credentials.NewTLS`; Rust's `tonic`
TLS via `rustls`; Python's `grpc.ssl_channel_credentials`;
TypeScript's `@grpc/grpc-js` `credentials.createSsl`) loads
and configures it; the same authorisation pattern (verified
caller identity → per-RPC authorisation decision) applies.

### §4.4 — Per-service authorisation policies

mTLS provides **authentication** (who is the caller); it
does not provide **authorisation** (what is the caller
allowed to do). Per-service authorisation policies are
**per-service-build-PR concerns**:

- `proxy-broker` may authorise only engine + adapter
  identities to call `Proxy.Acquire`; reject others.
- `secret-broker` may authorise per-service identity to
  fetch only that service's secrets (proxy-broker fetches
  proxy credentials; captcha-solver fetches captcha
  credentials; cross-service secret access is rejected).
- `audit-log` may authorise any service identity to emit
  `Audit.Emit`; restrict `Audit.Query` to operator +
  user-facing identities.

Authorisation policies live in the per-service code; this
ADR commits the **identity** primitive (mTLS-verified
service identity); per-service-build PRs commit the
authorisation policy.

## §5 — Operational shape

### §5.1 — Certificate rotation

cert-manager handles rotation automatically — at 30 days
before the certificate's 90-day expiry, cert-manager
reissues, and the new certificate's Secret is updated
in-place. Services must **reload** their TLS credentials
without a Pod restart:

- **Go services**: use `credentials.NewServerTLSFromCert` /
  `NewClientTLSFromCert` with periodic reload (the
  `crypto/tls` package supports `Config.GetCertificate` for
  per-handshake credential lookup).
- **Rust services**: `tonic` + `rustls` with
  `ServerConfig::cert_resolver` for dynamic cert reload.
- **Python services**: `grpc.ssl_server_credentials` with
  reload via service restart on Secret update (Python's
  gRPC bindings have weaker dynamic-credential support; a
  rolling restart is acceptable at the rotation cadence).
- **TypeScript services**: `@grpc/grpc-js` with credential
  reload via `Server.bindAsync` on Secret update.

The reload pattern is **per-language**; the per-language
SDK build PRs (per ADR-0027) ship the appropriate reload
plumbing in `sdks/<lang>/common/`.

### §5.2 — Health probes during rotation

Kubernetes-native `grpc:` health probes (per ADR-0030 §3)
must continue to pass during rotation. The Go / Rust gRPC
servers handle this transparently; the Python / TypeScript
restart-on-rotation path may cause brief probe-failure
windows during the restart — operators may tune
`livenessProbe.failureThreshold` accordingly for those
services.

### §5.3 — Trust bundle rotation

The cluster's trust bundle (the CA certificate chain)
rotates separately from per-service certificates. When the
bundle rotates:

- **trust-manager-managed**: the bundle ConfigMap updates
  in-place; services reload via the same path as
  per-service rotation.
- **Manually-managed**: the cluster operator updates the
  ConfigMap; services may need a rolling restart depending
  on the per-language reload behaviour.

Trust bundle rotation is a **rare event** (typically
multi-year cycles for internal CAs); the operational cost
is acceptable.

### §5.4 — Disaster recovery

When mTLS scaffolding fails (cert-manager outage; trust
bundle corruption; certificate expiry not handled by
rotation), the chart's `cert-manager.enabled: false` toggle
is the **escape hatch** — operators flip the flag off,
roll out the chart, and services revert to plaintext gRPC.
The platform continues to function in degraded auth posture
while the cert-manager issue resolves.

This is the **explicit DR path** — single-tenant
deployments accept this trade-off; multi-tenant deployments
treat the toggle-off as an incident requiring resolution
before resuming operation.

## §6 — Migration sequence

R9.3's ADR-0031 + ADR-0032 are documentation-only; no
service code lands. Per-service mTLS wiring lands
incrementally across Waves 3 onwards:

| Wave | mTLS scope |
|---|---|
| Wave 3 (first auth PR) | Operator ↔ engine mTLS. Two services already deployed; the simplest end-to-end mTLS pair. The chart's `cert-manager.enabled` flag and `_helpers.tpl` certificate template land. Per-language reload plumbing (Go for operator; Rust for engine) lands in `sdks/<lang>/common/`. |
| Wave 3 (second auth PR) | Engine ↔ adapter mTLS. The three adapters (Playwright TS / SeleniumBase Python / curl-impersonate Go) extend the per-language reload plumbing. |
| Wave 5 (proxy-broker + captcha-solver) | First infra-service mTLS wiring. Each service ships with the canonical certificate template invocation per §3.4. |
| Wave 6+ | Per-service mTLS uniform across the canonical service shape. No additional ADR needed; ADR-0036 §5.5 + ADR-0032 are the contract. |
| Wave 8+ | Per-service authorisation policies (§4.4). The authorisation layer is per-service-build PR scope; ADR-0032 commits the identity primitive, not the per-service policies. |

The Wave 3 first-auth PR is **transformational scope**
under the v1alpha2 process rigor matrix
([CONTRIBUTING.md](../../CONTRIBUTING.md), R9.0) — it
introduces the cert-manager scaffolding, the chart template
helper, and the per-language reload plumbing the Wave 5+
services extend. Subsequent per-service mTLS wiring is
**incremental scope** (no ADR; canonical service shape
applies).

## §7 — Webhook authentication deferred

The `OutputSink.Webhook` per
[ADR-0024](0024-output-sinks.md) §4 dispatches HTTP requests
to user-configured external endpoints. mTLS is **not** the
authentication primitive for webhook callers — the platform
emits to the user's webhook, not the other way around. The
authentication direction is opposite from service-to-service
mTLS.

Webhook authentication is **deferred to its own follow-up
PR** outside R9.3 scope:

- **HMAC-SHA-256** signing of webhook payloads is the
  likely v1alpha2 path — the engine signs each payload with
  a per-tenant shared secret; the receiver verifies the
  signature.
- **Bearer token** authentication (the engine sends a
  per-receiver bearer token in `Authorization`) is an
  alternative; less common in webhook patterns.
- **mTLS as an option for webhook receivers** is feasible
  when the receiver runs cert-manager-equivalent
  infrastructure; the engine emits client certificates and
  the receiver verifies them. The chart's
  `cert-manager.enabled` flag carries forward.

The decision deferral matches ADR-0024's existing scope —
webhook authentication is a per-deployment, per-receiver
configuration concern that does not need to land in R9.3.
A future ADR (numbered after R9.3) settles the webhook auth
shape.

## §8 — Confirmation (acceptance criteria)

mTLS is working when the following hold **by the close of
Wave 5**:

- **Every Wave-5+ infra-service ships with a
  `Certificate` resource** rendered by the chart's
  `_helpers.tpl` template (§3.4) when `cert-manager.enabled:
  true`. No service ships without the certificate
  scaffolding.
- **Engine-side dials verify peer certificates** — the
  engine's adapter client dials with TLS credentials and
  rejects connections with no certificate / invalid
  certificate / expired certificate when `cert-manager.enabled:
  true`.
- **Per-service authorisation policies enforce caller
  identity** — at least one Wave-5+ infra-service rejects
  unauthorised caller identities with a `PERMISSION_DENIED`
  gRPC code (per ADR-0009's error mapping).
- **Certificate rotation does not cause cascading restarts**
  — at least one rotation cycle completes in production
  smoke (R7.2 extended for Wave 5) without service
  unavailability.
- **The `cert-manager.enabled: false` plaintext posture
  continues to work** in production smoke alongside the
  enabled posture; operators without cert-manager are not
  blocked.
- **Trust bundle is correct** — services trust certificates
  from the configured `Issuer` and reject certificates from
  any other issuer (verified by integration tests in CI).

A signal that the framework needs revision: more than one
Wave build PR encounters an mTLS use-case that doesn't fit
§3 – §5. That's evidence the canonical surface is
incomplete; the response is an ADR amendment that extends
the surface, not a per-service deviation.

## §9 — What's deferred / out of scope

R9.3 declines these deliberately. Each is a real concern;
each belongs to a later phase or to deployment-side
configuration.

- **The choice of cert-manager `Issuer`** — deployment-side
  configuration per §3.1.
- **Per-service authorisation policies** — per-service-build
  PR scope per §4.4. ADR-0032 commits the identity
  primitive; per-service policies are per-service decisions.
- **Webhook authentication** — deferred per §7 to its own
  follow-up ADR.
- **Service mesh integration** (Istio / Linkerd / Consul
  Connect) — service-mesh adoption is a deployment-side
  decision; the platform's mTLS posture works under any
  mesh that respects the existing certificate provisioning,
  but the chart does not commit to a specific mesh.
- **Mutual TLS for stateful services** (Postgres / Redis /
  Mongo / Kafka). These have their own auth schemes
  (SCRAM-SHA-256, X.509 cert-based per ADR-0023 §14.4 for
  Mongo); ADR-0032 commits service-to-service mTLS, not
  service-to-stateful-tier auth.
- **Certificate transparency / public CA logging** —
  internal-only certificates do not require CT logging;
  external-facing endpoints (rare in v1alpha2) follow
  whichever ACME `Issuer` provides CT logging by default.
- **Hardware-backed certificate storage** (HSM, TPM, secure
  enclave) — out of scope; cert-manager's Kubernetes Secret
  storage is the v1alpha2 default.
- **External-caller authentication** — the platform is
  internal-cluster-only at v1alpha2; external API surfaces
  are a v1beta1 concern (the operator's potential REST API
  for ScrapeJob submission, for example).
- **End-user authentication** — distinct from
  service-to-service. Multi-tenant per-user auth is a
  v1beta1 concern.
- **Audit log for certificate operations** — cert-manager's
  own audit logging covers this at the cluster layer; the
  platform does not duplicate.
- **Rate limits on TLS handshakes** — TLS handshake DoS is
  a deployment-side concern handled by ingress / network
  policy; the platform does not implement application-layer
  handshake rate limiting.

## §10 — Reference materials

- [ADR-0001](0001-driver-protocol-as-architectural-primitive.md)
  — Driver Protocol primitive; mTLS overlays the protocol
  unchanged.
- [ADR-0009](0009-navigate-and-session-lifecycle.md) —
  driver error mapping; `PERMISSION_DENIED` (§8) follows
  ADR-0009's gRPC status mapping.
- [ADR-0019](0019-control-plane-architecture-and-scrapejob-crd.md)
  — control plane; the operator → engine mTLS path.
- [ADR-0021](0021-service-discovery.md) — service discovery;
  certificate SANs (§2.3) match service-discovery DNS
  patterns.
- [ADR-0022](0022-tcp-grpc-transport.md) — gRPC transport;
  mTLS layers on the same TCP / gRPC transport.
- [ADR-0023](0023-stateful-services-architecture.md) —
  stateful services; ADR-0023 §14.4 commits Mongo X.509 auth
  with the same per-service certificate provisioning.
- [ADR-0024](0024-output-sinks.md) — output sinks; webhook
  authentication deferred per §7.
- [ADR-0027](0027-sdk-strategy.md) — SDK strategy;
  per-language reload plumbing (§5.1) lives in
  `sdks/<lang>/common/`.
- [ADR-0030](0030-helm-chart-structure.md) — Helm chart;
  the `_helpers.tpl` certificate template (§3.4) extends
  the chart's existing helper conventions.
- [ADR-0031](0031-observability-framework.md) — observability
  framework; mTLS-handshake metrics (TLS handshake count,
  rejection count) follow ADR-0031's per-service metric
  taxonomy.
- [ADR-0036](0036-microservices-catalog-expansion.md) —
  the 15-service catalog; §5.5 canonical mTLS surface that
  this ADR makes normative.
- cert-manager documentation: <https://cert-manager.io/docs/>
- trust-manager documentation:
  <https://cert-manager.io/docs/trust/trust-manager/>
- gRPC TLS configuration:
  <https://grpc.io/docs/guides/auth/>
- Kubernetes TLS bootstrapping: <https://kubernetes.io/docs/reference/access-authn-authz/kubelet-tls-bootstrapping/>
