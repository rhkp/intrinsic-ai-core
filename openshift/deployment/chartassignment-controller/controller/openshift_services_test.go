// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestAdaptOpenShiftResourcesTargetsProjectSimulationService(t *testing.T) {
	deployment := func(image string, args ...interface{}) *unstructured.Unstructured {
		return object("Deployment", gzserverDeploymentName, map[string]interface{}{
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{map[string]interface{}{
							"name":  gzserverContainerName,
							"image": image,
							"args":  args,
						}},
					},
				},
			},
		})
	}
	image := "registry.example/gzserver_insrc_3xcbw2p75tkkh7r6@sha256:abcdef"
	input := deployment(image,
		gzserverMainBinary,
		"--simulation_service_address=simulation-service.app-intrinsic-base.svc.cluster.local:8088",
		"--opencensus_tracing=true",
	)
	simulationServer := object("Service", "simulation-server", map[string]interface{}{
		"spec": map[string]interface{}{
			"clusterIP": "None",
			"selector":  map[string]interface{}{"app": "gzserver"},
			"type":      "ClusterIP",
		},
	})

	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{input, simulationServer}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("adapted resources = %v, want gzserver Deployment and simulation-server Service", kinds(got))
	}
	wantAddress := "--simulation_service_address=simulation-service." + pilotNamespace + ".svc.cluster.local:8088"
	check := func(resource *unstructured.Unstructured) {
		t.Helper()
		containers, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
		if err != nil || !found || len(containers) != 1 {
			t.Fatalf("gzserver containers: found=%v err=%v", found, err)
		}
		container := containers[0].(map[string]interface{})
		args, found, err := unstructured.NestedStringSlice(container, "args")
		if err != nil || !found {
			t.Fatalf("gzserver args: found=%v err=%v", found, err)
		}
		count := 0
		for _, arg := range args {
			if strings.HasPrefix(arg, simulationServiceAddressFlag) {
				count++
				if arg != wantAddress {
					t.Errorf("simulation service argument = %q, want %q", arg, wantAddress)
				}
			}
		}
		if count != 1 {
			t.Errorf("simulation service argument count = %d, want 1 (args=%v)", count, args)
		}
	}
	check(got[0])
	ports, found, err := unstructured.NestedSlice(got[1].Object, "spec", "ports")
	if err != nil || !found || len(ports) != 7 {
		t.Fatalf("simulation-server ports: found=%v err=%v count=%d, want 7", found, err, len(ports))
	}
	for _, item := range ports {
		port := item.(map[string]interface{})
		if port["port"] != port["targetPort"] || port["protocol"] != "TCP" || !strings.HasPrefix(port["name"].(string), "grpc-") {
			t.Errorf("unexpected simulation-server mesh port: %v", port)
		}
	}

	// Reconciliation is idempotent when the deployment already contains the
	// project-local endpoint.
	gotAgain, err := adaptOpenShiftResources(got, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	check(gotAgain[0])
	portsAgain, found, err := unstructured.NestedSlice(gotAgain[1].Object, "spec", "ports")
	if err != nil || !found || len(portsAgain) != 7 {
		t.Errorf("reconciled simulation-server ports: found=%v err=%v count=%d, want same seven ports", found, err, len(portsAgain))
	}

	wrongImage := deployment("registry.example/unreviewed-gzserver:latest", gzserverMainBinary)
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{wrongImage}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "unexpected image repository") {
		t.Fatalf("unreviewed gzserver image should fail closed, got %v", err)
	}
}

func TestAdaptOpenShiftResourcesExposesWorldConductorGRPCPort(t *testing.T) {
	service := object("Service", "world", map[string]interface{}{
		"spec": map[string]interface{}{
			"type": "ClusterIP",
			"ports": []interface{}{map[string]interface{}{
				"name": "grpc-world", "port": int64(8080), "protocol": "TCP", "targetPort": int64(8080),
			}},
		},
	})

	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{service}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	ports, found, err := unstructured.NestedSlice(got[0].Object, "spec", "ports")
	if err != nil || !found || len(ports) != 2 {
		t.Fatalf("World Service ports = %v, found=%t, err=%v; want world and Conductor gRPC ports", ports, found, err)
	}
	conductor := ports[1].(map[string]interface{})
	name, _, _ := unstructured.NestedString(conductor, "name")
	port, _, _ := unstructured.NestedInt64(conductor, "port")
	targetPort, _, _ := unstructured.NestedInt64(conductor, "targetPort")
	protocol, _, _ := unstructured.NestedString(conductor, "protocol")
	if name != "grpc-conductor" || port != 8082 || targetPort != 8082 || protocol != "TCP" {
		t.Fatalf("Conductor Service port = %v, want named gRPC TCP port 8082 targeting 8082", conductor)
	}

	if err := adaptService(got[0].DeepCopy()); err != nil {
		t.Fatalf("adapting World Service a second time should be idempotent: %v", err)
	}
}

