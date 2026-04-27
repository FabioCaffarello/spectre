#!/usr/bin/env bash
# In-cluster end-to-end smoke test for the bundled operator image
# (PR16). Runs against a kind cluster, applies the hello-hackernews
# sample, and asserts that the operator transitions the ScrapeJob to
# `Completed` with `rowsExtracted >= MIN_ROWS`.
#
# Assumes the engine and operator images already exist locally as
# `spectre-engine:dev` and `spectre-control-plane:dev`. The justfile
# `op-smoke-kind` recipe wires the build dependency; CI builds the
# images in a separate step before invoking this one.
#
# Usage:
#   bash core/control-plane/hack/smoke-kind.sh [cluster-name]
#
# Environment overrides:
#   ENGINE_IMAGE      — image tag to load (default: spectre-engine:dev)
#   OPERATOR_IMAGE    — image tag to load (default: spectre-control-plane:dev)
#   TIMEOUT_SECONDS   — wait deadline for terminal phase (default: 300)
#   MIN_ROWS          — minimum rowsExtracted to accept (default: 1)
#   KEEP_CLUSTER      — if set, skip `kind delete` on success
#                       (default: cluster persists either way; the
#                       caller is responsible for teardown)

set -euo pipefail

CLUSTER="${1:-spectre-pr16}"
NAMESPACE="control-plane-system"
DEPLOY="deployment/control-plane-controller-manager"
SAMPLE="core/control-plane/config/samples/spectre_v1alpha1_scrapejob_hello-hackernews.yaml"
SCRAPEJOB="hello-hackernews"
ENGINE_IMAGE="${ENGINE_IMAGE:-spectre-engine:dev}"
OPERATOR_IMAGE="${OPERATOR_IMAGE:-spectre-control-plane:dev}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-300}"
MIN_ROWS="${MIN_ROWS:-1}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

log() { printf '\n=== %s ===\n' "$*"; }

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

log "Apply hello-hackernews sample"
kubectl apply -f "${SAMPLE}"

log "Poll for terminal phase (timeout ${TIMEOUT_SECONDS}s)"
deadline=$((SECONDS + TIMEOUT_SECONDS))
phase=""
while [ "${SECONDS}" -lt "${deadline}" ]; do
    phase="$(kubectl get scrapejob "${SCRAPEJOB}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    case "${phase}" in
        Completed|Failed) break ;;
    esac
    sleep 3
done

log "ScrapeJob status"
kubectl get scrapejob "${SCRAPEJOB}" -o yaml || true

log "Operator manager logs (last 80 lines)"
kubectl -n "${NAMESPACE}" logs "${DEPLOY}" -c manager --tail=80 || true

if [ "${phase}" != "Completed" ]; then
    log "FAIL: phase=${phase:-<empty>} (expected Completed)"
    log "Operator pod describe"
    kubectl -n "${NAMESPACE}" describe pod -l control-plane=controller-manager || true
    exit 1
fi

rows="$(kubectl get scrapejob "${SCRAPEJOB}" -o jsonpath='{.status.rowsExtracted}')"
if [ -z "${rows}" ] || [ "${rows}" -lt "${MIN_ROWS}" ]; then
    log "FAIL: rowsExtracted=${rows:-<empty>} (expected >= ${MIN_ROWS})"
    exit 1
fi

log "JSONL rows in operator stdout (first 5)"
kubectl -n "${NAMESPACE}" logs "${DEPLOY}" -c manager 2>/dev/null \
    | grep -E '^\{.*"title".*"url"' \
    | head -5 || true

log "PASS: phase=Completed rowsExtracted=${rows}"
