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
	openshiftImagePullServiceAccount   = "intrinsic-runtime"
	intrinsicSimRealtimeServiceAccount = "intrinsic-sim-realtime"
	openshiftStorageClass              = "gp3-csi"
	dataStorePVC                       = "intrinsic-data-store"
	intrinsicIconPVC                   = "intrinsic-icon-data"
	gazeboMeshesPVC                    = "intrinsic-gazebo-meshes"
	openshiftSharedStorageClass        = "ocs-storagecluster-cephfs"
	quayResourceRegistryImage          = "quay.io/rhkp/intrinsic/resource_registry_mz6oamw4xrhc5j4b@sha256:289da3d7464048dbe74a7e9d15f5054cce7ba1ab730eb61db83172593cf441f6"
	workcellServiceImage               = "image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic/workcell-cluster-service@sha256:b53e963051ab87b497773934c52223a3552ee62870764207c58668578442d9e3"
	quayZenohdImage                    = "quay.io/rhkp/intrinsic/zenohd@sha256:1e72a172c19cf1c48279b6d939d26da2eef1e07223208e896ed5a5801c702345"
	quayJupyterServerImage             = "quay.io/rhkp/intrinsic/code-execution-jupyter-server@sha256:fbd8aa00879fe5976fa431a4ca4fe9b9aa7cb548c49f00c1003f0ce9641f0ed5"
	jupyterHomeDir                     = "/home/defaultuser"
	jupyterRuntimeDir                  = jupyterHomeDir + "/.local/share/jupyter/runtime"
	upstreamIngressAddress             = "istio-ingressgateway.app-ingress.svc.cluster.local:80"
	istioSidecarInjectAnnotation       = "sidecar.istio.io/inject"
	serviceMeshControlPlaneNamespace   = "istio-system"
	serviceMeshControlPlaneRevision    = "data-science-smcp"
	serviceMeshControlPlanePort        = int64(15012)
	openshiftDNSNamespace              = "openshift-dns"
	openshiftDNSPodLabel               = "dns.operator.openshift.io/daemonset-dns"
	openshiftDNSPodValue               = "default"
	openshiftDNSUDPPortName            = "dns"
	openshiftDNSTCPPortName            = "dns-tcp"
	upstreamDNSPodLabel                = "k8s-app"
	upstreamDNSPodValue                = "kube-dns"
	ingressAddressEnv                  = "INTRINSIC_INGRESS_ADDRESS"
	ingressGatewayEnv                  = "INTRINSIC_INGRESS_GATEWAY"
	ingressSelectorEnv                 = "INTRINSIC_INGRESS_POD_SELECTOR"
)

