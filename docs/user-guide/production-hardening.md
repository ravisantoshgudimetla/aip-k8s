# Production Hardening

Before deploying AIP to a production cluster, configure authentication, role-based access, and resource limits.

## 1. Enable OIDC authentication

The recommended production setup uses an OIDC provider (Keycloak, Google, Azure AD, etc.).

```bash
helm upgrade aip-k8s charts/aip-k8s/ \
  --namespace aip-k8s-system \
  --set gateway.auth.oidcIssuerURL=https://accounts.google.com \
  --set gateway.auth.oidcAudience=aip-gateway \
  --set gateway.auth.agentSubjects=agent@example.com \
  --set gateway.auth.reviewerSubjects=reviewer1@example.com,reviewer2@example.com \
  --set gateway.auth.adminSubjects=admin@example.com
```

For Keycloak setup, see the [OIDC with Keycloak guide](../oidc-keycloak.md).

## 2. Configure role subjects

| Role | Flag | Who |
|---|---|---|
| Agent | `--set gateway.auth.agentSubjects` | Service accounts that submit requests |
| Reviewer | `--set gateway.auth.reviewerSubjects` | Humans who approve/deny requests |
| Admin | `--set gateway.auth.adminSubjects` | Platform operators |

You can also use group-based roles:

```bash
--set gateway.auth.agentGroups=sre-team \
--set gateway.auth.reviewerGroups=sre-leads
```

## 3. Enable JWT minting (for MCP proxy)

If agents will call external tools (GitHub, Jira) via the MCP proxy:

```bash
--set gateway.jwtKeyPath=/etc/aip/jwt/tls.key
```

This requires the `aip-jwt-signing-key` Secret to exist in the namespace.

## 4. Set resource limits

Review and adjust resource requests and limits for your cluster size:

```bash
--set gateway.resources.requests.cpu=100m \
--set gateway.resources.requests.memory=128Mi \
--set gateway.resources.limits.cpu=500m \
--set gateway.resources.limits.memory=256Mi
```

## 5. Enable garbage collection

For long-running clusters, enable record cleanup:

```bash
--set gc.enabled=true \
--set gc.dryRun=false \
--set gc.hardTTL=720h
```

## 6. Network policies

Consider adding NetworkPolicy resources to restrict ingress to the gateway and dashboard.

## Verification

After upgrading, verify auth is enforced:

```bash
# This should fail with 401
curl -s http://localhost:8080/agent-requests

# This should succeed with a valid token
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/agent-requests
```

## Security checklist

- [ ] OIDC issuer URL configured
- [ ] Role subjects or groups defined
- [ ] JWT key path set (if using MCP proxy)
- [ ] Resource limits configured
- [ ] Network policies applied
- [ ] Dashboard auth proxy enabled (if exposed externally)
- [ ] GC enabled for record retention
- [ ] Image tags pinned (not `:latest`)
