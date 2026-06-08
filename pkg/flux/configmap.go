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

	openmcpresources "github.com/openmcp-project/controller-utils/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
)

// ErrConfigMapCleanup is an user-facing error that indicates configmap cleanup failures
var ErrConfigMapCleanup = errors.New("configmap cleanup failed")

// ConfigMapCopyConfig holds the configuration for copying configmap.
type ConfigMapCopyConfig struct {
	// SourceClient is the client to read the source configmap from.
	SourceClient client.Client
	// SourceNamespace is the namespace of the source configmap.
	SourceNamespace string
	// TargetNamespace is the namespace of the target configmap.
	TargetNamespace string
	// TargetName is an optional value to adjust the name of the target configmap
	// instead of using the source configmap name.
	TargetName string
}

// ManageCaConfigMap syncs the ca configmap to the target cluster.
func ManageCaConfigMap(targetCluster ManagedCluster, caConfigMap corev1.LocalObjectReference, config ConfigMapCopyConfig) {
	caConfigMapName := caConfigMap.Name
	if config.TargetName != "" {
		caConfigMapName = config.TargetName
	}
	configMap := NewManagedObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caConfigMapName,
			Namespace: config.TargetNamespace,
		},
	}, ManagedObjectContext{
		ReconcileFunc: func(ctx context.Context, o client.Object) error {
			oConfigMap, ok := o.(*corev1.ConfigMap)
			if !ok {
				return fmt.Errorf("expected *corev1.ConfigMap, got %T", o)
			}
			sourceConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caConfigMap.Name,
					Namespace: config.SourceNamespace,
				},
			}
			// retrieve source configmap from platform cluster
			if err := config.SourceClient.Get(ctx, client.ObjectKeyFromObject(sourceConfigMap), sourceConfigMap); err != nil {
				return err
			}
			mutator := openmcpresources.NewConfigMapMutator(caConfigMapName, config.TargetNamespace, sourceConfigMap.Data)
			return mutator.Mutate(oConfigMap)
		},
		StatusFunc: SimpleStatus,
	})
	targetCluster.AddObject(configMap)
}

// ConfigMapStatus returns the status of a configmap object.
func ConfigMapStatus(o client.Object, rl apiv1alpha1.ResourceLocation) Status {
	if !o.GetDeletionTimestamp().IsZero() {
		return Status{
			Phase:    apiv1alpha1.Terminating,
			Message:  "ConfigMap is terminating.",
			Location: rl,
		}
	}
	if o.GetUID() == "" {
		return Status{
			Phase:    apiv1alpha1.Pending,
			Message:  "ConfigMap has not been created yet.",
			Location: rl,
		}
	}
	return Status{
		Phase:    apiv1alpha1.Ready,
		Message:  "ConfigMap exists.",
		Location: rl,
	}
}

var _ OrphanCleaner = &configMapCleaner{}

type configMapCleaner struct {
	cluster          ManagedCluster
	namespace        string
	configMapsToKeep []corev1.LocalObjectReference
}

// NewConfigMapCleaner removes redundant configmaps in the given target namespace
// by removing any configmap labeled as managed by sp-flux that is not in configMapsToKeep.
func NewConfigMapCleaner(cluster ManagedCluster, namespace string, configMapsToKeep []corev1.LocalObjectReference) OrphanCleaner {
	return &configMapCleaner{
		cluster:          cluster,
		namespace:        namespace,
		configMapsToKeep: configMapsToKeep,
	}
}

func (c *configMapCleaner) Cleanup(ctx context.Context) ([]Result, error) {
	results := []Result{}
	configMapCopies := &corev1.ConfigMapList{}
	cl := c.cluster.GetClient()
	if err := cl.List(ctx, configMapCopies,
		client.InNamespace(c.namespace),
		client.MatchingLabels{LabelManagedBy: labelServiceProviderFlux},
	); err != nil {
		log.FromContext(ctx).Error(err, "failed to list configmap for orphan cleanup")
		return nil, ErrConfigMapCleanup
	}
	for _, configMap := range configMapCopies.Items {
		if !slices.ContainsFunc(c.configMapsToKeep, func(ref corev1.LocalObjectReference) bool { return configMap.Name == ref.Name }) {
			if err := cl.Delete(ctx, &configMap); client.IgnoreNotFound(err) != nil {
				results = append(results, c.cleanupErrorResult(&configMap, err))
			}
		}
	}
	return results, nil
}

func (c *configMapCleaner) cleanupErrorResult(obj *corev1.ConfigMap, err error) Result {
	return Result{
		Object: &managedObject{
			object:         obj,
			statusFunc:     cleanupErrorStatusConfigMap,
			deletionPolicy: Delete,
		},
		Cluster:         c.cluster,
		OperationResult: OperationResultDeletionFailed,
		Error:           err,
	}
}

func cleanupErrorStatusConfigMap(_ client.Object, rl apiv1alpha1.ResourceLocation) Status {
	return Status{
		Phase:    apiv1alpha1.Terminating,
		Message:  "ConfigMap cleanup failed",
		Location: rl,
	}
}
