# Flux Service Provider - Manager Pattern

This document describes the refactored architecture of the Flux service provider, which now follows the manager pattern introduced in service-provider-external-secrets.

## Architecture Overview

The service provider has been refactored to use a **Manager pattern** that provides:

1. **Abstraction**: Clean separation between resource management logic and controller concerns
2. **Dependency Management**: Proper ordering and dependency tracking between managed objects
3. **Status Tracking**: Comprehensive status reporting for each managed resource
4. **Proper Deletion**: Ensures resources are fully deleted before removing finalizers, preventing orphaned resources

## Core Components

### 1. Manager (`pkg/flux/manager.go`)

The Manager orchestrates the lifecycle of all managed Kubernetes objects across one or more clusters.

**Key features:**
- Manages multiple `ManagedCluster` instances
- Handles both create/update (`Apply`) and deletion (`Delete`) operations
- Respects dependency ordering - ensures dependencies exist before creating dependent objects
- Tracks deletion progress - waits for actual resource deletion, not just deletion request
- Supports orphaning resources (leaving them in place during deletion)

```go
type Manager interface {
    AddCluster(mc ManagedCluster)
    Apply(context.Context) []Result
    Delete(context.Context) []Result
}
```

### 2. ManagedObject (`pkg/flux/managedobject.go`)

Represents a single Kubernetes object managed by the system.

**Key features:**
- Encapsulates the object and its reconciliation logic
- Declares dependencies on other ManagedObjects
- Defines deletion policy (Delete or Orphan)
- Provides custom status reporting

```go
type ManagedObject interface {
    GetObject() client.Object
    Reconcile(ctx context.Context) error
    GetDependencies() []ManagedObject
    GetDeletionPolicy() DeletionPolicy
    GetStatus(ResourceLocation) Status
}
```

### 3. ManagedCluster (`pkg/flux/managedcluster.go`)

Represents a Kubernetes cluster that hosts managed objects.

**Key features:**
- Holds references to the cluster client and configuration
- Maintains a list of ManagedObjects to be managed in that cluster
- Provides cluster metadata (type, namespace, host/port)

```go
type ManagedCluster interface {
    AddObject(o ManagedObject)
    GetObjects() []ManagedObject
    GetClient() client.Client
    GetClusterType() ClusterType
}
```

### 4. Flux Configuration (`pkg/flux/flux.go`)

Flux-specific configuration that creates OCIRepository and HelmRelease resources.

**Key features:**
- **Uninstall Configuration**: Explicitly configures `helmRelease.Spec.Uninstall` to ensure proper cleanup
- **Dependency Declaration**: HelmRelease depends on OCIRepository
- **Flux-aware Status**: Uses Flux conditions to determine resource readiness

```go
helmRelease.Spec.Uninstall = &helmv2.Uninstall{
    KeepHistory: false,
    Timeout:     &metav1.Duration{Duration: 5 * time.Minute},
}
```

## Why This Matters: The Uninstall Fix

### The Problem

Previously, when deleting a Flux resource:
1. Controller deleted HelmRelease from platform cluster ✓
2. Controller deleted OCIRepository from platform cluster ✓
3. Controller immediately deleted cluster access (kubeconfig secret) ✗
4. Flux Helm Controller tried to run `helm uninstall` on the MCP
5. **But the kubeconfig was already gone** → uninstall failed silently
6. Result: Flux deployment remained orphaned on the MCP

### The Solution

The refactored implementation fixes this by:

1. **Explicit Uninstall Spec**: `helmRelease.Spec.Uninstall` tells Flux Helm Controller how to clean up
2. **Proper Deletion Ordering**:
   - HelmRelease deletion is requested first
   - Manager waits for actual deletion (not just deletion request)
   - Only after resources are gone does the controller remove finalizer
   - Cluster access cleanup happens after finalizer removal
3. **Status Tracking**: Real-time visibility into each resource's lifecycle phase

## Flow Diagrams

### Create/Update Flow

