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

	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	openmcpresources "github.com/openmcp-project/controller-utils/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1alpha1 "github.com/openmcp-project/service-provider-flux/api/v1alpha1"
)

const secretNamePrefix = "sp-flux-"

// SecretCopyConfig holds the configuration for copying secrets.
type SecretCopyConfig struct {
	// SourceClient is the client to read the source secret from.
	SourceClient client.Client
	// SourceNamespace is the namespace of the source secret.
	SourceNamespace string
	// TargetNamespace is the namespace of the target secret.
	TargetNamespace string
	// TargetName is an optional value to adjust the name of the target secret
	// instead of using the source secret name.
	TargetName string
}

// ManagePullSecrets syncs every image pull secret to the target cluster.
func ManagePullSecrets(targetCluster ManagedCluster, imagePullSecrets []corev1.LocalObjectReference, config SecretCopyConfig) {
	for _, pullSecret := range imagePullSecrets {
		secretName := pullSecret.Name
		if config.TargetName != "" {
			secretName = config.TargetName
		}
		secret := NewManagedObject(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: config.TargetNamespace,
			},
		}, ManagedObjectContext{
			ReconcileFunc: func(ctx context.Context, o client.Object) error {
				oSecret, ok := o.(*corev1.Secret)
				if !ok {
					return fmt.Errorf("expected *corev1.Secret, got %T", o)
				}
				sourceSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pullSecret.Name,
						Namespace: config.SourceNamespace,
					},
				}
				// retrieve source secret from platform cluster
				if err := config.SourceClient.Get(ctx, client.ObjectKeyFromObject(sourceSecret), sourceSecret); err != nil {
					return err
				}
				mutator := openmcpresources.NewSecretMutator(secretName, config.TargetNamespace, sourceSecret.Data, corev1.SecretTypeDockerConfigJson)
				return mutator.Mutate(oSecret)
			},
			StatusFunc: SimpleStatus,
		})
		targetCluster.AddObject(secret)
	}
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

// PrefixSecretName adds the "sp-eso-" prefix to the given secret name
// to prevent name collisions in namespaces where multiple service providers operate.
// If the resulting name exceeds 63 characters (K8s limit), it will be truncated
// and a hash suffix appended for uniqueness via ShortenToXCharacters.
func PrefixSecretName(secretName string) (string, error) {
	return ctrlutils.ShortenToXCharacters(fmt.Sprintf("%s%s", secretNamePrefix, secretName), ctrlutils.K8sMaxNameLength)
}
