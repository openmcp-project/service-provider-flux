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
	"encoding/json"
	"errors"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
)

func TestMergeImagePullSecrets(t *testing.T) {
	tests := []struct {
		name          string
		specSecrets   []string
		valuesSecrets any
		expected      []map[string]string
	}{
		{
			name:          "spec secrets only",
			specSecrets:   []string{"secret-a", "secret-b"},
			valuesSecrets: nil,
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
		{
			name:        "values secrets only (spec empty)",
			specSecrets: []string{},
			valuesSecrets: []any{
				map[string]any{"name": "secret-x"},
			},
			expected: nil, // function is only called when len(specSecrets) > 0
		},
		{
			name:        "merge spec and values secrets",
			specSecrets: []string{"secret-a"},
			valuesSecrets: []any{
				map[string]any{"name": "secret-b"},
			},
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
		{
			name:        "deduplicate secrets",
			specSecrets: []string{"secret-a", "secret-b"},
			valuesSecrets: []any{
				map[string]any{"name": "secret-b"},
				map[string]any{"name": "secret-c"},
			},
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
				{"name": "secret-c"},
			},
		},
		{
			name:        "deduplicate within spec secrets",
			specSecrets: []string{"secret-a", "secret-a", "secret-b"},
			valuesSecrets: nil,
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
		{
			name:          "invalid values type (not a slice)",
			specSecrets:   []string{"secret-a"},
			valuesSecrets: "invalid",
			expected: []map[string]string{
				{"name": "secret-a"},
			},
		},
		{
			name:        "invalid item in values (not a map)",
			specSecrets: []string{"secret-a"},
			valuesSecrets: []any{
				"invalid",
				map[string]any{"name": "secret-b"},
			},
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
		{
			name:        "missing name field in values item",
			specSecrets: []string{"secret-a"},
			valuesSecrets: []any{
				map[string]any{"other": "value"},
				map[string]any{"name": "secret-b"},
			},
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
		{
			name:        "empty name in values item",
			specSecrets: []string{"secret-a"},
			valuesSecrets: []any{
				map[string]any{"name": ""},
				map[string]any{"name": "secret-b"},
			},
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
		{
			name:        "name is not a string in values item",
			specSecrets: []string{"secret-a"},
			valuesSecrets: []any{
				map[string]any{"name": 123},
				map[string]any{"name": "secret-b"},
			},
			expected: []map[string]string{
				{"name": "secret-a"},
				{"name": "secret-b"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip the "values secrets only" case since the function
			// is only called when spec secrets exist
			if len(tt.specSecrets) == 0 {
				t.Skip("function only called when spec secrets exist")
			}

			result := mergeImagePullSecrets(tt.specSecrets, tt.valuesSecrets)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBuildHelmValues tests the buildHelmValues function
func TestBuildHelmValues(t *testing.T) {
	tests := []struct {
		name       string
		pc         *apiv1alpha1.ProviderConfig
		wantErr    bool
		wantNil    bool
		checkValue func(t *testing.T, values map[string]any)
	}{
		{
			name: "no values and no image pull secrets",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{},
			},
			wantNil: true,
		},
		{
			name: "only image pull secrets",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					ImagePullSecrets: []string{"secret-a", "secret-b"},
				},
			},
			checkValue: func(t *testing.T, values map[string]any) {
				secrets, ok := values["imagePullSecrets"].([]any)
				require.True(t, ok)
				assert.Len(t, secrets, 2)
				s0 := secrets[0].(map[string]any)
				s1 := secrets[1].(map[string]any)
				assert.Equal(t, "secret-a", s0["name"])
				assert.Equal(t, "secret-b", s1["name"])
			},
		},
		{
			name: "only user values",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Values: mustMarshalJSON(t, map[string]any{
						"helmController": map[string]any{
							"image": "custom-image",
						},
					}),
				},
			},
			checkValue: func(t *testing.T, values map[string]any) {
				hc, ok := values["helmController"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "custom-image", hc["image"])
			},
		},
		{
			name: "merge image pull secrets with user values",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					ImagePullSecrets: []string{"spec-secret"},
					Values: mustMarshalJSON(t, map[string]any{
						"imagePullSecrets": []map[string]any{
							{"name": "values-secret"},
						},
						"helmController": map[string]any{
							"image": "custom-image",
						},
					}),
				},
			},
			checkValue: func(t *testing.T, values map[string]any) {
				secrets, ok := values["imagePullSecrets"].([]any)
				require.True(t, ok)
				assert.Len(t, secrets, 2)
				s0 := secrets[0].(map[string]any)
				s1 := secrets[1].(map[string]any)
				assert.Equal(t, "spec-secret", s0["name"])
				assert.Equal(t, "values-secret", s1["name"])
				// User values should be preserved
				hc, ok := values["helmController"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "custom-image", hc["image"])
			},
		},
		{
			name: "invalid JSON in values",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Values: &apiextensionsv1.JSON{Raw: []byte("invalid json")},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildHelmValues(tt.pc)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			var values map[string]any
			err = json.Unmarshal(result.Raw, &values)
			require.NoError(t, err)
			if tt.checkValue != nil {
				tt.checkValue(t, values)
			}
		})
	}
}

// TestFluxStatus tests the FluxStatus function
func TestFluxStatus(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		rl       apiv1alpha1.ResourceLocation
		expected apiv1alpha1.InstancePhase
	}{
		{
			name: "ready HelmRelease",
			obj: &helmv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
				},
				Status: helmv2.HelmReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			rl:       apiv1alpha1.PlatformCluster,
			expected: apiv1alpha1.Ready,
		},
		{
			name: "not ready HelmRelease",
			obj: &helmv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
				},
				Status: helmv2.HelmReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			rl:       apiv1alpha1.PlatformCluster,
			expected: apiv1alpha1.Pending, // FluxStatus returns Pending when not ready
		},
		{
			name: "terminating HelmRelease",
			obj: &helmv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test",
					Namespace:         "test-ns",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{"test-finalizer"},
				},
			},
			rl:       apiv1alpha1.PlatformCluster,
			expected: apiv1alpha1.Terminating,
		},
		{
			name: "HelmRelease with no UID (not yet created)",
			obj: &helmv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
				},
			},
			rl:       apiv1alpha1.PlatformCluster,
			expected: apiv1alpha1.Pending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := FluxStatus(tt.obj, tt.rl)
			assert.Equal(t, tt.expected, status.Phase)
			assert.Equal(t, tt.rl, status.Location)
		})
	}
}

