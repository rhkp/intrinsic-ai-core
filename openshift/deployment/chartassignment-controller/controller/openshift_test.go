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
	role := object("ClusterRole", "workcell-cluster-service", map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"apiGroups": []interface{}{"apps.cloudrobotics.com"},
				"resources": []interface{}{"chartassignments"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{""},
				"resources": []interface{}{"namespaces", "services"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"registry.cloudrobotics.com"},
				"resources": []interface{}{"robots", "robots/status"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
		},
	})
	binding := object("ClusterRoleBinding", "workcell-cluster-service", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "workcell-cluster-service"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "workcell-cluster-service", "namespace": "default"}},
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
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want the project-scoped ChartAssignment and Service rules", len(rules))
	}
	resources, _, _ := unstructured.NestedStringSlice(rules[0].(map[string]interface{}), "resources")
	if len(resources) != 1 || resources[0] != "chartassignments" {
		t.Fatalf("remaining role resources = %v", resources)
	}
	namespaceRuleResources, _, _ := unstructured.NestedStringSlice(rules[1].(map[string]interface{}), "resources")
	if len(namespaceRuleResources) != 1 || namespaceRuleResources[0] != "services" {
		t.Fatalf("cluster-scoped namespace permission was not removed: %v", namespaceRuleResources)
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

func TestAdaptOpenShiftResourcesRetargetsVirtualServiceAndRemovesK3sOnlyObjects(t *testing.T) {
	routing := testRoutingConfig()
	resources := []*unstructured.Unstructured{
		object("Deployment", "artifacts-deployment", nil),
		object("Service", "artifacts-deployment", nil),
		object("ServiceMonitor", "artifacts-deployment-metrics", nil),
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
	if len(got) != 1 || got[0].GetKind() != "VirtualService" {
		t.Fatalf("adapted objects = %v, want only the project-scoped VirtualService", kinds(got))
	}
	gateways, _, _ := unstructured.NestedStringSlice(got[0].Object, "spec", "gateways")
	if len(gateways) != 1 || gateways[0] != routing.gateway {
		t.Fatalf("gateways = %v, want %q", gateways, routing.gateway)
	}
	exportTo, _, _ := unstructured.NestedStringSlice(got[0].Object, "spec", "exportTo")
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
			"containers": []interface{}{map[string]interface{}{"name": "service", "args": []interface{}{
				"--sim_service_address=istio-ingressgateway.app-ingress.svc.cluster.local:80",
			}}},
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
	if namespaceName != "mesh-system" || !containsStringValue(peer, "istio-ingressgateway") {
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
