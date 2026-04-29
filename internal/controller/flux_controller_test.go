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
	"testing"
	"time"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
	"github.com/openmcp-project/service-provider-flux/pkg/flux"
	"github.com/openmcp-project/service-provider-flux/pkg/spruntime"
	"github.com/openmcp-project/service-provider-flux/pkg/testutils"
)

func TestFluxReconciler_CreateOrUpdate(t *testing.T) {
	tests := []struct {
		name            string
		obj             *apiv1alpha1.Flux
		pc              *apiv1alpha1.ProviderConfig
		clusters        spruntime.ClusterContext
		manager         flux.Manager
		want            ctrl.Result
		wantErr         bool
		wantStatusPhase string
	}{
		{
			name:     "managed objects ready -> status ready",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Ready, controllerutil.OperationResultCreated, flux.PlatformCluster, nil),
					fakeResult(apiv1alpha1.Ready, controllerutil.OperationResultCreated, flux.ManagedControlPlane, nil),
				},
			},
			want:            ctrl.Result{},
			wantStatusPhase: spruntime.StatusPhaseReady,
			wantErr:         false,
		},
		{
			name:     "managed objects not ready -> status progressing",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Progressing, controllerutil.OperationResultCreated, flux.PlatformCluster, nil),
					fakeResult(apiv1alpha1.Pending, controllerutil.OperationResultCreated, flux.ManagedControlPlane, nil),
				},
			},
			want:            ctrl.Result{},
			wantStatusPhase: spruntime.StatusPhaseProgressing,
			wantErr:         false,
		},
		{
			name:     "some objects ready, some progressing -> status progressing",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Ready, controllerutil.OperationResultCreated, flux.PlatformCluster, nil),
					fakeResult(apiv1alpha1.Progressing, controllerutil.OperationResultCreated, flux.ManagedControlPlane, nil),
				},
			},
			want:            ctrl.Result{},
			wantStatusPhase: spruntime.StatusPhaseProgressing,
			wantErr:         false,
		},
		{
			name:     "managed objects with errors -> error",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Failed, controllerutil.OperationResultCreated, flux.PlatformCluster, errors.New("reconcile error")),
				},
			},
			want:    ctrl.Result{},
			wantErr: true,
		},
		{
			name:     "empty results -> status ready",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{},
			},
			want:            ctrl.Result{},
			wantStatusPhase: spruntime.StatusPhaseReady,
			wantErr:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			onboardingCluster := testutils.CreateFakeCluster(t, "onboarding", tt.obj)
			r := &FluxReconciler{
				OnboardingCluster: onboardingCluster,
				PlatformCluster:   testutils.CreateFakeCluster(t, "platform", tt.pc),
				PodNamespace:      "openmcp-system",
			}
			// Use a test helper to inject the fake manager
			got, gotErr := createOrUpdateWithManager(r, context.Background(), tt.obj, tt.pc, tt.clusters, tt.manager)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("CreateOrUpdate() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("CreateOrUpdate() succeeded unexpectedly")
			}
			if tt.wantStatusPhase != "" {
				assert.Equal(t, tt.wantStatusPhase, tt.obj.Status.Phase)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFluxReconciler_Delete(t *testing.T) {
	tests := []struct {
		name     string
		obj      *apiv1alpha1.Flux
		pc       *apiv1alpha1.ProviderConfig
		clusters spruntime.ClusterContext
		manager  flux.Manager
		want     ctrl.Result
		wantErr  bool
	}{
		{
			name:     "all objects deleted -> no requeue",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeleted, flux.PlatformCluster, nil),
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeleted, flux.ManagedControlPlane, nil),
				},
			},
			want:    ctrl.Result{},
			wantErr: false,
		},
		{
			name:     "objects pending deletion -> requeue",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeletionRequested, flux.PlatformCluster, nil),
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeletionRequested, flux.ManagedControlPlane, nil),
				},
			},
			want: ctrl.Result{
				RequeueAfter: 5 * time.Second,
			},
			wantErr: false,
		},
		{
			name:     "some deleted, some pending -> requeue",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeleted, flux.PlatformCluster, nil),
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeletionRequested, flux.ManagedControlPlane, nil),
				},
			},
			want: ctrl.Result{
				RequeueAfter: 5 * time.Second,
			},
			wantErr: false,
		},
		{
			name:     "deletion with errors -> error",
			obj:      createFluxObj("2.4.0"),
			pc:       createProviderConfig(),
			clusters: fakeClusterContext(t),
			manager: fakeManager{
				results: []flux.Result{
					fakeResult(apiv1alpha1.Terminating, flux.OperationResultDeletionRequested, flux.PlatformCluster, errors.New("deletion error")),
				},
			},
			want:    ctrl.Result{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &FluxReconciler{
				OnboardingCluster: testutils.CreateFakeCluster(t, "onboarding", tt.obj),
				PlatformCluster:   testutils.CreateFakeCluster(t, "platform", tt.pc),
				PodNamespace:      "openmcp-system",
			}
			got, gotErr := deleteWithManager(r, context.Background(), tt.obj, tt.pc, tt.clusters, tt.manager)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Delete() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Delete() succeeded unexpectedly")
			}
			assert.Equal(t, tt.want, got)
			assert.Equal(t, spruntime.StatusPhaseTerminating, tt.obj.Status.Phase)
		})
	}
}

