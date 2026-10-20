// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const serviceMeshControlPlanePort = int64(15012)

const openshiftDNSNamespace = "openshift-dns"

const openshiftDNSPodLabel = "dns.operator.openshift.io/daemonset-dns"

const openshiftDNSPodValue = "default"

const openshiftDNSUDPPortName = "dns"

const openshiftDNSTCPPortName = "dns-tcp"

const upstreamDNSPodLabel = "k8s-app"

const upstreamDNSPodValue = "kube-dns"

// adaptNetworkPolicy retargets upstream ingress and DNS peers to their
// OpenShift equivalents. It retains the verified Gateway and DNS pod selectors
// so the rules stay narrow. An empty from/to list is never retained because
// Kubernetes interprets it as allowing every peer.
func adaptNetworkPolicy(resource *unstructured.Unstructured, routing openshiftRoutingConfig) error {
	for _, direction := range []struct{ rules, peers string }{{"ingress", "from"}, {"egress", "to"}} {
		rules, found, err := unstructured.NestedSlice(resource.Object, "spec", direction.rules)
		if err != nil {
			return fmt.Errorf("%s rules are malformed: %w", direction.rules, err)
		}
		if !found {
			continue
		}
		adaptedRules := make([]interface{}, 0, len(rules))
		for _, item := range rules {
			rule, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s rule is malformed", direction.rules)
			}
			peers, hasPeers, err := unstructured.NestedSlice(rule, direction.peers)
			if err != nil {
				return fmt.Errorf("%s peers are malformed: %w", direction.peers, err)
			}
			if !hasPeers {
				adaptedRules = append(adaptedRules, rule)
				continue
			}
			adaptedPeers := make([]interface{}, 0, len(peers))
			for _, peerItem := range peers {
				peer, ok := peerItem.(map[string]interface{})
				if !ok {
					return fmt.Errorf("%s peer is malformed", direction.peers)
				}
				namespaceSelector, hasNamespaceSelector, err := unstructured.NestedMap(peer, "namespaceSelector")
				if err != nil {
					return fmt.Errorf("namespace selector is malformed: %w", err)
				}
				if hasNamespaceSelector {
					namespaceName, _, err := unstructured.NestedString(namespaceSelector, "matchLabels", "kubernetes.io/metadata.name")
					if err != nil {
						return fmt.Errorf("namespace selector labels are malformed: %w", err)
					}
					switch namespaceName {
					case "app-ingress":
						if routing.serviceNamespace == "" {
							return fmt.Errorf("configure the Service Mesh ingress Service before adapting its NetworkPolicy peer")
						}
						if err := unstructured.SetNestedField(namespaceSelector, routing.serviceNamespace, "matchLabels", "kubernetes.io/metadata.name"); err != nil {
							return fmt.Errorf("retarget ingress namespace selector: %w", err)
						}
						if err := unstructured.SetNestedMap(peer, namespaceSelector, "namespaceSelector"); err != nil {
							return err
						}
						if len(routing.serviceSelector) == 0 {
							return fmt.Errorf("verified Service Mesh ingress pod selector is required")
						}
						podSelector := map[string]interface{}{"matchLabels": routing.serviceSelector}
						if err := unstructured.SetNestedMap(peer, podSelector, "podSelector"); err != nil {
							return fmt.Errorf("set ingress pod selector: %w", err)
						}
					case "app-intrinsic-base", "app-intrinsic-app-chart":
						if _, hasPodSelector := peer["podSelector"]; !hasPodSelector {
							return fmt.Errorf("cannot safely collapse namespace-only selector for %q", namespaceName)
						}
						delete(peer, "namespaceSelector")
					case "kube-system":
						if direction.rules == "egress" {
							labels, _, err := unstructured.NestedStringMap(peer, "podSelector", "matchLabels")
							if err != nil {
								return fmt.Errorf("upstream DNS pod selector is malformed: %w", err)
							}
							if labels[upstreamDNSPodLabel] == upstreamDNSPodValue {
								if err := unstructured.SetNestedMap(peer, map[string]interface{}{"matchLabels": map[string]interface{}{
									"kubernetes.io/metadata.name": openshiftDNSNamespace,
								}}, "namespaceSelector"); err != nil {
									return fmt.Errorf("retarget DNS namespace selector: %w", err)
								}
								if err := unstructured.SetNestedMap(peer, map[string]interface{}{"matchLabels": map[string]interface{}{
									openshiftDNSPodLabel: openshiftDNSPodValue,
								}}, "podSelector"); err != nil {
									return fmt.Errorf("retarget DNS pod selector: %w", err)
								}
							}
						}
					}
				}
				adaptedPeers = append(adaptedPeers, peer)
			}
			if len(adaptedPeers) == 0 {
				continue
			}
			if err := unstructured.SetNestedSlice(rule, adaptedPeers, direction.peers); err != nil {
				return err
			}
			if direction.rules == "egress" {
				if hasDedicatedGatewayPeer(adaptedPeers, routing) {
					if err := retargetGatewayEgressPort(rule, 8080, 443); err != nil {
						return fmt.Errorf("retarget dedicated Gateway egress port: %w", err)
					}
				}
				if err := ensureOpenShiftDNSFallback(rule); err != nil {
					return err
				}
			}
			adaptedRules = append(adaptedRules, rule)
		}
		if err := unstructured.SetNestedSlice(resource.Object, adaptedRules, "spec", direction.rules); err != nil {
			return err
		}
	}
	return ensureServiceMeshControlPlaneEgress(resource)
}

