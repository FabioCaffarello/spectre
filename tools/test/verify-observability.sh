#!/usr/bin/env bash
# tools/test/verify-observability.sh
#
# W3.1 Cluster G + W3.2 Cluster E production-smoke assertions for
# the engine + operator + three adapter observability surface
# (ADR-0031 §3 / §4 / §5).
#
# Checks:
#   1. Engine `/metrics` returns 200 and includes at least one of
#      the `spectre_engine_*` instrument names from ADR-0031 §5.1
#      after the preceding workflow steps have run three ScrapeJobs
#      to Completed.
#   2. Operator `/metrics` returns 200 and includes the
#      `spectre_operator_*` instrument names from ADR-0031 §5.2 plus
#      the controller-runtime defaults that confirm controller-
#      runtime's metrics server is bound to the same port.
#   3. Engine pod logs contain at least one JSON event with a
#      non-empty `trace_id` field — the per-row event from the
#      drainer loop's `engine.assemble_row` span (Cluster D, via
#      the `tracing-opentelemetry` bridge).
#   4. Each of the three adapter Services (`/metrics`) is reachable
#      and includes `spectre_adapter_*{kind=...}` names per
#      ADR-0031 §5.3 — the W3.2 landing for Playwright /
#      SeleniumBase / curl-impersonate.
#   5. Trace topology end-to-end (ADR-0031 §4.2): the trace_id the
#      operator emits for a Completed ScrapeJob also surfaces in
#      the engine's log and in the playwright adapter's log,
#      proving the W3C propagator chain works across the three
#      language boundaries the engine + operator + adapter span.
#
# Scrapes via the Kubernetes apiserver proxy
# (`kubectl get --raw /api/v1/namespaces/.../services/.../proxy/metrics`)
# rather than `kubectl exec curl` — every Spectre image is a
# minimal-base container with no shell + no curl. The apiserver
# proxy reaches the Service's `metrics` port end-to-end without
# touching the target container's PATH.
#
# Usage:
#   bash tools/test/verify-observability.sh [namespace] [release]
#
# Exits 0 on success; non-zero with a debug dump on any failure.

set -euo pipefail

NAMESPACE="${1:-spectre-system}"
RELEASE="${2:-spectre}"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

scrape_metrics() {
    local service="$1"
    kubectl get --raw \
        "/api/v1/namespaces/${NAMESPACE}/services/${service}:metrics/proxy/metrics" \
        2>&1
}

# --- 1. Engine /metrics --------------------------------------------------

echo "Scraping engine /metrics via apiserver proxy..."

ENGINE_METRICS="$(scrape_metrics "${RELEASE}-engine")" || fail "engine /metrics scrape failed:
${ENGINE_METRICS}"

ENGINE_REQUIRED_METRICS=(
    "spectre_engine_jobs_active"
    "spectre_engine_jobs_completed_total"
    "spectre_engine_rows_emitted_total"
)
for needle in "${ENGINE_REQUIRED_METRICS[@]}"; do
    if ! grep -q "^${needle}" <<<"${ENGINE_METRICS}"; then
        fail "engine /metrics missing ${needle}:
${ENGINE_METRICS}"
    fi
    echo "  ✓ ${needle}"
done

# --- 2. Operator /metrics ------------------------------------------------

echo "Scraping operator /metrics via apiserver proxy..."

OPERATOR_METRICS="$(scrape_metrics "${RELEASE}-control-plane")" || fail "operator /metrics scrape failed:
${OPERATOR_METRICS}"

OPERATOR_REQUIRED_METRICS=(
    "spectre_operator_scrapejobs_total"
    "controller_runtime_reconcile_total"
)
for needle in "${OPERATOR_REQUIRED_METRICS[@]}"; do
    if ! grep -q "^${needle}" <<<"${OPERATOR_METRICS}"; then
        fail "operator /metrics missing ${needle}:
${OPERATOR_METRICS}"
    fi
    echo "  ✓ ${needle}"