func TestResultsToResources(t *testing.T) {
	tests := []struct {
		name              string
		results           []flux.Result
		wantCount         int
		wantContainsError bool
	}{
		{
			name:              "empty results",
			results:           []flux.Result{},
			wantCount:         0,
			wantContainsError: false,
		},
		{
			name: "single result without error",
			results: []flux.Result{
				fakeResult(apiv1alpha1.Ready, controllerutil.OperationResultCreated, flux.PlatformCluster, nil),
			},
			wantCount:         1,
			wantContainsError: false,
		},
		{
			name: "multiple results without errors",
			results: []flux.Result{
				fakeResult(apiv1alpha1.Ready, controllerutil.OperationResultCreated, flux.PlatformCluster, nil),
				fakeResult(apiv1alpha1.Progressing, controllerutil.OperationResultUpdated, flux.ManagedControlPlane, nil),
			},
			wantCount:         2,
			wantContainsError: false,
		},
		{
			name: "results with error",
			results: []flux.Result{
				fakeResult(apiv1alpha1.Ready, controllerutil.OperationResultCreated, flux.PlatformCluster, nil),
				fakeResult(apiv1alpha1.Failed, controllerutil.OperationResultNone, flux.ManagedControlPlane, errors.New("test error")),
			},
			wantCount:         2,
			wantContainsError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources, containsError := resultsToResources(context.Background(), tt.results)
			assert.Len(t, resources, tt.wantCount)
			assert.Equal(t, tt.wantContainsError, containsError)
		})
	}
}

