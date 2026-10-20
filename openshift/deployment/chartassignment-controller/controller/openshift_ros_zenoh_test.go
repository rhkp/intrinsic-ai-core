// This project was developed with assistance from AI tools.
package chartassignment

import (
	"bytes"
	"encoding/base64"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestAdaptFlowstateRuntimeConfigUsesProjectZenohRouter(t *testing.T) {
	oldEndpoint := []byte(upstreamZenohRouterEndpoint)
	firstNested := protobufBytesField(1, oldEndpoint)
	secondNested := protobufBytesField(2, protobufBytesField(1, oldEndpoint))
	runtimeConfig := append(protobufBytesField(1, firstNested), secondNested...)
	resource := object("ConfigMap", flowstateRuntimeConfigMapName, map[string]interface{}{
		"binaryData": map[string]interface{}{flowstateRuntimeConfigKey: base64.StdEncoding.EncodeToString(runtimeConfig)},
	})

	if err := adaptFlowstateRuntimeConfig(resource, pilotNamespace); err != nil {
		t.Fatal(err)
	}
	encoded, _, err := unstructured.NestedString(resource.Object, "binaryData", flowstateRuntimeConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoint := []byte("tcp/zenoh-router." + pilotNamespace + ".svc.cluster.local:7447")
	if got := strings.Count(string(updated), string(wantEndpoint)); got != 2 {
		t.Fatalf("adapted endpoint count = %d, want 2", got)
	}
	if _, count, err := rewriteProtobufStrings(updated, oldEndpoint, wantEndpoint, 0); err != nil || count != 0 {
		t.Fatalf("adapted protobuf is invalid or not idempotent: count=%d err=%v", count, err)
	}
	if err := adaptFlowstateRuntimeConfig(resource, pilotNamespace); err != nil {
		t.Fatalf("second adaptation: %v", err)
	}
}

func TestAdaptFlowstateRuntimeConfigUsesCanonicalZenohRouter(t *testing.T) {
	useControllerConfig(t, &ControllerConfig{
		ZenohRouterEndpoint: "zenoh.example.com:7447",
	})
	oldEndpoint := []byte(upstreamZenohRouterEndpoint)
	firstNested := protobufBytesField(1, oldEndpoint)
	secondNested := protobufBytesField(2, protobufBytesField(1, oldEndpoint))
	runtimeConfig := append(protobufBytesField(1, firstNested), secondNested...)
	resource := object("ConfigMap", flowstateRuntimeConfigMapName, map[string]interface{}{
		"binaryData": map[string]interface{}{flowstateRuntimeConfigKey: base64.StdEncoding.EncodeToString(runtimeConfig)},
	})

	if err := adaptFlowstateRuntimeConfig(resource, pilotNamespace); err != nil {
		t.Fatal(err)
	}
	encoded, _, err := unstructured.NestedString(resource.Object, "binaryData", flowstateRuntimeConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoint := []byte("tcp/zenoh.example.com:7447")
	if got := strings.Count(string(updated), string(wantEndpoint)); got != 2 {
		t.Fatalf("adapted canonical endpoint count = %d, want 2", got)
	}
}

func TestAdaptSimulationServerRuntimeContextUsesProjectNamespace(t *testing.T) {
	oldHost := []byte(upstreamSimulationServerHost)
	runtimeConfig := protobufBytesField(3, oldHost)
	resource := object("ConfigMap", "rc-ur-module", map[string]interface{}{
		"binaryData": map[string]interface{}{flowstateRuntimeConfigKey: base64.StdEncoding.EncodeToString(runtimeConfig)},
	})

	if err := adaptSimulationServerRuntimeContext(resource, pilotNamespace); err != nil {
		t.Fatal(err)
	}
	encoded, _, err := unstructured.NestedString(resource.Object, "binaryData", flowstateRuntimeConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantHost := []byte("simulation-server." + pilotNamespace + ".svc.cluster.local")
	if !bytes.Contains(updated, wantHost) || bytes.Contains(updated, oldHost) {
		t.Fatalf("adapted RuntimeContext does not contain only the project simulation-server host: %q", updated)
	}
	if err := adaptSimulationServerRuntimeContext(resource, pilotNamespace); err != nil {
		t.Fatalf("second adaptation: %v", err)
	}
}

func TestAdaptOpenShiftResourcesConfiguresProjectZenohClients(t *testing.T) {
	deployment := object("Deployment", "kvstore-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name": "kvstore-service", "image": "example.invalid/kvstore",
				"args": []interface{}{"server_main", "--port=8080"},
			}},
		}}},
	})
	moveToContactSkill := object("Deployment", "skill-group-09", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "move-to-contact-ai-intrinsic",
				"image": "image-registry.openshift-image-registry.svc:5000/your-project-namespace/" + moveToContactSkillImageRepo + "@sha256:0cfc737f4670c9b9b486ac6e2ec137b8ac552e32a98df5479f8ad929a9898a5b",
				"args":  []interface{}{"/skills/skill_service", "--port=8003"},
			}},
		}}},
	})
	statefulSet := object("StatefulSet", "rs-orbbec-gemini-driver", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{"name": "rs-orbbec-gemini-driver", "image": "example.invalid/gemini"}},
		}}},
	})
	simulator := object("StatefulSet", "rs-gazebo-simulator", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name": "rs-gazebo-simulator", "image": "example.invalid/ai.intrinsic.gazebo_simulator.asset-gzserver-image",
				"resources": map[string]interface{}{"requests": map[string]interface{}{"nvidia.com/gpu": "1"}},
				"args":      []interface{}{"/intrinsic/simulation/gazebo/asset/asset_simulation_server_main", "--simulation_service_address=localhost:80"},
			}},
		}}},
	})
	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment, moveToContactSkill, statefulSet, simulator}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoint := "tcp/zenoh-router." + pilotNamespace + ".svc.cluster.local:7447"
	for _, resource := range adapted {
		containers, _, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
		if err != nil {
			t.Fatal(err)
		}
		container := containers[0].(map[string]interface{})
		if resource.GetName() == "rs-gazebo-simulator" {
			tolerations, _, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "tolerations")
			if err != nil {
				t.Fatal(err)
			}
			if len(tolerations) != 1 || tolerations[0].(map[string]interface{})["key"] != openshiftGPUTaintKey || tolerations[0].(map[string]interface{})["operator"] != "Equal" || tolerations[0].(map[string]interface{})["value"] != "true" || tolerations[0].(map[string]interface{})["effect"] != "NoSchedule" {
				t.Fatalf("%s/%s GPU tolerations = %v, want only %s=true:NoSchedule", resource.GetKind(), resource.GetName(), tolerations, openshiftGPUTaintKey)
			}
		}
		if resource.GetName() == "rs-orbbec-gemini-driver" {
			tolerations, _, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "tolerations")
			if err != nil {
				t.Fatal(err)
			}
			if len(tolerations) != 0 {
				t.Fatalf("non-GPU workload %s/%s got tolerations: %v", resource.GetKind(), resource.GetName(), tolerations)
			}
		}
		if resource.GetName() == "skill-group-09" {
			wantImage := "image-registry.openshift-image-registry.svc:5000/your-project-namespace/move-to-contact-openshift@sha256:79494450d82e41ca3f856eaf89a46396816e86b96e0c3269a57bbd1bd677e520"
			if container["image"] != wantImage {
				t.Fatalf("%s/%s image = %v, want pinned OpenShift-derived MTC image %q", resource.GetKind(), resource.GetName(), container["image"], wantImage)
			}
			args, _, err := unstructured.NestedStringSlice(container, "args")
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range args {
				if strings.HasPrefix(arg, "--zenoh_router=") {
					t.Fatalf("%s/%s args contain unsupported Zenoh router flag: %v", resource.GetKind(), resource.GetName(), args)
				}
			}
			env, _, err := unstructured.NestedSlice(container, "env")
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, item := range env {
				entry := item.(map[string]interface{})
				values[entry["name"].(string)] = entry["value"].(string)
			}
			if values["INTRINSIC_ZENOH_ROUTER_ENDPOINT"] != wantEndpoint {
				t.Fatalf("%s/%s PubSub config endpoint = %v, want %q", resource.GetKind(), resource.GetName(), values["INTRINSIC_ZENOH_ROUTER_ENDPOINT"], wantEndpoint)
			}
			for _, name := range []string{"ZENOH_CONFIG_OVERRIDE", "ZENOH_ROUTER_CHECK_ATTEMPTS", "PYTHONPATH"} {
				if _, exists := values[name]; exists {
					t.Fatalf("%s/%s unexpectedly sets ineffective bootstrap variable %s", resource.GetKind(), resource.GetName(), name)
				}
			}
			volumes, _, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "volumes")
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range volumes {
				volume := item.(map[string]interface{})
				if volume["name"] == "move-to-contact-zenoh-bootstrap" {
					t.Fatalf("%s/%s unexpectedly mounts failed Python startup hook", resource.GetKind(), resource.GetName())
				}
			}
			continue
		}
		if resource.GetName() == "kvstore-service" || resource.GetName() == "rs-gazebo-simulator" {
			args, _, err := unstructured.NestedStringSlice(container, "args")
			if err != nil {
				t.Fatal(err)
			}
			wantArg := "--zenoh_router=" + wantEndpoint
			count := 0
			for _, arg := range args {
				if arg == wantArg {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("%s/%s args = %v, want exactly one %q", resource.GetKind(), resource.GetName(), args, wantArg)
			}
			continue
		}
		env, _, err := unstructured.NestedSlice(container, "env")
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for _, item := range env {
			entry := item.(map[string]interface{})
			values[entry["name"].(string)] = entry["value"].(string)
		}
		wantOverride := `mode="client";connect/endpoints=["` + wantEndpoint + `"]`
		if values["ZENOH_CONFIG_OVERRIDE"] != wantOverride || values["ZENOH_ROUTER_CHECK_ATTEMPTS"] != "-1" {
			t.Fatalf("%s/%s Zenoh environment = %v", resource.GetKind(), resource.GetName(), values)
		}
	}
	annotations, _, err := unstructured.NestedStringMap(adapted[0].Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		t.Fatal(err)
	}
	if annotations[istioSidecarInjectAnnotation] != "true" {
		t.Fatalf("KVStore sidecar injection annotation = %q, want true", annotations[istioSidecarInjectAnnotation])
	}
}

