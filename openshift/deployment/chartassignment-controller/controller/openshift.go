package chartassignment

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	openshiftImagePullServiceAccount = "intrinsic-runtime"
	openshiftStorageClass            = "gp3-csi"
	dataStorePVC                     = "intrinsic-data-store"
	upstreamIngressAddress           = "istio-ingressgateway.app-ingress.svc.cluster.local:80"
	ingressAddressEnv                = "INTRINSIC_INGRESS_ADDRESS"
	ingressGatewayEnv                = "INTRINSIC_INGRESS_GATEWAY"
	ingressSelectorEnv               = "INTRINSIC_INGRESS_POD_SELECTOR"
)

type openshiftRoutingConfig struct {
	address          string
	serviceNamespace string
	gateway          string
	gatewayNamespace string
	selectorJSON     string
	serviceSelector  map[string]interface{}
}

func routingConfigFromEnvironment() openshiftRoutingConfig {
	return openshiftRoutingConfig{
		address:      strings.TrimSpace(os.Getenv(ingressAddressEnv)),
		gateway:      strings.TrimSpace(os.Getenv(ingressGatewayEnv)),
		selectorJSON: strings.TrimSpace(os.Getenv(ingressSelectorEnv)),
	}
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
		return fmt.Errorf("%s must use the internal plaintext HTTP/2 Service port 80", ingressAddressEnv)
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

// adaptOpenShiftResources applies the deployment's OpenShift policy after Helm
// has rendered a ChartAssignment and before Synk can write anything to the API.
// It is intentionally strict: the pilot accepts project-scoped workloads only,
// drops upstream K3s-only/external-ingress resources, and fails on unexpected
// host access, secret objects, or cluster-scoped kinds.
func adaptOpenShiftResources(resources []*unstructured.Unstructured, namespace string) ([]*unstructured.Unstructured, error) {
	return adaptOpenShiftResourcesWithRouting(resources, namespace, routingConfigFromEnvironment())
}

func adaptOpenShiftResourcesWithRouting(resources []*unstructured.Unstructured, namespace string, routing openshiftRoutingConfig) ([]*unstructured.Unstructured, error) {
	if namespace == "" {
		return nil, fmt.Errorf("OpenShift target namespace is required")
	}
	if routing.address != "" || routing.gateway != "" || routing.selectorJSON != "" {
		if err := routing.validate(); err != nil {
			return nil, err
		}
	}

	adapted := make([]*unstructured.Unstructured, 0, len(resources)+1)
	needsDataStorePVC := false
	for _, original := range resources {
		if original == nil {
			return nil, fmt.Errorf("chart rendered a nil resource")
		}
		resource := original.DeepCopy()
		kind, name := resource.GetKind(), resource.GetName()

		// K3s artifact import uses the host's containerd socket and host port.
		// The OpenShift publisher uploads images to the internal registry instead.
		if kind == "Deployment" && name == "artifacts-deployment" ||
			kind == "Service" && name == "artifacts-deployment" ||
			kind == "ServiceMonitor" && name == "artifacts-deployment-metrics" {
			continue
		}
		// The static local PV is not valid on OpenShift/CSI storage.
		if kind == "PersistentVolume" {
			continue
		}
		if kind == "Namespace" {
			return nil, fmt.Errorf("chart %q attempts to manage Namespace %q; projects are pre-provisioned", resource.GetAPIVersion(), name)
		}
		if kind == "Secret" {
			return nil, fmt.Errorf("chart rendered Secret %q; supply runtime values through the reviewed Opaque Secret path", name)
		}
		if kind == "ClusterRoleBinding" && name == "zenoh-router-sa-xfa-config-ipcidentity-fetcher" {
			// This optional binding targets an external identity-fetcher service
			// account that is not part of this project deployment.
			continue
		}

		switch kind {
		case "ClusterRole":
			if !isPilotRole(name) {
				return nil, fmt.Errorf("ClusterRole %q is not in the reviewed project-role allowlist", name)
			}
			if err := scopeRoleRulesToProject(resource); err != nil {
				return nil, fmt.Errorf("scope ClusterRole %q: %w", name, err)
			}
			resource.SetKind("Role")
			if err := validatePilotRoleRules(resource); err != nil {
				return nil, fmt.Errorf("validate Role %q: %w", name, err)
			}
		case "Role":
			if !isPilotRole(name) {
				return nil, fmt.Errorf("Role %q is not in the reviewed project-role allowlist", name)
			}
			if err := scopeRoleRulesToProject(resource); err != nil {
				return nil, fmt.Errorf("validate Role %q: %w", name, err)
			}
			if err := validatePilotRoleRules(resource); err != nil {
				return nil, fmt.Errorf("validate Role %q: %w", name, err)
			}
		case "ClusterRoleBinding":
			if !isPilotRole(name) {
				return nil, fmt.Errorf("ClusterRoleBinding %q is not in the reviewed project-binding allowlist", name)
			}
			resource.SetKind("RoleBinding")
			roleRef, found, err := unstructured.NestedMap(resource.Object, "roleRef")
			if err != nil || !found {
				return nil, fmt.Errorf("ClusterRoleBinding %q has no roleRef", name)
			}
			if roleRef["kind"] == "ClusterRole" {
				roleRef["kind"] = "Role"
			}
			if err := unstructured.SetNestedMap(resource.Object, roleRef, "roleRef"); err != nil {
				return nil, fmt.Errorf("update roleRef for %q: %w", name, err)
			}
			if err := normalizeRoleBinding(resource, namespace); err != nil {
				return nil, fmt.Errorf("validate RoleBinding %q: %w", name, err)
			}
		case "RoleBinding":
			if !isPilotRole(name) {
				return nil, fmt.Errorf("RoleBinding %q is not in the reviewed project-binding allowlist", name)
			}
			if err := normalizeRoleBinding(resource, namespace); err != nil {
				return nil, fmt.Errorf("validate RoleBinding %q: %w", name, err)
			}
		case "PersistentVolumeClaim":
			if err := adaptPVC(resource); err != nil {
				return nil, fmt.Errorf("adapt PVC %q: %w", name, err)
			}
		case "Service":
			if err := adaptService(resource); err != nil {
				return nil, fmt.Errorf("adapt Service %q: %w", name, err)
			}
		}

		if !isProjectScopedKind(resource.GetKind()) {
			return nil, fmt.Errorf("chart rendered unsupported or cluster-scoped resource %s/%s", resource.GetKind(), name)
		}
		resource.SetNamespace(namespace)
		rewriteProjectReferences(resource.Object, namespace)
		if err := adaptKnownIngressAddresses(resource, namespace); err != nil {
			return nil, fmt.Errorf("adapt service addresses for %s/%s: %w", kind, name, err)
		}
		if needsIngressRouting(resource) {
			if err := routing.validate(); err != nil {
				return nil, fmt.Errorf("adapt %s/%s: %w", kind, name, err)
			}
			rewriteIngressAddress(resource.Object, routing.address)
		}
		if kind == "VirtualService" {
			if err := adaptVirtualService(resource, routing); err != nil {
				return nil, fmt.Errorf("adapt VirtualService %q: %w", name, err)
			}
		}
		if kind == "NetworkPolicy" {
			if err := adaptNetworkPolicy(resource, routing); err != nil {
				return nil, fmt.Errorf("adapt NetworkPolicy %q: %w", name, err)
			}
		}
		if err := rejectOpenShiftIncompatibleReferences(resource); err != nil {
			return nil, fmt.Errorf("validate project references for %s/%s: %w", resource.GetKind(), name, err)
		}

		if err := adaptPodSpec(resource, namespace, &needsDataStorePVC, routing); err != nil {
			return nil, fmt.Errorf("adapt %s/%s: %w", kind, name, err)
		}
		if err := adaptSecurityContexts(resource.Object); err != nil {
			return nil, fmt.Errorf("adapt security context for %s/%s: %w", resource.GetKind(), name, err)
		}
		adapted = append(adapted, resource)
	}

	if needsDataStorePVC {
		adapted = append(adapted, &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "PersistentVolumeClaim",
			"metadata": map[string]interface{}{
				"name":      dataStorePVC,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/part-of": "intrinsic-core",
				},
			},
			"spec": map[string]interface{}{
				"accessModes": []interface{}{"ReadWriteOnce"},
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"storage": "2Gi"},
				},
				"storageClassName": openshiftStorageClass,
			},
		}})
	}
	return adapted, nil
}

