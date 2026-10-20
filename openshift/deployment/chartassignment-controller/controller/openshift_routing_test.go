// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

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

func TestRoutingConfigRequiresVerifiedInternalGatewayDetails(t *testing.T) {
	useControllerConfig(t, &ControllerConfig{IngressAddress: "istio-ingressgateway.mesh-system.svc.cluster.local:80", IngressGateway: "mesh-system/gateway", IngressPodSelector: map[string]string{"app": "istio-ingressgateway"}})
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
		controllerConfig.IngressAddress = address
		invalidRouting := routingConfigFromEnvironment()
		if err := invalidRouting.validate(); err == nil {
			t.Fatalf("invalid internal ingress address %q was accepted", address)
		}
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
