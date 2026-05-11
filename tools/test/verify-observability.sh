#!/usr/bin/env bash
# tools/test/verify-observability.sh
#
# W3.1 Cluster G production-smoke assertions for the engine +
# operator observability surface (ADR-0031 §3 / §4 / §5).
#
# Checks:
#   1. Engine `/metrics` returns 200 and includes at least one of
#      the `spectre_engine_*` instrument names from ADR-0031 §5.1
#      after at least one ScrapeJob has run (the preceding workflow
#      steps applied + waited for three jobs to reach Completed,
#      so jobs_completed_total must have surfaced by now).
#   2. Operator `/metrics` returns 200 and includes the
#      `spectre_operator_*` instrument names from ADR-0031 §5.2 plus
#      the controller-runtime defaults that confirm controller-
#      runtime's metrics server is bound to the same port.
#   3. Engine pod logs contain at least one JSON event with a
#      non-empty `trace_id` field — the per-row event from the
#      drainer loop's `engine.assemble_row` span (Cluster D, via
#      the `tracing-opentelemetry` bridge).
#
# Scrapes via the Kubernetes apiserver proxy
# (`kubectl get --raw /api/v1/namespaces/.../services/.../proxy/metrics`)
# rather than `kubectl exec curl` — the engine container is a
# musl-static binary on a `scratch`-like base with no shell + no
# curl. The apiserver proxy reaches the Service's `metrics` port
# end-to-end without touching the target container's PATH.
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

echo "✓ Observability surface (ADR-0031 §3 / §5) end-to-end on engine + operator"
