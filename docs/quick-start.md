# Quick Start

Install the AIP Kubernetes Control Plane and submit your first governed agent request in under five minutes.

## Prerequisites

- A running Kubernetes cluster (local KIND, minikube, or remote)
- kubectl configured to talk to your cluster
- Helm 3

## Install

```bash
helm install aip-k8s charts/aip-k8s/ \
  --namespace aip-k8s-system \
  --create-namespace
```

This installs the gateway, controller, and dashboard with **dev mode defaults**:
- No authentication (anyone can submit requests)
- No JWT minting
- No OIDC

> ⚠️ **Dev mode only.** For production, see [Production Hardening](./user-guide/production-hardening.md).

## Verify

Wait for pods to be ready:

```bash
kubectl wait --for=condition=ready pod \
  -l app.kubernetes.io/component=gateway \
  -n aip-k8s-system --timeout=60s
```

## Access the gateway

Port-forward the gateway service to your local machine:

```bash
kubectl port-forward -n aip-k8s-system svc/aip-k8s-gateway 8080:8080
```

Leave this running in a terminal. The gateway is now available at `http://localhost:8080`.

## Your first request

### 1. Register a governed resource

```bash
curl -s -X POST http://localhost:8080/governed-resources \
  -H "Content-Type: application/json" \
  -d '{
    "name": "payment-api",
    "uriPattern": "k8s://prod/default/deployment/payment-api",
    "permittedActions": ["scale", "delete", "escalate"]
  }'
```

### 2. Submit an agent request

```bash
curl -s -X POST http://localhost:8080/agent-requests \
  -H "Content-Type: application/json" \
  -d '{
    "agentIdentity": "cost-optimizer",
    "action": "delete",
    "targetURI": "k8s://prod/default/deployment/payment-api",
    "reason": "CPU at 3% for 45 minutes. Assessed as idle."
  }'
```

### 3. See the response

The gateway returns the resolved phase:

```json
{
  "phase": "Denied",
  "denialCode": "POLICY_VIOLATION",
  "reason": "SafetyPolicy live-traffic-guard: readyReplicas=3. Agent claimed idle."
}
```

The request was blocked because the agent's claimed state (idle) contradicted the live state (3 ready replicas).

## Next steps

- Open the [Dashboard Walkthrough](./dashboard.md) to see the visual interface
- Read [Dev Mode](./user-guide/dev-mode.md) to understand what "no authentication" means
- Run the [Scaledown Demo](./user-guide/demos/scaledown.md) end-to-end
- See [Production Hardening](./user-guide/production-hardening.md) before deploying to a real cluster
