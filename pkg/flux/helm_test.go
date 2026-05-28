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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestExtractHelmValues(t *testing.T) {
	tests := []struct {
		name       string
		values     *apiextensionsv1.JSON
		wantErr    bool
		checkValue func(t *testing.T, helmValues *HelmValues)
	}{
		{
			name:   "nil values returns empty HelmValues",
			values: nil,
			checkValue: func(t *testing.T, helmValues *HelmValues) {
				assert.Empty(t, helmValues.ImagePullSecrets)
				assert.Empty(t, helmValues.NamespaceOverride)
			},
		},
		{
			name:   "empty raw values returns empty HelmValues",
			values: &apiextensionsv1.JSON{Raw: []byte{}},
			checkValue: func(t *testing.T, helmValues *HelmValues) {
				assert.Empty(t, helmValues.ImagePullSecrets)
				assert.Empty(t, helmValues.NamespaceOverride)
			},
		},
		{
			name: "extracts imagePullSecrets",
			values: mustMarshalJSON(t, map[string]any{
				"imagePullSecrets": []map[string]any{
					{"name": "secret-a"},
					{"name": "secret-b"},
				},
			}),
			checkValue: func(t *testing.T, helmValues *HelmValues) {
				require.Len(t, helmValues.ImagePullSecrets, 2)
				assert.Equal(t, "secret-a", helmValues.ImagePullSecrets[0].Name)
				assert.Equal(t, "secret-b", helmValues.ImagePullSecrets[1].Name)
			},
		},
		{
			name: "extracts namespaceOverride",
			values: mustMarshalJSON(t, map[string]any{
				"namespaceOverride": "custom-flux-ns",
			}),
			checkValue: func(t *testing.T, helmValues *HelmValues) {
				assert.Equal(t, "custom-flux-ns", helmValues.NamespaceOverride)
				assert.Empty(t, helmValues.ImagePullSecrets)
			},
		},
		{
			name: "extracts both imagePullSecrets and namespaceOverride",
			values: mustMarshalJSON(t, map[string]any{
				"namespaceOverride": "my-flux-ns",
				"imagePullSecrets": []map[string]any{
					{"name": "my-secret"},
				},
			}),
			checkValue: func(t *testing.T, helmValues *HelmValues) {
				assert.Equal(t, "my-flux-ns", helmValues.NamespaceOverride)
				require.Len(t, helmValues.ImagePullSecrets, 1)
				assert.Equal(t, "my-secret", helmValues.ImagePullSecrets[0].Name)
			},
		},
		{
			name: "ignores unrecognized values",
			values: mustMarshalJSON(t, map[string]any{
				"helmController": map[string]any{
					"image": "custom-image",
				},
				"sourceController": map[string]any{
					"resources": map[string]any{
						"limits": map[string]any{
							"memory": "256Mi",
						},
					},
				},
			}),
			checkValue: func(t *testing.T, helmValues *HelmValues) {
				assert.Empty(t, helmValues.ImagePullSecrets)
				assert.Empty(t, helmValues.NamespaceOverride)
			},
		},
		{
			name:    "invalid JSON returns error",
			values:  &apiextensionsv1.JSON{Raw: []byte("invalid json")},
			wantErr: true,
		},
		{
			name:    "malformed JSON returns error",
			values:  &apiextensionsv1.JSON{Raw: []byte(`{"namespaceOverride": }`)},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractHelmValues(tt.values)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			if tt.checkValue != nil {
				tt.checkValue(t, result)
			}
		})
	}
}

