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
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const labelManagedBy = "flux.services.openmcp.cloud/managed-by"

// SetManagedBy sets the managed-by label on the given object.
func SetManagedBy(obj client.Object) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[labelManagedBy] = "service-provider-flux"
	obj.SetLabels(labels)
}

// ObjectID returns a unique identifier for the given object.
func ObjectID(obj client.Object) string {
	return fmt.Sprintf("%s/%s/%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName())
}
