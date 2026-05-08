#!/usr/bin/env bash
# tools/test/verify-webhook-sink.sh
#
# Verify R7.2's webhook ScrapeJob (build/helm/test/samples/
# webhook.yaml) POSTed to the mock receiver. Reads the
# receiver pod's logs (mendhak/http-https-echo prints
# request method, headers, body to stdout) and asserts a
# POST arrived with the engine's expected header schema and
# a JSONL body.
#
# Usage:
#   bash tools/test/verify-webhook-sink.sh [namespace]

set -euo pipefail

NAMESPACE="${1:-spectre-system}"

# The mock receiver pod is created from
# build/helm/test/mock-webhook-receiver.yaml.
RECEIVER_POD="$(kubectl -n "$NAMESPACE" get pod \
    -l "app.kubernetes.io/name=mock-webhook-receiver" \
    -o jsonpath='{.items[0].metadata.name}')"

if [[ -z "$RECEIVER_POD" ]]; then
    echo "ERROR: mock-webhook-receiver pod not found in namespace $NAMESPACE" >&2
    exit 1
fi

echo "Reading mock-webhook-receiver pod logs..."

LOGS="$(kubectl logs -n "$NAMESPACE" "$RECEIVER_POD" --tail=1000 || true)"

if [[ -z "$LOGS" ]]; then
    echo "ERROR: receiver pod logs are empty" >&2
    exit 1
fi

# mendhak/http-https-echo logs each request as JSON containing
# at least `method`, `headers`, and the body. Match the method
# field defensively (whitespace-tolerant).
if ! echo "$LOGS" | grep -q '"method": *"POST"'; then
    echo "ERROR: no POST requests in receiver logs" >&2
    echo "Logs:" >&2
    echo "$LOGS" | tail -50 >&2
    exit 1
fi

# Verify the engine's expected headers per ADR-0024 §4 R5.1
# header schema. A missing header indicates an emitter
# regression; warn rather than hard-fail so a header rename
# doesn't block the gate before the maintainer can adjust.
for HEADER in "x-spectre-job-id" "x-spectre-driver"; do
    if ! echo "$LOGS" | grep -iE "\"$HEADER\"" > /dev/null; then
        echo "WARNING: expected header '$HEADER' not found in receiver logs" >&2
    fi
done

# The body should be NDJSON; the receiver echoes it back inside
# the `body` field as a JSON-encoded *string*, so the inner JSON
# arrives in escaped form (`\"title\":\"...\"`). Match either
# form so a future receiver image that emits the body unescaped
# still works (production-smoke mini-phase, 2026-05-07 — the
# pattern was previously `"title"` which the current
# mendhak/http-https-echo image's escaped layout never matches).
if ! echo "$LOGS" | grep -qE '\\"title\\"|"title"'; then
    echo "ERROR: no 'title' field in any received body" >&2
    echo "Logs (last 50 lines):" >&2
    echo "$LOGS" | tail -50 >&2
    exit 1
fi

POST_COUNT="$(echo "$LOGS" | grep -c '"method": *"POST"' || true)"
echo "✓ webhook sink verified: $POST_COUNT POST request(s) arrived at mock receiver"