func hasDedicatedGatewayPeer(peers []interface{}, routing openshiftRoutingConfig) bool {
	for _, peerItem := range peers {
		peer, ok := peerItem.(map[string]interface{})
		if !ok {
			continue
		}
		namespaceName, _, err := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
		if err != nil || namespaceName != routing.serviceNamespace {
			continue
		}
		labels, _, err := unstructured.NestedStringMap(peer, "podSelector", "matchLabels")
		if err != nil {
			continue
		}
		matches := true
		for key, expected := range routing.serviceSelector {
			if labels[key] != fmt.Sprint(expected) {
				matches = false
				break
			}
		}
		if matches && len(routing.serviceSelector) > 0 {
			return true
		}
	}
	return false
}

// retargetGatewayEgressPort keeps workload egress limited to the dedicated
// Gateway pods while matching their actual TLS listener port. Core continues
// dialing Service port 80; the Service forwards that traffic to pod port 443.
func retargetGatewayEgressPort(rule map[string]interface{}, oldPort, newPort int64) error {
	ports, found, err := unstructured.NestedSlice(rule, "ports")
	if err != nil || !found {
		return err
	}
	changed := false
	for _, item := range ports {
		portRule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("egress port entry is malformed")
		}
		port, found, err := unstructured.NestedInt64(portRule, "port")
		if err != nil {
			return err
		}
		if found && port == oldPort {
			if err := unstructured.SetNestedField(portRule, newPort, "port"); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		return unstructured.SetNestedSlice(rule, ports, "ports")
	}
	return nil
}

func ensureOpenShiftDNSFallback(rule map[string]interface{}) error {
	peers, found, err := unstructured.NestedSlice(rule, "to")
	if err != nil || !found {
		return err
	}
	for _, item := range peers {
		peer, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("NetworkPolicy egress peer is malformed")
		}
		namespaceName, _, err := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
		if err != nil {
			return fmt.Errorf("OpenShift DNS namespace selector is malformed: %w", err)
		}
		labels, _, err := unstructured.NestedStringMap(peer, "podSelector", "matchLabels")
		if err != nil {
			return fmt.Errorf("OpenShift DNS pod selector is malformed: %w", err)
		}
		if namespaceName != openshiftDNSNamespace || labels[openshiftDNSPodLabel] != openshiftDNSPodValue {
			continue
		}
		if len(peers) != 1 {
			return fmt.Errorf("OpenShift DNS peer must have a dedicated egress rule")
		}
		// The DNS Service exposes port 53 but routes to named resolver Pod ports.
		// NetworkPolicy rules select those destination Pods, so use their named
		// ports to allow DNS without opening unrelated ports.
		ports := []interface{}{
			map[string]interface{}{"protocol": "UDP", "port": openshiftDNSUDPPortName},
			map[string]interface{}{"protocol": "TCP", "port": openshiftDNSTCPPortName},
		}
		if err := unstructured.SetNestedSlice(rule, ports, "ports"); err != nil {
			return fmt.Errorf("set named OpenShift DNS endpoint ports: %w", err)
		}
		return nil
	}
	return nil
}

