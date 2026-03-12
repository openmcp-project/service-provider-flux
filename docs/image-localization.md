# Image Localization for Air-Gapped Environments

This document describes how to configure custom image registries for Flux deployments in air-gapped or enterprise environments using the `values` field in the ProviderConfig.

## Overview

The Flux service provider uses the [Flux community Helm chart](https://github.com/fluxcd-community/helm-charts/tree/main/charts/flux2), which provides comprehensive configuration options for image localization. All chart options can be configured through the `spec.values` field in the ProviderConfig.

## Configuration

### Basic Structure

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-provider-config
spec:
  # Flux Helm chart from your internal registry
  chartUrl: "oci://my-registry.example.com/charts/flux2"

  # Helm values for image localization and other configurations
  values:
    helmController:
      image: my-registry.example.com/fluxcd/helm-controller
    sourceController:
      image: my-registry.example.com/fluxcd/source-controller
    kustomizeController:
      image: my-registry.example.com/fluxcd/kustomize-controller
    notificationController:
      image: my-registry.example.com/fluxcd/notification-controller
    imageAutomationController:
      image: my-registry.example.com/fluxcd/image-automation-controller
    imageReflectorController:
      image: my-registry.example.com/fluxcd/image-reflector-controller
    imagePullSecrets:
      - name: my-registry-secret
```

## Flux Controllers

The Flux Helm chart supports configuring the following controllers:

| Controller | Values Key | Default Image |
|------------|------------|---------------|
| Helm Controller | `helmController` | `ghcr.io/fluxcd/helm-controller` |
| Source Controller | `sourceController` | `ghcr.io/fluxcd/source-controller` |
| Kustomize Controller | `kustomizeController` | `ghcr.io/fluxcd/kustomize-controller` |
| Notification Controller | `notificationController` | `ghcr.io/fluxcd/notification-controller` |
| Image Automation Controller | `imageAutomationController` | `ghcr.io/fluxcd/image-automation-controller` |
| Image Reflector Controller | `imageReflectorController` | `ghcr.io/fluxcd/image-reflector-controller` |

### Controller Configuration Options

Each controller supports the following configuration:

```yaml
values:
  helmController:
    # Container image (without tag)
    image: my-registry.example.com/fluxcd/helm-controller
    # Image tag (defaults to chart appVersion)
    tag: v1.0.0
    # Image pull policy
    imagePullPolicy: IfNotPresent
    # Resource limits and requests
    resources:
      limits:
        cpu: 1000m
        memory: 1Gi
      requests:
        cpu: 100m
        memory: 64Mi
    # Additional annotations
    annotations: {}
    # Additional labels
    labels: {}
    # Node selector
    nodeSelector: {}
    # Tolerations
    tolerations: []
    # Affinity rules
    affinity: {}
```

## Examples

### Complete Air-Gapped Setup

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-airgapped
spec:
  chartUrl: "oci://registry.internal.corp/charts/flux2"
  pollInterval: "5m"
  values:
    # Image configuration for all controllers
    helmController:
      image: registry.internal.corp/fluxcd/helm-controller
    sourceController:
      image: registry.internal.corp/fluxcd/source-controller
    kustomizeController:
      image: registry.internal.corp/fluxcd/kustomize-controller
    notificationController:
      image: registry.internal.corp/fluxcd/notification-controller
    imageAutomationController:
      image: registry.internal.corp/fluxcd/image-automation-controller
    imageReflectorController:
      image: registry.internal.corp/fluxcd/image-reflector-controller

    # Image pull secrets for authentication
    imagePullSecrets:
      - name: internal-registry-credentials

    # CLI image (used for pre-flight checks)
    cli:
      image: registry.internal.corp/fluxcd/flux-cli
```

### Minimal Configuration (Single Registry)

If all your images are mirrored to a single registry with the same path structure:

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-config
spec:
  chartUrl: "oci://mirror.corp/charts/flux2"
  values:
    helmController:
      image: mirror.corp/fluxcd/helm-controller
    sourceController:
      image: mirror.corp/fluxcd/source-controller
    kustomizeController:
      image: mirror.corp/fluxcd/kustomize-controller
    notificationController:
      image: mirror.corp/fluxcd/notification-controller
    imagePullSecrets:
      - name: mirror-creds
```

### With Custom Resource Limits

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-config
spec:
  chartUrl: "oci://ghcr.io/fluxcd-community/charts/flux2"
  values:
    helmController:
      resources:
        limits:
          memory: 2Gi
        requests:
          memory: 256Mi
    sourceController:
      resources:
        limits:
          memory: 1Gi
```

### Disabling Unused Controllers

```yaml
apiVersion: flux.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: flux-minimal
spec:
  chartUrl: "oci://ghcr.io/fluxcd-community/charts/flux2"
  values:
    # Only enable controllers you need
    imageAutomationController:
      create: false
    imageReflectorController:
      create: false
    notificationController:
      create: false
```

## Mirroring Images

To mirror FluxCD images to your internal registry, you can use tools like:

- **skopeo**: `skopeo copy docker://ghcr.io/fluxcd/helm-controller:v1.0.0 docker://my-registry/fluxcd/helm-controller:v1.0.0`
- **crane**: `crane copy ghcr.io/fluxcd/helm-controller:v1.0.0 my-registry/fluxcd/helm-controller:v1.0.0`
- **docker**: `docker pull ghcr.io/fluxcd/helm-controller:v1.0.0 && docker tag ... && docker push ...`

### Required Images

For a complete Flux deployment, mirror these images:

```bash
# Core controllers
ghcr.io/fluxcd/helm-controller
ghcr.io/fluxcd/source-controller
ghcr.io/fluxcd/kustomize-controller
ghcr.io/fluxcd/notification-controller

# Image automation (if used)
ghcr.io/fluxcd/image-automation-controller
ghcr.io/fluxcd/image-reflector-controller

# CLI (for pre-flight checks)
ghcr.io/fluxcd/flux-cli
```

## Troubleshooting

### Verifying Configuration

Check the HelmRelease values on the platform cluster:

```bash
kubectl get helmrelease flux -n <tenant-namespace> -o jsonpath='{.spec.values}' | jq .
```

### Common Issues

1. **Image pull errors**: Ensure the `imagePullSecrets` reference existing secrets
2. **Wrong image path**: Verify the full image path matches your registry structure
3. **Tag mismatch**: If specifying custom tags, ensure compatibility with the chart version

### Checking Pod Images

After deployment, verify the images used:

```bash
kubectl get pods -n flux-system -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].image}{"\n"}{end}'
```

## Reference

For the complete list of available Helm values, see:
- [Flux2 Helm Chart README](https://github.com/fluxcd-community/helm-charts/tree/main/charts/flux2)
- [Flux2 Helm Chart values.yaml](https://github.com/fluxcd-community/helm-charts/blob/main/charts/flux2/values.yaml)
