// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestAdaptInferenceCASAddressUsesRHOAITriton(t *testing.T) {
	resource := object("StatefulSet", inferenceServiceName, map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  inferenceServiceContainerName,
							"image": "registry.example/ai.intrinsic.inference_service.inference_service_image@sha256:abcdef",
						},
						map[string]interface{}{
							"name":  inferenceServiceTritonContainerName,
							"image": "registry.example/ai.intrinsic.inference_service.triton_inference_server_image@sha256:abcdef",
						},
					},
				},
			},
		},
	})
	podSpec, found, err := unstructured.NestedMap(resource.Object, "spec", "template", "spec")
	if err != nil || !found {
		t.Fatalf("inference pod spec: found=%v err=%v", found, err)
	}
	if err := adaptInferenceCASAddress(resource, podSpec, pilotNamespace); err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(podSpec, "containers")
	if len(containers) != 1 || containers[0].(map[string]interface{})["name"] != inferenceServiceContainerName {
		t.Fatalf("native Triton container was not removed: %v", containers)
	}
	container := containers[0].(map[string]interface{})
	env, _, _ := unstructured.NestedSlice(container, "env")
	if len(env) != 1 || env[0].(map[string]interface{})["value"] != "triton-rhoai-grpc."+pilotNamespace+".svc.cluster.local:9000" {
		t.Fatalf("inference endpoint env = %v", env)
	}
	mounts, _, _ := unstructured.NestedSlice(container, "volumeMounts")
	if len(mounts) != 2 {
		t.Fatalf("inference volume mounts = %v, want patched source and projected token", mounts)
	}
	volumes, _, _ := unstructured.NestedSlice(podSpec, "volumes")
	var hasProjectedToken bool
	for _, item := range volumes {
		volume := item.(map[string]interface{})
		if volume["name"] == inferenceServiceTokenVolume {
			sources, found, sourceErr := unstructured.NestedSlice(volume, "projected", "sources")
			if sourceErr == nil && found && len(sources) == 1 {
				token, tokenFound, tokenErr := unstructured.NestedMap(sources[0].(map[string]interface{}), "serviceAccountToken")
				hasProjectedToken = tokenErr == nil && tokenFound && token["audience"] == "https://kubernetes.default.svc" && token["path"] == "token"
			}
		}
	}
	if !hasProjectedToken {
		t.Fatal("missing projected service-account token volume")
	}
	initContainers, _, _ := unstructured.NestedSlice(podSpec, "initContainers")
	initContainer := initContainers[0].(map[string]interface{})
	args, _, _ := unstructured.NestedStringSlice(initContainer, "args")
	if len(args) != 2 {
		t.Fatalf("patch init args = %v", args)
	}
	for _, expected := range []string{
		"_RhoaiBearerInterceptor",
		"INTRINSIC_RHOAI_TRITON_ENDPOINT",
		"model_controller._run_triton_action = lambda",
		"use_shm=False",
		"content-addressable-storage." + pilotNamespace + ".svc.cluster.local:9747",
	} {
		if !strings.Contains(args[1], expected) {
			t.Errorf("init patch script lacks %q", expected)
		}
	}
}