// TestSimpleStatus tests the SimpleStatus function
func TestSimpleStatus(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		rl       apiv1alpha1.ResourceLocation
		expected apiv1alpha1.InstancePhase
	}{
		{
			name: "object with UID - ready",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
					UID:       "test-uid",
				},
			},
			rl:       apiv1alpha1.ManagedControlPlane,
			expected: apiv1alpha1.Ready,
		},
		{
			name: "object without UID - pending",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
				},
			},
			rl:       apiv1alpha1.ManagedControlPlane,
			expected: apiv1alpha1.Pending,
		},
		{
			name: "object being deleted - terminating",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test",
					Namespace:         "test-ns",
					UID:               "test-uid",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{"test-finalizer"},
				},
			},
			rl:       apiv1alpha1.ManagedControlPlane,
			expected: apiv1alpha1.Terminating,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := SimpleStatus(tt.obj, tt.rl)
			assert.Equal(t, tt.expected, status.Phase)
			assert.Equal(t, tt.rl, status.Location)
		})
	}
}

// TestSecretStatus tests the SecretStatus function
func TestSecretStatus(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		rl       apiv1alpha1.ResourceLocation
		expected apiv1alpha1.InstancePhase
	}{
		{
			name: "secret with UID - ready",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
					UID:       "test-uid",
				},
			},
			rl:       apiv1alpha1.ManagedControlPlane,
			expected: apiv1alpha1.Ready,
		},
		{
			name: "secret without UID - pending",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
				},
			},
			rl:       apiv1alpha1.ManagedControlPlane,
			expected: apiv1alpha1.Pending,
		},
		{
			name: "secret being deleted - terminating",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test",
					Namespace:         "test-ns",
					UID:               "test-uid",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{"test-finalizer"},
				},
			},
			rl:       apiv1alpha1.ManagedControlPlane,
			expected: apiv1alpha1.Terminating,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := SecretStatus(tt.obj, tt.rl)
			assert.Equal(t, tt.expected, status.Phase)
			assert.Equal(t, tt.rl, status.Location)
		})
	}
}

