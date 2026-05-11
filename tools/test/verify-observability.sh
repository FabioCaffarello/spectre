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
# The script `kubectl exec`s a one-shot `curl` from inside the
# target pod so it does not need a Service exposed externally.
#
# Usage:
#   bash tools/test/verify-observability.sh [namespace]
#
# Exits 0 on success; non-zero with a debug dump on any failure.

set -euo pipefail

NAMESPACE="${1:-spectre-system}"
METRICS_PORT="${SPECTRE_METRICS_PORT:-9090}"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

# --- 1. Engine /metrics --------------------------------------------------

ENGINE_POD="$(
    kubectl -n "${NAMESPACE}" get pod \
        -l app.kubernetes.io/component=engine \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
)"
[[ -n "${ENGINE_POD}" ]] || fail "no engine pod found in namespace ${NAMESPACE}"

echo "Engine pod: ${ENGINE_POD}"
echo "Scraping engine /metrics on :${METRICS_PORT}..."

ENGINE_METRICS="$(
    kubectl -n "${NAMESPACE}" exec "${ENGINE_POD}" -- \
        curl -fsS "http://localhost:${METRICS_PORT}/metrics" 2>&1
)" || fail "engine /metrics scrape failed:
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

OPERATOR_POD="$(
    kubectl -n "${NAMESPACE}" get pod \
        -l app.kubernetes.io/component=control-plane \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
)"
[[ -n "${OPERATOR_POD}" ]] || fail "no control-plane pod found in namespace ${NAMESPACE}"

echo "Operator pod: ${OPERATOR_POD}"
echo "Scraping operator /metrics on :${METRICS_PORT}..."

OPERATOR_METRICS="$(
    kubectl -n "${NAMESPACE}" exec "${OPERATOR_POD}" -- \
        curl -fsS "http://localhost:${METRICS_PORT}/metrics" 2>&1
)" || fail "operator /metrics scrape failed:
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

echo "Inspecting engine pod logs for JSON events with a non-empty trace_id..."

ENGINE_LOGS="$(
    kubectl -n "${NAMESPACE}" logs "${ENGINE_POD}" --tail=500 2>&1
)" || fail "kubectl logs failed:
${ENGINE_LOGS}"

if ! grep -E '^\{.*"trace_id":"[0-9a-f]{32}"' <<<"${ENGINE_LOGS}" >/dev/null; then
    fail "no engine log line with a non-empty trace_id (32-hex) found.
Sample of recent log output:
$(tail -20 <<<"${ENGINE_LOGS}")"
fi

TRACE_LINES="$(grep -cE '^\{.*"trace_id":"[0-9a-f]{32}"' <<<"${ENGINE_LOGS}" || true)"
echo "  ✓ ${TRACE_LINES} engine log line(s) carry a valid trace_id"

echo "✓ Observability surface (ADR-0031 §3 / §5) end-to-end on engine + operator"
