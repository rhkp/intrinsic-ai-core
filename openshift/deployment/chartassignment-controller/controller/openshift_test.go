package chartassignment

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const pilotNamespace = "arhkp-intrinsic"

func object(kind, name string, fields map[string]interface{}) *unstructured.Unstructured {
	value := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name},
	}
	for key, field := range fields {
		value[key] = field
	}
	return &unstructured.Unstructured{Object: value}
}

func protobufBytesField(number uint64, value []byte) []byte {
	var encoded [binary.MaxVarintLen64]byte
	fieldTagBytes := binary.PutUvarint(encoded[:], number<<3|2)
	result := append([]byte(nil), encoded[:fieldTagBytes]...)
	lengthBytes := binary.PutUvarint(encoded[:], uint64(len(value)))
	result = append(result, encoded[:lengthBytes]...)
	return append(result, value...)
}

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
				"image": "image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic/" + moveToContactSkillImageRepo + "@sha256:0cfc737f4670c9b9b486ac6e2ec137b8ac552e32a98df5479f8ad929a9898a5b",
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
			wantImage := "image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic/move-to-contact-openshift@sha256:79494450d82e41ca3f856eaf89a46396816e86b96e0c3269a57bbd1bd677e520"
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

func TestAdaptOpenShiftResourcesScopesRBAC(t *testing.T) {
	role := object("ClusterRole", "resource-registry", map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"apiGroups": []interface{}{""},
				"resources": []interface{}{"configmaps"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{""},
				"resources": []interface{}{"namespaces"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"registry.cloudrobotics.com"},
				"resources": []interface{}{"robots", "robots/status"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
		},
	})
	binding := object("ClusterRoleBinding", "resource-registry", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "resource-registry"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "resource-registry", "namespace": "default"}},
	})
	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{role, binding}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	if got[0].GetKind() != "Role" || got[0].GetNamespace() != pilotNamespace {
		t.Fatalf("role scope = %s/%s, want Role/%s", got[0].GetKind(), got[0].GetNamespace(), pilotNamespace)
	}
	rules, _, _ := unstructured.NestedSlice(got[0].Object, "rules")
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want only the project-scoped ConfigMap rule", len(rules))
	}
	resources, _, _ := unstructured.NestedStringSlice(rules[0].(map[string]interface{}), "resources")
	if len(resources) != 1 || resources[0] != "configmaps" {
		t.Fatalf("remaining role resources = %v", resources)
	}
	if got[1].GetKind() != "RoleBinding" || got[1].GetNamespace() != pilotNamespace {
		t.Fatalf("binding scope = %s/%s, want RoleBinding/%s", got[1].GetKind(), got[1].GetNamespace(), pilotNamespace)
	}
	kind, _, _ := unstructured.NestedString(got[1].Object, "roleRef", "kind")
	if kind != "Role" {
		t.Fatalf("roleRef.kind = %q, want Role", kind)
	}
	subjects, _, _ := unstructured.NestedSlice(got[1].Object, "subjects")
	subjectNS, _, _ := unstructured.NestedString(subjects[0].(map[string]interface{}), "namespace")
	if subjectNS != pilotNamespace {
		t.Fatalf("ServiceAccount subject namespace = %q", subjectNS)
	}
}

func TestAdaptOpenShiftResourcesPreprovisionsWorkcellRBAC(t *testing.T) {
	rules := []interface{}{
		map[string]interface{}{
			"apiGroups": []interface{}{"apps.cloudrobotics.com"},
			"resources": []interface{}{"chartassignments"},
			"verbs":     []interface{}{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"services", "configmaps"},
			"verbs":     []interface{}{"get", "list", "watch"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"pods"},
			"verbs":     []interface{}{"get", "list", "watch", "delete"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{"apps"},
			"resources": []interface{}{"deployments"},
			"verbs":     []interface{}{"get", "list", "watch"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{"batch"},
			"resources": []interface{}{"jobs"},
			"verbs":     []interface{}{"get", "list", "watch"},
		},
	}
	clusterRole := object("ClusterRole", "workcell-cluster-service", map[string]interface{}{"rules": rules})
	clusterRoleBinding := object("ClusterRoleBinding", "workcell-cluster-service", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "workcell-cluster-service"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "workcell-cluster-service", "namespace": "default"}},
	})
	projectRole := object("Role", "workcell-cluster-service", map[string]interface{}{"rules": rules})
	projectRoleBinding := object("RoleBinding", "workcell-cluster-service", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "workcell-cluster-service"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "workcell-cluster-service", "namespace": "default"}},
	})
	keep := object("ConfigMap", "unrelated-resource", map[string]interface{}{})

	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{clusterRole, clusterRoleBinding, projectRole, projectRoleBinding, keep}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GetKind() != "ConfigMap" || got[0].GetName() != "unrelated-resource" {
		t.Fatalf("adapted resources = %v, want only unrelated ConfigMap", kinds(got))
	}
}

func TestAdaptOpenShiftResourcesRejectsUnapprovedPreprovisionedWorkcellRBAC(t *testing.T) {
	role := object("Role", "workcell-cluster-service", map[string]interface{}{
		"rules": []interface{}{map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"pods"},
			"verbs":     []interface{}{"create"},
		}},
	})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{role}, pilotNamespace); err == nil {
		t.Fatal("unapproved Workcell RBAC rule was accepted")
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
	if storageClass != openshiftStorageClass {
		t.Fatalf("PVC storage class = %q", storageClass)
	}
}

