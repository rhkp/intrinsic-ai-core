// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestCanonicalControllerConfigSuppliesEndpoints(t *testing.T) {
	useControllerConfig(t, &ControllerConfig{
		ZenohRouterEndpoint: "zenoh.example.com:7447",
		RHOAITritonEndpoint: "triton.example.com:9000",
	})
	if got := configuredZenohRouterEndpoint(pilotNamespace); got != "tcp/zenoh.example.com:7447" {
		t.Fatalf("Zenoh endpoint = %q", got)
	}
	if got := configuredRHOAITritonEndpoint(pilotNamespace); got != "triton.example.com:9000" {
		t.Fatalf("RHOAI endpoint = %q", got)
	}
	container := map[string]interface{}{"args": []interface{}{"server"}}
	if err := ensureProjectZenohRouter(container, pilotNamespace); err != nil {
		t.Fatal(err)
	}
	args, _, err := unstructured.NestedStringSlice(container, "args")
	if err != nil || len(args) != 2 || args[1] != "--zenoh_router=tcp/zenoh.example.com:7447" {
		t.Fatalf("Zenoh args = %v, err = %v", args, err)
	}
}

func TestAdaptOpenShiftResourcesRetargetsVirtualServiceAndRemovesK3sOnlyObjects(t *testing.T) {
	routing := testRoutingConfig()
	resources := []*unstructured.Unstructured{
		object("Deployment", "artifacts-deployment", map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{
					"name":         "artifacts-deployment",
					"image":        "quay.io/your-registry-org/intrinsic/artifacts_service_ap6rsu7y7q2zdhlk@sha256:0976d6fd4ce4916d8388c48709604f79a19e59c7d4adb1adb5a669f9066c0b99",
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
	useControllerConfig(t, &ControllerConfig{})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{gatewayReference}, pilotNamespace); err == nil || !strings.Contains(err.Error(), ingressAddressEnv) {
		t.Fatalf("legacy gateway reference without routing configuration should be rejected, got %v", err)
	}
}
