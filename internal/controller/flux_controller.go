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
	"fmt"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
	spruntime "github.com/openmcp-project/service-provider-flux/pkg/runtime"
)

const (
	// FluxNamespace is the namespace where Flux components are deployed
	FluxNamespace = "flux-system"

	// OCIRepositoryName is the name of the Flux OCIRepository resource
	OCIRepositoryName = "flux-helm-chart"

	// HelmReleaseName is the name of the Flux HelmRelease resource
	HelmReleaseName = "flux"
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

// createOrUpdateOCIRepository creates or updates the Flux OCIRepository
// resource that points to the Flux Helm chart in an OCI registry.
func (r *FluxReconciler) createOrUpdateOCIRepository(
	ctx context.Context,
	obj *apiv1alpha1.Flux,
	pc *apiv1alpha1.ProviderConfig,
	_ spruntime.ClusterContext,
	namespace string,
) error {
	l := logf.FromContext(ctx)

	ociRepo := &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, r.PlatformCluster.Client(), ociRepo, func() error {
		ociRepo.Spec.URL = pc.Spec.ChartURL
		ociRepo.Spec.Interval = metav1.Duration{Duration: 10 * time.Minute}

		// Set chart version from Flux object
		if obj.Spec.Version != "" {
			ociRepo.Spec.Reference = &sourcev1.OCIRepositoryRef{
				Tag: obj.Spec.Version,
			}
		}

		return nil
	})

	if err != nil {
		l.Error(err, "failed to create or update OCIRepository")
		return fmt.Errorf("failed to create OCIRepository: %w", err)
	}

	l.Info("created or updated OCIRepository on platform cluster",
		"name", OCIRepositoryName,
		"namespace", namespace)

	return nil
}

// createOrUpdateHelmRelease creates or updates the Flux HelmRelease
// resource that deploys Flux using the chart from OCIRepository.
func (r *FluxReconciler) createOrUpdateHelmRelease(
	ctx context.Context,
	_ *apiv1alpha1.Flux,
	pc *apiv1alpha1.ProviderConfig,
	clusters spruntime.ClusterContext,
	namespace string,
) error {
	l := logf.FromContext(ctx)

	helmRelease := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HelmReleaseName,
			Namespace: namespace,
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, r.PlatformCluster.Client(), helmRelease, func() error {
		helmRelease.Spec.Interval = metav1.Duration{Duration: 10 * time.Minute}
		helmRelease.Spec.Chart = &helmv2.HelmChartTemplate{
			Spec: helmv2.HelmChartTemplateSpec{
				Chart: "*", // Use latest version from OCI repo
				SourceRef: helmv2.CrossNamespaceObjectReference{
					Kind:      "OCIRepository",
					Name:      OCIRepositoryName,
					Namespace: namespace,
				},
				Interval: &metav1.Duration{Duration: 10 * time.Minute},
			},
		}

		// Configure install behavior
		retries := 3
		helmRelease.Spec.Install = &helmv2.Install{
			CRDs:            helmv2.Create,
			CreateNamespace: true,
			Remediation: &helmv2.InstallRemediation{
				Retries: retries,
			},
		}

		// Configure upgrade behavior
		remediationStrategy := helmv2.RollbackRemediationStrategy
		helmRelease.Spec.Upgrade = &helmv2.Upgrade{
			CRDs: helmv2.CreateReplace,
			Remediation: &helmv2.UpgradeRemediation{
				Retries:  retries,
				Strategy: &remediationStrategy,
			},
		}

		// Configure rollback behavior
		recreate := true
		helmRelease.Spec.Rollback = &helmv2.Rollback{
			Recreate: recreate,
		}

		// Set target namespace for Flux deployment
		helmRelease.Spec.TargetNamespace = FluxNamespace
		helmRelease.Spec.StorageNamespace = FluxNamespace

		helmRelease.Spec.KubeConfig = &meta.KubeConfigReference{
			SecretRef: &meta.SecretKeyReference{
				Name: clusters.MCPAccessSecretKey.Name,
				Key:  "kubeconfig",
			},
		}

		// Add custom Helm values if provided
		if pc.Spec.Values != nil && len(pc.Spec.Values.Raw) > 0 {
			helmRelease.Spec.Values = pc.Spec.Values
		}

		return nil
	})

	if err != nil {
		l.Error(err, "failed to create or update HelmRelease")
		return fmt.Errorf("failed to create HelmRelease: %w", err)
	}

	l.Info("created or updated HelmRelease on platform cluster",
		"name", HelmReleaseName,
		"namespace", namespace)

	return nil
}

