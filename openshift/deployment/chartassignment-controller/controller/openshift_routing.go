// This project was developed with assistance from AI tools.
package chartassignment

import (
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"net"
	"strconv"
	"strings"
)

const upstreamIngressAddress = "istio-ingressgateway.app-ingress.svc.cluster.local:80"

const istioSidecarInjectAnnotation = "sidecar.istio.io/inject"

const ingressAddressEnv = "INTRINSIC_INGRESS_ADDRESS"

const ingressGatewayEnv = "INTRINSIC_INGRESS_GATEWAY"

const ingressSelectorEnv = "INTRINSIC_INGRESS_POD_SELECTOR"

type openshiftRoutingConfig struct {
	address          string
	serviceNamespace string
	gateway          string
	gatewayNamespace string
	selectorJSON     string
	serviceSelector  map[string]interface{}
}

func routingConfigFromEnvironment() openshiftRoutingConfig {
	if controllerConfig == nil {
		return openshiftRoutingConfig{}
	}
	selector, _ := json.Marshal(controllerConfig.IngressPodSelector)
	return openshiftRoutingConfig{address: controllerConfig.IngressAddress, gateway: controllerConfig.IngressGateway, selectorJSON: string(selector)}
}

func (c *openshiftRoutingConfig) validate() error {
	if c.address == "" || c.gateway == "" {
		return fmt.Errorf("configure %s and %s before applying upstream gRPC routes", ingressAddressEnv, ingressGatewayEnv)
	}
	host, portText, err := net.SplitHostPort(c.address)
	labels := strings.Split(host, ".")
	if err != nil || net.ParseIP(host) != nil || len(labels) != 5 || labels[0] == "" || labels[1] == "" ||
		labels[2] != "svc" || labels[3] != "cluster" || labels[4] != "local" {
		return fmt.Errorf("%s must be service.namespace.svc.cluster.local:80", ingressAddressEnv)
	}
	if len(validation.IsDNS1123Label(labels[0])) != 0 || len(validation.IsDNS1123Label(labels[1])) != 0 {
		return fmt.Errorf("%s must use valid DNS labels for the Service and namespace", ingressAddressEnv)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port != 80 {
		return fmt.Errorf("%s must use the internal HTTP/2 Gateway Service port 80", ingressAddressEnv)
	}
	c.serviceNamespace = labels[1]
	parts := strings.Split(c.gateway, "/")
	if len(parts) != 2 || len(validation.IsDNS1123Label(parts[0])) != 0 || len(validation.IsDNS1123Label(parts[1])) != 0 {
		return fmt.Errorf("%s must be a namespaced Gateway reference in namespace/name form", ingressGatewayEnv)
	}
	c.gatewayNamespace = parts[0]
	if c.gatewayNamespace != c.serviceNamespace {
		return fmt.Errorf("%s and %s must refer to the same Service Mesh ingress namespace", ingressAddressEnv, ingressGatewayEnv)
	}
	if c.serviceSelector == nil {
		var selector map[string]string
		if err := json.Unmarshal([]byte(c.selectorJSON), &selector); err != nil || len(selector) == 0 {
			return fmt.Errorf("%s must contain the verified ingress Service pod selector", ingressSelectorEnv)
		}
		c.serviceSelector = make(map[string]interface{}, len(selector))
		for key, value := range selector {
			c.serviceSelector[key] = value
		}
	}
	return nil
}

// workloadsBehindGatewayRoutes finds project workloads selected by Services
// that receive traffic from an adapted VirtualService, plus workloads that
// call the upstream ingress address which is rewritten to the dedicated
// gateway. The mesh uses STRICT mTLS, so both sides of those connections need
// an injected sidecar. Unrelated workloads keep their existing pod networking.
func workloadsBehindGatewayRoutes(resources []*unstructured.Unstructured) (map[string]struct{}, error) {
	routedServices := make(map[string]struct{})
	serviceSelectors := make(map[string]map[string]string)
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		switch resource.GetKind() {
		case "Service":
			selector, found, err := unstructured.NestedStringMap(resource.Object, "spec", "selector")
			if err != nil {
				return nil, fmt.Errorf("Service %q selector is malformed: %w", resource.GetName(), err)
			}
			if found && len(selector) != 0 {
				serviceSelectors[resource.GetName()] = selector
			}
		case "VirtualService":
			if resource.GetName() == "zenoh-router" {
				continue
			}
			routes, found, err := unstructured.NestedSlice(resource.Object, "spec", "http")
			if err != nil {
				return nil, fmt.Errorf("VirtualService %q HTTP routes are malformed: %w", resource.GetName(), err)
			}
			if !found {
				continue // adaptVirtualService reports the missing route later.
			}
			for _, route := range routes {
				if err := collectVirtualServiceDestinations(route, routedServices); err != nil {
					return nil, fmt.Errorf("VirtualService %q: %w", resource.GetName(), err)
				}
			}
		}
	}

	routedWorkloads := make(map[string]struct{})
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		labels, found, err := workloadPodLabels(resource)
		if err != nil {
			return nil, fmt.Errorf("%s/%s pod labels are malformed: %w", resource.GetKind(), resource.GetName(), err)
		}
		if isWorkloadKind(resource.GetKind()) && containsStringValue(resource.Object, upstreamIngressAddress) {
			routedWorkloads[resource.GetKind()+"/"+resource.GetName()] = struct{}{}
		}
		if !found {
			continue
		}
		for serviceName := range routedServices {
			selector, ok := serviceSelectors[serviceName]
			if ok && selectorMatches(labels, selector) {
				routedWorkloads[resource.GetKind()+"/"+resource.GetName()] = struct{}{}
				break
			}
		}
	}
	return routedWorkloads, nil
}

