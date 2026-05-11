# Production smoke (R7.2)

This document describes the production-smoke gate that R7.2
ships at `.github/workflows/production-smoke.yml`. It exists
to give future contributors a single place to learn what the
gate proves, how to read its failures, and how to reproduce
it locally.

R7.2 closed Phase R7 — the v1alpha1 production-posture phase.
R8.1 (documentation refresh + narrative closing) closed the
microservices refactor (R1 → R8.1, 2026-05-03).

## §1 — Purpose

R7.2's gate proves three propositions about the chart at
`build/helm/spectre/`:

1. **The chart installs cleanly** against a fresh kind
   cluster — every Pod (engine, three adapters, operator,
   Postgres, Redis, Kafka, MinIO, mock webhook receiver)
   reaches `Ready`.
2. **Reference ScrapeJobs reach `Completed`** — the
   operator reconciles three v1alpha2 ScrapeJobs (kafka, s3,
   webhook sinks) end-to-end.
3. **Sinks actually receive rows** — Kafka topic carries the
   JSONL message; MinIO bucket has the JSONL object at the
   templated key; webhook receiver logs the POST body.

Proposition 3 is the **distinguishing signal** vs the gates
that already exist:

| Gate (workflow / job)            | Proves                                                        |
|----------------------------------|----------------------------------------------------------------|
| `helm-lint` (R7.1, in `ci.yml`)  | Chart structurally valid: lint, template, kubeval.             |
| `full-stack` (R6.5.2, in `ci.yml`) | Operator → engine → adapter wiring, against the Compose stack. ScrapeJob reaches `Completed`. |
| `production-smoke` (R7.2)        | Chart installs in a real cluster + sinks receive rows. End-to-end correctness through the sink boundary. |

Each gate has a different surface; together they cover the
v1alpha1 production posture.