func isPilotRole(name string) bool {
	switch name {
	case "resource-registry", "skill-registry", "workcell-cluster-service":
		return true
	default:
		return false
	}
}

func validatePilotRoleRules(role *unstructured.Unstructured) error {
	allowed := map[string]map[string]map[string]map[string]bool{
		"resource-registry": {
			"": {"configmaps": {"get": true, "list": true, "watch": true}},
		},
		"skill-registry": {
			"": {"configmaps": {"get": true, "list": true, "watch": true}},
		},
		"workcell-cluster-service": {
			"apps.cloudrobotics.com": {"chartassignments": {"get": true, "list": true, "watch": true, "create": true, "update": true, "patch": true, "delete": true}},
			"":                       {"services": {"get": true, "list": true, "watch": true}, "configmaps": {"get": true, "list": true, "watch": true}, "pods": {"get": true, "list": true, "watch": true, "delete": true}},
			"apps":                   {"deployments": {"get": true, "list": true, "watch": true}},
			"batch":                  {"jobs": {"get": true, "list": true, "watch": true}},
		},
	}
	rules, _, err := unstructured.NestedSlice(role.Object, "rules")
	if err != nil {
		return err
	}
	for _, item := range rules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed RBAC rule")
		}
		for _, group := range stringSlice(rule["apiGroups"]) {
			for _, resource := range stringSlice(rule["resources"]) {
				verbs, ok := allowed[role.GetName()][group][resource]
				if !ok {
					return fmt.Errorf("permission for %s/%s is not approved", group, resource)
				}
				for _, verb := range stringSlice(rule["verbs"]) {
					if !verbs[verb] {
						return fmt.Errorf("verb %s on %s/%s is not approved", verb, group, resource)
					}
				}
			}
		}
	}
	return nil
}

