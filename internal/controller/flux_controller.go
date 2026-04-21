/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
	"github.com/openmcp-project/service-provider-flux/pkg/flux"
	"github.com/openmcp-project/service-provider-flux/pkg/spruntime"
)

// FluxReconciler reconciles a Flux object
type FluxReconciler struct {
	// OnboardingCluster is the cluster where this controller watches Flux resources and reacts to their changes.
	OnboardingCluster *clusters.Cluster
	// PlatformCluster is the cluster where this controller is deployed and configured.
	PlatformCluster *clusters.Cluster
	// PodNamespace is the namespace where this controller is deployed in.
	PodNamespace string
}

// CreateOrUpdate is called on every add or update event
func (r *FluxReconciler) CreateOrUpdate(ctx context.Context, obj *apiv1alpha1.Flux, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusProgressing(obj, "Reconciling", "Reconcile in progress")
	mgr, err := r.createObjectManager(obj, pc, clusters)
	if err != nil {
		spruntime.StatusProgressing(obj, "ReconcileError", err.Error())
		return ctrl.Result{}, err
	}
	results := mgr.Apply(ctx)
	managedResources, resultContainsErrors := resultsToResources(ctx, results)
	obj.Status.Resources = managedResources
	if allResourcesReady(managedResources) {
		spruntime.StatusReady(obj)
	}
	if resultContainsErrors {
		resultWithErrors := errors.New("resources contain reconcile errors")
		spruntime.StatusProgressing(obj, "ReconcileError", resultWithErrors.Error())
		return ctrl.Result{}, resultWithErrors
	}
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *FluxReconciler) Delete(ctx context.Context, obj *apiv1alpha1.Flux, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusTerminating(obj)
	mgr, err := r.createObjectManager(obj, pc, clusters)
	if err != nil {
		spruntime.StatusProgressing(obj, "ReconcileError", err.Error())
		return ctrl.Result{}, err
	}
	results := mgr.Delete(ctx)
	managedResources, resultContainsErrors := resultsToResources(ctx, results)
	obj.Status.Resources = managedResources
	if flux.AllDeleted(results) {
		return ctrl.Result{}, nil
	}
	if resultContainsErrors {
		resultWithErrors := errors.New("resources contain reconcile errors")
		spruntime.StatusProgressing(obj, "ReconcileError", resultWithErrors.Error())
		return ctrl.Result{}, resultWithErrors
	}
	return ctrl.Result{
		RequeueAfter: time.Second * 5,
	}, nil
}

func (r *FluxReconciler) createObjectManager(obj *apiv1alpha1.Flux, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (flux.Manager, error) {
	tenantNamespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to determine tenant namespace for Flux deployment: %w", err)
	}

	// Extract helm values to determine namespace and image pull secrets
	helmValues, err := flux.ExtractHelmValues(pc.Spec.Values)
	if err != nil {
		return nil, fmt.Errorf("failed to extract helm values: %w", err)
	}

	// Create managed clusters
	platformCluster := flux.NewManagedCluster(r.PlatformCluster, r.PlatformCluster.RESTConfig(), tenantNamespace, flux.PlatformCluster)

	// Support namespace override from Helm values
	fluxNamespace := flux.DefaultFluxNamespace
	if helmValues.NamespaceOverride != "" {
		fluxNamespace = helmValues.NamespaceOverride
	}
	mcpCluster := flux.NewManagedCluster(clusters.MCPCluster, clusters.MCPCluster.RESTConfig(), fluxNamespace, flux.ManagedControlPlane)

	// Sync image pull secrets from platform cluster to MCP
	flux.ManagePullSecrets(mcpCluster, helmValues.ImagePullSecrets, flux.SecretCopyConfig{
		SourceClient:    r.PlatformCluster.Client(),
		SourceNamespace: r.PodNamespace,
		TargetNamespace: fluxNamespace,
	})

	// Sync chart pull secret within platform cluster from pod namespace to tenant namespace
	var prefixedChartPullSecret string
	if pc.Spec.ChartPullSecret != "" {
		prefixedChartPullSecret, err = flux.PrefixSecretName(pc.Spec.ChartPullSecret)
		if err != nil {
			return nil, fmt.Errorf("error generating secret name: %w", err)
		}
		flux.ManagePullSecrets(platformCluster, []corev1.LocalObjectReference{
			{Name: pc.Spec.ChartPullSecret},
		}, flux.SecretCopyConfig{
			SourceClient:    r.PlatformCluster.Client(),
			SourceNamespace: r.PodNamespace,
			TargetNamespace: tenantNamespace,
			TargetName:      prefixedChartPullSecret,
		})
	}

	// Configure Flux resources (OCIRepository and HelmRelease)
	flux.ManageFluxResources(flux.ManageFluxResourcesParams{
		Cluster:             platformCluster,
		MCPNamespace:        fluxNamespace,
		ChartPullSecretName: prefixedChartPullSecret,
		Obj:                 obj,
		ProviderConfig:      pc,
		ClusterContext:      clusters,
	})

	// Create manager and add clusters
	mgr := flux.NewManager()
	mgr.AddCluster(mcpCluster)
	mgr.AddCluster(platformCluster)

	return mgr, nil
}

func resultsToResources(ctx context.Context, results []flux.Result) ([]apiv1alpha1.ManagedResource, bool) {
	l := log.FromContext(ctx)
	containsError := false
	resources := make([]apiv1alpha1.ManagedResource, 0, len(results))
	for _, res := range results {
		obj := res.Object.GetObject()
		status := res.Object.GetStatus(apiv1alpha1.ResourceLocation(res.Cluster.GetClusterType()))
		resources = append(resources, apiv1alpha1.ManagedResource{
			TypedObjectReference: corev1.TypedObjectReference{
				Kind:      reflect.TypeOf(obj).Elem().Name(),
				Name:      obj.GetName(),
				Namespace: nilIfEmptyString(obj.GetNamespace()),
			},
			Phase:    status.Phase,
			Message:  status.Message,
			Location: status.Location,
		})
		if res.Error != nil {
			containsError = true
			l.Error(res.Error, "objectID", flux.ObjectID(obj))
		}
	}
	return resources, containsError
}

func nilIfEmptyString(str string) *string {
	if str == "" {
		return nil
	}
	return ptr.To(str)
}

func allResourcesReady(resources []apiv1alpha1.ManagedResource) bool {
	for _, res := range resources {
		if res.Phase != apiv1alpha1.Ready {
			return false
		}
	}
	return true
}
