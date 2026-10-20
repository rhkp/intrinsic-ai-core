// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

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