// These upstream charts pin two images directly in templates instead of using
// their image abstraction values. Keep the destinations synchronized with
// openshift/image-lock.json; the rendered-manifest validator checks the lock.
var openshiftImageOverrides = map[string]string{
	"us-central1-docker.pkg.dev/intrinsic-mirror/intrinsic-build-images/zenohd:1.7.2":                                            quayZenohdImage,
	"ghcr.io/intrinsic-ai/code-execution-jupyter-server@sha256:e14b4e15b1b8341671c372eeadc328b25663c506827dc47e2e60e0f7b7ef1f2c": quayJupyterServerImage,
}

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
	routedWorkloads, err := workloadsBehindGatewayRoutes(resources)
	if err != nil {
		return nil, fmt.Errorf("identify Service Mesh gateway backends: %w", err)
	}

	adapted := make([]*unstructured.Unstructured, 0, len(resources)+1)
	needsDataStorePVC := false
	needsIntrinsicIconPVC := false
	needsGazeboMeshesPVC := false
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
		if kind == "VirtualService" && name == "zenoh-router" {
			// Upstream exposes the Zenoh router's TCP port for external robot/host
			// clients. The OpenShift pilot keeps the project-local ClusterIP path
			// used by Core services and does not expose this TCP ingress route.
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
		// Kubernetes RBAC escalation protection rejects this Role when the
		// controller tries to create it: the Role grants permissions that the
		// controller itself intentionally does not hold. The exact rules are
		// still validated above, but the project-owned Role and RoleBinding are
		// provisioned separately from manifests/workcell-cluster-service-rbac.yaml.
		if isPreprovisionedWorkcellRBAC(resource.GetKind(), name) {
			continue
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

		if err := adaptPodSpec(resource, namespace, &needsDataStorePVC, &needsIntrinsicIconPVC, &needsGazeboMeshesPVC, routing); err != nil {
			return nil, fmt.Errorf("adapt %s/%s: %w", kind, name, err)
		}
		if err := adaptPersistentDeploymentStrategy(resource); err != nil {
			return nil, fmt.Errorf("adapt rollout strategy for %s/%s: %w", kind, name, err)
		}
		_, isGatewayBackend := routedWorkloads[kind+"/"+name]
		// runtime-db is not a Gateway backend, but the Gateway-routed
		// workcell-cluster-service calls it over gRPC. The cluster's strict
		// ISTIO_MUTUAL policy requires the runtime DB server to join the mesh;
		// keep that policy intact instead of opting this connection out of TLS.
		needsSidecar := isGatewayBackend || kind == "Deployment" && name == "runtime-db"
		if needsSidecar {
			if err := enableIstioSidecar(resource); err != nil {
				return nil, fmt.Errorf("enable Service Mesh sidecar for %s/%s: %w", kind, name, err)
			}
		}
		if err := adaptSecurityContexts(resource); err != nil {
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
	if needsIntrinsicIconPVC {
		adapted = append(adapted, sharedStoragePVC(intrinsicIconPVC, namespace, "1Gi"))
	}
	if needsGazeboMeshesPVC {
		adapted = append(adapted, sharedStoragePVC(gazeboMeshesPVC, namespace, "5Gi"))
	}
	return adapted, nil
}

func sharedStoragePVC(name, namespace, size string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app.kubernetes.io/part-of":    "intrinsic-core",
				"app.kubernetes.io/managed-by": "openshift-overlay",
			},
		},
		"spec": map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteMany"},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"storage": size},
			},
			"storageClassName": openshiftSharedStorageClass,
		},
	}}
}

// adaptPersistentDeploymentStrategy prevents a RollingUpdate from starting a
// second pod while the old pod still holds a ReadWriteOnce claim. On CSI
// storage this can leave the replacement Pending with a Multi-Attach error;
// Recreate releases the old attachment before scheduling the replacement.
func adaptPersistentDeploymentStrategy(resource *unstructured.Unstructured) error {
	if resource.GetKind() != "Deployment" {
		return nil
	}
	volumes, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	usesPVC := false
	for _, value := range volumes {
		volume, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("pod template contains a malformed volume")
		}
		if _, ok := volume["persistentVolumeClaim"]; ok {
			usesPVC = true
			break
		}
	}
	if !usesPVC {
		return nil
	}
	if err := unstructured.SetNestedField(resource.Object, "Recreate", "spec", "strategy", "type"); err != nil {
		return err
	}
	unstructured.RemoveNestedField(resource.Object, "spec", "strategy", "rollingUpdate")
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

func selectorMatches(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, expected := range selector {
		if labels[key] != expected {
			return false
		}
	}
	return true
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

func isPreprovisionedWorkcellRBAC(kind, name string) bool {
	return name == "workcell-cluster-service" && (kind == "Role" || kind == "RoleBinding")
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
	if resource.GetName() == "world" {
		if err := exposeWorldConductorGRPCPort(resource); err != nil {
			return fmt.Errorf("expose World Conductor gRPC port: %w", err)
		}
	}
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

// exposeWorldConductorGRPCPort publishes the World pod's Conductor listener
// through the project Service. The upstream Workcell chart calls world:8082,
// while the rendered World Service otherwise exposed only its separate
// world-service listener on 8080.
func exposeWorldConductorGRPCPort(resource *unstructured.Unstructured) error {
	ports, found, err := unstructured.NestedSlice(resource.Object, "spec", "ports")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("World Service has no ports")
	}
	hasWorldGRPCPort := false
	hasConductorGRPCPort := false
	for i, item := range ports {
		port, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("World Service port %d is not an object", i)
		}
		name, _ := port["name"].(string)
		portNumber, portFound, err := unstructured.NestedInt64(port, "port")
		if err != nil || !portFound {
			return fmt.Errorf("World Service port %d has no numeric port", i)
		}
		targetPort, targetFound, err := unstructured.NestedInt64(port, "targetPort")
		if err != nil {
			return fmt.Errorf("World Service port %d has invalid targetPort: %w", i, err)
		}
		if !targetFound {
			targetPort = portNumber
		}
		if name == "grpc-world" || portNumber == 8080 {
			if name != "grpc-world" || portNumber != 8080 || targetPort != 8080 {
				return fmt.Errorf("World Service's grpc-world port must map 8080 to 8080")
			}
			hasWorldGRPCPort = true
		}
		if name == "grpc-conductor" || portNumber == 8082 {
			if name != "grpc-conductor" || portNumber != 8082 || targetPort != 8082 {
				return fmt.Errorf("World Service has a conflicting Conductor port")
			}
			hasConductorGRPCPort = true
		}
	}
	if !hasWorldGRPCPort {
		return fmt.Errorf("World Service is missing its expected grpc-world port 8080")
	}
	if hasConductorGRPCPort {
		return nil
	}
	ports = append(ports, map[string]interface{}{
		"name":       "grpc-conductor",
		"port":       int64(8082),
		"protocol":   "TCP",
		"targetPort": int64(8082),
	})
	return unstructured.SetNestedSlice(resource.Object, ports, "spec", "ports")
}