func isWorkloadKind(kind string) bool {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod", "Job", "CronJob":
		return true
	default:
		return false
	}
}

func collectVirtualServiceDestinations(value interface{}, destinations map[string]struct{}) error {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if key == "destination" || key == "mirror" {
				destination, ok := nested.(map[string]interface{})
				if !ok {
					return fmt.Errorf("%s is malformed", key)
				}
				host, ok := destination["host"].(string)
				if !ok || host == "" {
					return fmt.Errorf("%s host is missing", key)
				}
				destinations[strings.SplitN(host, ".", 2)[0]] = struct{}{}
			}
			if err := collectVirtualServiceDestinations(nested, destinations); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, nested := range item {
			if err := collectVirtualServiceDestinations(nested, destinations); err != nil {
				return err
			}
		}
	}
	return nil
}

func workloadPodLabels(resource *unstructured.Unstructured) (map[string]string, bool, error) {
	var path []string
	switch resource.GetKind() {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		path = []string{"spec", "template", "metadata", "labels"}
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "metadata", "labels"}
	case "Pod":
		path = []string{"metadata", "labels"}
	default:
		return nil, false, nil
	}
	labels, found, err := unstructured.NestedStringMap(resource.Object, path...)
	return labels, found, err
}

func enableIstioSidecar(resource *unstructured.Unstructured) error {
	var path []string
	switch resource.GetKind() {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		path = []string{"spec", "template", "metadata", "annotations"}
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "metadata", "annotations"}
	case "Pod":
		path = []string{"metadata", "annotations"}
	default:
		return fmt.Errorf("pod template is unsupported")
	}
	annotations, found, err := unstructured.NestedStringMap(resource.Object, path...)
	if err != nil {
		return err
	}
	if !found {
		annotations = make(map[string]string)
	}
	annotations[istioSidecarInjectAnnotation] = "true"
	return unstructured.SetNestedStringMap(resource.Object, annotations, path...)
}

// adaptKnownIngressAddresses replaces only the K3s/Istio addresses whose
// upstream routes have a direct in-project Service equivalent. Keep this
// explicit: a generic gateway-to-service rewrite could silently change gRPC
// routing semantics.
func adaptKnownIngressAddresses(resource *unstructured.Unstructured, namespace string) error {
	if resource.GetKind() != "Deployment" {
		return nil
	}
	var replacements map[string]string
	switch resource.GetName() {
	case "workcell-cluster-service":
		replacements = map[string]string{
			"--sim_service_address=istio-ingressgateway.app-ingress.svc.cluster.local:80": "--sim_service_address=simulation-service." + namespace + ".svc.cluster.local:8088",
		}
	case "solution-service":
		replacements = map[string]string{
			"--skill_registry_address=istio-ingressgateway.app-ingress.svc.cluster.local:80":           "--skill_registry_address=skill-registry." + namespace + ".svc.cluster.local:8080",
			"--installed_assets_service_address=istio-ingressgateway.app-ingress.svc.cluster.local:80": "--installed_assets_service_address=workcell-cluster-service." + namespace + ".svc.cluster.local:9957",
		}
	default:
		return nil
	}

	containers, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		return fmt.Errorf("pod template containers are missing or malformed")
	}
	for _, item := range containers {
		container, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("pod template contains a malformed container")
		}
		args, found, err := unstructured.NestedStringSlice(container, "args")
		if err != nil {
			return fmt.Errorf("container args are malformed: %w", err)
		}
		if !found {
			continue
		}
		for i, arg := range args {
			if replacement, ok := replacements[arg]; ok {
				args[i] = replacement
			}
		}
		if err := unstructured.SetNestedStringSlice(container, args, "args"); err != nil {
			return err
		}
	}
	return unstructured.SetNestedSlice(resource.Object, containers, "spec", "template", "spec", "containers")
}

