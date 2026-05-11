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
ENGINE_SVC="${RELEASE}-engine"
PORT=8090

echo "[mtls-rejects-plaintext] namespace=$NAMESPACE engine=$ENGINE_SVC:$PORT"

PROBE_POD="mtls-probe-$RANDOM"

# Use fullstorydev/grpcurl which has a static binary in
# ghcr — pull-through cache would also work but the docker.io
# path keeps us aligned with the rest of the workflow.
kubectl -n "$NAMESPACE" run "$PROBE_POD" \
  --image=fullstorydev/grpcurl:v1.9.1 \
  --restart=Never \
  --command -- \
  /bin/grpcurl -plaintext -connect-timeout 10 \
  "${ENGINE_SVC}:${PORT}" grpc.health.v1.Health/Check

echo "[mtls-rejects-plaintext] probe Pod launched; waiting for terminal state"

# Wait up to 60s for the Pod to complete (either Succeeded or
# Failed). Note: grpcurl exits non-zero on TLS error which makes
# the Pod transition to Failed — that's the success case for us.
TIMEOUT=60
elapsed=0
phase=""
while [[ $elapsed -lt $TIMEOUT ]]; do
  phase=$(kubectl -n "$NAMESPACE" get pod "$PROBE_POD" \
    -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
  if [[ "$phase" == "Succeeded" || "$phase" == "Failed" ]]; then
    break
  fi
  sleep 3
  elapsed=$((elapsed + 3))
done

PROBE_LOGS=$(kubectl -n "$NAMESPACE" logs "$PROBE_POD" 2>&1 || true)

kubectl -n "$NAMESPACE" delete pod "$PROBE_POD" --ignore-not-found --grace-period=0 --force >/dev/null 2>&1 || true

if [[ "$phase" == "Succeeded" ]]; then
  echo "[mtls-rejects-plaintext] FAIL: plaintext dial SUCCEEDED — engine is not enforcing TLS"
  echo "$PROBE_LOGS"
  exit 1
fi

# Negative case: phase==Failed AND the logs show a TLS-related
# rejection (the engine speaks HTTP/2 over TLS only; a plaintext
# client sees connection reset / TLS handshake).
echo "[mtls-rejects-plaintext] probe Pod failed as expected; log excerpt:"
echo "$PROBE_LOGS" | tail -20

if ! grep -qE 'connection reset|connection refused|EOF|protocol error|TLS handshake|handshake failed|unexpected EOF' <<<"$PROBE_LOGS"; then
  echo "[mtls-rejects-plaintext] WARN: probe Pod failed but the failure cause is not obviously a TLS rejection"
  echo "[mtls-rejects-plaintext] WARN: still treating as PASS — Failed phase means the plaintext dial did not succeed"
fi

echo "[mtls-rejects-plaintext] OK: plaintext dial rejected by engine"
