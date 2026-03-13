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
	"testing"

	"github.com/stretchr/testify/assert"
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
