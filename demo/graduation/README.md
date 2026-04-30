# Trust Graduation Demo

A self-running demo that takes an agent from **Observer** to **Autonomous** through
all five AIP trust levels, showing how each level changes what the control plane does
with the agent's requests.

```text
Observer → Advisor → Supervised → Trusted → Autonomous
```

---

## What you'll see

| Phase | What happens |
|---|---|
| **Observer** | Requests graded but not executed. Agent builds accuracy signal. |
| **Advisor** | Requests queued for human approval before execution. |
| **Supervised** | Same as Advisor — more executions required to advance. |
| **Trusted** | Trust gate auto-approves. No human in the loop. |
| **Autonomous** | Maximum trust. Fully autonomous execution. |

The demo simulates the reviewer role (approving Advisor/Supervised requests) because
the gateway runs in **open mode** — no auth flags set. This is how all AIP demos run
locally. In a real deployment, a human reviewer (or a separate reviewer service) would
approve those requests.

---

## Prerequisites

**1. A Kubernetes cluster** — local Kind is fine:
```bash
kind create cluster
```

**2. AIP CRDs installed:**
```bash
kubectl apply -k config/crd/bases
```

**3. The controller running** (separate terminal):
```bash
go run cmd/controller/main.go
```

**4. The gateway running in open mode** (separate terminal — no auth flags):
```bash
go run cmd/gateway/main.go
```

> The gateway must run without `--oidc-issuer-url`, `--agent-subjects`, or
> `--reviewer-subjects`. Open mode allows the demo to call both agent and reviewer
> endpoints from a single process.

---

## Run the demo

```bash
./demo/graduation/run.sh
```

Or with custom settings:
```bash
AIP_GATEWAY_URL=http://localhost:8080 NAMESPACE=default ./demo/graduation/run.sh
```

The demo takes **3–5 minutes** depending on controller reconcile speed.

---

## What the demo applies

`run.sh` applies two cluster resources before starting the agent:

**`k8s/policy.yaml`** — `AgentGraduationPolicy` named `default` with compressed
thresholds (the gateway always looks up the policy named `default`):

| Level | Accuracy threshold | Executions required |
|---|---|---|
| Observer | — | — |
| Advisor | ≥ 0.70 | — |
| Supervised | ≥ 0.80 | 3 |
| Trusted | ≥ 0.90 | 6 |
| Autonomous | ≥ 0.95 | 10 |

**`k8s/resource.yaml`** — `GovernedResource` for `k8s://demo/default/deployment/*`
with `minTrustLevel: Observer` and `maxAutonomyLevel: Autonomous`, allowing the full
ladder to operate.

---

## Inspect the results

**Trust profile after the demo:**
```bash
kubectl get agenttrustprofiles -n default
kubectl describe agenttrustprofile <name> -n default
```

**Full audit trail:**
```bash
kubectl get auditrecords -n default --sort-by=.spec.timestamp
```

**Trust level changes only:**
```bash
kubectl get auditrecords -n default \
  --field-selector spec.event=agent.trustprofile.updated
```

---

## Clean up

```bash
kubectl delete agentrequests,auditrecords -n default \
  -l aip.io/agentIdentity=graduation-demo-agent
kubectl delete governedresource demo-deployments
kubectl delete agentgraduationpolicy default
```