func TestAdaptOpenShiftResourcesReplacesSimulationHostPaths(t *testing.T) {
	statefulSet := func(name string, volumes ...interface{}) *unstructured.Unstructured {
		container := map[string]interface{}{
			"name":  "service",
			"image": "example.invalid/service",
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
		if class != openshiftSharedStorageClass || len(modes) != 1 || modes[0] != "ReadWriteMany" || size != wantSize {
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

func TestAdaptOpenShiftResourcesDropsOnlyKnownSimulationSecurityRequests(t *testing.T) {
	container := func(name, image string, add []interface{}, privileged bool) map[string]interface{} {
		securityContext := map[string]interface{}{}
		if len(add) != 0 {
			securityContext["capabilities"] = map[string]interface{}{"add": add}
		}
		if privileged {
			securityContext["privileged"] = true
		}
		return map[string]interface{}{
			"name":            name,
			"image":           image,
			"securityContext": securityContext,
		}
	}
	statefulSet := func(name string, containers ...interface{}) *unstructured.Unstructured {
		return object("StatefulSet", name, map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": containers,
			}}},
		})
	}
	input := []*unstructured.Unstructured{
		statefulSet("rs-ur-module", container("rs-ur-module", "registry.example:5000/arhkp-intrinsic/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image@sha256:abcdef", nil, false)),
		statefulSet("rs-icon", container("rs-icon", "registry.example:5000/arhkp-intrinsic/ai.intrinsic.generic_realtime_control_service.generic_icon_machine_resource:demo", []interface{}{"SYS_NICE"}, false)),
		statefulSet("rs-motion-planner-service", container("rs-motion-planner-service", "registry.example:5000/arhkp-intrinsic/ai.intrinsic.motion_planner_service.motion-planner-service-image:demo", []interface{}{"SYS_RAWIO"}, false)),
		statefulSet("rs-hande-gripper", container("rs-hande-gripper", "registry.example:5000/arhkp-intrinsic/ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml:demo", nil, true)),
	}

	got, err := adaptOpenShiftResources(input, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range got {
		containers, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
		if err != nil || !found || len(containers) != 1 {
			t.Fatalf("%s containers: found=%v err=%v", resource.GetName(), found, err)
		}
		container := containers[0].(map[string]interface{})
		if privileged, _, _ := unstructured.NestedBool(container, "securityContext", "privileged"); privileged {
			t.Errorf("%s retained privileged=true", resource.GetName())
		}
		gotCapabilities, found, err := unstructured.NestedStringSlice(container, "securityContext", "capabilities", "add")
		if err != nil {
			t.Fatalf("%s capabilities.add is malformed: %v", resource.GetName(), err)
		}
		wantCapabilities := map[string][]string{
			"rs-ur-module":              {"IPC_LOCK"},
			"rs-icon":                   {"SYS_NICE", "DAC_OVERRIDE", "IPC_LOCK"},
			"rs-motion-planner-service": nil,
			"rs-hande-gripper":          nil,
		}[resource.GetName()]
		if !found && len(wantCapabilities) != 0 || found && strings.Join(gotCapabilities, ",") != strings.Join(wantCapabilities, ",") {
			t.Errorf("%s capabilities.add = %v (found=%v), want %v", resource.GetName(), gotCapabilities, found, wantCapabilities)
		}
		if allow, found, err := unstructured.NestedBool(container, "securityContext", "allowPrivilegeEscalation"); err != nil || !found || allow {
			t.Errorf("%s allowPrivilegeEscalation=%v found=%v err=%v, want false", resource.GetName(), allow, found, err)
		}
		serviceAccount, found, err := unstructured.NestedString(resource.Object, "spec", "template", "spec", "serviceAccountName")
		wantServiceAccount := "intrinsic-runtime"
		if resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon" {
			wantServiceAccount = intrinsicSimRealtimeServiceAccount
		}
		if err != nil || !found || serviceAccount != wantServiceAccount {
			t.Errorf("%s serviceAccountName=%q found=%v err=%v, want %q", resource.GetName(), serviceAccount, found, err, wantServiceAccount)
		}
		uid, uidFound, uidErr := unstructured.NestedInt64(container, "securityContext", "runAsUser")
		wantsRoot := resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon"
		if wantsRoot && (uidErr != nil || !uidFound || uid != 0) {
			t.Errorf("%s runAsUser=%d found=%v err=%v, want explicit UID 0", resource.GetName(), uid, uidFound, uidErr)
		}
		if !wantsRoot && (uidErr != nil || uidFound) {
			t.Errorf("%s runAsUser=%d found=%v err=%v, want no explicit UID", resource.GetName(), uid, uidFound, uidErr)
		}
		if resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon" {
			for _, requirement := range []struct {
				field string
				name  string
				want  string
			}{
				{field: "requests", name: "cpu", want: "1"},
				{field: "requests", name: "memory", want: "1Gi"},
				{field: "limits", name: "cpu", want: "4"},
				{field: "limits", name: "memory", want: "4Gi"},
			} {
				got, found, err := unstructured.NestedString(container, "resources", requirement.field, requirement.name)
				if err != nil || !found || got != requirement.want {
					t.Errorf("%s resource %s.%s=%q found=%v err=%v, want %q", resource.GetName(), requirement.field, requirement.name, got, found, err, requirement.want)
				}
			}
		}
		if resource.GetName() == "rs-hande-gripper" {
			env, _, _ := unstructured.NestedSlice(container, "env")
			gotEnv := map[string]string{}
			for _, item := range env {
				entry := item.(map[string]interface{})
				gotEnv[entry["name"].(string)] = entry["value"].(string)
			}
			if gotEnv["HOME"] != "/tmp" || gotEnv["ROS_HOME"] != "/tmp/.ros" {
				t.Errorf("Hand-E simulator HOME=%q ROS_HOME=%q; want /tmp and /tmp/.ros", gotEnv["HOME"], gotEnv["ROS_HOME"])
			}
		}
	}
}

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

	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{input}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("adapted resources = %v, want only gzserver Deployment", kinds(got))
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

	// Reconciliation is idempotent when the deployment already contains the
	// project-local endpoint.
	gotAgain, err := adaptOpenShiftResources(got, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	check(gotAgain[0])

	wrongImage := deployment("registry.example/unreviewed-gzserver:latest", gzserverMainBinary)
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{wrongImage}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "unexpected image repository") {
		t.Fatalf("unreviewed gzserver image should fail closed, got %v", err)
	}
}

