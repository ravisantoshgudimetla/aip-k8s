# Scaledown Demo

The scaledown demo shows AIP blocking an agent that operates on stale metrics. The agent believes a service is idle; AIP independently verifies live state and blocks the deletion.

## What it demonstrates

1. **Stale data detection** — The agent's monitoring cache is 6 hours old
2. **Live state verification** — AIP checks actual endpoints and ready replicas
3. **Confidence scoring** — The agent's confidence drops when contradicted
4. **Escalation** — After two blocked attempts, the agent escalates to human review

## Prerequisites

- AIP installed in dev mode (see [Quick Start](../../quick-start.md))
- Gateway and dashboard port-forwarded

## Run the demo

### Option A: Against local processes (go run)

```bash
./demo/scaledown/start.sh  # starts controller, gateway, dashboard locally
./demo/scaledown/run.sh    # runs the demo agent
```

### Option B: Against Helm-deployed cluster

```bash
# Port-forward the gateway
kubectl port-forward -n aip-k8s-system svc/aip-k8s-gateway 8080:8080 &

# Run the demo with --cluster flag
./demo/scaledown/run.sh --cluster
```

The `--cluster` flag tells the agent to target `http://localhost:8080` (which is port-forwarded to the cluster).

## What happens

1. The agent deploys `payment-api` (3 replicas, active endpoints)
2. The agent reads stale metrics showing "CPU at 3% for 45 minutes"
3. The agent submits `AgentRequest` with action `delete`
4. AIP checks the live state — finds active endpoints and ready replicas
5. AIP denies the request with `POLICY_VIOLATION`
6. The agent retries with `scale-to-0`
7. AIP denies again
8. The agent escalates — confidence drops to 35%

## Expected dashboard view

After running the demo, open the dashboard at `http://localhost:8082`:

- Three denied requests from `idle-resource-reaper`
- Governance timeline showing: Intent declared → Policy evaluated → Human gate blocked
- Agent reasoning explaining the stale cache contradiction
- Audit trail with timestamps for each state transition

See the [Dashboard Walkthrough](../../dashboard.md) for a screenshot walkthrough.

## Technical details

For the agent implementation, demo manifests, and SafetyPolicy CEL rules, see:
- [`demo/scaledown/README.md`](https://github.com/agent-control-plane/aip-k8s/tree/main/demo/scaledown)
- [`demo/scaledown/policies/live-traffic-guard.yaml`](https://github.com/agent-control-plane/aip-k8s/tree/main/demo/scaledown/policies)

## Variations

### With LLM reasoning

To run the demo with a Claude-powered agent:

```bash
export ANTHROPIC_API_KEY=sk-...
AGENT=claude ./demo/scaledown/run.sh
```

This uses the LLM to reason about the metrics and decide whether to submit the request. The LLM may or may not be more cautious than the deterministic agent.

### With AWS Bedrock

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
AGENT=claude-go ./demo/scaledown/run.sh
```