func normalizeRoleBinding(binding *unstructured.Unstructured, namespace string) error {
	roleRef, found, err := unstructured.NestedMap(binding.Object, "roleRef")
	if err != nil || !found {
		return fmt.Errorf("roleRef is missing or malformed")
	}
	if roleRef["kind"] != "Role" || roleRef["name"] != binding.GetName() {
		return fmt.Errorf("binding must refer to same-name project Role")
	}
	subjects, found, err := unstructured.NestedSlice(binding.Object, "subjects")
	if err != nil || !found || len(subjects) != 1 {
		return fmt.Errorf("exactly one ServiceAccount subject is required")
	}
	subject, ok := subjects[0].(map[string]interface{})
	if !ok || subject["kind"] != "ServiceAccount" || subject["name"] != binding.GetName() {
		return fmt.Errorf("binding subject must be the same-name ServiceAccount")
	}
	subject["namespace"] = namespace
	return unstructured.SetNestedSlice(binding.Object, subjects, "subjects")
}

func isProjectScopedKind(kind string) bool {
	switch kind {
	case "ConfigMap", "ServiceAccount", "Service", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod", "Job", "CronJob", "PersistentVolumeClaim", "Role", "RoleBinding", "ServiceMonitor", "PodMonitor", "NetworkPolicy", "VirtualService", "ChartAssignment", "ResourceSet":
		return true
	default:
		return false
	}
}