// TestSetManagedBy tests the SetManagedBy function
func TestSetManagedBy(t *testing.T) {
	tests := []struct {
		name           string
		obj            client.Object
		existingLabels map[string]string
	}{
		{
			name: "object with no labels",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
				},
			},
			existingLabels: nil,
		},
		{
			name: "object with existing labels",
			obj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test-ns",
					Labels: map[string]string{
						"existing": "label",
					},
				},
			},
			existingLabels: map[string]string{"existing": "label"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetManagedBy(tt.obj)
			labels := tt.obj.GetLabels()
			assert.NotNil(t, labels)
			assert.Equal(t, "service-provider-flux", labels[labelManagedBy])
			// Verify existing labels are preserved
			for k, v := range tt.existingLabels {
				assert.Equal(t, v, labels[k])
			}
		})
	}
}

// TestObjectID tests the ObjectID function
func TestObjectID(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		expected string
	}{
		{
			name: "namespaced object",
			obj: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					Kind: "Secret",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: "my-ns",
				},
			},
			expected: "Secret/my-ns/my-secret",
		},
		{
			name: "cluster-scoped object",
			obj: &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					Kind: "Namespace",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-namespace",
				},
			},
			expected: "Namespace//my-namespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ObjectID(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestManagedObject tests the ManagedObject interface implementation
func TestManagedObject(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
	}

	t.Run("with all options", func(t *testing.T) {
		reconcileCalled := false
		mo := NewManagedObject(secret, ManagedObjectContext{
			ReconcileFunc: func(ctx context.Context, o client.Object) error {
				reconcileCalled = true
				return nil
			},
			DependsOn:      []ManagedObject{},
			DeletionPolicy: Orphan,
			StatusFunc:     SimpleStatus,
		})

		assert.Equal(t, secret, mo.GetObject())
		assert.Equal(t, Orphan, mo.GetDeletionPolicy())
		assert.Empty(t, mo.GetDependencies())

		err := mo.Reconcile(context.Background())
		assert.NoError(t, err)
		assert.True(t, reconcileCalled)
	})

	t.Run("with default deletion policy", func(t *testing.T) {
		mo := NewManagedObject(secret, ManagedObjectContext{})
		assert.Equal(t, Delete, mo.GetDeletionPolicy())
	})

	t.Run("with nil reconcile func", func(t *testing.T) {
		mo := NewManagedObject(secret, ManagedObjectContext{})
		err := mo.Reconcile(context.Background())
		assert.NoError(t, err)
	})

	t.Run("with nil status func", func(t *testing.T) {
		mo := NewManagedObject(secret, ManagedObjectContext{})
		status := mo.GetStatus(apiv1alpha1.PlatformCluster)
		assert.Equal(t, apiv1alpha1.Unknown, status.Phase)
	})

	t.Run("reconcile returns error", func(t *testing.T) {
		expectedErr := errors.New("reconcile failed")
		mo := NewManagedObject(secret, ManagedObjectContext{
			ReconcileFunc: func(ctx context.Context, o client.Object) error {
				return expectedErr
			},
		})
		err := mo.Reconcile(context.Background())
		assert.Equal(t, expectedErr, err)
	})
}

// TestNoOp tests the NoOp function
func TestNoOp(t *testing.T) {
	secret := &corev1.Secret{}
	err := NoOp(context.Background(), secret)
	assert.NoError(t, err)
}

