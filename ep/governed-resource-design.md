# Design: GovernedResource CRD

**Status**: Draft

## Problem

AIP currently has no concept of *what resources agents are permitted to mutate*. Platform engineering teams have no way to declare "Karpenter NodePools and GitHub PRs are in-scope for agent actions" without writing bespoke SafetyPolicy rules. There is no registry of governed resource types, no per-resource context fetchers, and no enforcement that an agent is even targeting a resource type the platform team has approved for agent mutation.

This gap means:

- An agent can submit an `AgentRequest` targeting *any* URI — there is no admission-time check that the target resource type is sanctioned.
- Reviewers have no live context when evaluating requests (current nodepool utilization, pending pods, PR diff) — they are approving based on agent-declared intent alone.
- There is no canonical mapping from resource type → which agents are permitted to mutate it.

## Design Goals

1. **Resource registry**: Platform engineering declares which resource types agents may mutate. Requests targeting unsanctioned resource types are rejected at admission time.
2. **Agent-to-resource binding**: A `GovernedResource` scopes which agent identities may target it, eliminating the need for a full RBAC system by leveraging naming conventions.
3. **Context fetchers**: When an `AgentRequest` is submitted, the control plane independently fetches live context for the target resource and surfaces it to the reviewer.
4. **Clear role separation**: Platform engineering owns `GovernedResource` creation. App teams own their agents and `SafetyPolicy`. Agents submit `AgentRequest` only.

## Non-Goals

- A full RBAC system with custom roles and bindings. The three-role model (admin / reviewer / agent) covers the majority of enterprise use cases.
- Namespace-scoped `GovernedResource`. Most governed resources (Karpenter NodePools, GitHub repos) are inherently cluster-scoped or external. Start cluster-scoped.
- A UI for `GovernedResource` management. kubectl and GitOps are sufficient for the initial version.

## Roles

AIP defines three roles enforced at the gateway:

| Role | Gateway flag | Responsibilities |
|---|---|---|
| **Admin** | `--admin-subjects` | Create/modify `GovernedResource` and `SafetyPolicy` via gateway API. Owned by platform engineering. |
| **Reviewer** | `--reviewer-subjects` | Approve or deny `AgentRequest`. Owned by platform engineering — not the team running the agent. |
| **Agent** | `--agent-subjects` | Submit `AgentRequest` only. Cannot modify policy or approve requests. |

### Why platform engineering owns review

The team proposing a change should not approve it. Platform engineering has cluster-wide context (cost, capacity, blast radius) that application teams lack. This separation is the same model as change management in mature engineering organizations and produces a stronger audit trail: "a platform engineer approved this agent action" is a defensible governance posture; "the agent's own team approved it" is not.

### Why a full RBAC system is not needed

Multi-team isolation is achieved through naming conventions and URI pattern matching in `GovernedResource`, not through a role/binding system. If Team A's nodepool is `nodepool/team-a-workers`, then:

```yaml
spec:
  uriPattern: "k8s://prod/karpenter/nodepool/team-a-*"
  permittedAgents: ["aip-agent-team-a"]
```

An agent submitting a request for `nodepool/team-b-workers` is rejected at admission — no `GovernedResource` matches that pattern for that agent identity. The naming convention *is* the access control. Full RBAC becomes relevant only when you have many teams with overlapping resource namespaces — add it then, not now.

## GovernedResource CRD

### Schema

```go
type GovernedResourceSpec struct {
    // URIPattern is a glob pattern matched against AgentRequest.spec.target.uri.
    // Requests targeting URIs that do not match any GovernedResource are rejected.
    // Examples:
    //   "k8s://prod/karpenter/nodepool/team-a-*"
    //   "github://org/repo-*"
    //   "k8s://*/default/deployment/*"
    // +kubebuilder:validation:MinLength=1
    URIPattern string `json:"uriPattern"`

    // PermittedActions lists the action strings agents may request on this resource.
    // Requests with actions not in this list are rejected.
    // +kubebuilder:validation:MinItems=1
    PermittedActions []string `json:"permittedActions"`

    // PermittedAgents lists agent identity values (matched against --oidc-identity-claim)
    // that may submit AgentRequests targeting this resource.
    // Empty means any authenticated agent may target this resource.
    // +optional
    PermittedAgents []string `json:"permittedAgents,omitempty"`

    // ContextFetcher names the built-in fetcher to invoke when an AgentRequest
    // targets this resource type. The fetcher populates status.providerContext
    // so reviewers see live resource state alongside the agent's declared intent.
    // Supported values: "karpenter", "github", "k8s-deployment", "none"
    // +kubebuilder:validation:Enum=karpenter;github;k8s-deployment;none
    // +kubebuilder:default=none
    ContextFetcher string `json:"contextFetcher"`

    // Description is a human-readable explanation of this governed resource type,
    // shown to reviewers during the approval decision.
    // +optional
    Description string `json:"description,omitempty"`
}
```

