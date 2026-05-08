#!/usr/bin/env bash
# tools/test/verify-s3-sink.sh
#
# Verify R7.2's s3 ScrapeJob (build/helm/test/samples/s3.yaml)
# produced an object in the spectre-rows bucket under the
# scrapes/<JobID>/ prefix. Reads via mc (MinIO client) inside
# the Bitnami minio pod.
#
# The s3 sample's key template `scrapes/{{.JobID}}/rows.jsonl`
# means the exact key isn't known upfront. The verifier lists
# the prefix and asserts ≥1 non-empty `.jsonl` object.
#
# Usage:
#   bash tools/test/verify-s3-sink.sh [namespace] [release]

set -euo pipefail

NAMESPACE="${1:-spectre-system}"
RELEASE="${2:-spectre}"
BUCKET="${SPECTRE_S3_BUCKET:-spectre-rows}"

# Bitnami minio standalone-mode names the Deployment
# `<release>-minio`; the pod's labels are
# `app.kubernetes.io/name=minio,instance=<release>`.
MINIO_POD="$(kubectl -n "$NAMESPACE" get pod \
    -l "app.kubernetes.io/name=minio,app.kubernetes.io/instance=$RELEASE" \
    -o jsonpath='{.items[0].metadata.name}')"

if [[ -z "$MINIO_POD" ]]; then
    echo "ERROR: no minio pod found in namespace $NAMESPACE" >&2
    exit 1
fi

echo "Listing s3://${BUCKET}/scrapes/ on pod '${MINIO_POD}'..."

# Bitnami's minio pod has mc preconfigured but we re-alias
# defensively to avoid relying on image-internal config. The
# root credentials are wired into the pod's env from the
# Secret the chart creates.
LISTING="$(kubectl exec -n "$NAMESPACE" "$MINIO_POD" -- \
    bash -c "
        mc alias set local http://127.0.0.1:9000 \"\$MINIO_ROOT_USER\" \"\$MINIO_ROOT_PASSWORD\" >/dev/null 2>&1 || true
        mc ls --recursive local/$BUCKET/scrapes/ 2>/dev/null
    " || true)"

if [[ -z "$LISTING" ]]; then
    echo "ERROR: no objects under s3://${BUCKET}/scrapes/" >&2
    echo "Bucket contents:" >&2
    kubectl exec -n "$NAMESPACE" "$MINIO_POD" -- \
        bash -c "mc ls --recursive local/$BUCKET/ 2>/dev/null || mc ls local/" >&2 || true
    exit 1
fi

# `mc ls --recursive` output format varies by mc version:
#   `[date time tz] size key`             (older)
#   `[date time tz] size STANDARD key`    (newer — adds a
#                                          storage-tier column)
# The verifier was originally written against the older form
# with `$(NF-1)+0 > 0` as the size check, which silently breaks
# when "STANDARD" sits in NF-1 (`"STANDARD"+0 == 0` → all rows
# rejected as zero-size). Scan fields for a `B|KiB|MiB|GiB|TiB`
# suffix to locate the size column robustly across versions
# (production-smoke mini-phase, 2026-05-07).
FIRST_KEY="$(echo "$LISTING" | awk '
  $NF ~ /\.jsonl$/ {
    for (i = 1; i < NF; i++) {
      if ($i ~ /^[0-9.]+(B|KiB|MiB|GiB|TiB)$/) {
        size = $i;
        gsub(/[^0-9.]/, "", size);
        if (size + 0 > 0) { print $NF; exit }
      }
    }
  }')"
if [[ -z "$FIRST_KEY" ]]; then
    echo "ERROR: no non-empty .jsonl objects under scrapes/" >&2
    echo "Listing was:" >&2
    echo "$LISTING" >&2
    exit 1
fi

echo "✓ s3 sink verified: found '$FIRST_KEY' in bucket '$BUCKET'"
echo "$LISTING" | head -3