func adaptPodSpec(resource *unstructured.Unstructured, namespace string, needsDataStorePVC, needsIntrinsicIconPVC, needsGazeboMeshesPVC *bool, routing openshiftRoutingConfig) error {
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
			if err := adaptKnownHostPath(resource, volume, needsDataStorePVC, needsIntrinsicIconPVC, needsGazeboMeshesPVC); err != nil {
				return err
			}
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
	if err := adaptDeploymentForOpenShift(resource, podSpec, namespace); err != nil {
		return err
	}

	containers := append(sliceMaps(podSpec["containers"]), sliceMaps(podSpec["initContainers"])...)
	if err := adaptGazeboSimulationServiceAddress(resource, namespace, containers); err != nil {
		return fmt.Errorf("configure Gazebo simulation service address: %w", err)
	}
	zenohContainerName := ""
	if resource.GetKind() == "Deployment" {
		switch resource.GetName() {
		case "world":
			zenohContainerName = "world"
		case "simulation-service":
			zenohContainerName = "simulation-service"
		}
	}
	zenohConfigured := false
	for _, container := range containers {
		if zenohContainerName != "" && container["name"] == zenohContainerName {
			if err := ensureProjectZenohRouter(container, namespace); err != nil {
				return fmt.Errorf("configure Zenoh router for %s/%s: %w", resource.GetKind(), resource.GetName(), err)
			}
			zenohConfigured = true
		}
		if image, ok := container["image"].(string); ok {
			if lockedImage, found := openshiftImageOverrides[image]; found {
				container["image"] = lockedImage
			}
		}
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
	if zenohContainerName != "" && !zenohConfigured {
		return fmt.Errorf("Deployment %q has no %q container for the project Zenoh router override", resource.GetName(), zenohContainerName)
	}
	needsSimulationRealtimeServiceAccount, err := adaptSimulationSecurityContexts(resource, containers)
	if err != nil {
		return err
	}
	if needsSimulationRealtimeServiceAccount {
		podSpec["serviceAccountName"] = intrinsicSimRealtimeServiceAccount
		podSpec["automountServiceAccountToken"] = false
	}

	return unstructured.SetNestedMap(resource.Object, podSpec, path...)
}

type simulationSecurityPolicy struct {
	imageRepository  string
	addCapabilities  []string
	dropCapabilities []string
	serviceAccount   string
	runAsRoot        bool
	removePrivileged bool
	setROSHomeToTmp  bool
}

var simulationSecurityPolicies = map[string]simulationSecurityPolicy{
	"rs-ur-module/rs-ur-module": {
		imageRepository: "ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image",
		addCapabilities: []string{"IPC_LOCK"},
		serviceAccount:  intrinsicSimRealtimeServiceAccount,
		runAsRoot:       true,
	},
	"rs-icon/rs-icon": {
		imageRepository: "ai.intrinsic.generic_realtime_control_service.generic_icon_machine_resource",
		addCapabilities: []string{"IPC_LOCK", "SYS_NICE"},
		serviceAccount:  intrinsicSimRealtimeServiceAccount,
		runAsRoot:       true,
	},
	"rs-motion-planner-service/rs-motion-planner-service": {
		imageRepository:  "ai.intrinsic.motion_planner_service.motion-planner-service-image",
		dropCapabilities: []string{"SYS_RAWIO"},
	},
	"rs-hande-gripper/rs-hande-gripper": {
		imageRepository:  "ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml",
		removePrivileged: true,
		setROSHomeToTmp:  true,
	},
}

const (
	gzserverDeploymentName       = "gzserver"
	gzserverContainerName        = "gzserver"
	gzserverImageRepository      = "gzserver_insrc_3xcbw2p75tkkh7r6"
	gzserverMainBinary           = "/intrinsic/simulation/gazebo/grpc_proxy/proxy_server_main"
	simulationServiceAddressFlag = "--simulation_service_address="
	simulationServicePort        = "8088"
)

// adaptGazeboSimulationServiceAddress keeps the simulator proxy pointed at
// the simulation Service in the single OpenShift project. The upstream default
// assumes a separate app-intrinsic-base namespace, which does not exist here.
// Match the exact Deployment, container, and pinned image so unrelated
// workloads remain unchanged; fail closed if that expected shape drifts.
func adaptGazeboSimulationServiceAddress(resource *unstructured.Unstructured, namespace string, containers []map[string]interface{}) error {
	if resource.GetKind() != "Deployment" || resource.GetName() != gzserverDeploymentName {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}

	var target map[string]interface{}
	for _, container := range containers {
		if container["name"] != gzserverContainerName {
			continue
		}
		if target != nil {
			return fmt.Errorf("Deployment %q has duplicate %q containers", gzserverDeploymentName, gzserverContainerName)
		}
		target = container
	}
	if target == nil {
		return fmt.Errorf("Deployment %q has no %q container", gzserverDeploymentName, gzserverContainerName)
	}
	image, _ := target["image"].(string)
	if imageRepositoryName(image) != gzserverImageRepository {
		return fmt.Errorf("Deployment %q container %q has unexpected image repository %q", gzserverDeploymentName, gzserverContainerName, imageRepositoryName(image))
	}
	args, ok := target["args"].([]interface{})
	if !ok || len(args) == 0 {
		return fmt.Errorf("Deployment %q container %q has no command arguments", gzserverDeploymentName, gzserverContainerName)
	}
	binary, ok := args[0].(string)
	if !ok || binary != gzserverMainBinary {
		return fmt.Errorf("Deployment %q container %q has unexpected entrypoint", gzserverDeploymentName, gzserverContainerName)
	}

	address := simulationServiceAddressFlag + "simulation-service." + namespace + ".svc.cluster.local:" + simulationServicePort
	updated := make([]interface{}, 0, len(args)+1)
	updated = append(updated, args[0])
	foundAddress := false
	for _, rawArg := range args[1:] {
		arg, ok := rawArg.(string)
		if !ok {
			return fmt.Errorf("Deployment %q container %q has a malformed argument", gzserverDeploymentName, gzserverContainerName)
		}
		if strings.HasPrefix(arg, simulationServiceAddressFlag) {
			if foundAddress {
				return fmt.Errorf("Deployment %q container %q has duplicate simulation service arguments", gzserverDeploymentName, gzserverContainerName)
			}
			updated = append(updated, address)
			foundAddress = true
			continue
		}
		updated = append(updated, arg)
	}
	if !foundAddress {
		updated = append(updated[:1], append([]interface{}{address}, updated[1:]...)...)
	}
	target["args"] = updated
	return nil
}

func simulationSecurityPolicyFor(resourceName, containerName, image string) (simulationSecurityPolicy, bool) {
	policy, found := simulationSecurityPolicies[resourceName+"/"+containerName]
	if !found || imageRepositoryName(image) != policy.imageRepository {
		return simulationSecurityPolicy{}, false
	}
	return policy, true
}

// adaptSimulationSecurityContexts adapts only the security settings verified
// in the OMTS simulation ChartAssignment. It matches both the StatefulSet/
// container identity and image repository, preserves the capabilities verified
// on AWS for UR/ICON, and uses UID 0 only for those exact main containers under
// a dedicated ServiceAccount. It removes other simulation-only requests.
// Unknown capabilities and privileged settings remain subject to the
// fail-closed adaptSecurityContexts check below.
func adaptSimulationSecurityContexts(resource *unstructured.Unstructured, containers []map[string]interface{}) (bool, error) {
	if resource.GetKind() != "StatefulSet" {
		return false, nil
	}
	needsSimulationRealtimeServiceAccount := false
	for _, container := range containers {
		containerName, _ := container["name"].(string)
		image, _ := container["image"].(string)
		policy, found := simulationSecurityPolicyFor(resource.GetName(), containerName, image)
		if !found {
			continue
		}
		securityContext, _ := container["securityContext"].(map[string]interface{})
		if len(policy.addCapabilities) != 0 {
			if securityContext == nil {
				securityContext = map[string]interface{}{}
			}
			capabilities, _ := securityContext["capabilities"].(map[string]interface{})
			if capabilities == nil {
				capabilities = map[string]interface{}{}
			}
			requested, found := capabilities["add"]
			adds, ok := requested.([]interface{})
			if found && !ok {
				return false, fmt.Errorf("container %q has malformed capabilities.add", containerName)
			}
			for _, capability := range policy.addCapabilities {
				present := false
				for _, existing := range adds {
					if existing == capability {
						present = true
						break
					}
				}
				if !present {
					adds = append(adds, capability)
				}
			}
			capabilities["add"] = adds
			securityContext["capabilities"] = capabilities
			container["securityContext"] = securityContext
		}
		if policy.runAsRoot {
			if securityContext == nil {
				securityContext = map[string]interface{}{}
			}
			securityContext["runAsUser"] = int64(0)
			container["securityContext"] = securityContext
		}
		if securityContext != nil {
			if policy.removePrivileged {
				if privileged, _ := securityContext["privileged"].(bool); privileged {
					securityContext["privileged"] = false
				}
			}
			if len(policy.dropCapabilities) != 0 {
				capabilities, ok := securityContext["capabilities"].(map[string]interface{})
				if ok {
					add, found := capabilities["add"]
					if found {
						requested, ok := add.([]interface{})
						if !ok {
							return false, fmt.Errorf("container %q has malformed capabilities.add", containerName)
						}
						drop := make(map[string]struct{}, len(policy.dropCapabilities))
						for _, capability := range policy.dropCapabilities {
							drop[capability] = struct{}{}
						}
						kept := make([]interface{}, 0, len(requested))
						for _, capability := range requested {
							name, ok := capability.(string)
							if ok {
								if _, remove := drop[name]; remove {
									continue
								}
							}
							kept = append(kept, capability)
						}
						if len(kept) == 0 {
							delete(capabilities, "add")
						} else {
							capabilities["add"] = kept
						}
						if len(capabilities) == 0 {
							delete(securityContext, "capabilities")
						}
					}
				}
			}
		}
		if policy.serviceAccount != "" && containerRequestsCapability(container, "IPC_LOCK") {
			needsSimulationRealtimeServiceAccount = true
		}
		if policy.serviceAccount != "" {
			if err := setSimulationRealtimeResourceBounds(container); err != nil {
				return false, err
			}
		}
		if policy.setROSHomeToTmp {
			if err := setContainerEnv(container, "HOME", "/tmp"); err != nil {
				return false, err
			}
			if err := setContainerEnv(container, "ROS_HOME", "/tmp/.ros"); err != nil {
				return false, err
			}
		}
	}
	return needsSimulationRealtimeServiceAccount, nil
}

func containerRequestsCapability(container map[string]interface{}, capability string) bool {
	securityContext, _ := container["securityContext"].(map[string]interface{})
	capabilities, _ := securityContext["capabilities"].(map[string]interface{})
	add, _ := capabilities["add"].([]interface{})
	for _, requested := range add {
		if requested == capability {
			return true
		}
	}
	return false
}

func setSimulationRealtimeResourceBounds(container map[string]interface{}) error {
	resources, found, err := unstructured.NestedMap(container, "resources")
	if err != nil {
		return fmt.Errorf("container resource requirements are malformed: %w", err)
	}
	if !found {
		resources = map[string]interface{}{}
	}
	for field, values := range map[string]map[string]interface{}{
		"requests": {"cpu": "1", "memory": "1Gi"},
		"limits":   {"cpu": "4", "memory": "4Gi"},
	} {
		current, _ := resources[field].(map[string]interface{})
		if current == nil {
			current = map[string]interface{}{}
		}
		for name, value := range values {
			current[name] = value
		}
		resources[field] = current
	}
	return unstructured.SetNestedMap(container, resources, "resources")
}

func imageRepositoryName(image string) string {
	if index := strings.IndexByte(image, '@'); index >= 0 {
		image = image[:index]
	}
	if index := strings.LastIndexByte(image, '/'); index >= 0 {
		image = image[index+1:]
	}
	if index := strings.LastIndexByte(image, ':'); index >= 0 {
		image = image[:index]
	}
	return image
}

func adaptKnownHostPath(resource *unstructured.Unstructured, volume map[string]interface{}, needsDataStorePVC, needsIntrinsicIconPVC, needsGazeboMeshesPVC *bool) error {
	name, _ := volume["name"].(string)
	hostPath, ok := volume["hostPath"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("hostPath volume %q is malformed", name)
	}
	path, _ := hostPath["path"].(string)
	claimName := ""
	switch {
	case resource.GetKind() == "Deployment" && resource.GetName() == "data-store" && name == "intrinsic-db-dir":
		claimName = dataStorePVC
		*needsDataStorePVC = true
	case resource.GetKind() == "StatefulSet" &&
		(resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon" || resource.GetName() == "rs-gazebo-simulator") &&
		volume["name"] == "intrinsic-icon" && path == "/tmp/intrinsic_icon":
		claimName = intrinsicIconPVC
		*needsIntrinsicIconPVC = true
	case resource.GetKind() == "StatefulSet" && resource.GetName() == "rs-gazebo-simulator" &&
		name == "gzserver-meshes-volume" && path == "/tmp/service_volumes/intrinsic/gzserver-meshes":
		claimName = gazeboMeshesPVC
		*needsGazeboMeshesPVC = true
	default:
		return fmt.Errorf("hostPath volume %q at %q is not supported", name, path)
	}
	delete(volume, "hostPath")
	volume["persistentVolumeClaim"] = map[string]interface{}{"claimName": claimName}
	return nil
}

// ensureProjectZenohRouter overrides the SDK's compiled app-intrinsic-base
// default. Namespace string rewriting only sees manifest values; it cannot
// alter a default compiled into the World or Simulation binary.
func ensureProjectZenohRouter(container map[string]interface{}, namespace string) error {
	endpoint := "tcp/zenoh-router." + namespace + ".svc.cluster.local:7447"
	args, found, err := unstructured.NestedStringSlice(container, "args")
	if err != nil {
		return fmt.Errorf("read container args: %w", err)
	}
	if !found {
		return fmt.Errorf("container has no args")
	}

	configured := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var value string
		switch {
		case arg == "--zenoh_router":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return fmt.Errorf("--zenoh_router has no endpoint")
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, "--zenoh_router="):
			value = strings.TrimPrefix(arg, "--zenoh_router=")
		default:
			continue
		}
		if value != endpoint {
			return fmt.Errorf("container configures Zenoh endpoint %q; expected project endpoint", value)
		}
		if configured {
			return fmt.Errorf("container has multiple --zenoh_router flags")
		}
		configured = true
	}
	if configured {
		return nil
	}
	args = append(args, "--zenoh_router="+endpoint)
	return unstructured.SetNestedStringSlice(container, args, "args")
}

