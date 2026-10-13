// This project was developed with assistance from AI tools.
package synk

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestInitializeEnforcesSingleNamespaceAndRejectsClusterScopedKinds(t *testing.T) {
	tests := []struct {
		name     string
		resource *unstructured.Unstructured
		wantErr  bool
	}{
		{
			name:     "resource in target namespace",
			resource: newUnstructured("v1", "Pod", "your-project-namespace", "pilot"),
		},
		{
			name:     "resource in another namespace",
			resource: newUnstructured("v1", "Pod", "other-project", "escape"),
			wantErr:  true,
		},
		{
			name:     "cluster scoped resource",
			resource: newUnstructured("v1", "Namespace", "", "escape"),
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			s := f.newSynk()
			rs, _, err := s.initialize(context.Background(), &ApplyOptions{
				Namespace:        "your-project-namespace",
				EnforceNamespace: true,
			}, tc.resource)
			if (err != nil) != tc.wantErr {
				t.Fatalf("initialize error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && rs.Namespace != "your-project-namespace" {
				t.Fatalf("ResourceSet namespace = %q, want your-project-namespace", rs.Namespace)
			}
		})
	}
}