func needsIngressRouting(resource *unstructured.Unstructured) bool {
	return resource.GetKind() == "VirtualService" ||
		containsStringValue(resource.Object, upstreamIngressAddress) ||
		containsStringValue(resource.Object, "app-ingress")
}

func rewriteIngressAddress(value interface{}, address string) {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if text, ok := nested.(string); ok {
				item[key] = strings.ReplaceAll(text, upstreamIngressAddress, address)
			} else {
				rewriteIngressAddress(nested, address)
			}
		}
	case []interface{}:
		for i, nested := range item {
			if text, ok := nested.(string); ok {
				item[i] = strings.ReplaceAll(text, upstreamIngressAddress, address)
			} else {
				rewriteIngressAddress(nested, address)
			}
		}
	}
}

func adaptVirtualService(resource *unstructured.Unstructured, routing openshiftRoutingConfig) error {
	if err := routing.validate(); err != nil {
		return err
	}
	if err := validateVirtualServiceDestinations(resource); err != nil {
		return err
	}
	if err := unstructured.SetNestedStringSlice(resource.Object, []string{routing.gateway}, "spec", "gateways"); err != nil {
		return fmt.Errorf("set Service Mesh Gateway reference: %w", err)
	}
	exportTo := []string{"."}
	if routing.gatewayNamespace != "" && routing.gatewayNamespace != resource.GetNamespace() {
		exportTo = append(exportTo, routing.gatewayNamespace)
	}
	return unstructured.SetNestedStringSlice(resource.Object, exportTo, "spec", "exportTo")
}

func validateVirtualServiceDestinations(resource *unstructured.Unstructured) error {
	for _, routeType := range []string{"tcp", "tls"} {
		if routes, found, err := unstructured.NestedSlice(resource.Object, "spec", routeType); err != nil {
			return fmt.Errorf("%s routes are malformed: %w", routeType, err)
		} else if found && len(routes) != 0 {
			return fmt.Errorf("%s VirtualService routes are not supported by the OpenShift gRPC adapter", routeType)
		}
	}
	httpRoutes, found, err := unstructured.NestedSlice(resource.Object, "spec", "http")
	if err != nil || !found {
		return fmt.Errorf("HTTP routes are missing or malformed")
	}
	for _, item := range httpRoutes {
		route, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("HTTP route entry is malformed")
		}
		if err := validateLocalDestinations(route, resource.GetNamespace()); err != nil {
			return err
		}
	}
	return nil
}

func validateLocalDestinations(value interface{}, namespace string) error {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if key == "destination" || key == "mirror" {
				destination, ok := nested.(map[string]interface{})
				if !ok {
					return fmt.Errorf("VirtualService destination is malformed")
				}
				host, ok := destination["host"].(string)
				if !ok || host == "" {
					return fmt.Errorf("VirtualService destination host is missing")
				}
				if strings.Contains(host, ".") && !strings.HasSuffix(host, "."+namespace+".svc.cluster.local") {
					return fmt.Errorf("VirtualService destination must be a Service in project %q", namespace)
				}
				if strings.ContainsAny(host, ":/@ \t\n") {
					return fmt.Errorf("VirtualService destination is not a Kubernetes Service name")
				}
			}
			if err := validateLocalDestinations(nested, namespace); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, nested := range item {
			if err := validateLocalDestinations(nested, namespace); err != nil {
				return err
			}
		}
	}
	return nil
}