func adaptDeploymentForOpenShift(resource *unstructured.Unstructured, podSpec map[string]interface{}, namespace string) error {
	if resource.GetKind() != "Deployment" {
		return nil
	}

	containerName := ""
	image := ""
	watchConfigMaps := false
	configureJupyter := false
	switch resource.GetName() {
	case "resource-registry":
		containerName = "resource-registry"
		image = quayResourceRegistryImage
		watchConfigMaps = true
	case "workcell-cluster-service":
		containerName = "workcell-cluster-service"
		image = workcellServiceImage
	case "code-execution":
		containerName = "jupyter-server"
		configureJupyter = true
	default:
		return nil
	}

	containers, found, err := unstructured.NestedSlice(podSpec, "containers")
	if err != nil {
		return fmt.Errorf("read containers: %w", err)
	}
	if !found {
		return fmt.Errorf("expected container %q is missing", containerName)
	}
	matched := 0
	for _, item := range containers {
		container, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed container")
		}
		if container["name"] != containerName {
			continue
		}
		matched++
		if image != "" {
			container["image"] = image
		}
		if watchConfigMaps {
			if err := setContainerStringArg(container, "--configmap_watch_namespace", namespace); err != nil {
				return err
			}
		}
		if configureJupyter {
			if err := requireWritableEmptyDirMount(podSpec, container, jupyterHomeDir); err != nil {
				return fmt.Errorf("configure Jupyter home: %w", err)
			}
			// The pinned image puts its full invocation in Entrypoint, including a
			// wildcard bind and disabled authentication. Replace it so the API is
			// loopback-only while preserving the colocated worker's expected setup.
			if err := unstructured.SetNestedStringSlice(container, []string{"jupyter", "server"}, "command"); err != nil {
				return fmt.Errorf("set Jupyter command: %w", err)
			}
			if err := unstructured.SetNestedStringSlice(container, []string{
				"--ip=::1",
				"--port=8888",
				"--IdentityProvider.token=''",
				"--ServerApp.disable_check_xsrf=True",
				"--notebook-dir=" + jupyterHomeDir,
			}, "args"); err != nil {
				return fmt.Errorf("set Jupyter arguments: %w", err)
			}
			if err := setContainerEnv(container, "HOME", jupyterHomeDir); err != nil {
				return fmt.Errorf("set Jupyter HOME: %w", err)
			}
			if err := setContainerEnv(container, "JUPYTER_RUNTIME_DIR", jupyterRuntimeDir); err != nil {
				return fmt.Errorf("set Jupyter runtime directory: %w", err)
			}
		}
	}
	if matched != 1 {
		return fmt.Errorf("expected exactly one container %q, found %d", containerName, matched)
	}
	return unstructured.SetNestedSlice(podSpec, containers, "containers")
}