func TestAdaptOpenShiftResourcesRejectsUnapprovedSimulationSecurityRequests(t *testing.T) {
	statefulSet := func(name, containerName, image string, add []interface{}, privileged bool) *unstructured.Unstructured {
		securityContext := map[string]interface{}{}
		if len(add) != 0 {
			securityContext["capabilities"] = map[string]interface{}{"add": add}
		}
		if privileged {
			securityContext["privileged"] = true
		}
		return object("StatefulSet", name, map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{
					"name": containerName, "image": image, "securityContext": securityContext,
				}},
			}}},
		})
	}
	tests := []struct {
		name     string
		resource *unstructured.Unstructured
		wantErr  string
	}{
		{
			name:     "unexpected capability on a simulation image",
			resource: statefulSet("rs-ur-module", "rs-ur-module", "registry/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image:demo", []interface{}{"IPC_LOCK", "SYS_ADMIN"}, false),
			wantErr:  "SYS_ADMIN",
		},
		{
			name:     "capability on an unreviewed workload",
			resource: statefulSet("rs-other", "rs-other", "registry/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image:demo", []interface{}{"IPC_LOCK"}, false),
			wantErr:  "IPC_LOCK",
		},
		{
			name:     "capability on an unreviewed image",
			resource: statefulSet("rs-ur-module", "rs-ur-module", "registry/ai.intrinsic.other_ur_module_image:demo", []interface{}{"IPC_LOCK"}, false),
			wantErr:  "IPC_LOCK",
		},
		{
			name:     "privileged non-simulation image",
			resource: statefulSet("rs-hande-gripper", "rs-hande-gripper", "registry/ai.intrinsic.hande-gripper-hardware-driver:demo", nil, true),
			wantErr:  "privileged containers are not supported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := adaptOpenShiftResources([]*unstructured.Unstructured{test.resource}, pilotNamespace)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestAdaptOpenShiftResourcesRewritesDirectImagesToLockedQuayDigests(t *testing.T) {
	deployment := object("Deployment", "direct-image-check", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{
					"name":  "zenohd",
					"image": "us-central1-docker.pkg.dev/intrinsic-mirror/intrinsic-build-images/zenohd:1.7.2",
				},
				map[string]interface{}{
					"name":  "jupyter-server",
					"image": "ghcr.io/intrinsic-ai/code-execution-jupyter-server@sha256:e14b4e15b1b8341671c372eeadc328b25663c506827dc47e2e60e0f7b7ef1f2c",
				},
			},
		}}},
	})
	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(got[0].Object, "spec", "template", "spec", "containers")
	for i, want := range []string{quayZenohdImage, quayJupyterServerImage} {
		image, _, _ := unstructured.NestedString(containers[i].(map[string]interface{}), "image")
		if image != want {
			t.Errorf("container %d image = %q, want locked Quay digest %q", i, image, want)
		}
	}
}

func TestAdaptOpenShiftResourcesPinsNamespaceScopedServiceImages(t *testing.T) {
	registry := object("Deployment", "resource-registry", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "resource-registry",
				"image": "upstream.example/resource-registry:stale",
				"args": []interface{}{
					"--port=8080",
					"--configmap_watch_namespace", "$(POD_NAMESPACE)",
					"--configmap_watch_namespace=stale",
				},
			}},
		}}},
	})
	workcell := object("Deployment", "workcell-cluster-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "workcell-cluster-service",
				"image": "upstream.example/workcell-service:stale",
				"args":  []interface{}{"--conductor_service_address=conductor.app-intrinsic-base.svc.cluster.local:8082"},
			}},
		}}},
	})

	assertAdapted := func(resources []*unstructured.Unstructured) {
		t.Helper()
		for _, test := range []struct {
			name      string
			image     string
			watchFlag bool
		}{
			{name: "resource-registry", image: quayResourceRegistryImage, watchFlag: true},
			{name: "workcell-cluster-service", image: workcellServiceImage},
		} {
			var deployment *unstructured.Unstructured
			for _, resource := range resources {
				if resource.GetName() == test.name {
					deployment = resource
					break
				}
			}
			if deployment == nil {
				t.Fatalf("Deployment %q is missing", test.name)
			}
			containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
			if err != nil || !found || len(containers) != 1 {
				t.Fatalf("Deployment %q containers = %#v, found = %t, err = %v", test.name, containers, found, err)
			}
			container := containers[0].(map[string]interface{})
			image, _, _ := unstructured.NestedString(container, "image")
			if image != test.image {
				t.Errorf("Deployment %q image = %q, want %q", test.name, image, test.image)
			}
			if test.watchFlag {
				args, _, err := unstructured.NestedStringSlice(container, "args")
				if err != nil {
					t.Fatalf("Deployment %q args: %v", test.name, err)
				}
				count := 0
				for _, arg := range args {
					if strings.HasPrefix(arg, "--configmap_watch_namespace") {
						count++
						if arg != "--configmap_watch_namespace="+pilotNamespace {
							t.Errorf("unexpected ConfigMap watch argument %q", arg)
						}
					}
				}
				if count != 1 {
					t.Errorf("ConfigMap namespace argument count = %d, want exactly 1; args = %v", count, args)
				}
			}
		}
	}

	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{registry, workcell}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertAdapted(adapted)

	adaptedAgain, err := adaptOpenShiftResources(adapted, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertAdapted(adaptedAgain)
}

