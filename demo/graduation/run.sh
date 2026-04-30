#!/usr/bin/env bash
set -euo pipefail

GATEWAY_URL="${AIP_GATEWAY_URL:-http://localhost:8080}"
NAMESPACE="${NAMESPACE:-default}"
DEMO_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  AIP Demo: Trust Graduation Ladder"
echo "  Observer → Advisor → Supervised → Trusted → Autonomous"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# ── Preflight checks ──────────────────────────────────────────────────────────

echo "[ 1/4 ] Checking Kubernetes cluster..."
if ! kubectl cluster-info > /dev/null 2>&1; then
  echo "  ✗ No Kubernetes cluster found."
  echo "    Start a local cluster: kind create cluster"
  exit 1
fi
echo "  ✓ Cluster ready"

echo "[ 2/4 ] Checking AIP CRDs..."
if ! kubectl get crd agentgraduationpolicies.governance.aip.io > /dev/null 2>&1; then
  echo "  ✗ AIP CRDs not installed."
  echo "    Install them: kubectl apply -k ${ROOT_DIR}/config/crd/bases"
  exit 1
fi
echo "  ✓ CRDs installed"

echo "[ 3/4 ] Checking AIP Gateway at ${GATEWAY_URL}..."
if ! curl -sf "${GATEWAY_URL}/healthz" > /dev/null; then
  echo "  ✗ Gateway not running."
  echo "    Start it (no auth — required for demo): go run ${ROOT_DIR}/cmd/gateway/main.go"
  echo ""
  echo "    The controller must also be running:"
  echo "    go run ${ROOT_DIR}/cmd/controller/main.go"
  exit 1
fi
echo "  ✓ Gateway running"

echo "[ 4/4 ] Applying demo resources..."

# Warn if a graduation policy already exists — we will overwrite it.
if kubectl get agentgraduationpolicy default > /dev/null 2>&1; then
  echo "  ⚠  AgentGraduationPolicy 'default' already exists — overwriting with demo thresholds."
fi

kubectl apply -f "${DEMO_DIR}/k8s/policy.yaml" > /dev/null
kubectl apply -f "${DEMO_DIR}/k8s/resource.yaml" > /dev/null
echo "  ✓ AgentGraduationPolicy (default) applied"
echo "  ✓ GovernedResource (demo-deployments) applied"

# Clean up any leftovers from a previous run of this demo.
kubectl delete agentrequests -n "${NAMESPACE}" -l "aip.io/agentIdentity=graduation-demo-agent" \
  --ignore-not-found > /dev/null 2>&1 || true
kubectl delete agenttrustprofile -n "${NAMESPACE}" \
  "$(kubectl get agenttrustprofile -n "${NAMESPACE}" \
     -o jsonpath='{range .items[?(@.spec.agentIdentity=="graduation-demo-agent")]}{.metadata.name}{end}' \
     2>/dev/null || true)" \
  --ignore-not-found > /dev/null 2>&1 || true
kubectl delete diagnosticaccuracysummary -n "${NAMESPACE}" "graduation-demo-agent" \
  --ignore-not-found > /dev/null 2>&1 || true
echo "  ✓ Previous demo state cleared"

echo ""

# ── Run the agent ─────────────────────────────────────────────────────────────

go run "${DEMO_DIR}/agent/main.go" \
  --gateway="${GATEWAY_URL}" \
  --namespace="${NAMESPACE}"

# ── Post-run summary ──────────────────────────────────────────────────────────

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Post-run audit (last 15 records):"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
kubectl get auditrecords -n "${NAMESPACE}" \
  --sort-by=.spec.timestamp 2>/dev/null | tail -15 || true

echo ""
echo "  To clean up all demo resources:"
echo "    kubectl delete agentrequests,auditrecords -n ${NAMESPACE} -l aip.io/agentIdentity=graduation-demo-agent"
echo "    kubectl delete governedresource demo-deployments"
echo "    kubectl delete agentgraduationpolicy default"