func requireWritableEmptyDirMount(podSpec, container map[string]interface{}, mountPath string) error {
	volumeMounts, found, err := unstructured.NestedSlice(container, "volumeMounts")
	if err != nil {
		return fmt.Errorf("read volume mounts: %w", err)
	}
	if !found {
		return fmt.Errorf("expected writable emptyDir mount at %q", mountPath)
	}

	volumeName := ""
	for _, item := range volumeMounts {
		mount, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed volume mount")
		}
		if mount["mountPath"] != mountPath {
			continue
		}
		readOnly, _, err := unstructured.NestedBool(mount, "readOnly")
		if err != nil {
			return fmt.Errorf("read-only setting for mount %q is malformed: %w", mountPath, err)
		}
		if readOnly {
			return fmt.Errorf("mount %q is read-only", mountPath)
		}
		name, ok := mount["name"].(string)
		if !ok || name == "" {
			return fmt.Errorf("mount %q has no volume name", mountPath)
		}
		volumeName = name
		break
	}
	if volumeName == "" {
		return fmt.Errorf("expected writable emptyDir mount at %q", mountPath)
	}

	volumes, found, err := unstructured.NestedSlice(podSpec, "volumes")
	if err != nil {
		return fmt.Errorf("read pod volumes: %w", err)
	}
	if !found {
		return fmt.Errorf("volume %q for mount %q is missing", volumeName, mountPath)
	}
	for _, item := range volumes {
		volume, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed pod volume")
		}
		if volume["name"] != volumeName {
			continue
		}
		if _, ok := volume["emptyDir"]; !ok {
			return fmt.Errorf("volume %q for mount %q must be an emptyDir", volumeName, mountPath)
		}
		return nil
	}
	return fmt.Errorf("volume %q for mount %q is missing", volumeName, mountPath)
}