func scopeRoleRulesToProject(role *unstructured.Unstructured) error {
	rules, found, err := unstructured.NestedSlice(role.Object, "rules")
	if err != nil || !found {
		return fmt.Errorf("rules are missing or malformed")
	}
	projectRules := make([]interface{}, 0, len(rules))
	for _, item := range rules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed RBAC rule")
		}
		if urls, found := rule["nonResourceURLs"]; found && len(stringSlice(urls)) != 0 {
			return fmt.Errorf("non-resource URL permissions cannot be represented by a namespaced Role")
		}
		groups := stringSlice(rule["apiGroups"])
		resources := stringSlice(rule["resources"])
		if contains(groups, "registry.cloudrobotics.com") {
			// The upstream Robot registry is a separate cluster service and is
			// not installed by this namespace-scoped pilot.
			continue
		}
		filtered := resources[:0]
		for _, resource := range resources {
			if resource == "*" {
				return fmt.Errorf("wildcard resource permissions are not accepted")
			}
			if isClusterScopedRBACResource(resource) {
				continue
			}
			filtered = append(filtered, resource)
		}
		if len(filtered) == 0 {
			continue
		}
		rule["resources"] = toInterfaceSlice(filtered)
		projectRules = append(projectRules, rule)
	}
	if len(projectRules) == 0 {
		return fmt.Errorf("no project-scoped permissions remain after filtering")
	}
	return unstructured.SetNestedSlice(role.Object, projectRules, "rules")
}

func isClusterScopedRBACResource(resource string) bool {
	switch resource {
	case "*", "namespaces", "nodes", "persistentvolumes", "storageclasses", "customresourcedefinitions", "clusterroles", "clusterrolebindings", "apiservices", "priorityclasses", "volumeattachments", "csidrivers", "csinodes":
		return true
	default:
		return false
	}
}

