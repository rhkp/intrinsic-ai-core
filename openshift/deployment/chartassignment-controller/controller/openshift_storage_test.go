// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestConfiguredStorageClassUsesCanonicalConfig(t *testing.T) {
	t.Setenv("OPENSHIFT_STORAGE_CLASS", "ignored-environment")
	useControllerConfig(t, &ControllerConfig{BlockStorageClass: "canonical-block", SharedStorageClass: "canonical-shared"})
	if configuredStorageClass(openshiftStorageClassEnv) != "canonical-block" || configuredStorageClass(openshiftSharedStorageClassEnv) != "canonical-shared" {
		t.Fatal("canonical storage was not used")
	}
}

func TestAdaptOpenShiftResourcesReplacesK3sDataStore(t *testing.T) {
	deployment := object("Deployment", "data-store", map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"automountServiceAccountToken": false,
					"securityContext":              map[string]interface{}{"runAsUser": int64(65532), "runAsGroup": int64(65532)},
					"containers": []interface{}{map[string]interface{}{
						"name":  "db",
						"image": "example.invalid/demo",
						"securityContext": map[string]interface{}{
							"runAsUser": int64(65532), "runAsGroup": int64(65532), "readOnlyRootFilesystem": true,
						},
					}},
					"volumes": []interface{}{map[string]interface{}{
						"name":     "intrinsic-db-dir",
						"hostPath": map[string]interface{}{"path": "/var/apps/intrinsic-db", "type": "DirectoryOrCreate"},
					}},
				},
			},
		},
	})
	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GetKind() != "Deployment" || got[1].GetKind() != "PersistentVolumeClaim" {
		t.Fatalf("adapted kinds = %v", kinds(got))
	}
	podSpec, _, _ := unstructured.NestedMap(got[0].Object, "spec", "template", "spec")
	if podSpec["serviceAccountName"] != openshiftImagePullServiceAccount || podSpec["automountServiceAccountToken"] != false {
		t.Fatalf("pod identity was not restricted: %#v", podSpec)
	}
	securityContext, _, _ := unstructured.NestedMap(podSpec, "securityContext")
	if _, exists := securityContext["runAsUser"]; exists {
		t.Fatal("fixed pod UID was retained")
	}
	containers, _, _ := unstructured.NestedSlice(podSpec, "containers")
	containerSC, _, _ := unstructured.NestedMap(containers[0].(map[string]interface{}), "securityContext")
	if _, exists := containerSC["runAsUser"]; exists {
		t.Fatal("fixed container UID was retained")
	}
	if containerSC["allowPrivilegeEscalation"] != false {
		t.Fatalf("allowPrivilegeEscalation = %#v", containerSC["allowPrivilegeEscalation"])
	}
	volumes, _, _ := unstructured.NestedSlice(podSpec, "volumes")
	volume := volumes[0].(map[string]interface{})
	if _, exists := volume["hostPath"]; exists {
		t.Fatal("hostPath was retained")
	}
	claim, found, _ := unstructured.NestedString(volume, "persistentVolumeClaim", "claimName")
	if !found || claim != dataStorePVC {
		t.Fatalf("PVC claim = %q, found=%v", claim, found)
	}
	if got[1].GetName() != dataStorePVC || got[1].GetNamespace() != pilotNamespace {
		t.Fatalf("data PVC identity = %s/%s", got[1].GetNamespace(), got[1].GetName())
	}
	storageClass, _, _ := unstructured.NestedString(got[1].Object, "spec", "storageClassName")
	if storageClass != openshiftStorageClassDefault {
		t.Fatalf("PVC storage class = %q", storageClass)
	}
}

