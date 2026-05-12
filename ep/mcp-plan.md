# AIP GitHub MCP Integration — Implementation Plan

## Overview

AIP acts as a governance layer for MCP tool calls. Agents submit intent to the AIP gateway,
receive a scoped Ed25519 JWT on approval, then call the MCP proxy which validates the JWT and
forwards the tool call to the registered MCP server (e.g. github-mcp-server).

---

## Phase 1 — JWT Infrastructure (#183) ✅ DONE (PR #188)

- `internal/jwt/manager.go` — Ed25519 JWT minting and validation
- `JWTManager` with injectable clock, RWMutex for hot-reload, `prevPublic` grace period
- `--jwt-key-path` flag in gateway `main.go`
- cert-manager `Certificate` manifest for key rotation (90-day TTL)
- Unit tests in `internal/jwt/manager_test.go`

---

## Phase 2 — MCP Registry + GitHub MCP (#184) ⚠️ PARTIAL (PR #193)

**Done (PR #193):**
- ✅ `cmd/gateway/mcp_registry.go` — `GET /mcp-registry` reads `MCP_REGISTRY` env var
- ✅ `cmd/gateway/mcp_registry_test.go`
- ✅ `config/mcp/github-mcp-deployment.yaml` — github-mcp-server Deployment
- ✅ `config/mcp/github-mcp-service.yaml` — ClusterIP Service
- ✅ `config/mcp/networkpolicy.yaml` — restricts ingress to gateway pods only
- ✅ `config/gateway-config.yaml` — ConfigMap with MCP_REGISTRY JSON

**Gap — controller-side fetcher was listed but never created in PR #193.**
The plan listed `internal/evaluation/fetchers/github_mcp.go` but only the gateway-side registry
and K8s manifests shipped. The old `github_fetcher.go` continued hitting `api.github.com` directly.

---

## Phase 3 — MCP Proxy (#185) ✅ DONE (PR #193)

- `cmd/gateway/mcp_proxy.go` — `POST /mcp-proxy/{server}/{tool}`
  - Nil-guard on `jwtManager` → 503
  - Extract + validate `Authorization: Bearer <jwt>`
  - Look up server in registry, check tool allowlist
  - Read-only tools: always allowed; write tools: `tool.Name == jwt.Action`
  - `http.MaxBytesReader` on body
  - `bindToolName` — inject validated tool name into request body
  - 30s context timeout for upstream call
  - `strings.TrimSuffix(url, "/") + "/tools/call"` for forwarding
  - `emitMCPLog` — structured stdout log (not K8s AuditRecord yet — Phase 7)
- `cmd/gateway/mcp_proxy_test.go` + `mcp_proxy_integration_test.go`
- Route registered in `main.go`

**Known gaps (deferred):**
- No test for valid-JWT happy path at handler level
- OIDC middleware wraps the proxy route — external agents need a path exemption before production
- `emitMCPLog` writes to stdout only; full K8s AuditRecord emission is Phase 7

---

## Phase 4 — Demo Agent (#186) ⏸️ REDESIGNED

**Original plan** called for a Python/LangChain agent in a separate repo (`aip-demo-agent`)
with `nodepool.yaml` scaling. The revised plan below (from `github-mcp-demo.md`) is
concrete, follows existing Go demo patterns, and targets a two-scenario narrative that
mirrors the scaledown demo — showing AIP governs both K8s and GitHub surfaces.

### Scenario

An AI agent monitors `payment-api` and wants to scale it by modifying
`infra/payment-service.json` in a GitHub repo via a pull request.

AIP intercepts the intent, reads the current config from GitHub via the
in-cluster github-mcp-server, evaluates the 90% rule, and either blocks
or approves. If approved, the agent creates the PR using the MCP proxy
with a scoped JWT.

Same narrative as the scaledown demo (agent tries to scale something it
shouldn't) but enforcement at the GitHub PR layer, not inside Kubernetes.

### Config File

Repo: `agent-control-plane/aip-demo-infra` (to be created)

`infra/payment-service.json`:
```json
{
  "service": "payment-api",
  "maxReplicas": 5,
  "absoluteMax": 20
}
```

### Two-Scenario Flow

**Scenario A — Denied (agent proposes 19 replicas, 95% of absoluteMax):**
1. Agent submits intent: `targetURI: github://agent-control-plane/aip-demo-infra/files/main/infra/payment-service.json`, `action: create_pr`, `parameters.proposedMaxReplicas: 19`
2. Controller calls `FetchGitHubMCP` → reads file → gets `absoluteMax: 20`
3. SafetyPolicy evaluates: `19 / 20 = 0.95 > 0.9` → **DENIED**
4. No PR created

**Scenario B — Approved (agent proposes 17 replicas, 85% of absoluteMax):**
1. Agent submits intent: same target, `parameters.proposedMaxReplicas: 17`
2. Controller fetches file → `17 / 20 = 0.85 <= 0.9` → **APPROVED**
3. Gateway mints scoped JWT
4. Agent calls `POST /mcp-proxy/github/create_pull_request` with JWT
5. Real PR appears on GitHub

Both scenarios run in sequence in a single demo script.

### SafetyPolicy

```yaml
- name: replica-cap-guard
  type: StateEvaluation
  action: Deny
  message: "Proposed maxReplicas exceeds 90% of absoluteMax. Reduce the request."
  expression: >
    has(request.spec.parameters) &&
    has(request.spec.parameters.proposedMaxReplicas) &&
    target != null &&
    has(target.fileContent) &&
    has(target.fileContent.absoluteMax) &&
    double(request.spec.parameters.proposedMaxReplicas) / double(target.fileContent.absoluteMax) > 0.9
```

### Infrastructure

- Kind cluster (no cloud provider needed)
- AIP gateway + controller + dashboard running locally
- github-mcp-server deployed in Kind (`ghcr.io/github/github-mcp-server`, pinned tag)
- One GitHub PAT: read scope for policy evaluation, write scope for PR creation
- One K8s Secret (`aip-github-token`) — already consumed by github-mcp-server Deployment

### Prerequisites Before Building

1. **Fix YAML-to-JSON gap in `FetchGitHubMCP`** — the fetcher currently
   falls back to a plain string for non-JSON files. Use JSON for the demo
   file to sidestep this until the fetcher is fixed.

2. **`agent-control-plane/aip-demo-infra` repo** — needs to be created
   with `infra/payment-service.json` at the above content.

3. **`parameters` field on `AgentRequest`** — verify the proposed value
   can be passed via `request.spec.parameters` and is accessible in CEL.

### Files to Create

```
demo/github/
  run.sh                        — orchestrates both scenarios
  agent/main.go                 — ReACT loop agent (Go, like scaledown)
  k8s/resource.yaml             — GovernedResource (github:// URI pattern)
  policies/replica-cap-guard.yaml — SafetyPolicy
  README.md
```

GovernedResource:
```yaml
spec:
  uriPattern: "github://agent-control-plane/aip-demo-infra/*"
  permittedActions:
    - create_pr
  contextFetcher: github
```

### e2e Test (separate, gated)

- Build tag: `-tags github_e2e` or env var `GITHUB_E2E=true`
- Deploys real github-mcp-server in Kind
- Expects `GITHUB_PAT` env var → creates `aip-github-token` Secret
- Test A: submit with `proposedMaxReplicas: 19` → assert phase == Denied
- Test B: submit with `proposedMaxReplicas: 17` → assert phase == Approved → assert PR created on GitHub
- Cleanup: close PR, reset file to baseline

### Narrative Thread Across Demos

| Demo       | Agent tries to...           | AIP reads...              | Enforcement surface |
|------------|-----------------------------|---------------------------|---------------------|
| scaledown  | delete a live K8s service   | live cluster state (k8s)  | Kubernetes          |
| github     | scale via a GitHub PR       | config file (github MCP)  | GitHub              |

Same agent behaviour, two surfaces — AIP governs the whole system.

### Status

- [ ] `agent-control-plane/aip-demo-infra` repo created
- [ ] `demo/github/` scaffold
- [ ] GovernedResource + SafetyPolicy manifests
- [ ] ReACT loop agent (Go)
- [ ] `run.sh` (both scenarios, idempotent)
- [ ] e2e test (gated)
- [ ] README

---

## Phase 5 — Controller-Side GitHub MCP Fetcher 🔍 IN REVIEW (PR #196)

**Gap from Phase 2 was completed here.** The `github://` URI context fetcher now calls the
in-cluster GitHub MCP server instead of the GitHub REST API directly.

**Files created:**
- `internal/evaluation/fetchers/github_mcp.go` — MCP JSON-RPC HTTP client
  - Parses `github://org/repo` and `github://org/repo/files/branch/path` URIs
  - Calls `get_file_contents` MCP tool for file-path URIs
  - Calls `list_pull_requests` MCP tool for PR context (open PR count)
  - Returns JSON with `owner`, `repo`, `branch`, `filePath`, `fileContent`, `openPRCount`
- `internal/evaluation/fetchers/github_mcp_test.go` — 7 tests
  - URI parsing (basic, with file, with branch-only, invalid, missing org)
  - File contents fetch via mocked MCP server
  - Repo-only fetch via mocked MCP server
  - MCP server unreachable
  - Invalid URI error

**Files deleted:**
- `internal/evaluation/fetchers/github_fetcher.go` — replaced by MCP-based fetcher
- `internal/evaluation/fetchers/github_fetcher_test.go` — tests no longer applicable

**Wiring:**
- `internal/controller/agentrequest_controller.go` — `case "github"` now dispatches to
  `fetchers.FetchGitHubMCP` instead of `fetchers.FetchGitHub`

**Configuration:**
- The MCP server URL defaults to `http://github-mcp.aip-k8s-system.svc` (in-cluster DNS)
- The GitHub MCP server already has the `GITHUB_PERSONAL_ACCESS_TOKEN` from
  `aip-github-token` Secret (configured in `github-mcp-deployment.yaml`)
- No additional credentials needed in the controller — auth is delegated to the MCP server

---

## Phase 6 — `cmd/aip-mcp` Binary Split ❌ NOT STARTED (deferred)

Extract the MCP proxy out of the gateway binary into its own `cmd/aip-mcp` binary.

**Why deferred:** Proxy in gateway is fine for demo. This is an operational concern, not a
correctness concern. Split before Phase 7 CRDs are wired in — easier to refactor the boundary
clean than retrofit it later.

**TODO comment to add in Phase 3 code:**
```go
// TODO(Phase 6): extract handleMCPProxy into cmd/aip-mcp binary
```

---

## Phase 7 — `AgentRegistration` + `AgentPolicy` CRDs ❌ NOT STARTED (design locked, deferred)

**Design decisions (locked):**
- Platform team creates `AgentRegistration` (identity) — agents cannot self-register
- Operators create `AgentPolicy`, platform team approves — two-party authorization
- Read-only MCP tool calls pass through without an `AgentRequest`
- Write tool calls require a matching approved `AgentRequest` (enforced in proxy)
- `MCPServer` registry becomes a CRD (not env var) with constraint: policy may only reference
  servers listed in the registry
- Scaling playbook for Jira/Salesforce: same pattern — register MCP server, write policy,
  reads pass through, writes governed

**Full K8s AuditRecord emission for every proxy call** (replaces stdout-only `emitMCPLog`).

---

## Open Issues

| # | Title | Blocks |
|---|---|---|
| 184 | MCP registry + GitHub MCP integration | ⚠️ Partial — fetcher gap done in Phase 5 |
| 185 | MCP proxy with JWT validation | ✅ Done |
| 186 | GitHub PR governance demo agent | Phase 4 — redesigned (see demo/github/) |
| 64  | Capability-based enforcement / scoped tokens | Phase 7 |
| 118 | GovernedResource binding + enforcement plugins | Phase 7 |
| 34  | Git repository state context fetcher | Phase 7 |
