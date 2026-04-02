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

func mustMarshalJSON(t *testing.T, v any) *apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return &apiextensionsv1.JSON{Raw: raw}
}