func TestAllResourcesReady(t *testing.T) {
	tests := []struct {
		name      string
		resources []apiv1alpha1.ManagedResource
		want      bool
	}{
		{
			name:      "empty resources",
			resources: []apiv1alpha1.ManagedResource{},
			want:      true,
		},
		{
			name: "all ready",
			resources: []apiv1alpha1.ManagedResource{
				{Phase: apiv1alpha1.Ready},
				{Phase: apiv1alpha1.Ready},
			},
			want: true,
		},
		{
			name: "one not ready",
			resources: []apiv1alpha1.ManagedResource{
				{Phase: apiv1alpha1.Ready},
				{Phase: apiv1alpha1.Progressing},
			},
			want: false,
		},
		{
			name: "all not ready",
			resources: []apiv1alpha1.ManagedResource{
				{Phase: apiv1alpha1.Pending},
				{Phase: apiv1alpha1.Progressing},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allResourcesReady(tt.resources)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNilIfEmptyString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  *string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "non-empty string",
			input: "test",
			want:  strPtr("test"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nilIfEmptyString(tt.input)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

// Helper functions

func createFluxObj(version string) *apiv1alpha1.Flux {
	return &apiv1alpha1.Flux{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-flux",
			Namespace: "test-namespace",
		},
		Spec: apiv1alpha1.FluxSpec{
			Version: version,
		},
	}
}

func createProviderConfig() *apiv1alpha1.ProviderConfig {
	chartURL := "oci://ghcr.io/fluxcd-community/charts/flux2"
	return &apiv1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-provider-config",
		},
		Spec: apiv1alpha1.ProviderConfigSpec{
			Versions: []apiv1alpha1.FluxVersion{
				{
					Version:  "todo",
					ChartURL: &chartURL,
				},
			},
			PollInterval: &metav1.Duration{
				Duration: time.Minute,
			},
		},
	}
}

func fakeClusterContext(t *testing.T) spruntime.ClusterContext {
	t.Helper()
	return spruntime.ClusterContext{
		MCPCluster:      testutils.CreateFakeCluster(t, "mcp"),
		WorkloadCluster: testutils.CreateFakeCluster(t, "workload"),
	}
}

func strPtr(s string) *string {
	return &s
}

// createOrUpdateWithManager is a test helper that allows injecting a fake manager
func createOrUpdateWithManager(_ *FluxReconciler, ctx context.Context, obj *apiv1alpha1.Flux, _ *apiv1alpha1.ProviderConfig, _ spruntime.ClusterContext, mgr flux.Manager) (ctrl.Result, error) {
	spruntime.StatusProgressing(obj, "Reconciling", "Reconcile in progress")
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

// deleteWithManager is a test helper that allows injecting a fake manager
func deleteWithManager(_ *FluxReconciler, ctx context.Context, obj *apiv1alpha1.Flux, _ *apiv1alpha1.ProviderConfig, _ spruntime.ClusterContext, mgr flux.Manager) (ctrl.Result, error) {
	spruntime.StatusTerminating(obj)
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

// Fake implementations for testing

var _ flux.Manager = fakeManager{}
var _ flux.ManagedObject = fakeObject{}
var _ flux.ManagedCluster = fakeManagedCluster{}

type fakeObject struct {
	status flux.Status
}

func (f fakeObject) GetDeletionPolicy() flux.DeletionPolicy {
	return flux.Delete
}

func (f fakeObject) GetDependencies() []flux.ManagedObject {
	return nil
}

func (f fakeObject) GetObject() client.Object {
	u := &unstructured.Unstructured{}
	u.SetName("test")
	u.SetNamespace("test")
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "openmcp.cloud",
		Version: "v1alpha1",
		Kind:    "Test",
	})
	return u
}

func (f fakeObject) GetStatus(rl apiv1alpha1.ResourceLocation) flux.Status {
	return f.status
}

func (f fakeObject) Reconcile(ctx context.Context) error {
	return nil
}

type fakeManager struct {
	results []flux.Result
}

func (f fakeManager) AddCluster(mc flux.ManagedCluster) {
}

func (f fakeManager) Apply(context.Context) []flux.Result {
	return f.results
}

func (f fakeManager) Delete(context.Context) []flux.Result {
	return f.results
}

type fakeManagedCluster struct {
	clusterType flux.ClusterType
}

func (f fakeManagedCluster) AddObject(o flux.ManagedObject) {}

func (f fakeManagedCluster) GetObjects() []flux.ManagedObject {
	return nil
}

func (f fakeManagedCluster) GetDefaultNamespace() string {
	return "default"
}

func (f fakeManagedCluster) GetHostAndPort() (string, string) {
	return "localhost", "6443"
}

func (f fakeManagedCluster) GetConfig() *rest.Config {
	return nil
}

func (f fakeManagedCluster) GetClient() client.Client {
	return nil
}

func (f fakeManagedCluster) GetCluster() *clusters.Cluster {
	return nil
}

func (f fakeManagedCluster) GetClusterType() flux.ClusterType {
	return f.clusterType
}

func fakeResult(phase apiv1alpha1.InstancePhase, opResult controllerutil.OperationResult, clusterType flux.ClusterType, err error) flux.Result {
	return flux.Result{
		Object: fakeObject{
			status: flux.Status{
				Phase:    phase,
				Location: apiv1alpha1.ResourceLocation(clusterType),
			},
		},
		OperationResult: opResult,
		Cluster:         fakeManagedCluster{clusterType: clusterType},
		Error:           err,
	}
}

func Test_selectFluxVersion(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		requestedVersion string
		pc               *apiv1alpha1.ProviderConfig
		want             apiv1alpha1.FluxVersion
		wantErr          bool
	}{
		{
			name:             "version is available",
			requestedVersion: "v1",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Versions: []apiv1alpha1.FluxVersion{{Version: "v1"}, {Version: "v2"}},
				},
			},
			want: apiv1alpha1.FluxVersion{
				Version: "v1",
			},
			wantErr: false,
		},
		{
			name:             "version is not available",
			requestedVersion: "v3",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Versions: []apiv1alpha1.FluxVersion{{Version: "v1"}, {Version: "v2"}},
				},
			},
			want:    apiv1alpha1.FluxVersion{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := selectFluxVersion(tt.requestedVersion, tt.pc)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("selectFluxVersion() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("selectFluxVersion() succeeded unexpectedly")
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
