package chartassignment

import (
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
			{name: "workcell-cluster-service", image: quayWorkcellServiceImage},
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
		object("Deployment", "artifacts-deployment", nil),
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
	if len(got) != 2 || got[0].GetKind() != "Service" || got[0].GetName() != "zenoh-router" || got[1].GetKind() != "VirtualService" {
		t.Fatalf("adapted objects = %v, want the in-project Zenoh Service and project-scoped VirtualService", kinds(got))
	}
	gateways, _, _ := unstructured.NestedStringSlice(got[1].Object, "spec", "gateways")
	if len(gateways) != 1 || gateways[0] != routing.gateway {
		t.Fatalf("gateways = %v, want %q", gateways, routing.gateway)
	}
	exportTo, _, _ := unstructured.NestedStringSlice(got[1].Object, "spec", "exportTo")
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
		"alias.istio-ingressgateway.mesh-system.svc.cluster.local:80",
		"bad_name.mesh-system.svc.cluster.local:80",
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

func TestAdaptOpenShiftResourcesRewritesIngressAndNetworkPolicyReferences(t *testing.T) {
	workcell := object("Deployment", "workcell-cluster-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "workcell-cluster-service",
				"image": "upstream.example/workcell-service:stale",
				"args": []interface{}{
					"--sim_service_address=istio-ingressgateway.app-ingress.svc.cluster.local:80",
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