func TestAdaptOpenShiftResourcesRejectsConflictingWorldConductorPort(t *testing.T) {
	service := object("Service", "world", map[string]interface{}{
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{"name": "grpc-world", "port": int64(8080), "targetPort": int64(8080)},
				map[string]interface{}{"name": "metrics", "port": int64(8082), "targetPort": int64(9101)},
			},
		},
	})
	if err := adaptService(service); err == nil || !strings.Contains(err.Error(), "conflicting Conductor port") {
		t.Fatalf("conflicting World port should fail closed, got %v", err)
	}
}

func TestAdaptOpenShiftResourcesConfiguresGazeboAssetInstancesServiceAddress(t *testing.T) {
	statefulSet := object("StatefulSet", gazeboSimulatorName, map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  gazeboSimulatorContainerName,
				"image": "registry.invalid/intrinsic/" + gazeboSimulatorImageRepository + "@sha256:0123456789abcdef",
				"args":  []interface{}{gazeboSimulatorMainBinary, "--mesh_savepath=/mnt/gzserver-meshes"},
			}},
		}}},
	})

	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{statefulSet}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(adapted[0].Object, "spec", "template", "spec", "containers")
	args, _, _ := unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
	want := assetInstancesServiceAddressFlag + "=asset-instances-v1." + pilotNamespace + ".svc.cluster.local:" + assetInstancesServiceAddressPort
	count := 0
	for _, arg := range args {
		if arg == want {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("adapted args = %v, want exactly one %q", args, want)
	}

	adaptedAgain, err := adaptOpenShiftResources(adapted, pilotNamespace)
	if err != nil {
		t.Fatalf("second adaptation: %v", err)
	}
	containers, _, _ = unstructured.NestedSlice(adaptedAgain[0].Object, "spec", "template", "spec", "containers")
	args, _, _ = unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
	count = 0
	for _, arg := range args {
		if arg == want {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("second adaptation args = %v, want exactly one %q", args, want)
	}
}

func TestAdaptOpenShiftResourcesRejectsUnexpectedGazeboSimulatorImage(t *testing.T) {
	statefulSet := object("StatefulSet", gazeboSimulatorName, map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name": gazeboSimulatorContainerName, "image": "registry.invalid/other-image",
				"args": []interface{}{gazeboSimulatorMainBinary},
			}},
		}}},
	})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{statefulSet}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "unexpected image repository") {
		t.Fatalf("unexpected simulator image should fail closed, got %v", err)
	}
}

func TestAdaptOpenShiftResourcesInjectsSidecarForGazeboProxy(t *testing.T) {
	deployment := object("Deployment", gzserverDeploymentName, map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{
			"metadata": map[string]interface{}{"annotations": map[string]interface{}{"app-deployment-id": "test"}},
			"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{
				"name":  gzserverContainerName,
				"image": "quay.io/your-registry-org/intrinsic/" + gzserverImageRepository + "@sha256:0123456789abcdef",
				"args":  []interface{}{gzserverMainBinary, "--simulation_service_address=upstream.invalid:8088"},
			}}},
		}},
	})

	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	annotations, found, err := unstructured.NestedStringMap(got[0].Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found || annotations[istioSidecarInjectAnnotation] != "true" || annotations["app-deployment-id"] != "test" {
		t.Fatalf("Gazebo proxy annotations = %v, found=%t, err=%v", annotations, found, err)
	}
	containers, _, _ := unstructured.NestedSlice(got[0].Object, "spec", "template", "spec", "containers")
	args, _, _ := unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
	wantAddress := simulationServiceAddressFlag + "simulation-service." + pilotNamespace + ".svc.cluster.local:" + simulationServicePort
	if len(args) != 2 || args[1] != wantAddress {
		t.Fatalf("Gazebo proxy args = %v, want rewritten simulation service address %q", args, wantAddress)
	}
}