func TestAdaptOpenShiftResourcesReplacesSimulationHostPaths(t *testing.T) {
	statefulSet := func(name string, volumes ...interface{}) *unstructured.Unstructured {
		container := map[string]interface{}{
			"name":  "service",
			"image": "example.invalid/service",
		}
		if name == "rs-icon" {
			container["name"] = "rs-icon"
		}
		if name == gazeboSimulatorName {
			container = map[string]interface{}{
				"name":  gazeboSimulatorContainerName,
				"image": "example.invalid/" + gazeboSimulatorImageRepository,
				"args":  []interface{}{gazeboSimulatorMainBinary},
			}
		}
		return object("StatefulSet", name, map[string]interface{}{
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{container},
						"volumes":    volumes,
					},
				},
			},
		})
	}
	iconVolume := func() interface{} {
		return map[string]interface{}{
			"name":     "intrinsic-icon",
			"hostPath": map[string]interface{}{"path": "/tmp/intrinsic_icon"},
		}
	}
	meshesVolume := map[string]interface{}{
		"name":     "gzserver-meshes-volume",
		"hostPath": map[string]interface{}{"path": "/tmp/service_volumes/intrinsic/gzserver-meshes"},
	}
	input := []*unstructured.Unstructured{
		statefulSet("rs-ur-module", iconVolume()),
		statefulSet("rs-icon", iconVolume()),
		statefulSet("rs-gazebo-simulator", iconVolume(), meshesVolume),
	}
	got, err := adaptOpenShiftResources(input, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("adapted resources = %v, want three StatefulSets and two PVCs", kinds(got))
	}
	wantClaims := []map[string]string{
		{"intrinsic-icon": intrinsicIconPVC},
		{"intrinsic-icon": intrinsicIconPVC},
		{"intrinsic-icon": intrinsicIconPVC, "gzserver-meshes-volume": gazeboMeshesPVC},
	}
	for i, want := range wantClaims {
		podSpec, found, err := unstructured.NestedMap(got[i].Object, "spec", "template", "spec")
		if err != nil || !found {
			t.Fatalf("%s pod spec missing: found=%v err=%v", got[i].GetName(), found, err)
		}
		volumes, found, err := unstructured.NestedSlice(podSpec, "volumes")
		if err != nil || !found {
			t.Fatalf("%s volumes missing: found=%v err=%v", got[i].GetName(), found, err)
		}
		for _, item := range volumes {
			volume := item.(map[string]interface{})
			volumeName := volume["name"].(string)
			if claimName, expected := want[volumeName]; expected {
				if _, exists := volume["hostPath"]; exists {
					t.Fatalf("%s retained hostPath volume %q", got[i].GetName(), volumeName)
				}
				gotClaim, found, err := unstructured.NestedString(volume, "persistentVolumeClaim", "claimName")
				if err != nil || !found || gotClaim != claimName {
					t.Fatalf("%s volume %q claim = %q, found=%v err=%v; want %q", got[i].GetName(), volumeName, gotClaim, found, err, claimName)
				}
			}
		}
	}
	wantPVCs := map[string]string{intrinsicIconPVC: "1Gi", gazeboMeshesPVC: "5Gi"}
	for _, resource := range got[3:] {
		if resource.GetKind() != "PersistentVolumeClaim" {
			t.Fatalf("generated storage resource = %s/%s", resource.GetKind(), resource.GetName())
		}
		wantSize, found := wantPVCs[resource.GetName()]
		if !found {
			t.Fatalf("unexpected generated PVC %q", resource.GetName())
		}
		class, _, _ := unstructured.NestedString(resource.Object, "spec", "storageClassName")
		modes, _, _ := unstructured.NestedStringSlice(resource.Object, "spec", "accessModes")
		size, _, _ := unstructured.NestedString(resource.Object, "spec", "resources", "requests", "storage")
		if class != openshiftSharedStorageClassDefault || len(modes) != 1 || modes[0] != "ReadWriteMany" || size != wantSize {
			t.Fatalf("PVC %q class=%q modes=%v size=%q", resource.GetName(), class, modes, size)
		}
		delete(wantPVCs, resource.GetName())
	}
	if len(wantPVCs) != 0 {
		t.Fatalf("missing generated PVCs: %v", wantPVCs)
	}

	unsupported := statefulSet("rs-other", iconVolume())
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{unsupported}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "/tmp/intrinsic_icon") {
		t.Fatalf("unapproved workload hostPath should fail closed, got %v", err)
	}
}

func TestAdaptOpenShiftResourcesUsesRecreateForPVCBackedDeployment(t *testing.T) {
	deployment := object("Deployment", "world", map[string]interface{}{
		"spec": map[string]interface{}{
			"strategy": map[string]interface{}{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]interface{}{"maxSurge": int64(1), "maxUnavailable": int64(0)},
			},
			"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{
					"name": "world", "image": "example.invalid/world", "args": []interface{}{"world_binary"},
				}},
				"volumes": []interface{}{map[string]interface{}{
					"name":                  "world-storage",
					"persistentVolumeClaim": map[string]interface{}{"claimName": "world-storage-claim"},
				}},
			}},
		},
	})
	got, err := adaptOpenShiftResourcesWithRouting([]*unstructured.Unstructured{deployment}, pilotNamespace, testRoutingConfig())
	if err != nil {
		t.Fatal(err)
	}
	strategy, found, err := unstructured.NestedMap(got[0].Object, "spec", "strategy")
	if err != nil || !found || strategy["type"] != "Recreate" {
		t.Fatalf("PVC-backed Deployment strategy = %#v, found=%t, err=%v; want Recreate", strategy, found, err)
	}
	if _, found := strategy["rollingUpdate"]; found {
		t.Fatalf("Recreate strategy retained rollingUpdate settings: %#v", strategy)
	}
}