func TestAdaptOpenShiftResourcesConfiguresJupyterWritableHome(t *testing.T) {
	jupyter := object("Deployment", "code-execution", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{"name": "code-execution-service", "image": "example.invalid/code-execution"},
				map[string]interface{}{
					"name":  "jupyter-server",
					"image": "example.invalid/jupyter",
					"command": []interface{}{
						"jupyter", "server", "--ip=0.0.0.0",
					},
					"args": []interface{}{
						"--ip=0.0.0.0",
						"--port=8888",
						"--ip", "0.0.0.0",
					},
					"env": []interface{}{
						map[string]interface{}{"name": "HOME", "value": "/"},
						map[string]interface{}{"name": "PATH", "value": "/usr/bin"},
						map[string]interface{}{"name": "HOME", "value": "/root"},
						map[string]interface{}{"name": "JUPYTER_RUNTIME_DIR", "value": "/.local/share/jupyter/runtime"},
					},
					"volumeMounts": []interface{}{map[string]interface{}{
						"name":      "jupyter-home",
						"mountPath": jupyterHomeDir,
					}},
				},
			},
			"volumes": []interface{}{map[string]interface{}{
				"name":     "jupyter-home",
				"emptyDir": map[string]interface{}{"sizeLimit": "2Gi"},
			}},
		}}},
	})

	assertJupyterHome := func(resources []*unstructured.Unstructured) {
		t.Helper()
		var deployment *unstructured.Unstructured
		for _, resource := range resources {
			if resource.GetKind() == "Deployment" && resource.GetName() == "code-execution" {
				deployment = resource
				break
			}
		}
		if deployment == nil {
			t.Fatal("code-execution Deployment is missing")
		}
		containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
		if err != nil || !found || len(containers) != 2 {
			t.Fatalf("code-execution containers = %#v, found = %t, err = %v", containers, found, err)
		}
		var jupyterContainer map[string]interface{}
		for _, item := range containers {
			container := item.(map[string]interface{})
			if container["name"] == "jupyter-server" {
				jupyterContainer = container
			}
		}
		if jupyterContainer == nil {
			t.Fatal("jupyter-server container is missing")
		}
		command, found, err := unstructured.NestedStringSlice(jupyterContainer, "command")
		if err != nil || !found || strings.Join(command, " ") != "jupyter server" {
			t.Fatalf("Jupyter command = %#v, found = %t, err = %v; want [jupyter server]", command, found, err)
		}
		args, found, err := unstructured.NestedStringSlice(jupyterContainer, "args")
		if err != nil || !found {
			t.Fatalf("Jupyter arguments = %#v, found = %t, err = %v", args, found, err)
		}
		wantArgs := []string{
			"--ip=::1",
			"--port=8888",
			"--IdentityProvider.token=''",
			"--ServerApp.disable_check_xsrf=True",
			"--notebook-dir=" + jupyterHomeDir,
		}
		if strings.Join(args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Errorf("Jupyter args = %v, want %v", args, wantArgs)
		}
		env, found, err := unstructured.NestedSlice(jupyterContainer, "env")
		if err != nil || !found {
			t.Fatalf("Jupyter environment = %#v, found = %t, err = %v", env, found, err)
		}
		wantEnv := map[string]string{"HOME": jupyterHomeDir, "JUPYTER_RUNTIME_DIR": jupyterRuntimeDir, "PATH": "/usr/bin"}
		counts := make(map[string]int)
		for _, item := range env {
			entry := item.(map[string]interface{})
			name, _ := entry["name"].(string)
			counts[name]++
			if want, ok := wantEnv[name]; ok && entry["value"] != want {
				t.Errorf("Jupyter env %s = %v, want %q", name, entry["value"], want)
			}
		}
		for name := range wantEnv {
			if counts[name] != 1 {
				t.Errorf("Jupyter env %s appears %d times, want exactly once", name, counts[name])
			}
		}

		mounts, found, err := unstructured.NestedSlice(jupyterContainer, "volumeMounts")
		if err != nil || !found || len(mounts) != 1 || mounts[0].(map[string]interface{})["mountPath"] != jupyterHomeDir {
			t.Fatalf("Jupyter home mount = %#v, found = %t, err = %v", mounts, found, err)
		}
	}

	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{jupyter}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertJupyterHome(adapted)

	adaptedAgain, err := adaptOpenShiftResources(adapted, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertJupyterHome(adaptedAgain)
}

func TestAdaptOpenShiftResourcesRequiresWritableJupyterHome(t *testing.T) {
	for _, test := range []struct {
		name       string
		mountPath  string
		readOnly   bool
		wantErrMsg string
	}{
		{name: "missing home mount", mountPath: "/home/other", wantErrMsg: "expected writable emptyDir mount"},
		{name: "read-only home mount", mountPath: jupyterHomeDir, readOnly: true, wantErrMsg: "is read-only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			jupyter := object("Deployment", "code-execution", map[string]interface{}{
				"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{
						"name": "jupyter-server",
						"volumeMounts": []interface{}{map[string]interface{}{
							"name":      "jupyter-home",
							"mountPath": test.mountPath,
							"readOnly":  test.readOnly,
						}},
					}},
					"volumes": []interface{}{map[string]interface{}{
						"name":     "jupyter-home",
						"emptyDir": map[string]interface{}{},
					}},
				}}},
			})
			_, err := adaptOpenShiftResources([]*unstructured.Unstructured{jupyter}, pilotNamespace)
			if err == nil || !strings.Contains(err.Error(), test.wantErrMsg) {
				t.Fatalf("adaptOpenShiftResources() error = %v, want substring %q", err, test.wantErrMsg)
			}
		})
	}
}

