#!/usr/bin/env bash
# tools/test/check-ci-samples-sync.sh
#
# CI invariant: build/helm/test/samples/ matches the
# regeneration of tools/test/sync-ci-samples.sh against the
# current source samples. Drift means someone edited a source
# sample without regenerating the CI copy. Run as a gate in
# .github/workflows/production-smoke.yml.

set -euo pipefail

# Regenerate to a tmpdir; diff against the committed copies.
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# Run sync against tmp output (override DST_DIR via env). The
# repo-root invocation lets sync-ci-samples.sh resolve SRC_DIR
# relative to the working directory.
DST_DIR="$TMP_DIR" bash tools/test/sync-ci-samples.sh > /dev/null

drift=0
for sample in kafka.yaml s3.yaml webhook.yaml; do
    if ! diff -u "build/helm/test/samples/$sample" "$TMP_DIR/$sample"; then
        echo "ERROR: build/helm/test/samples/$sample is out of sync." >&2
        drift=1
    fi
done

if [[ $drift -eq 1 ]]; then
    echo "" >&2
    echo "Run: bash tools/test/sync-ci-samples.sh" >&2
    exit 1
fi

echo "CI samples are in sync with source."
