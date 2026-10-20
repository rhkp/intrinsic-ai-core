// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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

		// Replace the K3s ArtifactService containerd backend with the OpenShift
		// project registry, while keeping its pod-local sidecar restricted.
		if kind == "Deployment" && name == artifactsDeploymentName {
			if err := adaptArtifactsServiceDeployment(resource, namespace); err != nil {
				return nil, fmt.Errorf("adapt artifact service for OpenShift: %w", err)
			}
		}
		if kind == "ServiceMonitor" && name == "artifacts-deployment-metrics" {
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

		if err := adaptProjectRBAC(resource, namespace); err != nil {
			return nil, err
		}
		switch kind {
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
		if err := adaptSimulationServerRuntimeContext(resource, namespace); err != nil {
			return nil, fmt.Errorf("adapt simulation server runtime context for %s/%s: %w", kind, name, err)
		}
		if err := adaptFlowstateRuntimeConfig(resource, namespace); err != nil {
			return nil, fmt.Errorf("adapt runtime config for %s/%s: %w", kind, name, err)
		}
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
		needsSidecar := isGatewayBackend || kind == "Deployment" && (name == "runtime-db" || name == "kvstore-service" || name == gzserverDeploymentName) ||
			kind == "StatefulSet" && (name == "rs-flowstate-ros-bridge" || name == "rs-orbbec-gemini-driver" || name == "rs-hande-gripper")
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

	return appendRequiredStorageClaims(adapted, namespace, needsDataStorePVC, needsIntrinsicIconPVC, needsGazeboMeshesPVC), nil
}