### Example: Karpenter NodePool

```yaml
apiVersion: governance.aip.io/v1alpha1
kind: GovernedResource
metadata:
  name: karpenter-nodepool-team-a
spec:
  uriPattern: "k8s://prod/karpenter/nodepool/team-a-*"
  permittedActions:
    - scale-up
    - scale-down
  permittedAgents:
    - aip-agent-team-a
  contextFetcher: karpenter
  description: "Team A Karpenter NodePools. Scaling requires platform engineering approval."
```

### Example: GitHub PRs

```yaml
apiVersion: governance.aip.io/v1alpha1
kind: GovernedResource
metadata:
  name: github-prs-infra
spec:
  uriPattern: "github://myorg/infra-*"
  permittedActions:
    - open-pr
    - close-pr
  permittedAgents:
    - aip-agent-team-a
  contextFetcher: github
  description: "Infrastructure repos. PRs opened by agents require platform engineering review."
```

## URI Scheme

All URIs submitted in `AgentRequest.spec.target.uri` and matched in `GovernedResource.spec.uriPattern` follow this canonical format:

```
<scheme>://<cluster-or-org>/<group-or-type>/<name-or-path>
```

Examples:
- `k8s://prod/karpenter.sh/nodepool/team-a-workers` — Karpenter NodePool in the `prod` cluster
- `k8s://prod/apps/deployment/default/payment-api` — Kubernetes Deployment, with namespace before name
- `github://myorg/infra-platform` — GitHub repository

Agents and `GovernedResource` authors must agree on which URI format they use. The gateway performs literal glob matching against whatever URI the agent provides — it does not normalise or parse URIs.

## Glob Pattern Semantics

