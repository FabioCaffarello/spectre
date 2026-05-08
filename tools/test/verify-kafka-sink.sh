#!/usr/bin/env bash
# tools/test/verify-kafka-sink.sh
#
# Verify R7.2's kafka ScrapeJob (build/helm/test/samples/kafka.yaml)
# produced messages on the configured topic. Reads from the
# Bitnami kafka controller pod via `kubectl exec` and
# `kafka-console-consumer.sh`.
#
# Usage:
#   bash tools/test/verify-kafka-sink.sh [namespace] [release]
#
# Exits 0 on first valid message; non-zero on timeout or
# malformed payload.

set -euo pipefail

NAMESPACE="${1:-spectre-system}"
RELEASE="${2:-spectre}"
TOPIC="${SPECTRE_KAFKA_TOPIC:-spectre.rows.default}"
TIMEOUT_MS="${SPECTRE_KAFKA_TIMEOUT_MS:-30000}"

# Bitnami kafka 30.0.0 in KRaft mode names the StatefulSet
# `<release>-kafka-controller`; pod-0 is always present in
# the chart's single-controller config.
KAFKA_POD="${RELEASE}-kafka-controller-0"

echo "Reading from topic '${TOPIC}' on pod '${KAFKA_POD}'..."

TMP_OUT="$(mktemp)"
trap 'rm -f "$TMP_OUT"' EXIT

# `-c kafka` selects the broker container explicitly. Bitnami
# kafka 30.0.0 added a `kafka-init` init container alongside
# the main `kafka` container; without `-c` kubectl emits
# `Defaulted container "kafka" out of: kafka, kafka-init (init)`
# on stderr, which the `2>&1` redirect below interleaves into
# TMP_OUT and contaminates jq parsing downstream.
if ! kubectl exec -n "$NAMESPACE" "$KAFKA_POD" -c kafka -- \
    /opt/bitnami/kafka/bin/kafka-console-consumer.sh \
        --bootstrap-server "${RELEASE}-kafka.${NAMESPACE}.svc.cluster.local:9092" \
        --topic "$TOPIC" \
        --from-beginning \
        --max-messages 1 \
        --timeout-ms "$TIMEOUT_MS" \
        > "$TMP_OUT" 2>&1; then
    echo "ERROR: kafka-console-consumer.sh failed or timed out" >&2
    cat "$TMP_OUT" >&2
    exit 1
fi

# kafka-console-consumer prints framing lines ("Processed a
# total of N messages", "Bye!") alongside the actual messages.
# Filter framing + the kubectl `Defaulted container` warning
# (defence-in-depth — the `-c kafka` flag above silences it
# upstream, but a future chart adding more containers would
# re-introduce a similar diagnostic). Keep only the first
# non-blank, non-framing line.
MSG="$(grep -v '^$\|Processed a total\|^Bye!\|^Defaulted container' "$TMP_OUT" | head -n 1 || true)"
if [[ -z "$MSG" ]]; then
    echo "ERROR: empty message body from kafka topic" >&2
    cat "$TMP_OUT" >&2
    exit 1
fi

# The hello-hackernews extraction in build/helm/test/samples/
# kafka.yaml emits {"title": ..., "url": ...} per row. Validate
# both fields exist; brittle assertions on exact values would
# tie the gate to remote site state.
if ! echo "$MSG" | jq -e 'has("title") and has("url")' > /dev/null; then
    echo "ERROR: kafka message missing required fields (title, url)" >&2
    echo "Got: $MSG" >&2
    exit 1
fi

echo "✓ kafka sink verified: message on topic '$TOPIC' has title + url"
echo "  Sample: $(echo "$MSG" | jq -c '.')"