// deleteFluxResources removes the HelmRelease and OCIRepository from Platform cluster.
func (r *FluxReconciler) deleteFluxResources(
	ctx context.Context,
	_ spruntime.ClusterContext,
	namespace string,
) error {
	l := logf.FromContext(ctx)

	// Delete HelmRelease first (triggers Helm uninstall)
	helmRelease := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HelmReleaseName,
			Namespace: namespace,
		},
	}

	if err := r.PlatformCluster.Client().Delete(ctx, helmRelease); err != nil {
		if !apierrors.IsNotFound(err) {
			l.Error(err, "failed to delete HelmRelease")
			return fmt.Errorf("failed to delete HelmRelease: %w", err)
		}
	}

	// Delete OCIRepository
	ociRepo := &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
	}

	if err := r.PlatformCluster.Client().Delete(ctx, ociRepo); err != nil {
		if !apierrors.IsNotFound(err) {
			l.Error(err, "failed to delete OCIRepository")
			return fmt.Errorf("failed to delete OCIRepository: %w", err)
		}
	}

	l.Info("deleted Flux resources from platform cluster", "namespace", namespace)
	return nil
}

// CreateOrUpdate is called on every add or update event
func (r *FluxReconciler) CreateOrUpdate(ctx context.Context, obj *apiv1alpha1.Flux, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)

	// Set status to progressing
	spruntime.StatusProgressing(obj, "Reconciling", "Setting up Flux deployment")

	// Get target namespace for Flux configuration resources
	namespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		spruntime.StatusProgressing(obj, "ConfigError", fmt.Sprintf("Failed to determine tenant config namespace: %v", err))
		return ctrl.Result{}, fmt.Errorf("failed to generate stable MCP namespace: %w", err)
	}

	// Step 1: Create or update OCIRepository
	if err := r.createOrUpdateOCIRepository(ctx, obj, pc, clusters, namespace); err != nil {
		spruntime.StatusProgressing(obj, "OCIRepositoryFailed",
			fmt.Sprintf("Failed to create OCIRepository: %v", err))
		return ctrl.Result{}, err
	}

	// Step 2: Create or update HelmRelease
	if err := r.createOrUpdateHelmRelease(ctx, obj, pc, clusters, namespace); err != nil {
		spruntime.StatusProgressing(obj, "HelmReleaseFailed",
			fmt.Sprintf("Failed to create HelmRelease: %v", err))
		return ctrl.Result{}, err
	}

	// Set status to ready
	spruntime.StatusReady(obj)

	l.Info("successfully reconciled Flux deployment",
		"namespace", namespace,
		"version", obj.Spec.Version)

	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *FluxReconciler) Delete(ctx context.Context, obj *apiv1alpha1.Flux, _ *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)

	// Set status to terminating
	spruntime.StatusTerminating(obj)

	// Get target namespace for Flux configuration resources
	namespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		spruntime.StatusProgressing(obj, "ConfigError", fmt.Sprintf("Failed to determine tenant config namespace: %v", err))
		return ctrl.Result{}, fmt.Errorf("failed to generate stable MCP namespace: %w", err)
	}

	// Delete Flux resources (HelmRelease, OCIRepository)
	if err := r.deleteFluxResources(ctx, clusters, namespace); err != nil {
		l.Error(err, "failed to delete Flux resources, will retry")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	l.Info("successfully deleted Flux deployment", "namespace", namespace)
	return ctrl.Result{}, nil
}
