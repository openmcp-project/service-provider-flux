// Copyright 2025.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package flux

import (
	"context"
	"fmt"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
	"github.com/openmcp-project/service-provider-flux/pkg/spruntime"
)

const (
	// FluxNamespace is the namespace where Flux components are deployed on the ManagedControlPlane
	FluxNamespace = "flux-system"
	// OCIRepositoryName is the name of the Flux OCIRepository resource
	OCIRepositoryName = "flux"
	// HelmReleaseName is the name of the Flux HelmRelease resource
	HelmReleaseName = "flux"
)

// ConfigureContext holds the context for configuring Flux resources.
type ConfigureContext struct {
	PlatformClient client.Client
	MCPClient      client.Client
	// SourceNamespace is the namespace where source secrets are located on the platform cluster.
	// This is typically the pod namespace where the service provider is deployed.
	SourceNamespace string
}

// Configure sets up Flux OCIRepository and HelmRelease resources on the platform cluster,
// and handles secret copying for air-gapped environments.
func Configure(platformCluster, mcpCluster ManagedCluster, namespace string, obj *apiv1alpha1.Flux, pc *apiv1alpha1.ProviderConfig, cc spruntime.ClusterContext, ctx ConfigureContext) error {
	var chartPullSecretObj ManagedObject

	// Extract image pull secrets from Helm values
	helmValues, err := ExtractHelmValues(pc.Spec.Values)
	if err != nil {
		return fmt.Errorf("failed to extract helm values: %w", err)
	}

	// Configure chart pull secret copy (platform cluster: source namespace -> tenant namespace)
	if pc.Spec.ChartPullSecret != "" {
		chartPullSecretObj = ConfigureSecretCopy(platformCluster, SecretCopyConfig{
			SourceClient: ctx.PlatformClient,
			SourceKey:    types.NamespacedName{Namespace: ctx.SourceNamespace, Name: pc.Spec.ChartPullSecret},
			TargetKey:    types.NamespacedName{Namespace: namespace, Name: pc.Spec.ChartPullSecret},
		})
	}

	// Configure image pull secrets copy from Helm values (ManagedControlPlane cluster: copy to flux-system namespace)
	imagePullSecretObjs := make([]ManagedObject, 0, len(helmValues.ImagePullSecrets))
	for _, secretRef := range helmValues.ImagePullSecrets {
		secretObj := ConfigureSecretCopy(mcpCluster, SecretCopyConfig{
			SourceClient: ctx.PlatformClient,
			SourceKey:    types.NamespacedName{Namespace: ctx.SourceNamespace, Name: secretRef.Name},
			TargetKey:    types.NamespacedName{Namespace: FluxNamespace, Name: secretRef.Name},
		})
		imagePullSecretObjs = append(imagePullSecretObjs, secretObj)
	}

	// Configure OCIRepository
	ociRepoDeps := []ManagedObject{}
	if chartPullSecretObj != nil {
		ociRepoDeps = append(ociRepoDeps, chartPullSecretObj)
	}

	ociRepo := NewManagedObject(&sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
	}, ManagedObjectContext{
		ReconcileFunc: func(_ context.Context, o client.Object) error {
			ociRepo, ok := o.(*sourcev1.OCIRepository)
			if !ok {
				return fmt.Errorf("expected *sourcev1.OCIRepository, got %T", o)
			}
			ociRepo.Spec = sourcev1.OCIRepositorySpec{
				Interval: metav1.Duration{Duration: 10 * time.Minute},
				URL:      pc.Spec.ChartURL,
				Reference: &sourcev1.OCIRepositoryRef{
					Tag: obj.Spec.Version,
				},
			}
			// Set secret reference for chart pull authentication
			if pc.Spec.ChartPullSecret != "" {
				ociRepo.Spec.SecretRef = &meta.LocalObjectReference{
					Name: pc.Spec.ChartPullSecret,
				}
			}
			return nil
		},
		DependsOn:      ociRepoDeps,
		DeletionPolicy: Delete,
		StatusFunc:     FluxStatus,
	})
	platformCluster.AddObject(ociRepo)

	// Configure HelmRelease - depends on OCIRepository and image pull secrets
	helmReleaseDeps := make([]ManagedObject, 0, 1+len(imagePullSecretObjs))
	helmReleaseDeps = append(helmReleaseDeps, ociRepo)
	helmReleaseDeps = append(helmReleaseDeps, imagePullSecretObjs...)

	helmRelease := NewManagedObject(&helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HelmReleaseName,
			Namespace: namespace,
		},
	}, ManagedObjectContext{
		ReconcileFunc: func(_ context.Context, o client.Object) error {
			helmRelease, ok := o.(*helmv2.HelmRelease)
			if !ok {
				return fmt.Errorf("expected *helmv2.HelmRelease, got %T", o)
			}
			retries := 3

			helmRelease.Spec = helmv2.HelmReleaseSpec{
				Interval: metav1.Duration{Duration: 10 * time.Minute},
				ChartRef: &helmv2.CrossNamespaceSourceReference{
					Kind: "OCIRepository",
					Name: OCIRepositoryName,
				},
				ReleaseName: HelmReleaseName,
				KubeConfig: &meta.KubeConfigReference{
					SecretRef: &meta.SecretKeyReference{
						Name: cc.MCPAccessSecretKey.Name,
						Key:  "kubeconfig",
					},
				},
				Install: &helmv2.Install{
					CRDs:            helmv2.Create,
					CreateNamespace: true,
					Remediation: &helmv2.InstallRemediation{
						Retries: retries,
					},
				},
				Upgrade: &helmv2.Upgrade{
					CRDs: helmv2.CreateReplace,
					Remediation: &helmv2.UpgradeRemediation{
						Retries:  retries,
						Strategy: func() *helmv2.RemediationStrategy { s := helmv2.RollbackRemediationStrategy; return &s }(),
					},
				},
				Uninstall: &helmv2.Uninstall{
					KeepHistory: false,
					Timeout:     &metav1.Duration{Duration: 5 * time.Minute},
				},
				TargetNamespace:  FluxNamespace,
				StorageNamespace: FluxNamespace,
			}

			// Pass through user-provided Helm values directly
			if pc.Spec.Values != nil {
				helmRelease.Spec.Values = pc.Spec.Values
			}

			return nil
		},
		DependsOn:      helmReleaseDeps,
		DeletionPolicy: Delete,
		StatusFunc:     FluxStatus,
	})
	platformCluster.AddObject(helmRelease)

	return nil
}

// FluxStatus indicates whether the given Flux object is in phase terminating, pending or ready.
func FluxStatus(o client.Object, rl apiv1alpha1.ResourceLocation) Status { // nolint:revive
	fluxObject, ok := o.(conditions.Getter)
	if !ok {
		return Status{
			Phase:    apiv1alpha1.Unknown,
			Message:  fmt.Sprintf("Object %T does not implement conditions.Getter", o),
			Location: rl,
		}
	}
	if !o.GetDeletionTimestamp().IsZero() {
		return Status{
			Phase:    apiv1alpha1.Terminating,
			Message:  "Resource is terminating.",
			Location: rl,
		}
	}
	if conditions.IsReady(fluxObject) {
		return Status{
			Phase:    apiv1alpha1.Ready,
			Message:  "Resource is ready",
			Location: rl,
		}
	}
	return Status{
		Phase:    apiv1alpha1.Pending,
		Message:  "Resource is not ready",
		Location: rl,
	}
}
