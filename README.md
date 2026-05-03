# Agent Intent Protocol (AIP) Kubernetes Control Plane

## Description

`aip-k8s` is a Kubernetes-native Control Plane implementation of the [Agent Intent Protocol (AIP)](https://github.com/agent-control-plane/agent-intent-protocol).

AIP is an open standard designed to govern autonomous AI agents interacting with critical infrastructure. By requiring agents to declare their intents as cryptographic `AgentRequests` *before* action, this control plane provides strict mutual exclusion (via locking), policy-based governance (via CEL rules), and irrefutable audit trails (via immutable `AuditRecords`).

This repository contains the `governance.aip.io` controller, which serves as the core authority for evaluating and approving AI agent operations across a Kubernetes cluster.

**Documentation**: [agent-control-plane.github.io/aip-k8s](https://agent-control-plane.github.io/aip-k8s/)

### Core APIs

- **AgentRequest**: The primary CRD agents create to request mutating actions on infrastructure.
- **GovernedResource**: Platform engineering declares which resource types agents may mutate, which agent identities may target them, and which context fetcher to invoke. Requests targeting unregistered resource types are rejected at admission.
- **SafetyPolicy**: CEL-based rules defined by administrators to govern which agents can perform what actions. Binds to `GovernedResource` objects via `governedResourceSelector`.
- **AuditRecord**: Immutable event logs generated on every state transition of an AgentRequest.
- **AgentTrustProfile**: Per-agent measured accuracy and trust level, computed from graded verdicts on `AwaitingVerdict` requests.
- **AgentGraduationPolicy**: Namespace-wide thresholds that define when an agent's measured accuracy qualifies it for higher autonomy levels.

## Quick Start

### Install via Helm (Recommended)

```sh
helm install aip-k8s \
  oci://ghcr.io/agent-control-plane/aip-k8s/charts/aip-k8s \
  --version 0.1.0 \
  --namespace aip-k8s-system \
  --create-namespace
```

This single command installs CRDs, the governance controller, the gateway (port 8080), and the dashboard (port 8082).

> **Upgrading an existing installation?** CRDs require a manual step before every `helm upgrade`. See [Upgrading](https://agent-control-plane.github.io/aip-k8s/) in the docs.

**Verify the installation:**

```sh
kubectl get pods -n aip-k8s-system
# NAME                                          READY   STATUS    RESTARTS
# aip-k8s-controller-manager-...               1/1     Running   0
# aip-k8s-gateway-...                          1/1     Running   0
# aip-k8s-dashboard-...                        1/1     Running   0
```

## Documentation

| Doc | Description |
|---|---|
| [API Reference](https://agent-control-plane.github.io/aip-k8s/api-reference/) | Gateway endpoints, authentication, and examples |
| [Agent Graduation Ladder](https://agent-control-plane.github.io/aip-k8s/agent-graduation-ladder/) | How agents earn autonomy through measured accuracy |
| [Trust Gate](https://agent-control-plane.github.io/aip-k8s/trust-gate/) | How measured accuracy gates agent execution |
| [Governed Resources](https://agent-control-plane.github.io/aip-k8s/governed-resources/) | Operator guide: creating GovernedResources, context fetchers, SafetyPolicy binding |
| [Garbage Collection](https://agent-control-plane.github.io/aip-k8s/garbage-collection/) | GC engine configuration, safe rollout, OTLP export |
| [OIDC with Keycloak](https://agent-control-plane.github.io/aip-k8s/oidc-keycloak/) | Step-by-step setup for OIDC authentication |

Runnable demos (scaledown, opslock, kiro) live in the [`demo/`](demo/) directory.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local development, testing, and build instructions.

All new features must conform to the core AIP specification.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