func TestAdaptOpenShiftResourcesRetargetsVirtualServiceAndRemovesK3sOnlyObjects(t *testing.T) {
	routing := testRoutingConfig()
	resources := []*unstructured.Unstructured{
		object("Deployment", "artifacts-deployment", map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{
					"name":         "artifacts-deployment",
					"image":        "quay.io/rhkp/intrinsic/artifacts_service_ap6rsu7y7q2zdhlk@sha256:0976d6fd4ce4916d8388c48709604f79a19e59c7d4adb1adb5a669f9066c0b99",
					"args":         []interface{}{"intrinsic/storage/artifacts/artifact_service", "--registry_port=9090", "--containerd_namespace=k8s.io"},
					"ports":        []interface{}{map[string]interface{}{"name": "http-registry", "containerPort": int64(9090), "hostPort": int64(17127)}},
					"volumeMounts": []interface{}{map[string]interface{}{"name": "containerd-socket", "mountPath": "/run/containerd/containerd.sock"}},
				}},
				"volumes": []interface{}{map[string]interface{}{"name": "containerd-socket", "hostPath": map[string]interface{}{"path": "/run/k3s/containerd/containerd.sock"}}},
			}}},
		}),
		object("Service", "artifacts-deployment", nil),
		object("ServiceMonitor", "artifacts-deployment-metrics", nil),
		object("Service", "zenoh-router", map[string]interface{}{"spec": map[string]interface{}{
			"type": "ClusterIP",
		}}),
		object("VirtualService", "zenoh-router", map[string]interface{}{"spec": map[string]interface{}{
			"hosts":    []interface{}{"*"},
			"gateways": []interface{}{"app-ingress/gateway"},
			"tcp": []interface{}{map[string]interface{}{
				"match": []interface{}{map[string]interface{}{"port": int64(7447)}},
				"route": []interface{}{map[string]interface{}{"destination": map[string]interface{}{"host": "zenoh-router"}}},
			}},
		}}),
		object("VirtualService", "internal-api", map[string]interface{}{"spec": map[string]interface{}{
			"hosts":    []interface{}{"*"},
			"gateways": []interface{}{"app-ingress/gateway"},
			"http": []interface{}{map[string]interface{}{
				"route": []interface{}{map[string]interface{}{"destination": map[string]interface{}{"host": "internal-api", "port": map[string]interface{}{"number": int64(8080)}}}},
			}},
		}}),
		object("PersistentVolume", "local-storage", nil),
		object("ClusterRoleBinding", "zenoh-router-sa-xfa-config-ipcidentity-fetcher", nil),
	}
	got, err := adaptOpenShiftResourcesWithRouting(resources, pilotNamespace, routing)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].GetKind() != "Deployment" || got[0].GetName() != "artifacts-deployment" || got[1].GetKind() != "Service" || got[1].GetName() != "artifacts-deployment" || got[2].GetKind() != "Service" || got[2].GetName() != "zenoh-router" || got[3].GetKind() != "VirtualService" {
		t.Fatalf("adapted objects = %v, want adapted ArtifactService objects and the project-scoped Zenoh resources", kinds(got))
	}
	gateways, _, _ := unstructured.NestedStringSlice(got[3].Object, "spec", "gateways")
	if len(gateways) != 1 || gateways[0] != routing.gateway {
		t.Fatalf("gateways = %v, want %q", gateways, routing.gateway)
	}
	exportTo, _, _ := unstructured.NestedStringSlice(got[3].Object, "spec", "exportTo")
	if len(exportTo) != 2 || exportTo[0] != "." || exportTo[1] != "mesh-system" {
		t.Fatalf("exportTo = %v, want project and ingress-Gateway namespaces", exportTo)
	}
}

func TestAdaptOpenShiftResourcesRewritesIngressAddressAndRejectsExternalVirtualService(t *testing.T) {
	routing := testRoutingConfig()
	deployment := object("Deployment", "resource-client", map[string]interface{}{"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
		"containers": []interface{}{map[string]interface{}{"name": "client", "args": []interface{}{upstreamIngressAddress}}},
	}}}})
	got, err := adaptOpenShiftResourcesWithRouting([]*unstructured.Unstructured{deployment}, pilotNamespace, routing)
	if err != nil {
		t.Fatal(err)
	}
	annotations, found, err := unstructured.NestedStringMap(got[0].Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found || annotations[istioSidecarInjectAnnotation] != "true" {
		t.Fatalf("gateway client sidecar annotation = %v, found=%t, err=%v", annotations, found, err)
	}
	containers, _, _ := unstructured.NestedSlice(got[0].Object, "spec", "template", "spec", "containers")
	args, _, _ := unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
	if len(args) != 1 || args[0] != routing.address {
		t.Fatalf("ingress argument = %v, want configured in-cluster Service", args)
	}
	env, _, _ := unstructured.NestedSlice(containers[0].(map[string]interface{}), "env")
	if len(env) != 1 || env[0].(map[string]interface{})["name"] != ingressAddressEnv {
		t.Fatalf("configured ingress environment was not injected: %#v", env)
	}
	external := object("VirtualService", "external-destination", map[string]interface{}{"spec": map[string]interface{}{
		"hosts":    []interface{}{"*"},
		"gateways": []interface{}{"app-ingress/gateway"},
		"http": []interface{}{map[string]interface{}{
			"route": []interface{}{map[string]interface{}{"destination": map[string]interface{}{"host": "outside.example.invalid"}}},
		}},
	}})
	if _, err := adaptOpenShiftResourcesWithRouting([]*unstructured.Unstructured{external}, pilotNamespace, routing); err == nil || !strings.Contains(err.Error(), "destination must be a Service") {
		t.Fatalf("external VirtualService destination should be rejected, got %v", err)
	}
}

func testRoutingConfig() openshiftRoutingConfig {
	return openshiftRoutingConfig{
		address:         "istio-ingressgateway.mesh-system.svc.cluster.local:80",
		gateway:         "mesh-system/gateway",
		serviceSelector: map[string]interface{}{"app": "istio-ingressgateway"},
	}
}

func TestRoutingConfigRequiresVerifiedInternalGatewayDetails(t *testing.T) {
	t.Setenv(ingressAddressEnv, "istio-ingressgateway.mesh-system.svc.cluster.local:80")
	t.Setenv(ingressGatewayEnv, "mesh-system/gateway")
	t.Setenv(ingressSelectorEnv, `{"app":"istio-ingressgateway"}`)
	routing := routingConfigFromEnvironment()
	if err := routing.validate(); err != nil {
		t.Fatal(err)
	}
	if routing.serviceNamespace != "mesh-system" || routing.gatewayNamespace != "mesh-system" || routing.serviceSelector["app"] != "istio-ingressgateway" {
		t.Fatalf("routing configuration was not fully resolved")
	}
	for _, address := range []string{
		"istio-ingressgateway.mesh-system.svc.cluster.local:443",
		"alias.istio-ingressgateway.mesh-system.svc.cluster.local:443",
		"bad_name.mesh-system.svc.cluster.local:443",
	} {
		t.Setenv(ingressAddressEnv, address)
		invalidRouting := routingConfigFromEnvironment()
		if err := invalidRouting.validate(); err == nil {
			t.Fatalf("invalid internal ingress address %q was accepted", address)
		}
	}
}