func stringSlice(value interface{}) []string {
	items, _ := value.([]interface{})
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func toInterfaceSlice(items []string) []interface{} {
	result := make([]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	return result
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func adaptPVC(resource *unstructured.Unstructured) error {
	if err := unstructured.SetNestedField(resource.Object, openshiftStorageClass, "spec", "storageClassName"); err != nil {
		return err
	}
	unstructured.RemoveNestedField(resource.Object, "spec", "selector")
	accessModes, found, err := unstructured.NestedStringSlice(resource.Object, "spec", "accessModes")
	if err != nil {
		return err
	}
	if found {
		for i, mode := range accessModes {
			if mode == "ReadWriteMany" {
				accessModes[i] = "ReadWriteOnce"
			}
		}
		if err := unstructured.SetNestedStringSlice(resource.Object, accessModes, "spec", "accessModes"); err != nil {
			return err
		}
	}
	return nil
}

func adaptService(resource *unstructured.Unstructured) error {
	if externalIPs, found, err := unstructured.NestedStringSlice(resource.Object, "spec", "externalIPs"); err != nil {
		return err
	} else if found && len(externalIPs) != 0 {
		return fmt.Errorf("externalIPs are not supported")
	}
	serviceType, found, err := unstructured.NestedString(resource.Object, "spec", "type")
	if err != nil {
		return err
	}
	if found && (serviceType == "LoadBalancer" || serviceType == "NodePort") {
		if err := unstructured.SetNestedField(resource.Object, "ClusterIP", "spec", "type"); err != nil {
			return err
		}
		ports, found, err := unstructured.NestedSlice(resource.Object, "spec", "ports")
		if err != nil {
			return err
		}
		if found {
			for _, item := range ports {
				if port, ok := item.(map[string]interface{}); ok {
					delete(port, "nodePort")
				}
			}
			return unstructured.SetNestedSlice(resource.Object, ports, "spec", "ports")
		}
	}
	return nil
}

func adaptPodSpec(resource *unstructured.Unstructured, namespace string, needsDataStorePVC *bool, routing openshiftRoutingConfig) error {
	var path []string
	switch resource.GetKind() {
	case "Pod":
		path = []string{"spec"}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		path = []string{"spec", "template", "spec"}
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	default:
		return nil
	}
	podSpec, found, err := unstructured.NestedMap(resource.Object, path...)
	if err != nil || !found {
		if err != nil {
			return err
		}
		return nil
	}

	serviceAccount, _ := podSpec["serviceAccountName"].(string)
	if serviceAccount == "" || serviceAccount == "default" {
		serviceAccount = openshiftImagePullServiceAccount
		podSpec["serviceAccountName"] = serviceAccount
	}
	automount := serviceAccount == "resource-registry" || serviceAccount == "skill-registry" || serviceAccount == "workcell-cluster-service"
	podSpec["automountServiceAccountToken"] = automount

	volumes, found, err := unstructured.NestedSlice(podSpec, "volumes")
	if err != nil {
		return err
	}
	if found {
		for _, item := range volumes {
			volume, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("malformed volume")
			}
			if _, hasHostPath := volume["hostPath"]; !hasHostPath {
				continue
			}
			if resource.GetKind() != "Deployment" || resource.GetName() != "data-store" || volume["name"] != "intrinsic-db-dir" {
				return fmt.Errorf("hostPath volume %q is not supported", volume["name"])
			}
			delete(volume, "hostPath")
			volume["persistentVolumeClaim"] = map[string]interface{}{"claimName": dataStorePVC}
			*needsDataStorePVC = true
		}
		if err := unstructured.SetNestedSlice(podSpec, volumes, "volumes"); err != nil {
			return err
		}
	}
	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if enabled, found, err := unstructured.NestedBool(podSpec, field); err != nil {
			return err
		} else if found && enabled {
			return fmt.Errorf("%s is not supported", field)
		}
	}

	containers := append(sliceMaps(podSpec["containers"]), sliceMaps(podSpec["initContainers"])...)
	for _, container := range containers {
		if routing.address != "" {
			if err := setContainerEnv(container, ingressAddressEnv, routing.address); err != nil {
				return err
			}
		}
		ports, _ := container["ports"].([]interface{})
		for _, item := range ports {
			port, ok := item.(map[string]interface{})
			if ok {
				if _, exists := port["hostPort"]; exists {
					return fmt.Errorf("hostPort is not supported")
				}
			}
		}
	}

	return unstructured.SetNestedMap(resource.Object, podSpec, path...)
}

func setContainerEnv(container map[string]interface{}, name, value string) error {
	env, found, err := unstructured.NestedSlice(container, "env")
	if err != nil {
		return fmt.Errorf("container environment is malformed: %w", err)
	}
	if !found {
		env = []interface{}{}
	}
	for i, item := range env {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("container environment entry is malformed")
		}
		if entry["name"] == name {
			env[i] = map[string]interface{}{"name": name, "value": value}
			return unstructured.SetNestedSlice(container, env, "env")
		}
	}
	env = append(env, map[string]interface{}{"name": name, "value": value})
	return unstructured.SetNestedSlice(container, env, "env")
}

func sliceMaps(value interface{}) []map[string]interface{} {
	items, _ := value.([]interface{})
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]interface{}); ok {
			result = append(result, object)
		}
	}
	return result
}

func rewriteProjectReferences(value interface{}, namespace string) {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if key == "namespace" {
				if current, ok := nested.(string); ok && (current == "default" || current == "app-intrinsic-base" || current == "app-intrinsic-app-chart") {
					item[key] = namespace
					continue
				}
			}
			rewriteProjectReferences(nested, namespace)
		}
	case []interface{}:
		for i := range item {
			rewriteProjectReferences(item[i], namespace)
		}
	case string:
		_ = item
	}
	// Replace hard-coded upstream namespace names in arguments, selectors, and
	// embedded configuration while preserving all other string values.
	rewriteStrings(value, namespace)
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

