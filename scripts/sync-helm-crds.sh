#!/usr/bin/env bash
# sync-helm-crds.sh — copy every CRD from config/crd/bases/ into
# charts/aip-k8s/templates/crds/, injecting the three Helm lifecycle
# annotations required by the chart.
#
# Run after `make manifests` any time the Go types change.
# Counterpart: scripts/check-helm-crds.sh validates the result.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASES="$REPO_ROOT/config/crd/bases"
CHART="$REPO_ROOT/charts/aip-k8s/templates/crds"

for src in "$BASES"/*.yaml; do
    name="$(basename "$src")"
    dst="$CHART/$name"
    # Inject the three Helm annotations immediately after the
    # controller-gen annotation line.  awk prints the matching line
    # first, then the three extra lines, then resumes normal output.
    awk '/controller-gen\.kubebuilder\.io\/version:/ {
        print
        print "    helm.sh/resource-policy: keep"
        print "    \"helm.sh/hook\": pre-install,pre-upgrade"
        print "    \"helm.sh/hook-weight\": \"-5\""
        next
    }
    { print }' "$src" > "$dst"
    echo "synced $name"
done
echo "All Helm chart CRDs synced from config/crd/bases/."