func TestAddCaToHelmValues(t *testing.T) {
	const (
		volumeName = "sp-flux-custom-ca"
		secretName = "custom-ca-secret"
		secretKey  = "ca.crt"
		secretPath = "sp-flux-custom-ca.crt"
		mountPath  = "/etc/ssl/certs/sp-flux-custom-ca.crt"
		subPath    = "sp-flux-custom-ca.crt"
	)

	expectedCaVolume := corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items: []corev1.KeyToPath{{
					Key:  secretKey,
					Path: secretPath,
				}},
			},
		},
	}
	expectedCaVolumeMount := corev1.VolumeMount{
		Name:      volumeName,
		ReadOnly:  true,
		MountPath: mountPath,
		SubPath:   subPath,
	}

	tests := []struct {
		name       string
		values     *apiextensionsv1.JSON
		wantErr    string
		checkValue func(t *testing.T, out *apiextensionsv1.JSON)
	}{
		{
			name:   "Adds SourceController Volumes and VolumeMounts when no helm values are set",
			values: nil,
			checkValue: func(t *testing.T, out *apiextensionsv1.JSON) {
				require.NotNil(t, out)

				expected := buildHelmValues(t,
					withSourceControllerVolumes(expectedCaVolume),
					withSourceControllerVolumeMounts(expectedCaVolumeMount),
				)

				assert.JSONEq(t, string(expected.Raw), string(out.Raw))
			},
		},
		{
			name: "Preserves existing helm values and adds CA entries",
			values: buildHelmValues(t,
				withRootField("namespace", "other-namespace"),
				withSourceControllerField(
					"resources", map[string]any{
						"limits": map[string]any{
							"memory": "256Mi",
						},
					}),
				withSourceControllerVolumes(corev1.Volume{Name: "existing-volume"}),
				withSourceControllerVolumeMounts(
					corev1.VolumeMount{Name: "existing-volume", MountPath: "/tmp/existing"}),
			),
			checkValue: func(t *testing.T, out *apiextensionsv1.JSON) {
				require.NotNil(t, out)

				expected := buildHelmValues(t,
					withRootField("namespace", "other-namespace"),
					withSourceControllerField("resources", map[string]any{
						"limits": map[string]any{"memory": "256Mi"},
					}),
					withSourceControllerVolumes(
						corev1.Volume{Name: "existing-volume"},
						expectedCaVolume,
					),
					withSourceControllerVolumeMounts(
						corev1.VolumeMount{Name: "existing-volume", MountPath: "/tmp/existing"},
						expectedCaVolumeMount,
					),
				)
				assert.JSONEq(t, string(expected.Raw), string(out.Raw))
			},
		},
		{
			name: "Removes VolumeMounts with same name and/or same MountPath",
			values: buildHelmValues(t,
				withSourceControllerVolumeMounts(
					corev1.VolumeMount{Name: "volume1", MountPath: "/tmp/volume1"},
					corev1.VolumeMount{Name: "volume2", MountPath: mountPath},
					corev1.VolumeMount{Name: volumeName, MountPath: "/tmp/existing"},
					corev1.VolumeMount{Name: volumeName, MountPath: mountPath},
				),
			),
			checkValue: func(t *testing.T, out *apiextensionsv1.JSON) {
				require.NotNil(t, out)

				expected := buildHelmValues(t,
					withSourceControllerVolumes(expectedCaVolume),
					withSourceControllerVolumeMounts(
						corev1.VolumeMount{Name: "volume1", MountPath: "/tmp/volume1"},
						expectedCaVolumeMount,
					),
				)

				assert.JSONEq(t, string(expected.Raw), string(out.Raw))
			},
		},
		{
			name: "Removes Volumes with same name",
			values: buildHelmValues(t,
				withSourceControllerVolumes(
					corev1.Volume{Name: volumeName},
					corev1.Volume{Name: "volume1"},
				),
			),
			checkValue: func(t *testing.T, out *apiextensionsv1.JSON) {
				require.NotNil(t, out)

				expected := buildHelmValues(t,
					withSourceControllerVolumes(
						corev1.Volume{Name: "volume1"},
						expectedCaVolume),
					withSourceControllerVolumeMounts(
						expectedCaVolumeMount,
					),
				)
				assert.JSONEq(t, string(expected.Raw), string(out.Raw))
			},
		},
		{
			name:    "Returns error for invalid root json",
			values:  &apiextensionsv1.JSON{Raw: []byte("not-json")},
			wantErr: "failed to unmarshal helm values",
		},
		{
			name: "returns error for invalid sourceController json",
			values: mustMarshalJSON(t, map[string]any{
				"sourceController": "not-an-object",
			}),
			wantErr: "failed to unmarshal sourceController",
		},
		{
			name: "returns error for invalid sourceController.volumes json",
			values: mustMarshalJSON(t, map[string]any{
				"sourceController": map[string]any{
					"volumes": "not-a-list",
				},
			}),
			wantErr: "failed to unmarshal sourceController.volumes",
		},
		{
			name: "returns error for invalid sourceController.volumeMounts json",
			values: mustMarshalJSON(t, map[string]any{
				"sourceController": map[string]any{
					"volumeMounts": "not-a-list",
				},
			}),
			wantErr: "failed to unmarshal sourceController.volumeMounts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := AddCaToHelmValues(tt.values, secretName)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, out)
			if tt.checkValue != nil {
				tt.checkValue(t, out)
			}
		})
	}
}

type helmValues struct {
	root             map[string]any
	sourceController map[string]any
}

type helmValuesOption func(*helmValues)

func buildHelmValues(t *testing.T, opts ...helmValuesOption) *apiextensionsv1.JSON {
	t.Helper()

	builder := &helmValues{
		root:             map[string]any{},
		sourceController: map[string]any{},
	}

	for _, opt := range opts {
		opt(builder)
	}

	if len(builder.sourceController) > 0 {
		builder.root["sourceController"] = builder.sourceController
	}

	return mustMarshalJSON(t, builder.root)
}

func withRootField(key string, value any) helmValuesOption {
	return func(builder *helmValues) {
		builder.root[key] = value
	}
}

func withSourceControllerField(key string, value any) helmValuesOption {
	return func(builder *helmValues) {
		builder.sourceController[key] = value
	}
}

func withSourceControllerVolumes(volumes ...corev1.Volume) helmValuesOption {
	return func(builder *helmValues) {
		builder.sourceController["volumes"] = volumes
	}
}

func withSourceControllerVolumeMounts(volumeMounts ...corev1.VolumeMount) helmValuesOption {
	return func(builder *helmValues) {
		builder.sourceController["volumeMounts"] = volumeMounts
	}
}

func mustMarshalJSON(t *testing.T, v any) *apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return &apiextensionsv1.JSON{Raw: raw}
}