// adaptNetworkPolicy retargets the upstream ingress-gateway peer to the
// configured OpenShift Service Mesh gateway namespace. The gateway pod label
// is retained so the rule stays scoped to the ingress workload. An empty
// from/to list is never retained because Kubernetes interprets it as allowing
// every peer.
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
						if err := unstructured.SetNestedMap(peer, routing.serviceSelector, "podSelector"); err != nil {
							return fmt.Errorf("set ingress pod selector: %w", err)
						}
					case "app-intrinsic-base", "app-intrinsic-app-chart":
						if _, hasPodSelector := peer["podSelector"]; !hasPodSelector {
							return fmt.Errorf("cannot safely collapse namespace-only selector for %q", namespaceName)
						}
						delete(peer, "namespaceSelector")
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
			adaptedRules = append(adaptedRules, rule)
		}
		if err := unstructured.SetNestedSlice(resource.Object, adaptedRules, "spec", direction.rules); err != nil {
			return err
		}
	}
	return nil
}

func rejectOpenShiftIncompatibleReferences(resource *unstructured.Unstructured) error {
	for _, reference := range []string{
		"app-intrinsic-base",
		"app-intrinsic-app-chart",
		"app-ingress",
		upstreamIngressAddress,
	} {
		if containsStringValue(resource.Object, reference) {
			return fmt.Errorf("unsupported upstream namespace or service reference %q remains", reference)
		}
	}
	return nil
}

func containsStringValue(value interface{}, needle string) bool {
	switch item := value.(type) {
	case map[string]interface{}:
		for _, nested := range item {
			if containsStringValue(nested, needle) {
				return true
			}
		}
	case []interface{}:
		for _, nested := range item {
			if containsStringValue(nested, needle) {
				return true
			}
		}
	case string:
		return strings.Contains(item, needle)
	}
	return false
}

func rewriteStrings(value interface{}, namespace string) {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if text, ok := nested.(string); ok {
				text = strings.ReplaceAll(text, "app-intrinsic-base", namespace)
				text = strings.ReplaceAll(text, "app-intrinsic-app-chart", namespace)
				item[key] = text
			} else {
				rewriteStrings(nested, namespace)
			}
		}
	case []interface{}:
		for i := range item {
			if text, ok := item[i].(string); ok {
				text = strings.ReplaceAll(text, "app-intrinsic-base", namespace)
				text = strings.ReplaceAll(text, "app-intrinsic-app-chart", namespace)
				item[i] = text
			} else {
				rewriteStrings(item[i], namespace)
			}
		}
	}
}

func adaptSecurityContexts(value interface{}) error {
	switch item := value.(type) {
	case map[string]interface{}:
		if context, ok := item["securityContext"].(map[string]interface{}); ok {
			_, isContainer := item["image"]
			if isContainer {
				if privileged, _ := context["privileged"].(bool); privileged {
					return fmt.Errorf("privileged containers are not supported")
				}
				if allow, ok := context["allowPrivilegeEscalation"].(bool); ok && allow {
					return fmt.Errorf("allowPrivilegeEscalation must be false")
				}
				if caps, ok := context["capabilities"].(map[string]interface{}); ok {
					if add, ok := caps["add"].([]interface{}); ok && len(add) != 0 {
						return fmt.Errorf("adding Linux capabilities is not supported")
					}
				}
				context["allowPrivilegeEscalation"] = false
			}
			delete(context, "runAsUser")
			delete(context, "runAsGroup")
			if len(context) == 0 {
				delete(item, "securityContext")
			}
		}
		for key, nested := range item {
			if key == "securityContext" {
				continue
			}
			if err := adaptSecurityContexts(nested); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, nested := range item {
			if err := adaptSecurityContexts(nested); err != nil {
				return err
			}
		}
	}
	return nil
}
