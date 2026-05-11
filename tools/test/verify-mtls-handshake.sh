#!/usr/bin/env bash
# Verify the operator successfully dials the engine over mTLS
# after the W3.3 ADR-0032 first auth PR.
#
# The positive signal we look for: after applying a ScrapeJob
# referencing the in-cluster engine, the operator's RunJob RPC
# reaches the engine without a TLS handshake error. We can't
# observe the handshake bytes directly, but the operator's logs
# emit `engine client credentials ready mode=mutual` at startup
# (W3.3 cluster C wiring) and a successful job completion proves
# the dial worked end-to-end.
#
# Negative signal: if mTLS misconfiguration prevents the
# handshake, the operator logs surface `transport: authentication
# handshake failed` or `x509: certificate signed by unknown
# authority`. We grep against those patterns and fail if any
# match.
#
# Usage:
#   tools/test/verify-mtls-handshake.sh <namespace> <release>
#
# Args mirror verify-observability.sh / verify-kafka-sink.sh.

set -euo pipefail

NAMESPACE="${1:-spectre-system}"
RELEASE="${2:-spectre}"

echo "[mtls-handshake] namespace=$NAMESPACE release=$RELEASE"

# Step 1 — assert the operator initialised with mTLS credentials.
# The control-plane container logs the resolved mode at startup
# (W3.3 cluster C; cmd/main.go).
OPERATOR_LOGS=$(kubectl -n "$NAMESPACE" logs \
  -l app.kubernetes.io/component=control-plane --tail=500 2>/dev/null || true)

if ! grep -q 'engine client credentials ready' <<<"$OPERATOR_LOGS"; then
  echo "[mtls-handshake] FAIL: operator did not log credential readiness"
  echo "$OPERATOR_LOGS" | tail -50
  exit 1
fi
if ! grep -q 'mode.*mutual\|"mode":"mutual"' <<<"$OPERATOR_LOGS"; then
  echo "[mtls-handshake] FAIL: operator credential mode is not 'mutual'"
  grep -E 'engine client credentials ready' <<<"$OPERATOR_LOGS" || true
  exit 1
fi
echo "[mtls-handshake] PASS: operator initialised with mutual TLS credentials"

# Step 2 — assert the engine logged a mutual-TLS bind.
ENGINE_LOGS=$(kubectl -n "$NAMESPACE" logs \
  -l app.kubernetes.io/component=engine --tail=500 2>/dev/null || true)

if ! grep -qE 'tls mode: mutual|"tls_mode":"mutual"' <<<"$ENGINE_LOGS"; then
  echo "[mtls-handshake] FAIL: engine did not log mutual-TLS bind"
  echo "$ENGINE_LOGS" | tail -50
  exit 1
fi
echo "[mtls-handshake] PASS: engine bound with mutual-TLS"

# Step 3 — apply a tiny ScrapeJob that exercises the dial.
# Reuses the kafka sample (lowest-overhead path in this chart).
kubectl -n "$NAMESPACE" apply -f - <<EOF
apiVersion: spectre.io/v1alpha2
kind: ScrapeJob
metadata:
  name: mtls-smoke-handshake
  namespace: $NAMESPACE
spec:
  dsl: |
    name: mtls-smoke
    target:
      driver: curl-impersonate
      url: "https://example.com/"
    extract: []
    output:
      format: jsonl
  engineRef:
    endpoint: "${RELEASE}-engine:8090"
  outputSink:
    type: stdout
EOF

echo "[mtls-handshake] applied ScrapeJob; waiting for status transition (timeout 120s)"

# We accept Completed OR Failed because the engineRef and the
# example.com extract aren't the point — we just want the dial
# to occur. If TLS rejected the handshake the job would not
# reach a terminal phase; instead the operator would emit
# repeated dial errors.
TIMEOUT=120
elapsed=0
phase=""
while [[ $elapsed -lt $TIMEOUT ]]; do
  phase=$(kubectl -n "$NAMESPACE" get scrapejob mtls-smoke-handshake \
    -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
  if [[ "$phase" == "Completed" || "$phase" == "Failed" ]]; then
    break
  fi
  sleep 5
  elapsed=$((elapsed + 5))
done

if [[ "$phase" != "Completed" && "$phase" != "Failed" ]]; then
  echo "[mtls-handshake] FAIL: ScrapeJob did not reach terminal phase within ${TIMEOUT}s"
  kubectl -n "$NAMESPACE" describe scrapejob mtls-smoke-handshake | tail -40
  exit 1
fi
echo "[mtls-handshake] PASS: ScrapeJob reached terminal phase ($phase)"

# Step 4 — confirm no TLS handshake failures in either log stream.
POST_OPERATOR=$(kubectl -n "$NAMESPACE" logs \
  -l app.kubernetes.io/component=control-plane --tail=500 2>/dev/null || true)
POST_ENGINE=$(kubectl -n "$NAMESPACE" logs \
  -l app.kubernetes.io/component=engine --tail=500 2>/dev/null || true)

# Grep is grep-without-pcre so we OR the literal patterns. A
# match in EITHER log fails the check.
HANDSHAKE_ERRORS=$(printf '%s\n%s\n' "$POST_OPERATOR" "$POST_ENGINE" \
  | grep -E 'authentication handshake failed|certificate signed by unknown authority|tls: bad certificate|tls: certificate required' \
  || true)
if [[ -n "$HANDSHAKE_ERRORS" ]]; then
  echo "[mtls-handshake] FAIL: TLS handshake errors detected"
  echo "$HANDSHAKE_ERRORS"
  exit 1
fi

# Cleanup
kubectl -n "$NAMESPACE" delete scrapejob mtls-smoke-handshake --ignore-not-found

echo "[mtls-handshake] OK: operator → engine mTLS handshake verified"
