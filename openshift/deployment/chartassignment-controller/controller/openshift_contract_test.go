// This project was developed with assistance from AI tools.
package chartassignment

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAdapterDoesNotMutateInputsOnSuccessOrPartialFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial-failure"}[fail], func(t *testing.T) {
			first := object("ConfigMap", "first", map[string]interface{}{
				"data": map[string]interface{}{"address": "worker.app-intrinsic-base.svc.cluster.local:9000"},
			})
			first.SetNamespace("app-intrinsic-base")
			input := []*unstructured.Unstructured{first}
			if fail {
				input = append(input, object("Secret", "unsupported", nil))
			}
			before := make([]*unstructured.Unstructured, len(input))
			for i := range input {
				before[i] = input[i].DeepCopy()
			}
			got, err := adaptOpenShiftResources(input, pilotNamespace)
			if !reflect.DeepEqual(input, before) {
				t.Fatal("adapter mutated caller-owned resources")
			}
			if fail {
				if err == nil || !strings.Contains(err.Error(), "Secret") || got != nil {
					t.Fatalf("partial failure returned resources or the wrong error: %v", err)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].GetNamespace() != pilotNamespace {
				t.Fatalf("successful adaptation failed: %v", err)
			}
			got[0].Object["data"].(map[string]interface{})["address"] = "changed"
			if !reflect.DeepEqual(input, before) {
				t.Fatal("output aliases caller-owned nested maps")
			}
		})
	}
}

func TestAdapterPreservesObjectOrderAndGeneratedPVCOrder(t *testing.T) {
	workload := func(kind, name string, volumes []interface{}) *unstructured.Unstructured {
		container := map[string]interface{}{"name": "fixture", "image": "example.invalid/fixture"}
		if name == gazeboSimulatorName {
			container = map[string]interface{}{
				"name":  gazeboSimulatorContainerName,
				"image": "example.invalid/" + gazeboSimulatorImageRepository,
				"args":  []interface{}{gazeboSimulatorMainBinary},
			}
		}
		return object(kind, name, map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{container},
				"volumes":    volumes,
			}}},
		})
	}
	input := []*unstructured.Unstructured{
		object("ConfigMap", "first", nil),
		object("PersistentVolume", "omitted", nil),
		workload("StatefulSet", "rs-gazebo-simulator", []interface{}{
			map[string]interface{}{"name": "intrinsic-icon", "hostPath": map[string]interface{}{"path": "/tmp/intrinsic_icon"}},
			map[string]interface{}{"name": "gzserver-meshes-volume", "hostPath": map[string]interface{}{"path": "/tmp/service_volumes/intrinsic/gzserver-meshes"}},
		}),
		workload("Deployment", "data-store", []interface{}{
			map[string]interface{}{"name": "intrinsic-db-dir", "hostPath": map[string]interface{}{"path": "/var/apps/intrinsic-db"}},
		}),
		object("ConfigMap", "last", nil),
	}
	got, err := adaptOpenShiftResources(input, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ConfigMap/first", "StatefulSet/rs-gazebo-simulator", "Deployment/data-store", "ConfigMap/last",
		"PersistentVolumeClaim/" + dataStorePVC, "PersistentVolumeClaim/" + intrinsicIconPVC, "PersistentVolumeClaim/" + gazeboMeshesPVC}
	if !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("resource/PVC order changed: %v", kinds(got))
	}
}

func TestAdapterRejectsNilAndNamespaceBeforeApplyingPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, message string
		input                    []*unstructured.Unstructured
	}{
		{"namespace", "", "OpenShift target namespace is required", []*unstructured.Unstructured{object("Secret", "unsupported", nil)}},
		{"nil", pilotNamespace, "chart rendered a nil resource", []*unstructured.Unstructured{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adaptOpenShiftResources(tc.input, tc.namespace)
			if err == nil || err.Error() != tc.message || got != nil {
				t.Fatalf("rejection contract changed: %v", err)
			}
		})
	}
}

func TestAdapterUsesIndependentCanonicalNamespaces(t *testing.T) {
	for _, namespace := range []string{"offline-cell-a", "offline-cell-b"} {
		t.Run(namespace, func(t *testing.T) {
			config := *controllerConfig
			config.Namespace = namespace
			config.IngressAddress = "gateway." + namespace + ".svc.cluster.local:80"
			config.IngressGateway = namespace + "/gateway"
			config.RHOAITritonEndpoint = "triton." + namespace + ".svc.cluster.local:9000"
			config.ZenohRouterEndpoint = "zenoh-router." + namespace + ".svc.cluster.local:7447"
			useControllerConfig(t, &config)
			input := object("ConfigMap", "references", map[string]interface{}{
				"data": map[string]interface{}{"cas": "content-addressable-storage.app-intrinsic-base.svc.cluster.local:9747"},
			})
			got, err := adaptOpenShiftResources([]*unstructured.Unstructured{input}, namespace)
			if err != nil || len(got) != 1 {
				t.Fatalf("portable adaptation failed: %v", err)
			}
			address, _, _ := unstructured.NestedString(got[0].Object, "data", "cas")
			if got[0].GetNamespace() != namespace || address != "content-addressable-storage."+namespace+".svc.cluster.local:9747" {
				t.Fatal("namespace references drifted from canonical configuration")
			}
			if configuredRHOAITritonEndpoint(namespace) != config.RHOAITritonEndpoint || configuredZenohRouterEndpoint(namespace) != "tcp/"+config.ZenohRouterEndpoint {
				t.Fatal("canonical inference/Zenoh endpoint changed")
			}
		})
	}
}