func TestAdaptOpenShiftResourcesRewritesNamespacesAndInternalizesServices(t *testing.T) {
	service := object("Service", "http-gateway-ice", map[string]interface{}{
		"spec": map[string]interface{}{
			"type":  "NodePort",
			"ports": []interface{}{map[string]interface{}{"port": int64(32123), "nodePort": int64(32123)}},
		},
	})
	deployment := object("Deployment", "example", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{"name": "c", "image": "example.invalid/demo", "args": []interface{}{"service.app-intrinsic-base.svc.cluster.local:8080"}}},
		}}},
	})
	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{service, deployment}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	typ, _, _ := unstructured.NestedString(got[0].Object, "spec", "type")
	ports, _, _ := unstructured.NestedSlice(got[0].Object, "spec", "ports")
	if typ != "ClusterIP" {
		t.Fatalf("Service type = %q", typ)
	}
	if _, exists := ports[0].(map[string]interface{})["nodePort"]; exists {
		t.Fatal("nodePort survived")
	}
	containers, _, _ := unstructured.NestedSlice(got[1].Object, "spec", "template", "spec", "containers")
	args, _, _ := unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
	if len(args) != 1 || !strings.Contains(args[0], "service."+pilotNamespace+".svc.cluster.local") {
		t.Fatalf("service DNS was not rewritten: %v", args)
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

func TestAdaptOpenShiftResourcesInjectsSidecarForGatewayRoutedBackend(t *testing.T) {
	workcell := object("Deployment", "workcell-cluster-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{
			"metadata": map[string]interface{}{"labels": map[string]interface{}{"app": "workcell-cluster-service"}},
			"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{
				"name": "workcell-cluster-service", "image": "example.invalid/workcell",
				"args": []interface{}{"--conductor_service_address=conductor.app-intrinsic-base.svc.cluster.local:8082"},
			}}},
		}},
	})
	unrouted := object("Deployment", "unrouted", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{
			"metadata": map[string]interface{}{"labels": map[string]interface{}{"app": "unrouted"}},
			"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{
				"name": "unrouted", "image": "example.invalid/unrouted",
			}}},
		}},
	})
	runtimeDB := object("Deployment", "runtime-db", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{
			"metadata": map[string]interface{}{"labels": map[string]interface{}{"app": "runtime-db"}},
			"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{
				"name": "runtime-db", "image": "example.invalid/runtime-db",
			}}},
		}},
	})
	service := object("Service", "asset-instances-v1", map[string]interface{}{
		"spec": map[string]interface{}{
			"type":     "ClusterIP",
			"selector": map[string]interface{}{"app": "workcell-cluster-service"},
			"ports": []interface{}{map[string]interface{}{
				"name": "grpc-asset-instances-v1", "port": int64(8080), "targetPort": int64(9832),
			}},
		},
	})
	virtualService := object("VirtualService", "installer", map[string]interface{}{
		"spec": map[string]interface{}{
			"hosts": []interface{}{"*"},
			"http": []interface{}{map[string]interface{}{
				"route": []interface{}{map[string]interface{}{"destination": map[string]interface{}{
					"host": "asset-instances-v1",
					"port": map[string]interface{}{"number": int64(8080)},
				}}},
			}},
		},
	})

	got, err := adaptOpenShiftResourcesWithRouting([]*unstructured.Unstructured{workcell, unrouted, runtimeDB, service, virtualService}, pilotNamespace, testRoutingConfig())
	if err != nil {
		t.Fatal(err)
	}
	find := func(kind, name string) *unstructured.Unstructured {
		t.Helper()
		for _, resource := range got {
			if resource.GetKind() == kind && resource.GetName() == name {
				return resource
			}
		}
		t.Fatalf("%s/%s not found", kind, name)
		return nil
	}
	annotations, found, err := unstructured.NestedStringMap(find("Deployment", "workcell-cluster-service").Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found || annotations[istioSidecarInjectAnnotation] != "true" {
		t.Fatalf("Gateway-routed workload sidecar annotation = %v, found=%t, err=%v", annotations, found, err)
	}
	annotations, found, err = unstructured.NestedStringMap(find("Deployment", "unrouted").Object, "spec", "template", "metadata", "annotations")
	if err != nil || found && annotations[istioSidecarInjectAnnotation] == "true" {
		t.Fatalf("unrouted workload was unnecessarily sidecar-injected: %v, err=%v", annotations, err)
	}
	annotations, found, err = unstructured.NestedStringMap(find("Deployment", "runtime-db").Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found || annotations[istioSidecarInjectAnnotation] != "true" {
		t.Fatalf("runtime DB workload sidecar annotation = %v, found=%t, err=%v", annotations, found, err)
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

func TestAdaptOpenShiftResourcesRewritesIngressAndNetworkPolicyReferences(t *testing.T) {
	workcell := object("Deployment", "workcell-cluster-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "workcell-cluster-service",
				"image": "upstream.example/workcell-service:stale",
				"args": []interface{}{
					"--sim_service_address=istio-ingressgateway.app-ingress.svc.cluster.local:80",
					"--conductor_service_address=conductor.app-intrinsic-base.svc.cluster.local:8082",
				},
			}},
		}}},
	})
	solution := object("Deployment", "solution-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{"name": "service", "args": []interface{}{
				"--skill_registry_address=istio-ingressgateway.app-ingress.svc.cluster.local:80",
				"--installed_assets_service_address=istio-ingressgateway.app-ingress.svc.cluster.local:80",
			}}},
		}}},
	})
	policy := object("NetworkPolicy", "code-execution", map[string]interface{}{
		"spec": map[string]interface{}{
			"ingress": []interface{}{
				map[string]interface{}{"from": []interface{}{map[string]interface{}{
					"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "app-intrinsic-app-chart"}},
					"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"app": "executive"}},
				}}},
				map[string]interface{}{"from": []interface{}{map[string]interface{}{
					"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "app-ingress"}},
					"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"app": "istio-ingressgateway"}},
				}}},
			},
			"egress": []interface{}{map[string]interface{}{"to": []interface{}{map[string]interface{}{
				"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "app-intrinsic-base"}},
				"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"app": "world"}},
			}}}},
		},
	})
	got, err := adaptOpenShiftResourcesWithRouting([]*unstructured.Unstructured{workcell, solution, policy}, pilotNamespace, testRoutingConfig())
	if err != nil {
		t.Fatal(err)
	}
	argsFor := func(index int) []string {
		t.Helper()
		containers, _, _ := unstructured.NestedSlice(got[index].Object, "spec", "template", "spec", "containers")
		args, _, _ := unstructured.NestedStringSlice(containers[0].(map[string]interface{}), "args")
		return args
	}
	if gotArgs := strings.Join(argsFor(0), " "); !strings.Contains(gotArgs, "simulation-service."+pilotNamespace+".svc.cluster.local:8088") || strings.Contains(gotArgs, "app-ingress") {
		t.Fatalf("simulation address was not rewritten: %v", gotArgs)
	}
	if gotArgs := strings.Join(argsFor(1), " "); !strings.Contains(gotArgs, "skill-registry."+pilotNamespace+".svc.cluster.local:8080") || !strings.Contains(gotArgs, "workcell-cluster-service."+pilotNamespace+".svc.cluster.local:9957") || strings.Contains(gotArgs, "app-ingress") {
		t.Fatalf("solution service addresses were not rewritten: %v", gotArgs)
	}
	ingress, _, _ := unstructured.NestedSlice(got[2].Object, "spec", "ingress")
	if len(ingress) != 2 {
		t.Fatalf("ingress rules = %d, want the in-project and mesh-gateway rules", len(ingress))
	}
	foundGatewayPeer := false
	for _, rule := range ingress {
		from, _, _ := unstructured.NestedSlice(rule.(map[string]interface{}), "from")
		for _, item := range from {
			peer := item.(map[string]interface{})
			namespaceName, found, _ := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
			if found && namespaceName == "mesh-system" {
				foundGatewayPeer = true
			}
		}
	}
	if !foundGatewayPeer {
		t.Fatal("NetworkPolicy ingress did not retain the Service Mesh gateway peer")
	}
	if !containsStringValue(got[2].Object, "executive") || !containsStringValue(got[2].Object, "world") {
		t.Fatal("same-project pod selectors were lost")
	}
	egress, _, _ := unstructured.NestedSlice(got[2].Object, "spec", "egress")
	controlPlaneEgress := false
	for _, item := range egress {
		rule, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		matches, err := matchesServiceMeshControlPlaneEgress(rule)
		if err != nil {
			t.Fatal(err)
		}
		controlPlaneEgress = controlPlaneEgress || matches
	}
	if !controlPlaneEgress {
		t.Fatal("restricted gateway client policy does not allow TLS xDS to the mesh control plane")
	}
}