// TestAllDeleted tests the AllDeleted function
func TestAllDeleted(t *testing.T) {
	tests := []struct {
		name     string
		results  []Result
		expected bool
	}{
		{
			name:     "empty results",
			results:  []Result{},
			expected: true,
		},
		{
			name: "all deleted",
			results: []Result{
				{OperationResult: OperationResultDeleted},
				{OperationResult: OperationResultDeleted},
			},
			expected: true,
		},
		{
			name: "some not deleted",
			results: []Result{
				{OperationResult: OperationResultDeleted},
				{OperationResult: OperationResultDeletionRequested},
			},
			expected: false,
		},
		{
			name: "none deleted",
			results: []Result{
				{OperationResult: OperationResultDeletionRequested},
				{OperationResult: OperationResultDeletionRequested},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AllDeleted(tt.results)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestManager tests the Manager interface implementation
func TestManager(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = helmv2.AddToScheme(scheme)
	_ = sourcev1.AddToScheme(scheme)

	t.Run("Apply creates objects", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		cluster := &testManagedCluster{
			client:      fakeClient,
			clusterType: PlatformCluster,
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
		}
		mo := NewManagedObject(secret, ManagedObjectContext{
			ReconcileFunc: func(ctx context.Context, o client.Object) error {
				s := o.(*corev1.Secret)
				s.Data = map[string][]byte{"key": []byte("value")}
				return nil
			},
			StatusFunc: SimpleStatus,
		})
		cluster.objects = []ManagedObject{mo}

		mgr := NewManager()
		mgr.AddCluster(cluster)

		results := mgr.Apply(context.Background())
		assert.Len(t, results, 1)
		assert.NoError(t, results[0].Error)

		// Verify object was created
		var created corev1.Secret
		err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-secret", Namespace: "test-ns"}, &created)
		assert.NoError(t, err)
		assert.Equal(t, []byte("value"), created.Data["key"])
	})

	t.Run("Delete removes objects", func(t *testing.T) {
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingSecret).Build()
		cluster := &testManagedCluster{
			client:      fakeClient,
			clusterType: PlatformCluster,
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
		}
		mo := NewManagedObject(secret, ManagedObjectContext{
			DeletionPolicy: Delete,
		})
		cluster.objects = []ManagedObject{mo}

		mgr := NewManager()
		mgr.AddCluster(cluster)

		results := mgr.Delete(context.Background())
		assert.Len(t, results, 1)
		assert.NoError(t, results[0].Error)
		// First delete call returns deletionRequested since object exists
		assert.Equal(t, OperationResultDeletionRequested, results[0].OperationResult)
	})

	t.Run("Delete orphans objects with Orphan policy", func(t *testing.T) {
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingSecret).Build()
		cluster := &testManagedCluster{
			client:      fakeClient,
			clusterType: PlatformCluster,
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
		}
		mo := NewManagedObject(secret, ManagedObjectContext{
			DeletionPolicy: Orphan,
		})
		cluster.objects = []ManagedObject{mo}

		mgr := NewManager()
		mgr.AddCluster(cluster)

		results := mgr.Delete(context.Background())
		assert.Len(t, results, 1)
		assert.NoError(t, results[0].Error)
		assert.Equal(t, OperationResultOrphaned, results[0].OperationResult)

		// Verify object still exists
		var existing corev1.Secret
		err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-secret", Namespace: "test-ns"}, &existing)
		assert.NoError(t, err)
	})
}

// Helper types and functions

type testManagedCluster struct {
	client      client.Client
	objects     []ManagedObject
	clusterType ClusterType
}

func (t *testManagedCluster) AddObject(o ManagedObject) {
	t.objects = append(t.objects, o)
}

func (t *testManagedCluster) GetObjects() []ManagedObject {
	return t.objects
}

func (t *testManagedCluster) GetDefaultNamespace() string {
	return "default"
}

func (t *testManagedCluster) GetHostAndPort() (string, string) {
	return "localhost", "6443"
}

func (t *testManagedCluster) GetConfig() *rest.Config {
	return nil
}

func (t *testManagedCluster) GetClient() client.Client {
	return t.client
}

func (t *testManagedCluster) GetCluster() *clusters.Cluster {
	return nil
}

func (t *testManagedCluster) GetClusterType() ClusterType {
	return t.clusterType
}

func mustMarshalJSON(t *testing.T, v any) *apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return &apiextensionsv1.JSON{Raw: raw}
}
