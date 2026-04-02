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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// OperationResultDeletionRequested indicates that an object has been marked for deletion
	OperationResultDeletionRequested controllerutil.OperationResult = "deletionRequested"
	// OperationResultDeleted indicates that an object has been deleted
	OperationResultDeleted controllerutil.OperationResult = "deleted"
	// OperationResultOrphaned indicates that an object has been orphaned
	OperationResultOrphaned controllerutil.OperationResult = OperationResultDeleted
)

type dependents map[ManagedObject][]dependency

// Manager manages the objects of an arbitrary number of clusters
type Manager interface {
	AddCluster(mc ManagedCluster)
	Apply(context.Context) []Result
	Delete(context.Context) []Result
}

// NewManager creates a new Manager instance.
func NewManager() Manager {
	return &managerImpl{
		clusters: []ManagedCluster{},
	}
}

// managerImpl manages clusters and invokes reconciliation of ManagedObjects.
type managerImpl struct {
	clusters []ManagedCluster
}

// AddCluster adds a cluster to a Manager.
func (m *managerImpl) AddCluster(mc ManagedCluster) {
	m.clusters = append(m.clusters, mc)
}

// Apply invokes reconciliation of all ManagedObjects.
func (m *managerImpl) Apply(ctx context.Context) []Result {
	return m.reconcileObjects(ctx, false)
}

// Delete invokes deletion of all ManagedObjects.
func (m *managerImpl) Delete(ctx context.Context) []Result {
	return m.reconcileObjects(ctx, true)
}

func (m *managerImpl) reconcileObjects(ctx context.Context, isDeletion bool) []Result {
	dependents := m.getDependents()

	// Apply or delete objects from each cluster.
	results := []Result{}
	for _, mc := range m.clusters {
		for _, mo := range mc.GetObjects() {
			result := m.reconcileObject(ctx, mc, mo, dependents, isDeletion)
			results = append(results, result)
		}
	}

	return results
}

func (m *managerImpl) reconcileObject(ctx context.Context, mc ManagedCluster, mo ManagedObject, dependents dependents, isDeletion bool) Result {
	client := mc.GetClient()
	obj := mo.GetObject()

	if isDeletion {
		if err := m.checkForDependents(ctx, dependents[mo]); err != nil {
			return Result{
				Object:          mo,
				Cluster:         mc,
				OperationResult: controllerutil.OperationResultNone,
				Error:           err,
			}
		}

		if mo.GetDeletionPolicy() == Orphan {
			return Result{
				Object:          mo,
				Cluster:         mc,
				OperationResult: OperationResultOrphaned,
				Error:           nil,
			}
		}

		err := client.Delete(ctx, obj)
		if apierrors.IsNotFound(err) {
			return Result{
				Object:          mo,
				Cluster:         mc,
				OperationResult: OperationResultDeleted,
				Error:           nil,
			}
		}
		return Result{
			Object:          mo,
			Cluster:         mc,
			OperationResult: OperationResultDeletionRequested,
			Error:           err,
		}
	}

	opResult, err := controllerutil.CreateOrUpdate(ctx, client, obj, func() error {
		SetManagedBy(obj)
		return mo.Reconcile(ctx)
	})
	return Result{
		Object:          mo,
		Cluster:         mc,
		OperationResult: opResult,
		Error:           err,
	}
}

func (m *managerImpl) checkForDependents(ctx context.Context, deps []dependency) error {
	errs := []error{}
	for _, dep := range deps {
		obj := dep.Object.GetObject()
		err := dep.Cluster.GetClient().Get(ctx, client.ObjectKeyFromObject(obj), obj)
		if apierrors.IsNotFound(err) {
			// "Not found" is the success case: The object which depends on us does not exist anymore.
			continue
		}
		if err != nil {
			// Some unexpected error occurred.
			errs = append(errs, err)
			continue
		}
		// No error occurred, the GET request has been successful.
		// The object still exists and depends on us.
		errs = append(errs, fmt.Errorf("dependent object still exists: %s", ObjectID(obj)))
	}
	return errors.Join(errs...)
}

func (m *managerImpl) getDependents() dependents {
	deps := dependents{}
	for _, mc := range m.clusters {
		for _, mo := range mc.GetObjects() {
			for _, dep := range mo.GetDependencies() {
				if deps[dep] == nil {
					deps[dep] = []dependency{}
				}
				deps[dep] = append(deps[dep], dependency{
					Object:  mo,
					Cluster: mc,
				})
			}
		}
	}
	return deps
}

// Result summarizes a reconciliation result.
type Result struct {
	Object          ManagedObject
	Cluster         ManagedCluster
	OperationResult controllerutil.OperationResult
	Error           error
}

type dependency struct {
	Object  ManagedObject
	Cluster ManagedCluster
}

// AllDeleted returns true if every item's operation result is OperationResultDeleted.
func AllDeleted(results []Result) bool {
	for _, r := range results {
		if r.OperationResult != OperationResultDeleted {
			return false
		}
	}
	return true
}
