#!/usr/bin/env bash
set -euo pipefail

GATEWAY_URL="${AIP_GATEWAY_URL:-http://localhost:8080}"
NAMESPACE="${NAMESPACE:-default}"
DEMO_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  AIP Demo: Kiro — Autonomous Production Guardrails"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# ── Preflight checks ──────────────────────────────────────────────────────────

echo "[ 1/5 ] Checking Kubernetes cluster..."
if ! kubectl cluster-info > /dev/null 2>&1; then
  echo "  ✗ No Kubernetes cluster found. Start KIND:"
  echo "    kind create cluster"
  exit 1
fi
echo "  ✓ Cluster ready"

echo "[ 2/5 ] Checking AIP CRDs..."
if ! kubectl get crd governedresources.governance.aip.io > /dev/null 2>&1; then
  echo "  ✗ AIP CRDs not installed."
  echo "    Install them: kubectl apply -k ${ROOT_DIR}/config/crd/bases"
  exit 1
fi
echo "  ✓ CRDs installed"

echo "[ 3/5 ] Checking AIP Gateway at ${GATEWAY_URL}..."
if ! curl -sf "${GATEWAY_URL}/healthz" > /dev/null; then
  echo "  ✗ Gateway not running. Start it:"
  echo "    go run ${ROOT_DIR}/cmd/gateway/main.go"
  echo ""
  echo "    Controller must also be running:"
  echo "    go run ${ROOT_DIR}/cmd/controller/main.go"
  exit 1
fi
echo "  ✓ Gateway running"

echo "[ 4/5 ] Cleaning up any leftovers from previous runs..."
kubectl delete agentrequests --all -n "${NAMESPACE}" --ignore-not-found > /dev/null 2>&1 || true
kubectl delete auditrecords --all -n "${NAMESPACE}" --ignore-not-found > /dev/null 2>&1 || true
echo "  ✓ Clean slate"

echo "[ 5/5 ] Applying demo resources..."
kubectl apply -f "${DEMO_DIR}/k8s/resource.yaml" > /dev/null
kubectl apply -f "${DEMO_DIR}/policies/prod-require-approval.yaml" > /dev/null
echo "  ✓ GovernedResource (kiro-prod-deployments) applied"
echo "  ✓ SafetyPolicy (prod-require-approval) applied"

echo ""

# ── Run agent ─────────────────────────────────────────────────────────────────

go run "${DEMO_DIR}/agent/main.go" \
  --gateway="${GATEWAY_URL}" \
  --namespace="${NAMESPACE}"

# ── Post-run audit ────────────────────────────────────────────────────────────

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Post-run audit (last 5 records):"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
kubectl get auditrecords -n "${NAMESPACE}" \
  --sort-by=.spec.timestamp 2>/dev/null | tail -5 || true

echo ""
echo "  To clean up:"
echo "    kubectl delete agentrequests,auditrecords --all -n ${NAMESPACE}"
echo "    kubectl delete safetypolicy prod-require-approval -n ${NAMESPACE}"
echo "    kubectl delete governedresource kiro-prod-deployments"
