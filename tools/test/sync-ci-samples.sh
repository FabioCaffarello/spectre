#!/usr/bin/env bash
# tools/test/sync-ci-samples.sh
#
# Regenerate build/helm/test/samples/{kafka,s3,webhook}.yaml
# from operators/control-plane/config/samples/. The CI samples
# are derived from the source samples with only the URL /
# endpoint fields flipped to point at in-cluster services that
# the chart-installed cluster exposes.
#
# Per-sink flips:
#   kafka:   none — `KafkaSink.Brokers` is informational
#            for v1alpha1 (engine reads SPECTRE_KAFKA_BROKERS
#            at startup; per-job field is documented but unused).
#            Sample copied byte-identically.
#   s3:      `endpoint:` field rewritten from
#            `http://minio.spectre-system...` to
#            `http://spectre-minio.spectre-system...` so the
#            engine reaches the chart-installed MinIO service
#            (per ADR-0024 §3, the per-job endpoint beats the
#            engine's env override).
#   webhook: `url:` field rewritten from the placeholder
#            `https://example-receiver.example.com/spectre` to
#            the in-cluster mock receiver service.
#
# Run after editing any source sample. CI's
# check-ci-samples-sync.sh gate fails if this hasn't been run.
#
# DST_DIR can be overridden via env (the check script uses this
# to redirect output to a tmpdir for diff comparison).

set -euo pipefail

SRC_DIR="${SRC_DIR:-operators/control-plane/config/samples}"
DST_DIR="${DST_DIR:-build/helm/test/samples}"

mkdir -p "$DST_DIR"

# kafka: byte-identical copy.
cp "$SRC_DIR/spectre_v1alpha2_scrapejob_kafka.yaml" "$DST_DIR/kafka.yaml"

# s3: rewrite endpoint to the chart-installed MinIO service.
sed -E 's|endpoint: http://minio\.spectre-system\.svc\.cluster\.local:9000|endpoint: http://spectre-minio.spectre-system.svc.cluster.local:9000|' \
    "$SRC_DIR/spectre_v1alpha2_scrapejob_s3.yaml" \
    > "$DST_DIR/s3.yaml"

# webhook: rewrite URL to the in-cluster mock receiver.
sed -E 's|url: https://example-receiver\.example\.com/spectre|url: http://mock-webhook-receiver.spectre-system.svc.cluster.local:8888/|' \
    "$SRC_DIR/spectre_v1alpha2_scrapejob_webhook.yaml" \
    > "$DST_DIR/webhook.yaml"

# Sanity guards: each sed substitution must have actually
# fired. If a source sample's URL changes upstream, the guard
# trips and the maintainer updates the sed expression here.
# Match the post-substitution form (presence-positive), so
# substring overlap (`minio` ⊂ `spectre-minio`) doesn't
# false-trigger.
if ! grep -q "endpoint: http://spectre-minio.spectre-system.svc.cluster.local:9000" "$DST_DIR/s3.yaml"; then
    echo "ERROR: s3 endpoint substitution did not occur." >&2
    echo "  The source sample's endpoint doesn't match the expected pattern." >&2
    echo "  Update tools/test/sync-ci-samples.sh's sed expression for s3." >&2
    exit 1
fi
if ! grep -q "url: http://mock-webhook-receiver.spectre-system.svc.cluster.local:8888/" "$DST_DIR/webhook.yaml"; then
    echo "ERROR: webhook URL substitution did not occur." >&2
    echo "  The source sample's webhook URL doesn't match the expected placeholder." >&2
    echo "  Update tools/test/sync-ci-samples.sh's sed expression for webhook." >&2
    exit 1
fi

echo "Synced CI samples from $SRC_DIR → $DST_DIR"
