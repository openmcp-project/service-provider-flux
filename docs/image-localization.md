# Air-Gapped Environment Configuration

This document describes how to configure the Flux service provider for air-gapped or enterprise environments where images and Helm charts need to be pulled from private registries.

## Overview

In air-gapped environments, you typically need to:

1. **Mirror the Flux Helm chart** to your internal OCI registry
2. **Mirror Flux controller images** to your internal container registry
3. **Configure authentication** for both chart and image pulls

The Flux service provider handles this through:

- **`chartPullSecret`**: Credentials for pulling the Helm chart from a private OCI registry
- **`imagePullSecrets`**: Credentials for pulling Flux controller images from private registries
- **`values`**: Custom Helm values for image location overrides

## Secret Flow

```
Platform Cluster                         ManagedControlPlane
┌─────────────────────────────────────┐  ┌─────────────────────────┐
│  openmcp-system namespace           │  │  flux-system namespace  │
│  ┌─────────────────────────────┐    │  │  ┌───────────────────┐  │
│  │ chart-pull-secret           │────┼──┼─▶│ (not copied here) │  │
│  │ image-pull-secret-1         │────┼──┼──▶│ image-pull-secret │  │
│  │ image-pull-secret-2         │────┼──┼──▶│ image-pull-secret │  │
│  └─────────────────────────────┘    │  │  └───────────────────┘  │
└─────────────────────────────────────┘  └─────────────────────────┘
            │
            ▼
┌─────────────────────────────────────┐
│  tenant namespace (mcp--xxx)        │
│  ┌─────────────────────────────┐    │
│  │ chart-pull-secret (copy)    │◀───┘
│  │ OCIRepository (refs secret) │
│  │ HelmRelease                 │
│  └─────────────────────────────┘
└─────────────────────────────────────┘
```

## Configuration

### ProviderConfig

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-provider-config
spec:
  # Flux Helm chart location (private OCI registry)
  chartUrl: "oci://registry.internal.corp/charts/flux2"

  # Secret for authenticating to the chart OCI registry
  # Must exist in openmcp-system namespace
  # Will be copied to the tenant namespace on the platform cluster
  chartPullSecret: "chart-registry-credentials"

  # Secrets for authenticating to the image registry
  # Must exist in openmcp-system namespace
  # Will be copied to flux-system namespace on the ManagedControlPlane
  imagePullSecrets:
    - "image-registry-credentials"

  # Helm values for image location overrides
  values:
    helmController:
      image: registry.internal.corp/fluxcd/helm-controller
    sourceController:
      image: registry.internal.corp/fluxcd/source-controller
    kustomizeController:
      image: registry.internal.corp/fluxcd/kustomize-controller
    notificationController:
      image: registry.internal.corp/fluxcd/notification-controller
```

### Creating Secrets

Secrets must be created in the `openmcp-system` namespace on the platform cluster:

```bash
# Chart pull secret (for OCI registry authentication)
kubectl create secret docker-registry chart-registry-credentials \
  --namespace openmcp-system \
  --docker-server=registry.internal.corp \
  --docker-username=<username> \
  --docker-password=<password>

# Image pull secret (for container image authentication)
kubectl create secret docker-registry image-registry-credentials \
  --namespace openmcp-system \
  --docker-server=registry.internal.corp \
  --docker-username=<username> \
  --docker-password=<password>
```

## How It Works

### Chart Pull Secret

1. The secret specified in `chartPullSecret` is copied from `openmcp-system` to the tenant namespace on the platform cluster
2. The `OCIRepository` resource references this secret via `spec.secretRef`
3. The Flux Source Controller uses this secret to authenticate when pulling the Helm chart

### Image Pull Secrets

1. Secrets specified in `imagePullSecrets` are copied from `openmcp-system` on the platform cluster to `flux-system` on the ManagedControlPlane
2. The Helm values are automatically configured with `imagePullSecrets` referencing these secrets
3. The Flux controller pods use these secrets when pulling images

### Value Merging

Image pull secrets from `spec.imagePullSecrets` are automatically merged with any `imagePullSecrets` specified in `spec.values`. This allows you to use both fields together:

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: example
spec:
  # These secrets will be copied to the MCP and referenced in Helm values
  imagePullSecrets:
    - "primary-registry-credentials"

  # You can also specify additional imagePullSecrets directly in values
  values:
    imagePullSecrets:
      - name: "secondary-registry-credentials"
    helmController:
      image: registry.internal.corp/fluxcd/helm-controller
```

The resulting Helm values will contain a merged, deduplicated list:

```yaml
imagePullSecrets:
  - name: primary-registry-credentials    # from spec.imagePullSecrets
  - name: secondary-registry-credentials  # from spec.values.imagePullSecrets
helmController:
  image: registry.internal.corp/fluxcd/helm-controller
```

**Merge behavior:**
- Secrets from `spec.imagePullSecrets` are added first
- Secrets from `spec.values.imagePullSecrets` are appended
- Duplicates (by name) are automatically removed
- Other values in `spec.values` are preserved as-is

## Complete Example

### Air-Gapped Setup

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-airgapped
spec:
  chartUrl: "oci://harbor.corp.internal/charts/flux2"
  chartPullSecret: "harbor-credentials"
  imagePullSecrets:
    - "harbor-credentials"
  values:
    helmController:
      image: harbor.corp.internal/fluxcd/helm-controller
    sourceController:
      image: harbor.corp.internal/fluxcd/source-controller
    kustomizeController:
      image: harbor.corp.internal/fluxcd/kustomize-controller
    notificationController:
      image: harbor.corp.internal/fluxcd/notification-controller
    imageAutomationController:
      image: harbor.corp.internal/fluxcd/image-automation-controller
      create: false  # Disable if not needed
    imageReflectorController:
      image: harbor.corp.internal/fluxcd/image-reflector-controller
      create: false  # Disable if not needed
```

## Mirroring Images

To mirror FluxCD images to your internal registry:

```bash
# Mirror Helm chart
skopeo copy \
  docker://ghcr.io/fluxcd-community/charts/flux2:2.x.x \
  docker://harbor.corp.internal/charts/flux2:2.x.x

# Mirror controller images
for img in helm-controller source-controller kustomize-controller notification-controller; do
  skopeo copy \
    docker://ghcr.io/fluxcd/${img}:v1.x.x \
    docker://harbor.corp.internal/fluxcd/${img}:v1.x.x
done
```

## Troubleshooting

### Check Secret Copying

Verify secrets are copied to the correct namespaces:

```bash
# Platform cluster - tenant namespace
kubectl get secrets -n mcp--<tenant-id> | grep -E "chart|image"

# ManagedControlPlane - flux-system namespace
kubectl get secrets -n flux-system | grep -E "image"
```

### Check OCIRepository Secret Reference

```bash
kubectl get ocirepository flux -n mcp--<tenant-id> -o jsonpath='{.spec.secretRef}'
```

### Check HelmRelease Values

```bash
kubectl get helmrelease flux -n mcp--<tenant-id> -o jsonpath='{.spec.values}' | jq .
```

### Check Pod Image Pull Secrets

On the ManagedControlPlane:

```bash
kubectl get pods -n flux-system -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.imagePullSecrets[*].name}{"\n"}{end}'
```
