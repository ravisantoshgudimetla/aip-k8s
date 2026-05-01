# Contributing to AIP Kubernetes Control Plane

## Local Development

### Prerequisites
- `go` version v1.24.0+
- `docker` version 17.03+
- `kind` version v0.31.0+ (for local testing)
- `kubectl` version v1.11.3+

### Running Locally (Development in KIND)

You can automatically spin up a local Kubernetes cluster using `kind` and deploy the `aip-k8s` controller directly to it:

```sh
# This will:
# 1. Create a local 'aip-test' kind cluster (if it doesn't exist)
# 2. Build the 'aip-controller:test' docker image
# 3. Load the image into the cluster
# 4. Generate & apply all CRDs
# 5. Deploy the controller to the cluster
make kind-deploy IMG=aip-controller:test
```

### Build and Deploy to a Cluster

**Build and push your image:**

```sh
make docker-build docker-push IMG=<some-registry>/aip-k8s:tag
```

**Install the CRDs and deploy the Manager:**

```sh
make deploy IMG=<some-registry>/aip-k8s:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin privileges.

## Testing

This project uses `envtest` for rapid integration testing without a full cluster:

```sh
make test
```

For end-to-end tests on a Kind cluster:

```sh
make test-e2e
```

> **NOTE**: Run `make help` for more information on all potential `make` targets.

## Gateway Development

### Starting the gateway locally

```sh
# Build
make build-gateway

# Run locally (uses ~/.kube/config by default)
./bin/gateway --addr :8080
```

## All new features must conform to the core AIP specification.
