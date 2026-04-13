#!/usr/bin/env bash
# check-helm-crds.sh — verify that every CRD in charts/aip-k8s/templates/crds/
# matches the canonical CRD in config/crd/bases/ (modulo the three Helm
# lifecycle annotations that sync-helm-crds.sh injects).
#
# Exits 1 if any drift is detected; prints a diff for each drifted file.
# Run with `make helm-crds-check`.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASES="$REPO_ROOT/config/crd/bases"
CHART="$REPO_ROOT/charts/aip-k8s/templates/crds"

fail=0

for src in "$BASES"/*.yaml; do
    name="$(basename "$src")"
    dst="$CHART/$name"

    if [ ! -f "$dst" ]; then
        echo "MISSING: $name not found in $CHART"
        fail=1
        continue
    fi

    # Strip the three Helm-specific annotation lines before comparing.
    # All three contain "helm.sh/" so a single grep -v is sufficient.
    if ! diff -u \
            "$src" \
            <(grep -v 'helm\.sh/' "$dst") \
            > /dev/null 2>&1; then
        echo "DRIFT: $name"
        diff -u "$src" <(grep -v 'helm\.sh/' "$dst") || true
        echo ""
        fail=1
    fi
done

if [ "$fail" -ne 0 ]; then
    echo "Helm chart CRDs are out of sync with config/crd/bases/."
    echo "Run 'make sync-helm-crds' (after 'make manifests') to fix."
    exit 1
fi

echo "All Helm chart CRDs are in sync with config/crd/bases/."
