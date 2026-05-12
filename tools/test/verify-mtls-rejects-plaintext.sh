#!/usr/bin/env bash
# Verify the engine REJECTS a plaintext gRPC dial when mTLS is
# enabled (ADR-0032 §4.1: client certificates are required, not
# optional, when `certManager.enabled: true`).
#
# Run from inside a one-shot Pod in the cluster so we hit the
# engine Service over its real DNS, not via port-forward (which
# would also require TLS but adds setup overhead). The probe Pod
# runs `grpcurl -plaintext` against the engine's health endpoint;
# the expected outcome is connection failure with a TLS error
# code, NOT a successful health check.
#
# Usage:
#   tools/test/verify-mtls-rejects-plaintext.sh <namespace> <release>

set -euo pipefail

NAMESPACE="${1:-spectre-system}"
RELEASE="${2:-spectre}"

# W3.4 extends the negative-path coverage from engine alone
# (W3.3) to engine + all 3 adapters. Each target rejects
# plaintext gRPC under mTLS posture (ADR-0032 §4.1 + §4.2).
TARGETS=(
  "engine:8090"
  "playwright-adapter:8091"
  "seleniumbase-adapter:8092"
  "curl-impersonate-adapter:8093"
)

probe_target() {
  local svc_port="$1"
  local svc="${svc_port%:*}"
  local port="${svc_port#*:}"
  local probe_pod="mtls-probe-${svc}-$RANDOM"
  local full_svc="${RELEASE}-${svc}"

  echo "[mtls-rejects-plaintext] probing ${full_svc}:${port}"

  kubectl -n "$NAMESPACE" run "$probe_pod" \
    --image=fullstorydev/grpcurl:v1.9.1 \
    --restart=Never \
    --command -- \
    /bin/grpcurl -plaintext -connect-timeout 10 \
    "${full_svc}:${port}" grpc.health.v1.Health/Check || true

  local phase=""
  local elapsed=0
  while [[ $elapsed -lt 60 ]]; do
    phase=$(kubectl -n "$NAMESPACE" get pod "$probe_pod" \
      -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "$phase" == "Succeeded" || "$phase" == "Failed" ]]; then
      break
    fi
    sleep 3
    elapsed=$((elapsed + 3))
  done

  local probe_logs
  probe_logs=$(kubectl -n "$NAMESPACE" logs "$probe_pod" 2>&1 || true)
  kubectl -n "$NAMESPACE" delete pod "$probe_pod" \
    --ignore-not-found --grace-period=0 --force >/dev/null 2>&1 || true

  if [[ "$phase" == "Succeeded" ]]; then
    echo "[mtls-rejects-plaintext] FAIL: plaintext dial to ${full_svc}:${port} SUCCEEDED — TLS not enforced"
    echo "$probe_logs"
    return 1
  fi
  echo "[mtls-rejects-plaintext] PASS: ${full_svc}:${port} rejected plaintext dial"
}

for target in "${TARGETS[@]}"; do
  probe_target "$target"
done

echo "[mtls-rejects-plaintext] OK: plaintext dials rejected by engine + all 3 adapters"
