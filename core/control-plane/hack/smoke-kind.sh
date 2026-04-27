#!/usr/bin/env bash
# In-cluster end-to-end smoke test for the bundled operator image.
# Brings up (or reuses) a kind cluster, loads the operator + engine
# images, and runs the bundled adapters against the canonical sample
# ScrapeJobs.
#
# PR17: the script now exercises BOTH bundled adapters sequentially.
# `MaxConcurrentReconciles=1` means at most one ScrapeJob runs at a
# time anyway, so running two cases back-to-back matches what the
# operator actually does in production.
#
#   1. hello-hackernews   (Playwright; PR16 acceptance)
#   2. seleniumbase-extract (SeleniumBase; PR17 acceptance)
#
# Both must reach `phase: Completed` with `rowsExtracted >= MIN_ROWS`
# for the script (and the CI smoke job) to pass.
#
# Usage:
#   bash core/control-plane/hack/smoke-kind.sh [cluster-name]
#
# Environment overrides:
#   ENGINE_IMAGE      — image tag to load (default: spectre-engine:dev)
#   OPERATOR_IMAGE    — image tag to load (default: spectre-control-plane:dev)
#   TIMEOUT_SECONDS   — wait deadline for each terminal phase
#                       (default: 300)
#   MIN_ROWS          — minimum rowsExtracted to accept (default: 1)
#   KEEP_CLUSTER      — if set, skip `kind delete` on success
#                       (default: cluster persists either way; the
#                       caller is responsible for teardown)

set -euo pipefail

CLUSTER="${1:-spectre-pr17}"
NAMESPACE="control-plane-system"
DEPLOY="deployment/control-plane-controller-manager"
ENGINE_IMAGE="${ENGINE_IMAGE:-spectre-engine:dev}"
OPERATOR_IMAGE="${OPERATOR_IMAGE:-spectre-control-plane:dev}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-300}"
MIN_ROWS="${MIN_ROWS:-1}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

log() { printf '\n=== %s ===\n' "$*"; }

# Run one ScrapeJob end-to-end: apply, poll for terminal phase, assert
# rowsExtracted, dump the JSONL log lines. Exits 1 on any failure.
#
# Args:
#   $1 — ScrapeJob metadata.name (used by `kubectl get scrapejob`)
#   $2 — path to the sample manifest (relative to REPO_ROOT)
smoke_one() {
    local name="$1"
    local manifest="$2"

    log "[${name}] Apply sample (${manifest})"
    kubectl apply -f "${manifest}"

    log "[${name}] Poll for terminal phase (timeout ${TIMEOUT_SECONDS}s)"
    local deadline=$((SECONDS + TIMEOUT_SECONDS))
    local phase=""
    while [ "${SECONDS}" -lt "${deadline}" ]; do
        phase="$(kubectl get scrapejob "${name}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
        case "${phase}" in
            Completed|Failed) break ;;
        esac
        sleep 3
    done

    log "[${name}] ScrapeJob status"
    kubectl get scrapejob "${name}" -o yaml || true

    log "[${name}] Operator manager logs (last 80 lines)"
    kubectl -n "${NAMESPACE}" logs "${DEPLOY}" -c manager --tail=80 || true

    if [ "${phase}" != "Completed" ]; then
        log "[${name}] FAIL: phase=${phase:-<empty>} (expected Completed)"
        log "[${name}] Operator pod describe"
        kubectl -n "${NAMESPACE}" describe pod -l control-plane=controller-manager || true
        # Direct google-chrome diagnostic from inside the live
        # operator Pod when SeleniumBase fails: chromedriver
        # collapses any Chrome child-process exit into a generic
        # "Chrome instance exited" — running google-chrome
        # ourselves with the same flags surfaces Chrome's actual
        # stderr (crashpad failures, missing libs, syscall
        # rejection, etc.). The Pod is still up; only the
        # ScrapeJob's child process tree died.
        if [ "${name}" = "seleniumbase-extract" ]; then
            log "[${name}] Direct Chrome diagnostic from inside the operator Pod"
            local pod
            pod="$(kubectl -n "${NAMESPACE}" get pod \
                -l control-plane=controller-manager \
                -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
            if [ -n "${pod}" ]; then
                kubectl -n "${NAMESPACE}" exec "${pod}" -c manager -- \
                    /bin/sh -c '
                        set +e
                        echo "--- env"
                        echo "HOME=${HOME:-<unset>}"
                        id
                        echo "--- chrome direct (mirrors adapter chromium_arg)"
                        UD=$(mktemp -d -p /tmp chrome-diag-XXXXXX 2>/dev/null || echo /tmp/chrome-diag)
                        google-chrome \
                            --headless \
                            --no-sandbox \
                            --disable-dev-shm-usage \
                            --disable-gpu \
                            --disable-software-rasterizer \
                            --disable-breakpad \
                            --user-data-dir="${UD}" \
                            --dump-dom https://example.com 2>&1 | head -60
                        echo "--- chrome exit=$?"
                    ' || true
            fi
        fi
        return 1
    fi

    local rows
    rows="$(kubectl get scrapejob "${name}" -o jsonpath='{.status.rowsExtracted}')"
    if [ -z "${rows}" ] || [ "${rows}" -lt "${MIN_ROWS}" ]; then
        log "[${name}] FAIL: rowsExtracted=${rows:-<empty>} (expected >= ${MIN_ROWS})"
        return 1
    fi

    log "[${name}] JSONL rows in operator stdout (first 5)"
    kubectl -n "${NAMESPACE}" logs "${DEPLOY}" -c manager 2>/dev/null \
        | grep -E '^\{.*\}$' \
        | head -5 || true

    log "[${name}] PASS: phase=Completed rowsExtracted=${rows}"
}

log "Ensure kind cluster ${CLUSTER}"
if ! kind get clusters | grep -qx "${CLUSTER}"; then
    kind create cluster --name "${CLUSTER}"
else
    echo "kind cluster ${CLUSTER} already exists; reusing"
fi

log "Load images into kind nodes"
# kind load is a no-op when the digest matches; safe to re-run.
kind load docker-image "${ENGINE_IMAGE}"   --name "${CLUSTER}"
kind load docker-image "${OPERATOR_IMAGE}" --name "${CLUSTER}"

log "Install CRDs and deploy operator"
( cd core/control-plane && make install IMG="${OPERATOR_IMAGE}" )
( cd core/control-plane && make deploy  IMG="${OPERATOR_IMAGE}" )

log "Wait for operator pod ready"
kubectl -n "${NAMESPACE}" rollout status "${DEPLOY}" --timeout=180s

# Sample list. Each entry is `name:manifest` — name is the ScrapeJob
# metadata.name, manifest is the kustomize sample path relative to
# REPO_ROOT. Sequential execution matches MaxConcurrentReconciles=1
# (ADR-0019 §3); concurrent runs would contend for the single Pod.
SAMPLES=(
    "hello-hackernews:core/control-plane/config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml"
    "seleniumbase-extract:core/control-plane/config/samples/spectre_v1alpha1_scrapejob_seleniumbase.yaml"
)

failed=0
for entry in "${SAMPLES[@]}"; do
    name="${entry%%:*}"
    manifest="${entry#*:}"
    if ! smoke_one "${name}" "${manifest}"; then
        failed=1
    fi
done

if [ "${failed}" -ne 0 ]; then
    log "OVERALL FAIL"
    exit 1
fi

log "OVERALL PASS: both samples reached Completed"
