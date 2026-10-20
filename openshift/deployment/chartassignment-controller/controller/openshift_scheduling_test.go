// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func TestEnsureNvidiaGPUTolerationIsScopedAndIdempotent(t *testing.T) {
	for _, test := range []struct {
		name    string
		podSpec map[string]interface{}
		wantTol bool
	}{
		{
			name: "GPU request",
			podSpec: map[string]interface{}{"containers": []interface{}{map[string]interface{}{
				"resources": map[string]interface{}{"requests": map[string]interface{}{"nvidia.com/gpu": "1"}},
			}}},
			wantTol: true,
		},
		{
			name: "ICON co-location",
			podSpec: map[string]interface{}{"affinity": map[string]interface{}{"podAffinity": map[string]interface{}{
				"requiredDuringSchedulingIgnoredDuringExecution": []interface{}{map[string]interface{}{
					"labelSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": gazeboSimulatorName}},
				}},
			}}},
			wantTol: true,
		},
		{
			name: "UR co-location",
			podSpec: map[string]interface{}{"affinity": map[string]interface{}{"podAffinity": map[string]interface{}{
				"requiredDuringSchedulingIgnoredDuringExecution": []interface{}{map[string]interface{}{
					"labelSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "rs-icon"}},
				}},
			}}},
			wantTol: true,
		},
		{
			name:    "unrelated workload",
			podSpec: map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "web"}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ensureNvidiaGPUToleration(test.podSpec); err != nil {
				t.Fatal(err)
			}
			if err := ensureNvidiaGPUToleration(test.podSpec); err != nil {
				t.Fatalf("second adaptation: %v", err)
			}
			tolerations, _, err := unstructured.NestedSlice(test.podSpec, "tolerations")
			if err != nil {
				t.Fatal(err)
			}
			if got := len(tolerations) == 1; got != test.wantTol {
				t.Fatalf("tolerations = %v, want GPU toleration present=%t", tolerations, test.wantTol)
			}
		})
	}
}