## §2 — Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  GitHub Actions runner (ubuntu-latest, amd64, 30-min budget) │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  kind cluster (cluster_name: spectre-r72)              │  │
│  │  ┌────────────────────────────────────────────────┐    │  │
│  │  │  namespace: spectre-system                     │    │  │
│  │  │                                                │    │  │
│  │  │  Helm release: spectre  (build/helm/spectre/)  │    │  │
│  │  │   • engine, control-plane, 3 adapters          │    │  │
│  │  │   • Postgres, Redis, Kafka, MinIO (subcharts)  │    │  │
│  │  │  + mock-webhook-receiver (apart from chart)    │    │  │
│  │  │                                                │    │  │
│  │  │  3 ScrapeJobs:                                 │    │  │
│  │  │   • hello-hackernews-kafka   →  Kafka topic    │    │  │
│  │  │   • hello-hackernews-s3      →  MinIO bucket   │    │  │
│  │  │   • hello-hackernews-webhook →  mock receiver  │    │  │
│  │  └────────────────────────────────────────────────┘    │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  3 verifier scripts:                                         │
│   • kubectl exec kafka-controller → kafka-console-consumer   │
│   • kubectl exec minio → mc ls --recursive                   │
│   • kubectl logs mock-webhook-receiver → grep POST + body    │
└──────────────────────────────────────────────────────────────┘
```

The flow is: build images on the runner → load into kind →
helm install with CI overrides → apply 3 ScrapeJobs → wait
`Completed` → run 3 sink verifiers → emit always-run debug
logs.

## §3 — Trigger model

Three triggers, each with a distinct purpose.

**`workflow_dispatch`** — manual invocation from the GitHub
Actions UI. Used by the maintainer to validate a specific
commit, retry a flake, or test a draft PR before opening it.

**`pull_request` (paths-filtered)** — runs on PRs that touch
paths the smoke could plausibly regress:
- `build/helm/**` — chart changes
- `engines/engine/**` — engine source
- `operators/control-plane/**` — operator source
- `adapters/**` — adapter source
- `proto/**` — proto changes
- `tools/test/**` — verifier scripts and sample sync
- `.github/workflows/production-smoke.yml` — workflow itself

PRs that touch only docs, codegen tooling, or unrelated
build files do **not** trigger the smoke. The `images`
matrix in `ci.yml` already catches version-bump breakages
in those paths; R7.2's gate is sink-arrival territory.

**`schedule` (daily 06:00 UTC cron)** — drift detection.
Subchart auto-bumps and base-image refreshes can change
behaviour without any PR diff. The cron catches such drift
within 24 hours of its appearance.

## §4 — CI samples vs source samples

The three CI samples at `build/helm/test/samples/{kafka,s3,webhook}.yaml`
are derived from the source samples at
`operators/control-plane/config/samples/spectre_v1alpha2_scrapejob_*.yaml`.
The derivation is performed by `tools/test/sync-ci-samples.sh`;
the result is verified by `tools/test/check-ci-samples-sync.sh`.

Per-sink rule:

| Sink   | Flip                                                                                      |
|--------|-------------------------------------------------------------------------------------------|
| kafka  | none — `KafkaSink.Brokers` is informational at v1alpha1; engine reads `SPECTRE_KAFKA_BROKERS` env. |
| s3     | `endpoint:` rewritten to the chart-installed MinIO service: `http://spectre-minio.spectre-system.svc.cluster.local:9000`. |
| webhook| `url:` rewritten to the in-cluster mock receiver: `http://mock-webhook-receiver.spectre-system.svc.cluster.local:8888/`. |

The drift-detection invariant catches the case where a
source sample changes (CRD evolution, sink-shape edit) and
the CI copy gets stale. CI fails fast at that gate; the
maintainer runs `just chart-smoke-sync-samples` and commits.

## §5 — The mock webhook receiver

The webhook sink needs an in-cluster receiver that the
engine can POST to. R7.2 deploys a single Pod
`mock-webhook-receiver` running `mendhak/http-https-echo:31`
in the `spectre-system` namespace.

`mendhak/http-https-echo` is a maintained, single-purpose
image whose only job is to log every received HTTP request
to stdout — method, headers, and body. The verifier reads
the pod's logs via `kubectl logs` and asserts the engine's
expected POST shape arrived (per ADR-0024 §4 header schema).

**Digest pinning.** The image is pinned to its multi-arch
manifest list digest in `build/helm/test/mock-webhook-receiver.yaml`.
The pin is the supply-chain anchor; the workflow's
"Verify mock-webhook-receiver digest pin" step checks the
pin against the live registry on each run. If
`mendhak/http-https-echo:31` is republished, a warning
surfaces (the pinned digest still pulls because Docker Hub
preserves digests indefinitely).

**Alternatives considered.** `kennethreitz/httpbin` and
`bahmutov/echo-server` were considered. `mendhak/http-https-echo`
won on three counts: explicit "log every request to stdout"
behaviour (httpbin's stdout is more verbose and harder to
parse), maintained (recent releases vs httpbin's stale
upstream), and small image (~50 MB).

## §6 — Sink verification mechanisms

Per-sink verifier (in `tools/test/`):

### verify-kafka-sink.sh

`kubectl exec` into the Bitnami kafka controller pod
(`spectre-kafka-controller-0`) and run
`kafka-console-consumer.sh --from-beginning --max-messages 1
--timeout-ms 30000` against `spectre.rows.default`. Assert
the captured message is JSON with `title` and `url` fields
(the hello-hackernews extraction).

Asserts: ≥1 message arrived; message is valid JSON; required
fields present.
Does NOT assert: exact message count, exact partition,
exact offset, exact timestamps. Brittle assertions produce
flaky tests.

### verify-s3-sink.sh

`kubectl exec` into the Bitnami minio pod (`spectre-minio-*`)
and run `mc ls --recursive local/spectre-rows/scrapes/`.
Assert ≥1 non-empty `.jsonl` object exists under the
prefix.

Asserts: ≥1 object under `scrapes/`; object size > 0; key
ends in `.jsonl`.
Does NOT assert: exact key (the `{{.JobID}}` template
generates a fresh ID per run); exact object count; exact
row count within the object.

### verify-webhook-sink.sh

`kubectl logs` the mock receiver pod. Assert ≥1 POST
request arrived with the engine's expected ADR-0024 §4
header schema and a JSONL body containing the `title`
field.

Asserts: ≥1 POST in logs; required headers present
(soft-warn on absence — a header rename shouldn't block the
gate before the maintainer can adjust); body contains
`title` field.
Does NOT assert: exact request count, exact body bytes,
exact retry behaviour.

All three verifiers are **idempotent** — re-running produces
the same result. They have **internal timeouts** so a
hanging consumer or unresponsive pod doesn't burn the job's
30-min ceiling.

### verify-observability.sh

Added in W3.1 (2026-05-11). Asserts the engine + operator
observability surface lands per ADR-0031 §3 / §5:

- `kubectl exec` against the engine pod + `curl localhost:9090
  /metrics` returns 200 and the response includes the
  `spectre_engine_jobs_active`, `spectre_engine_jobs_completed_total`,
  and `spectre_engine_rows_emitted_total` series (the three
  guaranteed-to-have-samples instruments after the preceding
  steps complete three ScrapeJobs).
- Same against the control-plane pod for
  `spectre_operator_scrapejobs_total` (W3.1 §5.2 custom) and
  `controller_runtime_reconcile_total` (controller-runtime
  default — surfaces alongside the spectre custom on the same
  endpoint, confirming the metrics server's port flip).
- `kubectl logs` the engine pod and assert at least one JSON
  line carries a non-empty `trace_id` (32-hex). That line is
  the `engine.assemble_row` event emitted per row by the
  drainer loop under W3.1 Cluster D's `tracing-opentelemetry`
  bridge.

Asserts: §5.1 / §5.2 metric names present, trace_id field
populated in at least one log event.
Does NOT assert: exact scrape values (workload-dependent), span
tree shape (no trace-backend assertion), tenant_id population
(always `null` in v1alpha1).

## §7 — Debugging failures

Common failure modes and their diagnosis:

**Pod stuck `Pending`.** Usually a `nodeSelector` mismatch
(R7.2 runs on amd64 GitHub runners; the chart's amd64-only
adapters schedule normally) or resource pressure. Inspect
with `kubectl -n spectre-system describe pod <name>`; the
`Events` section names the issue.

**ScrapeJob stuck `Pending`.** The operator can't reach the
engine. Check the operator log (`kubectl -n spectre-system
logs -l app.kubernetes.io/component=control-plane`) — the
typical cause is the engine readiness probe still red.

**ScrapeJob `Failed` with `KAFKA_UNAVAILABLE`.** The engine
started but couldn't reach Kafka. Check the engine's
`SPECTRE_KAFKA_BROKERS` env (should be `spectre-kafka:9092`)
and the kafka pod's status.

**s3 verifier finds no objects.** The engine couldn't
authenticate to MinIO. Check the engine's `SPECTRE_S3_*`
env vars are set from the Bitnami minio Secret. The chart's
`_helpers.tpl` `spectre.commonEnv` block wires these.

**webhook verifier finds no POSTs.** The mock receiver
wasn't ready before the chart install (race). The workflow
applies the receiver manifest BEFORE `helm install` and
waits for `rollout status` to ensure ordering. If the order
breaks, the receiver pod's age will be younger than the
ScrapeJob's first reconciliation.

The workflow's `if: always()` debug steps (operator logs,
engine logs, adapter logs, receiver logs, ScrapeJob YAML)
capture state for forensic inspection. The kind cluster is
torn down by `helm/kind-action` automatically when the job
ends; download the run's logs from GitHub before the
artifacts expire.

## §8 — Local reproduction

Three justfile recipes:

```bash
# One-time setup: build images, kind cluster, chart install,
# mock receiver. ~5-10 minutes on a cold cache.
just chart-smoke-up

# Run the smoke tests. Idempotent; safe to retry. ~3-5
# minutes.
just chart-smoke-test

# Tear down everything.
just chart-smoke-down
```

Local reproduction uses the `spectre-dev` kind cluster
(the standard local cluster from R6.3); CI uses
`spectre-r72` to avoid collision with `full-stack`'s
`spectre-ci` on the same daemon.

When local-vs-CI results diverge:
- Image build differences: ensure local has run a fresh
  bake build at the same `REGISTRY`/`TAG` the CI uses.
- Cluster-version differences: CI uses `helm/kind-action@v1`
  default kind version; local uses whatever the dev's
  `kind` binary ships.
- DNS-resolution differences: in-cluster service names
  resolve identically; mock receiver's URL does too.

## §9 — What's intentionally out of scope

R7.2 ships in-cluster verification only. Out of scope:

- **Real-cloud sink integrations** (real AWS S3, real
  Confluent Cloud Kafka, real webhook with TLS). v1alpha1
  scope is in-cluster MinIO + Kafka. Real-cloud verification
  is post-refactor (or a separate optional workflow if the
  maintainer chooses).
- **Per-driver sample matrix.** All three R7.2 ScrapeJobs
  use `playwright` driver. The driver matrix is
  conformance-suite territory (per `python` job in
  `ci.yml`); R7.2 is sink-boundary territory.
- **Cross-cluster federation tests.** Far out of scope.
- **Observability tests** (metrics endpoints, traces).
  Out of scope.
- **Webhook authentication** (HMAC, bearer tokens).
  ADR-0024 §8 defers to v1alpha2.
- **Helm chart publish to OCI registry.** Post-refactor.

## §10 — References

- [ADR-0023](../adr/0023-stateful-services-architecture.md) §3
  — Kafka topology + sink contract.
- [ADR-0024](../adr/0024-output-sinks.md) §3 (s3) + §4
  (webhook) — the contracts the verifiers assert.
- [ADR-0030](../adr/0030-helm-chart-structure.md) §9
  — production smoke deferred from R7.1.
- [docs/architecture/helm-chart.md](helm-chart.md) §9 — CI
  integration overview (helm-lint + production-smoke).
- `.github/workflows/production-smoke.yml` — the workflow.
- `build/helm/test/` — values overrides, mock receiver, CI
  samples.
- `tools/test/verify-*-sink.sh` — verifier scripts.
- `tools/test/sync-ci-samples.sh`,
  `tools/test/check-ci-samples-sync.sh` — sample sync +
  drift gate.
- `mendhak/http-https-echo` upstream:
  <https://github.com/mendhak/docker-http-https-echo>.
- `helm/kind-action`:
  <https://github.com/helm/kind-action>.
- Bitnami chart conventions: <https://charts.bitnami.com/>.

## §11 — v1alpha2 forward-look

> *Added 2026-05-06 (R9.6). The above sections describe the
> R7.2-landed production-smoke gate — Helm install + 3
> ScrapeJobs (one per adapter) → assert sink arrival. Phase
> R9 commits to extending the gate per service materialisation
> across Waves 5 – 10; this subsection forwards readers to
> the v1alpha2 surface.*

The R7.2 production-smoke contract — Helm install into kind
cluster; deploy ScrapeJob CRs; assert outputs reach
configured sinks — is **the v1alpha2 contract base**. Each
Wave 5+ build PR extends the gate per
[`service-shape.md` §8](service-shape.md) step 14:

- **Wave 5** — proxy-broker + captcha-solver smoke:
  ScrapeJobs assert proxy lease acquisition + CAPTCHA
  solve trigger paths; no real provider integrations
  exercised (vendor-mock fixtures).
- **Wave 6** — schema-registry + input-broker smoke:
  ScrapeBatch CR with 100 URLs; assert
  `status.inputSourceStatus.succeeded` reaches 100;
  schema-validation per row; per-batch progress aggregation.
- **Wave 7** — rate-limit-broker + fingerprint-broker
  smoke; v1alpha2 DSL primitive smoke (pagination,
  conditional, multi-step navigation, transforms).
- **Wave 8** — session-store + secret-broker + scheduler
  smoke. mTLS smoke when `cert-manager.enabled: true` per
  [ADR-0032 §8](../adr/0032-service-to-service-mtls.md).
- **Wave 9** — cost-tracker + audit-log smoke; per-job
  cost ledger + per-tenant rollup compute exercised;
  ScrapeBatch `status.totalCost` aggregation verified.
- **Wave 10** — dedup-service + enricher + driver-router
  smoke per ADR-0035 §6's resolved decision.

The smoke-cluster topology grows across Waves; the
underlying R7.2 mechanisms (kind-action; chart install;
sample-sync drift gate) are unchanged. The
`production-smoke.yml` workflow auto-extends per
[`service-shape.md` §5](service-shape.md).

ADR-0034 §9 + ADR-0033 §10 + ADR-0038 §10 codify the
per-Wave acceptance criteria the smoke gate enforces.