func setContainerStringArg(container map[string]interface{}, name, value string) error {
	args, found, err := unstructured.NestedStringSlice(container, "args")
	if err != nil {
		return fmt.Errorf("container args are malformed: %w", err)
	}
	if !found {
		args = nil
	}
	updated := make([]string, 0, len(args)+1)
	replacement := name + "=" + value
	replaced := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == name {
			if !replaced {
				updated = append(updated, replacement)
				replaced = true
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, name+"=") {
			if !replaced {
				updated = append(updated, replacement)
				replaced = true
			}
			continue
		}
		updated = append(updated, arg)
	}
	if !replaced {
		updated = append(updated, replacement)
	}
	return unstructured.SetNestedStringSlice(container, updated, "args")
}

func setContainerEnv(container map[string]interface{}, name, value string) error {
	env, found, err := unstructured.NestedSlice(container, "env")
	if err != nil {
		return fmt.Errorf("container environment is malformed: %w", err)
	}
	if !found {
		env = []interface{}{}
	}
	updated := make([]interface{}, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("container environment entry is malformed")
		}
		if entry["name"] == name {
			if !replaced {
				updated = append(updated, map[string]interface{}{"name": name, "value": value})
				replaced = true
			}
			continue
		}
		updated = append(updated, item)
	}
	if !replaced {
		updated = append(updated, map[string]interface{}{"name": name, "value": value})
	}
	return unstructured.SetNestedSlice(container, updated, "env")
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
// limited to the verified dev01 control-plane pods and TLS xDS port; it does
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
			"kubernetes.io/metadata.name": serviceMeshControlPlaneNamespace,
		}},
		"podSelector": map[string]interface{}{"matchLabels": map[string]interface{}{
			"app":          "istiod",
			"istio.io/rev": serviceMeshControlPlaneRevision,
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
		if namespaceName == serviceMeshControlPlaneNamespace && labels["app"] == "istiod" && labels["istio.io/rev"] == serviceMeshControlPlaneRevision {
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

func adaptSecurityContexts(resource *unstructured.Unstructured) error {
	serviceAccount, _, _ := unstructured.NestedString(resource.Object, "spec", "template", "spec", "serviceAccountName")
	return adaptSecurityContextsInResource(resource.Object, resource.GetKind(), resource.GetName(), serviceAccount)
}

func adaptSecurityContextsInResource(value interface{}, resourceKind, resourceName, serviceAccount string) error {
	switch item := value.(type) {
	case map[string]interface{}:
		if context, ok := item["securityContext"].(map[string]interface{}); ok {
			image, isContainer := item["image"].(string)
			containerName, _ := item["name"].(string)
			allowSimulationRoot := false
			if isContainer {
				if privileged, _ := context["privileged"].(bool); privileged {
					return fmt.Errorf("privileged containers are not supported")
				}
				if allow, ok := context["allowPrivilegeEscalation"].(bool); ok && allow {
					return fmt.Errorf("allowPrivilegeEscalation must be false")
				}
				policy, knownSimulationContainer := simulationSecurityPolicyFor(resourceName, containerName, image)
				allowSimulationRoot = resourceKind == "StatefulSet" && knownSimulationContainer &&
					policy.runAsRoot && policy.serviceAccount == intrinsicSimRealtimeServiceAccount &&
					serviceAccount == intrinsicSimRealtimeServiceAccount
				if caps, ok := context["capabilities"].(map[string]interface{}); ok {
					if add, ok := caps["add"].([]interface{}); ok && len(add) != 0 {
						for _, capability := range add {
							allowed := false
							if resourceKind == "StatefulSet" && knownSimulationContainer &&
								policy.serviceAccount == intrinsicSimRealtimeServiceAccount &&
								serviceAccount == intrinsicSimRealtimeServiceAccount {
								for _, allowedCapability := range policy.addCapabilities {
									if capability == allowedCapability {
										allowed = true
										break
									}
								}
							}
							if allowed {
								continue
							}
							return fmt.Errorf("adding Linux capabilities %v is not supported for %s/%s container %q", add, resourceKind, resourceName, containerName)
						}
					}
				}
				context["allowPrivilegeEscalation"] = false
			}
			if allowSimulationRoot {
				uid, ok := context["runAsUser"].(int64)
				if !ok || uid != 0 {
					return fmt.Errorf("%s/%s container %q must explicitly run as UID 0 under its dedicated ServiceAccount", resourceKind, resourceName, containerName)
				}
			} else {
				delete(context, "runAsUser")
			}
			delete(context, "runAsGroup")
			if len(context) == 0 {
				delete(item, "securityContext")
			}
		}
		for key, nested := range item {
			if key == "securityContext" {
				continue
			}
			if err := adaptSecurityContextsInResource(nested, resourceKind, resourceName, serviceAccount); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, nested := range item {
			if err := adaptSecurityContextsInResource(nested, resourceKind, resourceName, serviceAccount); err != nil {
				return err
			}
		}
	}
	return nil
}