func TestAdaptNetworkPolicyRetargetsIngressPeerWithoutOpeningTraffic(t *testing.T) {
	routing := testRoutingConfig()
	if err := routing.validate(); err != nil {
		t.Fatal(err)
	}
	policy := object("NetworkPolicy", "gateway-only", map[string]interface{}{
		"spec": map[string]interface{}{"ingress": []interface{}{map[string]interface{}{"from": []interface{}{map[string]interface{}{
			"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "app-ingress"}},
			"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"app": "istio-ingressgateway"}},
		}}}}},
	})
	if err := adaptNetworkPolicy(policy, routing); err != nil {
		t.Fatal(err)
	}
	ingress, _, _ := unstructured.NestedSlice(policy.Object, "spec", "ingress")
	if len(ingress) != 1 {
		t.Fatalf("mesh gateway rule was dropped: %#v", ingress)
	}
	from, _, _ := unstructured.NestedSlice(ingress[0].(map[string]interface{}), "from")
	peer := from[0].(map[string]interface{})
	namespaceName, _, _ := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
	podSelector, found, _ := unstructured.NestedMap(peer, "podSelector", "matchLabels")
	if namespaceName != "mesh-system" || !found || podSelector["app"] != "istio-ingressgateway" {
		t.Fatalf("gateway peer is not correctly restricted: %#v", peer)
	}
}

func TestAdaptNetworkPolicyRetargetsGatewayEgressPort(t *testing.T) {
	routing := testRoutingConfig()
	if err := routing.validate(); err != nil {
		t.Fatal(err)
	}
	policy := object("NetworkPolicy", "gateway-client", map[string]interface{}{
		"spec": map[string]interface{}{
			"policyTypes": []interface{}{"Egress"},
			"egress": []interface{}{map[string]interface{}{
				"to": []interface{}{map[string]interface{}{
					"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "app-ingress"}},
					"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"app": "istio-ingressgateway"}},
				}},
				"ports": []interface{}{map[string]interface{}{"protocol": "TCP", "port": int64(8080)}},
			}},
		},
	})
	if err := adaptNetworkPolicy(policy, routing); err != nil {
		t.Fatal(err)
	}
	egress, _, _ := unstructured.NestedSlice(policy.Object, "spec", "egress")
	if len(egress) == 0 {
		t.Fatal("gateway egress rule was dropped")
	}
	rule := egress[0].(map[string]interface{})
	ports, _, _ := unstructured.NestedSlice(rule, "ports")
	port, found, _ := unstructured.NestedInt64(ports[0].(map[string]interface{}), "port")
	if !found || port != 443 {
		t.Fatalf("gateway egress port = %d, found=%t; want secure listener port 443", port, found)
	}
	to, _, _ := unstructured.NestedSlice(rule, "to")
	peer := to[0].(map[string]interface{})
	namespaceName, _, _ := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
	if namespaceName != routing.serviceNamespace {
		t.Fatalf("gateway namespace = %q, want %q", namespaceName, routing.serviceNamespace)
	}
}

func TestAdaptNetworkPolicyAllowsOnlyServiceMeshControlPlaneEgress(t *testing.T) {
	routing := testRoutingConfig()
	if err := routing.validate(); err != nil {
		t.Fatal(err)
	}
	policy := object("NetworkPolicy", "restricted-client", map[string]interface{}{
		"spec": map[string]interface{}{
			"policyTypes": []interface{}{"Egress"},
			"egress":      []interface{}{},
		},
	})
	for range 2 { // reconciliation must not append duplicate grants.
		if err := adaptNetworkPolicy(policy, routing); err != nil {
			t.Fatal(err)
		}
	}
	egress, found, err := unstructured.NestedSlice(policy.Object, "spec", "egress")
	if err != nil || !found || len(egress) != 1 {
		t.Fatalf("egress rules = %#v, found=%t, err=%v; want one idempotent xDS rule", egress, found, err)
	}
	rule, ok := egress[0].(map[string]interface{})
	if !ok {
		t.Fatalf("egress rule is malformed: %#v", egress[0])
	}
	matches, err := matchesServiceMeshControlPlaneEgress(rule)
	if err != nil || !matches {
		t.Fatalf("egress rule does not narrowly select Service Mesh xDS: matches=%t, err=%v", matches, err)
	}
	ports, _, _ := unstructured.NestedSlice(rule, "ports")
	port := ports[0].(map[string]interface{})
	portNumber, found, _ := unstructured.NestedInt64(port, "port")
	if !found || portNumber != 15012 {
		t.Fatalf("control-plane port = %d, found=%t; want TLS xDS port 15012", portNumber, found)
	}
}

