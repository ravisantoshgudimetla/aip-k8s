# Dev Mode

The default Helm installation runs in **dev mode** — no authentication, no authorization checks, and no production safeguards.

## What dev mode means

When you run:

```bash
helm install aip-k8s charts/aip-k8s/ \
  --namespace aip-k8s-system \
  --create-namespace
```

The gateway starts with:
- `authRequired: false` — anyone can submit requests
- No OIDC issuer configured
- No agent/reviewer/admin subjects required
- No JWT minting enabled
- No proxy header authentication

This is intentional. Dev mode lets you explore AIP without setting up identity providers.

## Accessing the services

The gateway and dashboard are exposed inside the cluster only. To access them from your local machine:

```bash
# Gateway API
kubectl port-forward -n aip-k8s-system svc/aip-k8s-gateway 8080:8080

# Dashboard UI
kubectl port-forward -n aip-k8s-system svc/aip-k8s-dashboard 8082:8082
```

> ℹ️ Port-forward is a development convenience. It is not suitable for production.

## When to use dev mode

- Local development and testing
- Exploring the API and dashboard
- Running the scaledown demo
- CI/CD integration tests

## When NOT to use dev mode

- Production clusters
- Multi-tenant environments
- Any cluster where untrusted users have network access

## Moving to production

Before deploying to production, you must:

1. **Enable authentication** — configure OIDC or proxy-header auth
2. **Set role subjects** — define who can act as agents, reviewers, and admins
3. **Enable JWT minting** — for MCP proxy tool access
4. **Review resource limits** — ensure CPU/memory limits are appropriate

See [Production Hardening](./production-hardening.md) for step-by-step instructions.