done

# --- 3. Engine logs carry trace_id --------------------------------------

ENGINE_POD="$(
    kubectl -n "${NAMESPACE}" get pod \
        -l app.kubernetes.io/component=engine \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
)"
[[ -n "${ENGINE_POD}" ]] || fail "no engine pod found in namespace ${NAMESPACE}"

echo "Inspecting engine pod ${ENGINE_POD} logs for JSON events with a non-empty trace_id..."

ENGINE_LOGS="$(
    kubectl -n "${NAMESPACE}" logs "${ENGINE_POD}" --tail=500 2>&1
)" || fail "kubectl logs failed:
${ENGINE_LOGS}"

if ! grep -E '"trace_id":"[0-9a-f]{32}"' <<<"${ENGINE_LOGS}" >/dev/null; then
    fail "no engine log line with a non-empty trace_id (32-hex) found.
Sample of recent log output:
$(tail -20 <<<"${ENGINE_LOGS}")"
fi

TRACE_LINES="$(grep -cE '"trace_id":"[0-9a-f]{32}"' <<<"${ENGINE_LOGS}" || true)"
echo "  ✓ ${TRACE_LINES} engine log line(s) carry a valid trace_id"

# --- 4. Adapter /metrics (W3.2 §5.3) ------------------------------------

for adapter in playwright seleniumbase curl-impersonate; do
    echo "Scraping ${adapter}-adapter /metrics via apiserver proxy..."
    ADAPTER_METRICS="$(scrape_metrics "${RELEASE}-${adapter}-adapter")" \
        || fail "${adapter}-adapter /metrics scrape failed:
${ADAPTER_METRICS}"

    # Different adapter kinds normalise to different label values
    # per ADR-0031 §3.4 (`lower_snake_case`).
    case "${adapter}" in
        playwright)        kind_label="playwright" ;;
        seleniumbase)      kind_label="seleniumbase" ;;
        curl-impersonate)  kind_label="curl_impersonate" ;;
        *) fail "unknown adapter ${adapter}" ;;
    esac

    # Sessions_active is the only §5.3 instrument guaranteed to
    # surface after a single job (gauge; emits even at zero).
    # Initialize_duration_seconds_count surfaces after the
    # adapter has handled at least one Initialize RPC. The
    # playwright adapter ran three jobs in the preceding steps;
    # the other two adapter pods may be idle. Match on the
    # series name with `{kind="..."}` label to confirm the
    # adapter's instrument-registration end-to-end without
    # requiring a workload-specific observation count.
    #
    # The label set is matched permissively: the Playwright
    # `@opentelemetry/exporter-prometheus` exporter injects an
    # `otel_scope_name` label alongside the canonical `kind`
    # (the curl-impersonate + SeleniumBase adapters use
    # `prometheus/client_golang` + `prometheus_client` directly
    # and emit a tighter label set). The regex matches
    # `{...kind="<lang>"...}` regardless of attribute order or
    # additional exporter-injected labels.
    pattern="^spectre_adapter_sessions_active\{[^}]*kind=\"${kind_label}\"[^}]*\}"
    if ! grep -qE "${pattern}" <<<"${ADAPTER_METRICS}"; then
        fail "${adapter}-adapter /metrics missing spectre_adapter_sessions_active{kind=\"${kind_label}\"}:
${ADAPTER_METRICS}"
    fi
    echo "  ✓ spectre_adapter_sessions_active{kind=\"${kind_label}\"}"
done

# --- 5. Trace topology end-to-end ---------------------------------------

# The roadmap §4.3 W3.2 acceptance criterion: a single trace_id
# spans operator + engine + adapter logs for one ScrapeJob. We
# pick a Completed job (all three smoke samples use the
# playwright driver), extract its trace_id from the engine log
# line that carries the matching job_id, and confirm that the
# same trace_id surfaces in (a) the operator's log and (b) the
# playwright adapter's log.