`GovernedResource.spec.uriPattern` uses Go [`path.Match`](https://pkg.go.dev/path#Match) semantics:

- `*` matches any sequence of non-`/` characters within a single path segment.
- `?` matches any single non-`/` character.
- `[...]` character classes are supported.
- `**` is **not** supported. Use multiple patterns or a broader `*` match at the right segment.

This is intentionally restrictive: a pattern like `k8s://prod/karpenter.sh/nodepool/*` matches `k8s://prod/karpenter.sh/nodepool/team-a-workers` but not `k8s://prod/karpenter.sh/nodepool/team-a/extra-segment`. Platform engineers must be explicit about the segment depth they intend to govern.

## Backward Compatibility: Zero GovernedResources

When the `GovernedResource` CRD is installed but no `GovernedResource` objects exist, the admission check would reject all `AgentRequest` submissions — a breaking change for existing deployments.

The gateway flag `--require-governed-resource` (default `false`) controls this:

- `false` (default): if the GovernedResource list is empty, skip the admission check. Existing deployments are unaffected.
- `true`: enforce strictly — no `AgentRequest` is accepted unless a matching `GovernedResource` exists. Opt in when the registry is fully populated.

Once at least one `GovernedResource` exists, the check is always enforced regardless of this flag. The flag only governs the zero-resources case.

## Action Vocabulary

`permittedActions` is free-form. There is no built-in registry of action names. This is intentional — governed resource types are heterogeneous and a shared vocabulary would become a maintenance burden.

Convention: action strings should be lower-kebab-case verb phrases describing the mutation. The agent and the `GovernedResource` author must agree out-of-band on which strings to use.

Recommended vocabulary for built-in fetcher types:

| Fetcher | Recommended actions |
|---|---|
| `karpenter` | `scale-up`, `scale-down`, `update-limits` |
| `github` | `open-pr`, `close-pr`, `merge-pr` |
| `k8s-deployment` | `update`, `rollout-restart`, `scale` |

## Admission Enforcement

When an `AgentRequest` is submitted, the gateway validates in order:

1. **Resource is governed**: At least one `GovernedResource` URI pattern matches `spec.target.uri`. If none match → reject with `ACTION_NOT_PERMITTED`.
   - **Zero-resources bypass**: if `--require-governed-resource=false` (default) and no `GovernedResource` objects exist, this check is skipped entirely.
   - **Multiple matches**: When multiple `GovernedResource` patterns match `spec.target.uri`, the gateway selects the single `GovernedResource` with the longest matching URI pattern (most-specific match). If multiple patterns have the same length, selection is stable-sorted by `metadata.name` (alphabetically) and the first is chosen. This ensures deterministic, reproducible admission decisions.
   - **Permission evaluation**: Once the matching `GovernedResource` is selected, only that resource's `permittedActions` and `permittedAgents` are evaluated. There is no union or aggregation across multiple matches — the single most-specific `GovernedResource` is authoritative.

2. **Agent identity is authoritative**: The caller's identity parsed from the OIDC token (via `--oidc-identity-claim`) is the sole source of truth for `AgentRequest.spec.agentIdentity`. The gateway enforces this invariant:
   - When an `AgentRequest` is created, the gateway overwrites `spec.agentIdentity` with the parsed OIDC identity claim, ignoring any client-supplied value.
   - When validating an existing `AgentRequest`, the gateway verifies that `spec.agentIdentity` exactly matches the caller's OIDC identity. If they differ → reject with `IDENTITY_INVALID`.
   - This prevents identity spoofing: `spec.agentIdentity` cannot drift from the authenticated caller identity.

3. **Agent is permitted**: The caller's identity (from step 2) is in the matching `GovernedResource`'s `permittedAgents` list (or `permittedAgents` is empty, meaning any authenticated agent is allowed). If not → reject with `IDENTITY_INVALID`.

4. **Action is permitted**: `spec.action` is in `permittedActions` for the matching `GovernedResource` (selected in step 1). If not → reject with `ACTION_NOT_PERMITTED`.

These checks happen before `SafetyPolicy` evaluation — they are hard gates, not policy rules. A `SafetyPolicy` can further restrict what is allowed within a `GovernedResource`'s envelope, but cannot expand beyond it.

## Context Fetchers

After admission, the control plane invokes the context fetcher named in the matching `GovernedResource` (selected via the deterministic most-specific pattern match described in Admission Enforcement) and writes the result to `AgentRequestStatus.ProviderContext`. This field is surfaced to reviewers alongside the agent's declared intent, so the reviewer sees both sides: what the agent declared and what AIP independently verified.

### Karpenter fetcher

Reads the target `NodePool` via the K8s client:

```json
{
  "currentLimitCPU": "100",
  "currentLimitMemory": "400Gi",
  "currentNodeCount": 47,
  "pendingPods": 12,
  "estimatedCostDeltaPerHour": "$8.40",
  "recentScalingEvents": [
    {"time": "2026-04-05T02:14:00Z", "direction": "up", "delta": 5}
  ]
}
```

Reviewer sees: *"Agent wants to raise the CPU limit from 100→150. Right now: 47 nodes, 12 pods pending, estimated +$8.40/hr."*

### GitHub fetcher

Reads the PR draft via GitHub API (token from a cluster Secret):

```json
{
  "title": "chore: bump node image to 1.31",
  "filesChanged": 3,
  "linesAdded": 12,
  "linesRemoved": 8,
  "codeowners": ["@platform-team"],
  "ciStatus": "pending"
}
```

### k8s-deployment fetcher

The existing `ControlPlaneVerification` logic — ready replicas, active endpoints, downstream services. Already implemented; this fetcher wraps it under the new model.

## AgentRequestStatus extension

`ControlPlaneVerification` is currently hardcoded for K8s Deployments. To support arbitrary resource types, add:

```go
// ProviderContext holds live resource state fetched by the context fetcher
// named in the matching GovernedResource. Schema is fetcher-specific.
// +optional
ProviderContext *apiextensionsv1.JSON `json:"providerContext,omitempty"`
```

`ControlPlaneVerification` is retained for backward compatibility; new fetchers write to `ProviderContext`.

## Trust Boundaries

```
Platform Engineering (Admin + Reviewer)
  ├── Creates GovernedResource   — what resource types agents may touch
  ├── Creates SafetyPolicy       — what requires approval within that envelope
  └── Approves/denies AgentRequest — runtime decisions

App Team
  ├── Builds and operates their agent
  └── Configures agent identity and SafetyPolicy within GovernedResource bounds

Agent (runtime identity)
  └── Submits AgentRequest only
      Cannot create GovernedResource, SafetyPolicy, or approve requests
```

## GovernedResource Status (Milestone 2)

In milestone 1 the CRD has spec only. Operational visibility is deferred to milestone 2, when context fetchers are added:

```go
type GovernedResourceStatus struct {
    // Conditions surfaces fetcher health and last-match events.
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    // LastMatchedTime is the most recent time an AgentRequest matched this resource.
    // +optional
    LastMatchedTime *metav1.Time `json:"lastMatchedTime,omitempty"`
}
```

## Implementation Sequence

| Step | What | Notes |
|---|---|---|
| 1 | Add `GovernedResource` CRD | `api/v1alpha1/governedresource_types.go`; cluster-scoped, spec only |
| 2 | Add `--admin-subjects` / `--admin-groups` flags to gateway | Mirrors `--agent-subjects` / `--agent-groups` pattern; admins may create GovernedResource and SafetyPolicy |
| 3 | Admission check in gateway | URI glob match (`path.Match`) → agent identity → action; `--require-governed-resource` bypass; most-specific-match with alphabetical tiebreak |
| 4 | Add `ProviderContext` to `AgentRequestStatus` | `*apiextensionsv1.JSON`, additive; `ControlPlaneVerification` retained |
| 5 | Karpenter context fetcher in controller | Pure K8s client, no external credentials |
| 6 | GitHub context fetcher in controller | Requires GitHub token Secret in cluster |
| 7 | Update Helm chart and docs | |

Steps 1–4 are the first milestone: hard admission gates with no context fetching yet. Steps 5–6 are the demo-worthy payoff for the blog post.