// ensureServiceMeshControlPlaneEgress allows restricted workloads to complete
// the Istio xDS connection required by an injected sidecar. The rule is
// limited to the verified control-plane pods and TLS xDS port; it does
// not allow general egress from the project.
func ensureServiceMeshControlPlaneEgress(resource *unstructured.Unstructured) error {
	policyTypes, hasPolicyTypes, err := unstructured.NestedStringSlice(resource.Object, "spec", "policyTypes")
	if err != nil {
		return fmt.Errorf("NetworkPolicy types are malformed: %w", err)
	}
	egressRules, hasEgress, err := unstructured.NestedSlice(resource.Object, "spec", "egress")
	if err != nil {
		return fmt.Errorf("NetworkPolicy egress rules are malformed: %w", err)
	}
	egressEnabled := hasEgress
	if hasPolicyTypes {
		for _, policyType := range policyTypes {
			if policyType == "Egress" {
				egressEnabled = true
				break
			}
		}
	}
	if !egressEnabled {
		return nil
	}
	for _, item := range egressRules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("NetworkPolicy egress rule is malformed")
		}
		matches, err := matchesServiceMeshControlPlaneEgress(rule)
		if err != nil {
			return err
		}
		if matches {
			return nil // keep reconciliation idempotent.
		}
	}
	peer := map[string]interface{}{
		"namespaceSelector": map[string]interface{}{"matchLabels": map[string]interface{}{
			"kubernetes.io/metadata.name": controllerConfig.MeshNamespace,
		}},
		"podSelector": map[string]interface{}{"matchLabels": map[string]interface{}{
			"app":          "istiod",
			"istio.io/rev": controllerConfig.MeshRevision,
		}},
	}
	egressRules = append(egressRules, map[string]interface{}{
		"to": []interface{}{peer},
		"ports": []interface{}{map[string]interface{}{
			"protocol": "TCP",
			"port":     serviceMeshControlPlanePort,
		}},
	})
	if err := unstructured.SetNestedSlice(resource.Object, egressRules, "spec", "egress"); err != nil {
		return fmt.Errorf("add Service Mesh control-plane egress: %w", err)
	}
	return nil
}

func matchesServiceMeshControlPlaneEgress(rule map[string]interface{}) (bool, error) {
	peers, hasPeers, err := unstructured.NestedSlice(rule, "to")
	if err != nil {
		return false, fmt.Errorf("NetworkPolicy egress peers are malformed: %w", err)
	}
	ports, hasPorts, err := unstructured.NestedSlice(rule, "ports")
	if err != nil {
		return false, fmt.Errorf("NetworkPolicy egress ports are malformed: %w", err)
	}
	if !hasPeers || !hasPorts {
		return false, nil
	}
	peerMatches := false
	for _, item := range peers {
		peer, ok := item.(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("NetworkPolicy egress peer is malformed")
		}
		namespaceName, _, err := unstructured.NestedString(peer, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name")
		if err != nil {
			return false, fmt.Errorf("Service Mesh namespace selector is malformed: %w", err)
		}
		labels, _, err := unstructured.NestedStringMap(peer, "podSelector", "matchLabels")
		if err != nil {
			return false, fmt.Errorf("Service Mesh pod selector is malformed: %w", err)
		}
		if namespaceName == controllerConfig.MeshNamespace && labels["app"] == "istiod" && labels["istio.io/rev"] == controllerConfig.MeshRevision {
			peerMatches = true
			break
		}
	}
	if !peerMatches {
		return false, nil
	}
	for _, item := range ports {
		port, ok := item.(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("NetworkPolicy egress port is malformed")
		}
		protocol, _, err := unstructured.NestedString(port, "protocol")
		if err != nil {
			return false, fmt.Errorf("NetworkPolicy egress protocol is malformed: %w", err)
		}
		portNumber, found, err := unstructured.NestedInt64(port, "port")
		if err != nil {
			return false, fmt.Errorf("NetworkPolicy egress port number is malformed: %w", err)
		}
		if protocol == "TCP" && found && portNumber == serviceMeshControlPlanePort {
			return true, nil
		}
	}
	return false, nil
}