JOB_UID="$(
    kubectl -n "${NAMESPACE}" get scrapejob hello-hackernews-s3 \
        -o jsonpath='{.metadata.uid}' 2>/dev/null
)"
[[ -n "${JOB_UID}" ]] || fail "could not read UID of ScrapeJob hello-hackernews-s3"

echo "Verifying trace topology for ScrapeJob hello-hackernews-s3 (uid=${JOB_UID})..."

# Engine log line carrying both job_id + trace_id. The engine's
# `RunJob accepted` info! emits both. `grep -m1` picks the first
# match; the trace_id extraction is a sed capture group on the
# canonical 32-hex value.
ENGINE_TRACE_LINE="$(
    grep -m1 "\"job_id\":\"${JOB_UID}\"" <<<"${ENGINE_LOGS}" || true
)"
[[ -n "${ENGINE_TRACE_LINE}" ]] || fail "engine logs have no line with job_id=${JOB_UID}.
Recent engine output:
$(tail -20 <<<"${ENGINE_LOGS}")"

ENGINE_TRACE_ID="$(
    sed -E 's/.*"trace_id":"([0-9a-f]{32})".*/\1/' <<<"${ENGINE_TRACE_LINE}"
)"
[[ ${#ENGINE_TRACE_ID} -eq 32 ]] || fail "could not extract trace_id from engine log line:
${ENGINE_TRACE_LINE}"
echo "  engine trace_id=${ENGINE_TRACE_ID}"

# Operator log line. zap's JSON format carries trace_id via the
# otelgrpc-installed span context (Cluster E of W3.1).
OPERATOR_POD="$(
    kubectl -n "${NAMESPACE}" get pod \
        -l app.kubernetes.io/component=control-plane \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
)"
[[ -n "${OPERATOR_POD}" ]] || fail "no control-plane pod found in namespace ${NAMESPACE}"

OPERATOR_LOGS="$(
    kubectl -n "${NAMESPACE}" logs "${OPERATOR_POD}" --tail=500 2>&1
)" || fail "kubectl logs control-plane failed:
${OPERATOR_LOGS}"

if ! grep -qF "${ENGINE_TRACE_ID}" <<<"${OPERATOR_LOGS}"; then
    fail "operator logs do not contain trace_id=${ENGINE_TRACE_ID}.
The W3C propagator chain is broken between operator and engine.
Sample of recent operator output:
$(tail -20 <<<"${OPERATOR_LOGS}")"
fi
echo "  ✓ operator logs carry trace_id=${ENGINE_TRACE_ID}"

# Playwright adapter log line. The HttpInstrumentation auto-
# instrumentation extracts the W3C `traceparent` from incoming
# Connect RPC metadata and opens a server-kind span — Pino reads
# the active span context for every log entry inside the RPC
# handler, so the trace_id appears in adapter logs covering any
# RPC that the engine made during this job.
PW_POD="$(
    kubectl -n "${NAMESPACE}" get pod \
        -l app.kubernetes.io/component=playwright-adapter \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
)"
[[ -n "${PW_POD}" ]] || fail "no playwright-adapter pod found in namespace ${NAMESPACE}"

PW_LOGS="$(
    kubectl -n "${NAMESPACE}" logs "${PW_POD}" --tail=500 2>&1
)" || fail "kubectl logs playwright-adapter failed:
${PW_LOGS}"

if ! grep -qF "${ENGINE_TRACE_ID}" <<<"${PW_LOGS}"; then
    fail "playwright-adapter logs do not contain trace_id=${ENGINE_TRACE_ID}.
The W3C propagator chain is broken between engine and adapter.
Sample of recent playwright-adapter output:
$(tail -20 <<<"${PW_LOGS}")"
fi
echo "  ✓ playwright-adapter logs carry trace_id=${ENGINE_TRACE_ID}"

echo "✓ Observability surface (ADR-0031 §3 / §4 / §5) end-to-end across operator + engine + adapter"