func TestAdaptNetworkPolicyRetargetsUpstreamDNSToOpenShiftResolver(t *testing.T) {
	routing := testRoutingConfig()
	if err := routing.validate(); err != nil {
		t.Fatal(err)
	}
	policy := object("NetworkPolicy", "restricted-client", map[string]interface{}{
		"spec": map[string]interface{}{
			"policyTypes": []interface{}{"Egress"},
			"egress": []interface{}{map[string]interface{}{
				"to": []interface{}{map[string]interface{}{
					"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "kube-system"}},
					"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"k8s-app": "kube-dns"}},
				}},
				"ports": []interface{}{map[string]interface{}{"protocol": "UDP", "port": int64(53)}},
			}},
		},
	})
	for range 2 {
		if err := adaptNetworkPolicy(policy, routing); err != nil {
			t.Fatal(err)
		}
	}
	egress, _, _ := unstructured.NestedSlice(policy.Object, "spec", "egress")
	var dnsRule map[string]interface{}
	for _, item := range egress {
		rule, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		peers, _, _ := unstructured.NestedSlice(rule, "to")
		for _, peerItem := range peers {
			peer, ok := peerItem.(map[string]interface{})
			if !ok {
				continue
			}
			namespaceName, _, _ := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
			labels, _, _ := unstructured.NestedStringMap(peer, "podSelector", "matchLabels")
			if namespaceName == openshiftDNSNamespace && labels[openshiftDNSPodLabel] == openshiftDNSPodValue {
				dnsRule = rule
			}
		}
	}
	if dnsRule == nil {
		t.Fatalf("upstream DNS peer was not retargeted to the OpenShift resolver: %#v", egress)
	}
	ports, _, _ := unstructured.NestedSlice(dnsRule, "ports")
	seen := map[string]int{}
	for _, item := range ports {
		port := item.(map[string]interface{})
		protocol, _, _ := unstructured.NestedString(port, "protocol")
		portName, found, _ := unstructured.NestedString(port, "port")
		if found {
			seen[protocol+"/"+portName]++
		}
	}
	for _, required := range []struct {
		protocol string
		port     string
	}{
		{protocol: "UDP", port: openshiftDNSUDPPortName},
		{protocol: "TCP", port: openshiftDNSTCPPortName},
	} {
		key := required.protocol + "/" + required.port
		if seen[key] != 1 {
			t.Fatalf("DNS port %s appears %d times; want exactly once (all ports: %v)", key, seen[key], seen)
		}
	}
	if len(ports) != 2 {
		t.Fatalf("DNS egress ports = %v, want only the named UDP and TCP endpoint ports", ports)
	}
}

func TestAdaptNetworkPolicyKeepsDNSPortsScopedToDNSPeer(t *testing.T) {
	routing := testRoutingConfig()
	if err := routing.validate(); err != nil {
		t.Fatal(err)
	}
	policy := object("NetworkPolicy", "mixed-dns-egress", map[string]interface{}{
		"spec": map[string]interface{}{
			"policyTypes": []interface{}{"Egress"},
			"egress": []interface{}{map[string]interface{}{
				"to": []interface{}{
					map[string]interface{}{
						"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "kube-system"}},
						"podSelector":       map[string]interface{}{"matchLabels": map[string]interface{}{"k8s-app": "kube-dns"}},
					},
					map[string]interface{}{
						"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": "other-namespace"}},
					},
				},
				"ports": []interface{}{map[string]interface{}{"protocol": "UDP", "port": int64(53)}},
			}},
		},
	})
	if err := adaptNetworkPolicy(policy, routing); err == nil || !strings.Contains(err.Error(), "dedicated egress rule") {
		t.Fatalf("mixed DNS and non-DNS egress peers should fail closed, got %v", err)
	}
}

func TestAdaptOpenShiftResourcesFailsClosed(t *testing.T) {
	secret := object("Secret", "unexpected-secret", nil)
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{secret}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "Secret") {
		t.Fatalf("Secret should be rejected, got %v", err)
	}
	hostPath := object("Deployment", "unexpected", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{"name": "c", "image": "example.invalid/demo"}},
			"volumes":    []interface{}{map[string]interface{}{"name": "host", "hostPath": map[string]interface{}{"path": "/etc"}}},
		}}},
	})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{hostPath}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "hostPath") {
		t.Fatalf("unexpected hostPath should be rejected, got %v", err)
	}
	unknown := object("ClusterRole", "unknown", map[string]interface{}{"rules": []interface{}{map[string]interface{}{"apiGroups": []interface{}{"*"}, "resources": []interface{}{"*"}, "verbs": []interface{}{"*"}}}})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{unknown}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("unknown ClusterRole should be rejected, got %v", err)
	}
	wildcard := object("ClusterRole", "workcell-cluster-service", map[string]interface{}{"rules": []interface{}{map[string]interface{}{"apiGroups": []interface{}{"*"}, "resources": []interface{}{"*"}, "verbs": []interface{}{"*"}}}})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{wildcard}, pilotNamespace); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("ClusterRole wildcard should be rejected, got %v", err)
	}
	gatewayReference := object("ConfigMap", "generated-resource-context", map[string]interface{}{
		"data": map[string]interface{}{
			"handle": upstreamIngressAddress,
		},
	})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{gatewayReference}, pilotNamespace); err == nil || !strings.Contains(err.Error(), ingressAddressEnv) {
		t.Fatalf("legacy gateway reference without routing configuration should be rejected, got %v", err)
	}
}

func kinds(resources []*unstructured.Unstructured) []string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		out = append(out, resource.GetKind()+"/"+resource.GetName())
	}
	return out
}
