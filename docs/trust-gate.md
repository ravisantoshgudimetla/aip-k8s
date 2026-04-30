# Trust Gate — Operator Guide

The trust gate is evaluated by the gateway on every `AgentRequest` submission. It
checks the submitting agent's earned trust level against the `trustRequirements` of the
matched `GovernedResource`, and stamps three annotations on the request that the
controller uses to route it — auto-approve, queue for human review, or reject.

Without an `AgentGraduationPolicy` named `default`, the gate falls through with
fail-closed defaults (`canExecute=false`). Without `trustRequirements` on a
`GovernedResource`, no trust gate check is performed for that resource.

---

## Quick start

```bash
# 1. Apply the graduation policy (cluster-wide, name must be "default")
kubectl apply -f config/samples/governance_v1alpha1_agentgraduationpolicy.yaml

# 2. Apply a GovernedResource with trust requirements
kubectl apply -f config/samples/governance_v1alpha1_governedresource.yaml

# 3. Submit a request as a new agent (Observer level — no profile yet)
curl -s -X POST http://localhost:8080/agent-requests \
  -H "Authorization: Bearer $AGENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "agentIdentity": "karpenter-nodepool-agent",
    "action": "scale-up",
    "targetURI": "k8s://prod/karpenter/nodepool/team-a-workers",
    "reason": "15 pods pending for 8 min, all 10 nodes occupied"
  }'

# New agents start at Observer. With minTrustLevel=Supervised, the gateway
# rejects the request immediately:
# HTTP 403: "agent trust level Observer does not meet resource minimum Supervised"

# 4. Observe trust profile state as the agent earns verdicts
kubectl get agenttrustprofiles -n production
# NAME                         TRUSTLEVEL   AGE
# karpenter-nodepool-agent-…   Advisor      3d
```

---

## How it works

When a request arrives at `POST /agent-requests`, the gateway runs the trust gate
before creating the `AgentRequest` object:

```
1. Match GovernedResource by URIPattern
2. If GovernedResource has no trustRequirements → skip trust gate
3. If request mode == "observe" → skip trust gate (grading, no action taken)
4. Look up AgentTrustProfile by agent identity
   └─ Not found → treat as Observer
5. Check minTrustLevel: reject if agent level < floor
6. Compute effectiveAutonomy = min(agentLevel, maxAutonomyLevel)
7. Look up AgentGraduationPolicy named "default"
   └─ Not found → fail-closed (canExecute=false, requiresApproval=true)
8. Stamp annotations on the request
```

The controller reads those annotations and routes accordingly:

| `can-execute` | `requires-human-approval` | Outcome |
|---|---|---|
| `false` | — | Request rejected before creation |
| `true` | `true` | Request created, routes to `Pending` (human approval) |
| `true` | `false` | Request created, auto-approved, proceeds to lock acquisition |

---

## Annotations stamped by the gateway

| Annotation | Values | Meaning |
|---|---|---|
| `governance.aip.io/effective-trust-level` | level name | The agent's level after applying `maxAutonomyLevel` cap |
| `governance.aip.io/can-execute` | `"false"` | Agent cannot execute; request is rejected |
| `governance.aip.io/requires-human-approval` | `"true"` / `"false"` | Whether a human reviewer must approve |

`can-execute` and `requires-human-approval` are mutually exclusive — only one is
set per request. If `can-execute=false`, the request never reaches the controller.

To inspect what the gateway decided on a submitted request:

```bash
kubectl get agentrequest <name> -n <namespace> -o jsonpath='{.metadata.annotations}' | jq
# {
#   "governance.aip.io/effective-trust-level": "Supervised",
#   "governance.aip.io/requires-human-approval": "true"
# }
```

---

## AgentGraduationPolicy fields

`AgentGraduationPolicy` is cluster-scoped. The gateway and controller both look up the
policy named **`default`** — this name is required.

```yaml
apiVersion: governance.aip.io/v1alpha1
kind: AgentGraduationPolicy
metadata:
  name: default       # must be "default"
spec:
  evaluationWindow:
    count: 50         # how many recent verdicts drive trust level computation

  awaitingVerdictTTL: "168h"   # ungraded Observer requests expire after this duration

  levels:
    - name: Observer
      canExecute: false         # required field — grading only, no execution
    - name: Advisor
      canExecute: true          # required field
      requiresHumanApproval: true
      accuracy:
        min: 0.70               # recentAccuracy must be >= this to hold the level
        demotionBuffer: 0.02    # demotion triggers at < min - buffer (= 0.68 here)
      executions:
        min: 0                  # totalExecutions must be >= this
    # ... Supervised, Trusted, Autonomous

  demotionPolicy:
    accuracyDropThreshold: 0.10
    windowSize: 20
    gracePeriod: "24h"          # no demotion within this window after promotion
```

### Level fields