```
User creates Flux CR
    ↓
Controller.CreateOrUpdate()
    ↓
createObjectManager()
    ├── Creates ManagedCluster (PlatformCluster)
    ├── Calls flux.Configure()
    │   ├── Creates ManagedObject(OCIRepository)
    │   └── Creates ManagedObject(HelmRelease) ← depends on OCIRepository
    └── Returns Manager
    ↓
manager.Apply(ctx)
    ├── Reconcile OCIRepository (no dependencies)
    ├── Reconcile HelmRelease (after OCIRepository exists)
    └── Returns []Result
    ↓
Update Flux.Status.Resources with managed resource status
```

### Delete Flow

```
User deletes Flux CR
    ↓
Controller.Delete()
    ↓
createObjectManager() (same as above)
    ↓
manager.Delete(ctx)
    ├── Check HelmRelease for dependents (none)
    ├── Delete HelmRelease from platform cluster
    │   └── Flux Helm Controller sees deletion
    │       └── Runs: helm uninstall --kubeconfig=<mcp-secret> --namespace=flux-system flux
    │           └── Uses Uninstall.Timeout: 5 minutes
    ├── Wait for HelmRelease to be fully removed (OperationResult: deleted)
    ├── Delete OCIRepository from platform cluster
    ├── Wait for OCIRepository to be fully removed
    └── Returns []Result with OperationResult: deleted
    ↓
If all deleted → return ctrl.Result{} (no requeue)
If not all deleted → return ctrl.Result{RequeueAfter: 5s}
    ↓
SPReconciler removes cluster access (kubeconfig)
SPReconciler removes finalizer
```

## Key Differences from Old Implementation

| Aspect | Old Implementation | New Implementation |
|--------|-------------------|-------------------|
| **Uninstall Spec** | ❌ Missing | ✅ Explicitly configured |
| **Deletion Waiting** | ❌ Buggy check (inverted logic) | ✅ Proper `AllDeleted()` check |
| **Dependency Tracking** | ❌ Manual, error-prone | ✅ Declarative dependencies |
| **Status Reporting** | ❌ Simple phase string | ✅ Per-resource status with phase, message, location |
| **Cluster Abstraction** | ❌ Direct client calls | ✅ ManagedCluster abstraction |
| **Reusability** | ❌ Tightly coupled controller logic | ✅ Reusable manager pattern |

## Benefits

### 1. **Correctness**
- Fixes the orphaned resource bug
- Proper ordering ensures dependencies are met
- Actual deletion verification prevents premature finalization

### 2. **Observability**
- Status shows each managed resource and its current phase
- Clear visibility into what's being created/deleted
- Error reporting per resource

### 3. **Maintainability**
- Clean separation of concerns
- Testable components (Manager, ManagedObject)
- Easy to add new managed resources

### 4. **Consistency**
- Follows the same pattern as service-provider-external-secrets
- Makes it easier for developers to work across service providers

## Usage Example

```go
// Create manager
mgr := flux.NewManager()

// Create platform cluster context
platformCluster := flux.NewManagedCluster(
    r.PlatformCluster,
    r.PlatformCluster.RESTConfig(),
    tenantNamespace,
    flux.PlatformCluster,
)

// Configure Flux resources (OCIRepository + HelmRelease)
flux.Configure(platformCluster, tenantNamespace, obj, pc, clusters)

// Add cluster to manager
mgr.AddCluster(platformCluster)

// Apply resources
results := mgr.Apply(ctx)

// Check results
if flux.AllDeleted(results) {
    // All resources successfully deleted
}
```

## Testing the Fix

To verify the fix works:

1. Create a Flux resource → observe HelmRelease created with Uninstall spec
2. Delete the Flux resource → observe:
   - HelmRelease deleted from platform cluster
   - Flux deployment removed from MCP (check `kubectl get deploy -n flux-system` on MCP)
   - OCIRepository deleted from platform cluster
   - Finalizer removed only after all resources gone

The key indicator is that **no Flux resources remain on the MCP after deletion**.

## Future Enhancements

1. **Add managed control plane cluster**: Currently only manages platform cluster resources
2. **Health checks**: Add health checking for deployed Flux instance
3. **Metrics**: Expose metrics for managed resource counts and status
4. **Reconciliation retries**: Smarter backoff for failed reconciliations
