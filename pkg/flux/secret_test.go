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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openmcp-project/service-provider-flux/pkg/testutils"
)

func TestManagePullSecrets(t *testing.T) {
	sourceSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pull-secret",
			Namespace: "source-ns",
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(`{"auths":{}}`),
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	fakeCluster := testutils.CreateFakeCluster(t, "platform", sourceSecret)

	tests := []struct {
		name             string
		targetCluster    ManagedCluster
		imagePullSecrets []corev1.LocalObjectReference
		config           SecretCopyConfig
	}{
		{
			name:          "syncs secret with correct type",
			targetCluster: NewManagedCluster(fakeCluster, &rest.Config{}, "target-ns", ManagedControlPlane),
			imagePullSecrets: []corev1.LocalObjectReference{
				{Name: "test-pull-secret"},
			},
			config: SecretCopyConfig{
				SourceClient:    fakeCluster.Client(),
				SourceNamespace: "source-ns",
				TargetNamespace: "target-ns",
			},
		},
		{
			name:          "sync secret with target name adjustment",
			targetCluster: NewManagedCluster(fakeCluster, &rest.Config{}, "target-ns", ManagedControlPlane),
			imagePullSecrets: []corev1.LocalObjectReference{
				{Name: "test-pull-secret"},
			},
			config: SecretCopyConfig{
				SourceClient:    fakeCluster.Client(),
				SourceNamespace: "source-ns",
				TargetNamespace: "target-ns",
				TargetName:      fmt.Sprintf("%s%s", secretNamePrefix, "test-pull-secret"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ManagePullSecrets(tt.targetCluster, tt.imagePullSecrets, tt.config)

			// Apply managed objects
			mgr := NewManager()
			mgr.AddCluster(tt.targetCluster)
			results := mgr.Apply(context.Background())
			for _, r := range results {
				require.NoError(t, r.Error)
			}

			// Verify secret was synced with correct type
			for _, pullSecret := range tt.imagePullSecrets {
				targetSecret := &corev1.Secret{}
				targetSecretName := pullSecret.Name
				if tt.config.TargetName != "" {
					targetSecretName = tt.config.TargetName
				}
				err := fakeCluster.Client().Get(context.Background(), client.ObjectKey{
					Name:      targetSecretName,
					Namespace: tt.config.TargetNamespace,
				}, targetSecret)
				require.NoError(t, err)

				assert.Equal(t, sourceSecret.Data, targetSecret.Data)
				assert.Equal(t, corev1.SecretTypeDockerConfigJson, targetSecret.Type, "target secret should have the correct type")
			}
		})
	}
}

func TestPrefixSecretName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int // expected max length
		wantErr bool
	}{
		{"short name", "privateregcred", 22, false}, // "sp-flux-" + 14 chars
		{"long name truncated", strings.Repeat("a", 60), 63, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PrefixSecretName(tt.input)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(got, secretNamePrefix))
			assert.LessOrEqual(t, len(got), 63)
		})
	}
}
