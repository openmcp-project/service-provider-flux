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
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
			results, gotErr := mgr.Apply(context.Background())
			require.NoError(t, gotErr)
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
		name  string
		input string
	}{
		{"short name", "privateregcred"},
		{"long name truncated", strings.Repeat("a", 60)},
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

func Test_secretCleaner_Cleanup(t *testing.T) {
	tests := []struct {
		name            string // description of this test case
		cluster         ManagedCluster
		targetNamespace string
		secretsToKeep   []corev1.LocalObjectReference
		want            []corev1.Secret
		wantResults     bool // []results indicate individual delete error
		wantErr         bool // error indicate general errors that are not related to individual objects
	}{
		{
			name:            "only managed secrets are deleted",
			targetNamespace: "flux-system",
			cluster: createFakeCluster(createFakeClient([]client.Object{
				testSecret("a", "flux-system", true),
				testSecret("b", "flux-system", false),
			})),
			secretsToKeep: []corev1.LocalObjectReference{},
			want: []corev1.Secret{
				*testSecret("b", "flux-system", false),
			},
			wantErr: false,
		},
		{
			name:            "secrets in other namespaces are not deleted",
			targetNamespace: "openmcp-system",
			cluster: createFakeCluster(createFakeClient([]client.Object{
				testSecret("a", "flux-system", true),
				testSecret("b", "flux-system", false),
			})),
			secretsToKeep: []corev1.LocalObjectReference{},
			want: []corev1.Secret{
				*testSecret("a", "flux-system", true),
				*testSecret("b", "flux-system", false),
			},
			wantErr: false,
		},
		{
			name:            "secrets to keep are not deleted",
			targetNamespace: "flux-system",
			cluster: createFakeCluster(createFakeClient([]client.Object{
				testSecret("a", "flux-system", true),
				testSecret("b", "flux-system", false),
			})),
			secretsToKeep: []corev1.LocalObjectReference{
				{
					Name: "a",
				},
			},
			want: []corev1.Secret{
				*testSecret("a", "flux-system", true),
				*testSecret("b", "flux-system", false),
			},
			wantErr: false,
		},
		{
			name:            "error is returned when list fails",
			cluster:         createFakeCluster(listErrorClient{}),
			targetNamespace: "flux-system",
			secretsToKeep:   []corev1.LocalObjectReference{},
			want:            []corev1.Secret{},
			wantErr:         true,
		},
		{
			name: "error is returned when delete fails",
			cluster: createFakeCluster(deleteErrorClient{
				fakeSecret: *testSecret("a", "flux-system", true),
			}),
			targetNamespace: "flux-system",
			secretsToKeep:   []corev1.LocalObjectReference{},
			want:            []corev1.Secret{},
			wantErr:         false,
			wantResults:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewSecretCleaner(tt.cluster, tt.targetNamespace, tt.secretsToKeep)
			results, gotErr := c.Cleanup(context.Background())
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Cleanup() failed: %v", gotErr)
				}
				return
			}
			if len(results) > 0 {
				if !tt.wantResults {
					t.Errorf("Cleanup() failed %v", results)
				}
				return
			}
			secretList := &corev1.SecretList{}
			require.NoError(t, tt.cluster.GetClient().List(context.Background(), secretList))
			for _, gotSecret := range secretList.Items {
				assert.True(t, slices.ContainsFunc(tt.want, func(s corev1.Secret) bool {
					return s.Name == gotSecret.Name && s.Namespace == gotSecret.Namespace
				}))
			}
		})
	}
}

var _ ManagedCluster = &fakeCluster{}

type fakeCluster struct {
	managedCluster
	fakeClient client.Client
}

// GetClient implements [ManagedCluster].
func (f *fakeCluster) GetClient() client.Client {
	return f.fakeClient
}

func createFakeCluster(client client.Client) ManagedCluster {
	return &fakeCluster{
		fakeClient: client,
	}
}

func testSecret(name, namespace string, managedByFlux bool) *corev1.Secret {
	labels := map[string]string{}
	if managedByFlux {
		labels[LabelManagedBy] = labelServiceProviderFlux
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
	}
}

func createFakeClient(clusterObjects []client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	return fake.NewClientBuilder().WithObjects(clusterObjects...).WithScheme(scheme).Build()
}

type listErrorClient struct {
	client.Client
}

func (l listErrorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return errors.New("list failed")
}

type deleteErrorClient struct {
	client.Client
	fakeSecret corev1.Secret
}

func (d deleteErrorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	seclist := list.(*corev1.SecretList)
	seclist.Items = []corev1.Secret{d.fakeSecret}
	return nil
}

func (d deleteErrorClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return errors.New("delete failed")
}
