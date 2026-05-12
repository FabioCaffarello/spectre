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

# Step 3 — confirm each adapter logged a mutual-TLS bind (W3.4).
# Each Go/Python/TS adapter emits a `tls ready` / `tls_mode=mutual`
# (or equivalent) log line at startup. Plain `mutual` token match
# is permissive enough to cover the three logger shapes (slog
# JSON for curl-impersonate, structlog JSON for seleniumbase,
# Pino JSON for playwright).
for slot in curl-impersonate-adapter seleniumbase-adapter playwright-adapter; do
  adapter_label="${slot%-adapter}"
  case "$adapter_label" in
    curl-impersonate) component=curl-impersonate-adapter ;;
    seleniumbase)     component=seleniumbase-adapter ;;
    playwright)       component=playwright-adapter ;;
  esac
  ADAPTER_LOGS=$(kubectl -n "$NAMESPACE" logs \
    -l "app.kubernetes.io/component=${component}" --tail=200 2>/dev/null || true)
  if ! grep -q '"tls_mode":"mutual"\|tls_mode=mutual' <<<"$ADAPTER_LOGS"; then
    echo "[mtls-handshake] FAIL: ${slot} did not log mutual TLS readiness"
    echo "$ADAPTER_LOGS" | tail -30
    exit 1
  fi
  echo "[mtls-handshake] PASS: ${slot} bound with mutual-TLS"
done

# Step 4 — apply all three driver-variant ScrapeJob samples
# (playwright via kafka.yaml; seleniumbase + curl-impersonate
# variants added in W3.4 Cluster E). Each exercises a distinct
# engine → adapter dial path. Job completion is the strong
# end-to-end signal; failure would surface as TLS-handshake
# errors (caught in Step 5) or scrape-side errors (independent
# of mTLS).
SAMPLES=(
  "build/helm/test/samples/kafka.yaml"
  "build/helm/test/samples/kafka-seleniumbase.yaml"
  "build/helm/test/samples/kafka-curl-impersonate.yaml"
)
SJ_NAMES=()
for sample in "${SAMPLES[@]}"; do
  sj_name=$(awk '/^metadata:/{m=1} m && /  name:/{print $2; exit}' "$sample")
  if [[ -z "$sj_name" ]]; then
    echo "[mtls-handshake] FAIL: could not parse ScrapeJob name from $sample"
    exit 1
  fi
  echo "[mtls-handshake] applying $sample (ScrapeJob/$sj_name)"
  kubectl -n "$NAMESPACE" apply -f "$sample"
  SJ_NAMES+=("$sj_name")
done

echo "[mtls-handshake] waiting for all ScrapeJobs terminal phase (timeout 360s)"

# Completed OR Failed acceptable — the test asserts the operator
# → engine + engine → adapter dial paths reach all three adapters
# over mTLS, not that the scrape returns rows.
TIMEOUT=360
elapsed=0
remaining=("${SJ_NAMES[@]}")
while [[ $elapsed -lt $TIMEOUT && ${#remaining[@]} -gt 0 ]]; do
  next=()
  for sj in "${remaining[@]}"; do
    phase=$(kubectl -n "$NAMESPACE" get scrapejob "$sj" \
      -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "$phase" == "Completed" || "$phase" == "Failed" ]]; then
      echo "[mtls-handshake] PASS: ScrapeJob/$sj reached terminal phase ($phase)"
    else
      next+=("$sj")
    fi
  done
  remaining=("${next[@]}")
  if [[ ${#remaining[@]} -gt 0 ]]; then
    sleep 10
    elapsed=$((elapsed + 10))
  fi
done

if [[ ${#remaining[@]} -gt 0 ]]; then
  for sj in "${remaining[@]}"; do
    echo "[mtls-handshake] FAIL: ScrapeJob/$sj did not reach terminal phase within ${TIMEOUT}s"
    kubectl -n "$NAMESPACE" describe scrapejob "$sj" | tail -40
  done
  exit 1
fi

# Step 5 — confirm no TLS handshake failures across operator,
# engine, or any of the three adapter log streams.
ALL_LOGS=$(
  for component in control-plane engine playwright-adapter seleniumbase-adapter curl-impersonate-adapter; do
    kubectl -n "$NAMESPACE" logs -l "app.kubernetes.io/component=${component}" --tail=500 2>/dev/null || true
  done
)
HANDSHAKE_ERRORS=$(grep -E 'authentication handshake failed|certificate signed by unknown authority|tls: bad certificate|tls: certificate required|TLS handshake error|x509: certificate' <<<"$ALL_LOGS" || true)
if [[ -n "$HANDSHAKE_ERRORS" ]]; then
  echo "[mtls-handshake] FAIL: TLS handshake errors detected"
  echo "$HANDSHAKE_ERRORS" | head -30
  exit 1
fi

# Cleanup
for sj in "${SJ_NAMES[@]}"; do
  kubectl -n "$NAMESPACE" delete scrapejob "$sj" --ignore-not-found
done

echo "[mtls-handshake] OK: operator → engine + engine → 3 adapters mTLS handshakes verified"
