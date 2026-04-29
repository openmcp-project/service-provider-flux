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

package v1alpha1

import (
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderConfigSpec defines the desired state of ProviderConfig
type ProviderConfigSpec struct {
	// Versions specify the valid inputs for the Flux.Spec.Version field.
	// +required
	Versions []FluxVersion `json:"versions"`

	// PollInterval determines how often to reconcile resources to prevent drift.
	// +optional
	// +kubebuilder:default:="1m"
	// +kubebuilder:validation:Format=duration
	PollInterval *metav1.Duration `json:"pollInterval,omitempty"`
}

// FluxVersion defines a version of Flux that can be installed
type FluxVersion struct {
	// Version is the Flux version to install.
	// This version is compared with Flux.Spec.Version to define available versions
	// and the deployment artifacts of a version.
	// +required
	Version string `json:"version"`

	// ChartVersion is the version of the Helm chart to install
	// +required
	CharVersion string `json:"chartVersion"`

	// ChartURL is the OCI registry URL for the Flux Helm chart.
	// +optional
	// +kubebuilder:default="oci://ghcr.io/fluxcd-community/charts/flux2"
	ChartURL *string `json:"chartUrl,omitempty"`

	// ChartPullSecret is the name of a Secret in the service provider's namespace
	// containing credentials to pull the Helm chart from a private OCI registry.
	// The secret will be copied to the tenant namespace on the platform cluster
	// and referenced by the OCIRepository.
	// The secret must be of type kubernetes.io/dockerconfigjson.
	// +optional
	ChartPullSecret string `json:"chartPullSecret,omitempty"`

	// Values contains Helm values to override defaults for the Flux deployment.
	// This field supports all configuration options from the Flux community Helm chart.
	// See https://github.com/fluxcd-community/helm-charts/tree/main/charts/flux2 for available options.
	//
	// Image pull secrets for Flux controllers should be specified via values.imagePullSecrets.
	// Any secrets referenced in imagePullSecrets will be automatically copied from the service
	// provider's namespace to the flux-system namespace on the ManagedControlPlane.
	//
	// Common use cases include:
	// - Image localization for air-gapped environments
	// - Resource limits and requests
	// - Controller-specific configurations
	//
	// Example for air-gapped environments:
	//   values:
	//     imagePullSecrets:
	//       - name: my-registry-credentials
	//     helmController:
	//       image: my-registry.example.com/fluxcd/helm-controller
	//     sourceController:
	//       image: my-registry.example.com/fluxcd/source-controller
	//
	// +optional
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
}

// ProviderConfigStatus defines the observed state of ProviderConfig.
type ProviderConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the ProviderConfig resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ProviderConfig is the Schema for the providerconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
type ProviderConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ProviderConfig
	// +required
	Spec ProviderConfigSpec `json:"spec"`

	// status defines the observed state of ProviderConfig
	// +optional
	Status ProviderConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ProviderConfigList contains a list of ProviderConfig
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
}

// PollInterval returns the poll interval duration from the spec.
func (o *ProviderConfig) PollInterval() time.Duration {
	return o.Spec.PollInterval.Duration
}
