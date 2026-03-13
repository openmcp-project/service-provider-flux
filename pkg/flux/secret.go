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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
)

// SecretCopyConfig holds the configuration for copying a secret.
type SecretCopyConfig struct {
	// SourceClient is the client to read the source secret from.
	SourceClient client.Client
	// SourceKey is the namespace/name of the source secret.
	SourceKey types.NamespacedName
	// TargetKey is the namespace/name of the target secret.
	TargetKey types.NamespacedName
}

// ConfigureSecretCopy creates a ManagedObject that copies a secret from source to target.
func ConfigureSecretCopy(cluster ManagedCluster, cfg SecretCopyConfig) ManagedObject {
	secret := NewManagedObject(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.TargetKey.Name,
			Namespace: cfg.TargetKey.Namespace,
		},
	}, ManagedObjectContext{
		ReconcileFunc: func(ctx context.Context, o client.Object) error {
			targetSecret, ok := o.(*corev1.Secret)
			if !ok {
				return fmt.Errorf("expected *corev1.Secret, got %T", o)
			}

			// Get source secret
			sourceSecret := &corev1.Secret{}
			if err := cfg.SourceClient.Get(ctx, cfg.SourceKey, sourceSecret); err != nil {
				return fmt.Errorf("failed to get source secret %s: %w", cfg.SourceKey, err)
			}

			// Copy data
			targetSecret.Type = sourceSecret.Type
			targetSecret.Data = sourceSecret.Data

			return nil
		},
		DependsOn:      []ManagedObject{},
		DeletionPolicy: Delete,
		StatusFunc:     SecretStatus,
	})

	cluster.AddObject(secret)
	return secret
}

// SecretStatus returns the status of a secret object.
func SecretStatus(o client.Object, rl apiv1alpha1.ResourceLocation) Status {
	if !o.GetDeletionTimestamp().IsZero() {
		return Status{
			Phase:    apiv1alpha1.Terminating,
			Message:  "Secret is terminating.",
			Location: rl,
		}
	}
	if o.GetUID() == "" {
		return Status{
			Phase:    apiv1alpha1.Pending,
			Message:  "Secret has not been created yet.",
			Location: rl,
		}
	}
	return Status{
		Phase:    apiv1alpha1.Ready,
		Message:  "Secret exists.",
		Location: rl,
	}
}