func TestAdaptOpenShiftResourcesConfiguresProjectZenohRouter(t *testing.T) {
	for _, test := range []struct {
		deployment string
		container  string
		binary     string
	}{
		{deployment: "world", container: "world", binary: "intrinsic/world/binary/world_binary"},
		{deployment: "simulation-service", container: "simulation-service", binary: "/intrinsic/simulation/service/simulation_service_main"},
	} {
		t.Run(test.deployment, func(t *testing.T) {
			deployment := object("Deployment", test.deployment, map[string]interface{}{
				"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{
						"name":  test.container,
						"image": "example.invalid/" + test.deployment,
						"args":  []interface{}{test.binary, "--port=8080"},
					}},
				}}},
			})
			adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment}, pilotNamespace)
			if err != nil {
				t.Fatal(err)
			}
			containers, _, _ := unstructured.NestedSlice(adapted[0].Object, "spec", "template", "spec", "containers")
			args, _, _ := unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
			want := "--zenoh_router=tcp/zenoh-router." + pilotNamespace + ".svc.cluster.local:7447"
			if len(args) != 3 || args[2] != want {
				t.Fatalf("adapted args = %v, want project Zenoh router flag %q", args, want)
			}

			adaptedAgain, err := adaptOpenShiftResources(adapted, pilotNamespace)
			if err != nil {
				t.Fatalf("second adaptation: %v", err)
			}
			containers, _, _ = unstructured.NestedSlice(adaptedAgain[0].Object, "spec", "template", "spec", "containers")
			args, _, _ = unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
			if len(args) != 3 || args[2] != want {
				t.Fatalf("second adaptation args = %v, want exactly one project Zenoh router flag", args)
			}
		})
	}
}

func TestAdaptOpenShiftResourcesRejectsConflictingProjectZenohRouter(t *testing.T) {
	deployment := object("Deployment", "world", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "world",
				"image": "example.invalid/world",
				"args":  []interface{}{"world_binary", "--zenoh_router=tcp/zenoh-router.other-project.svc.cluster.local:7447"},
			}},
		}}},
	})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "expected project endpoint") {
		t.Fatalf("conflicting Zenoh endpoint should fail closed, got %v", err)
	}
}