| Field | Required | Description |
|---|---|---|
| `name` | Yes | `Observer`, `Advisor`, `Supervised`, `Trusted`, or `Autonomous` |
| `canExecute` | Yes | Whether agents at this level may execute actions |
| `requiresHumanApproval` | No | Whether human approval is required (default `false`) |
| `accuracy.min` | No | Minimum `recentAccuracy` to hold this level |
| `accuracy.max` | No | Upper bound (used by the controller for level resolution) |
| `accuracy.demotionBuffer` | No | Hysteresis margin: demotion triggers at `min - buffer` |
| `executions.min` | No | Minimum `totalExecutions` required for promotion |
| `executions.max` | No | Upper bound for level resolution |

**Promotion** requires both `accuracy.min` and `executions.min` to be satisfied simultaneously. An agent at high accuracy but low execution count is held at the lower level until it accumulates enough executions.

**Demotion** is triggered by accuracy only — `recentAccuracy < accuracy.min - accuracy.demotionBuffer`. Execution count never causes demotion.

---

## GovernedResource trust requirements

`trustRequirements` is optional. Omitting it disables the trust gate for that resource.

```yaml
spec:
  trustRequirements:
    minTrustLevel: Supervised     # Observer | Advisor | Supervised | Trusted | Autonomous
    maxAutonomyLevel: Supervised  # same enum
```

| Field | Description |
|---|---|
| `minTrustLevel` | Agents below this level are rejected. Observer-mode requests always bypass this check. |
| `maxAutonomyLevel` | Caps the effective autonomy of even highly-trusted agents. A Trusted agent with a ceiling of Supervised still requires human approval for this resource. |

The ceiling is the key safety lever. It lets you keep a sensitive resource under human
oversight indefinitely, regardless of how trusted the agent becomes globally. Raise it
only after reviewing the agent's execution history for that resource type.

---

## AgentTrustProfile — controller-managed

`AgentTrustProfile` is created and updated automatically by the controller when the
first graded verdict lands for an agent. You do not create it manually.

The profile name is a stable hash of the agent identity — use `kubectl get` to observe it:

```bash
kubectl get agenttrustprofiles -n <namespace>
# NAME                               TRUSTLEVEL   AGE
# karpenter-nodepool-agent-a1b2c3d4  Trusted      14d

kubectl describe agenttrustprofile karpenter-nodepool-agent-a1b2c3d4 -n <namespace>
# Status:
#   Trust Level:        Trusted
#   Diagnostic Accuracy: 0.93
#   Recent Accuracy:     0.94
#   Total Reviewed:      52
#   Total Executions:    58
#   Success Rate:        0.97
#   Last Promoted At:    2026-04-15T09:22:00Z
```

The controller emits an `agent.trustprofile.updated` AuditRecord on every level change:

```bash
kubectl get auditrecords -n <namespace> \
  --field-selector spec.event=agent.trustprofile.updated
```

---

## Common patterns

### Soak mode + trust gate together

Use `soakMode: true` on a `GovernedResource` while the agent is new — all requests
route to `AwaitingVerdict` for grading regardless of trust level. Once you have
enough accuracy signal, set `soakMode: false` and configure `trustRequirements`.
The two features compose cleanly.

### Bootstrapping a higher trust level for testing

If you need to test auto-approval behavior without waiting for the graduation ladder,
you can manually patch an `AgentTrustProfile` status:

```bash
# Find the profile name
kubectl get agenttrustprofiles -n default

# Patch to Trusted level
kubectl patch agenttrustprofile <name> -n default \
  --subresource=status \
  --type=merge \
  -p '{"status":{"trustLevel":"Trusted"}}'
```

The controller will reconcile and recompute the level on the next trigger. Use this
only in test environments — in production, let the graduation ladder operate.

### Raising the ceiling after proven track record

```bash
kubectl patch governedresource karpenter-nodepools \
  --type=merge \
  -p '{"spec":{"trustRequirements":{"minTrustLevel":"Supervised","maxAutonomyLevel":"Trusted"}}}'
```

Review the agent's `AuditRecord` history before raising the ceiling:

```bash
kubectl get auditrecords -n production \
  -l governance.aip.io/agent-identity=karpenter-nodepool-agent \
  --sort-by=.spec.timestamp
```

---

## Troubleshooting

**Agent gets 403 "does not meet resource minimum"**
The agent's `AgentTrustProfile` is below `minTrustLevel`. Check its current level:
```bash
kubectl get agenttrustprofiles -n <namespace>
```
Either the agent needs more graded verdicts to graduate, or `minTrustLevel` is set
too high for where the agent currently is.

**Agent gets 403 but has no profile yet**
A missing profile defaults to `Observer`. If `minTrustLevel` is `Supervised` or
higher, all requests from new agents are rejected. Lower `minTrustLevel` to `Observer`
while the agent builds its accuracy record, then raise it.

**Requests are routing to Pending (human approval) even though agent is Trusted**
Check `maxAutonomyLevel` on the `GovernedResource`. If the ceiling is `Supervised`,
the agent is treated as Supervised regardless of its profile level. This is intentional
— raise the ceiling explicitly when you are ready to remove the human from the loop.

**Policy not found — requests fail closed**
The gateway requires `AgentGraduationPolicy` named `default`. If it is missing,
`canExecute` defaults to `false` and all requests for resources with `trustRequirements`
are rejected. Apply `config/samples/governance_v1alpha1_agentgraduationpolicy.yaml`.
