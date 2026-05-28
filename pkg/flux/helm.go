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
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// HelmValues defines the Helm values that are explicitly processed during reconciliation.
// The Flux Helm chart uses imagePullSecrets at the top level, not under global.
type HelmValues struct {
	// NamespaceOverride overrides the default flux-system namespace for the Flux deployment.
	NamespaceOverride string `json:"namespaceOverride,omitempty"`
	// ImagePullSecrets is a list of references to secrets used for pulling Flux controller images.
	// These secrets will be copied from the service provider's namespace to the flux-system namespace
	// on the ManagedControlPlane.
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// ExtractHelmValues extracts Helm values required for processing from the ProviderConfig.
func ExtractHelmValues(values *apiextensionsv1.JSON) (*HelmValues, error) {
	if values == nil || len(values.Raw) == 0 {
		return &HelmValues{}, nil
	}

	vals := &HelmValues{}
	if err := json.Unmarshal(values.Raw, vals); err != nil {
		return nil, err
	}

	return vals, nil
}

func AddCaToHelmValues(values *apiextensionsv1.JSON, secretName string) (*apiextensionsv1.JSON, error) {
	var root = map[string]json.RawMessage{}
	var sourceController = map[string]json.RawMessage{}
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount

	caVolume := corev1.Volume{
		Name: "sp-flux-custom-ca",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items: []corev1.KeyToPath{
					{
						Key:  "ca.crt",
						Path: "sp-flux-custom-ca.crt",
					},
				},
			},
		},
	}

	caVolumeMount := corev1.VolumeMount{
		Name:      "sp-flux-custom-ca",
		ReadOnly:  true,
		MountPath: "/etc/ssl/certs/sp-flux-custom-ca.crt",
		SubPath:   "sp-flux-custom-ca.crt",
	}

	if values != nil && len(values.Raw) > 0 {
		if err := json.Unmarshal(values.Raw, &root); err != nil {
			return nil, fmt.Errorf("failed to unmarshal helm values: %w", err)
		}

		if err := unmarshalIfPresent(root, "sourceController", &sourceController); err != nil {
			return nil, fmt.Errorf("failed to unmarshal sourceController: %w", err)
		}

		if err := unmarshalIfPresent(sourceController, "volumes", &volumes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal sourceController.volumes: %w", err)
		}

		if err := unmarshalIfPresent(sourceController, "volumeMounts", &volumeMounts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal sourceController.volumeMounts: %w", err)
		}
	}

	volumes = removeConflictingVolumes(volumes, caVolume)
	volumeMounts = removeConflictingVolumeMounts(volumeMounts, caVolumeMount)

	volumes = append(volumes, caVolume)
	volumeMounts = append(volumeMounts, caVolumeMount)

	volumesRaw, err := json.Marshal(volumes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sourceController.volumes: %w", err)
	}

	volumeMountsRaw, err := json.Marshal(volumeMounts)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sourceController.volumeMounts: %w", err)
	}

	sourceController["volumes"] = volumesRaw
	sourceController["volumeMounts"] = volumeMountsRaw

	sourceControllerRaw, err := json.Marshal(sourceController)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sourceController: %w", err)
	}

	root["sourceController"] = sourceControllerRaw

	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal helm values: %w", err)
	}

	return &apiextensionsv1.JSON{Raw: out}, nil
}

func removeConflictingVolumes(volumes []corev1.Volume, caVolume corev1.Volume) []corev1.Volume {
	r := []corev1.Volume{}
	for _, volume := range volumes {
		if volume.Name != caVolume.Name {
			r = append(r, volume)
		}
	}
	return r
}

func removeConflictingVolumeMounts(volumeMounts []corev1.VolumeMount, caVolumeMount corev1.VolumeMount) []corev1.VolumeMount {
	r := []corev1.VolumeMount{}
	for _, volumeMount := range volumeMounts {
		if volumeMount.MountPath != caVolumeMount.MountPath && volumeMount.Name != caVolumeMount.Name {
			r = append(r, volumeMount)
		}
	}
	return r
}

func unmarshalIfPresent(obj map[string]json.RawMessage, key string, out any) error {
	raw, ok := obj[key]
	if !ok || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid %s JSON: %w", key, err)
	}
	return nil
}
