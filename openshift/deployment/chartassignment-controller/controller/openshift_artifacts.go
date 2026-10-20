// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

const artifactsDeploymentName = "artifacts-deployment"

const artifactsServiceContainerName = "artifacts-deployment"

const artifactsServiceImageRepository = "artifacts_service_ap6rsu7y7q2zdhlk"

const artifactsServiceBinary = "intrinsic/storage/artifacts/artifact_service"

const artifactsUpstreamContainerdVolume = "containerd-socket"

const artifactsUpstreamContainerdMount = "/run/containerd/containerd.sock"

const artifactsUpstreamContainerdHost = "/run/k3s/containerd/containerd.sock"

const artifactsContainerdContainerName = "containerd"

const artifactsRegistryCAConfigMap = "intrinsic-registry-service-ca"

const artifactsRegistryCAVolume = "artifacts-registry-service-ca"

const artifactsRegistryCAMount = "/var/run/registry-ca"

const artifactsRegistryPasswordFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"

const artifactsRegistryServiceAccount = "omts-deployer"

// adaptArtifactsServiceDeployment configures the upstream remote registry
// backend with the project-scoped OpenShift service account and CA.
func adaptArtifactsServiceDeployment(resource *unstructured.Unstructured, namespace string) error {
	if resource.GetKind() != "Deployment" || resource.GetName() != artifactsDeploymentName {
		return nil
	}

	containers, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		return fmt.Errorf("Deployment %q has no container list", artifactsDeploymentName)
	}
	var target map[string]interface{}
	for _, raw := range containers {
		container, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Deployment %q has a malformed container", artifactsDeploymentName)
		}
		if container["name"] == artifactsServiceContainerName {
			if target != nil {
				return fmt.Errorf("Deployment %q has duplicate artifact-service containers", artifactsDeploymentName)
			}
			target = container
		}
		if container["name"] == artifactsContainerdContainerName {
			return fmt.Errorf("Deployment %q already has a containerd sidecar", artifactsDeploymentName)
		}
	}
	if target == nil {
		return fmt.Errorf("Deployment %q has no artifact-service container", artifactsDeploymentName)
	}
	image, _ := target["image"].(string)
	if imageRepositoryName(image) != artifactsServiceImageRepository {
		return fmt.Errorf("Deployment %q has unexpected image repository %q", artifactsDeploymentName, imageRepositoryName(image))
	}

	args, ok := target["args"].([]interface{})
	if !ok || len(args) == 0 || args[0] != artifactsServiceBinary {
		return fmt.Errorf("Deployment %q has an unexpected artifact-service entrypoint", artifactsDeploymentName)
	}
	filteredArgs := make([]interface{}, 0, len(args))
	removedRegistryPort, containerdNamespaceCount := 0, 0
	for _, raw := range args {
		arg, ok := raw.(string)
		if !ok {
			return fmt.Errorf("Deployment %q has a malformed artifact-service argument", artifactsDeploymentName)
		}
		switch arg {
		case "--registry_port=9090":
			removedRegistryPort++
		case "--containerd_namespace=k8s.io":
			containerdNamespaceCount++
		default:
			if strings.HasPrefix(arg, "--containerd_address") {
				return fmt.Errorf("Deployment %q has an unexpected containerd address override", artifactsDeploymentName)
			}
			filteredArgs = append(filteredArgs, arg)
		}
	}
	if removedRegistryPort != 1 || containerdNamespaceCount != 1 {
		return fmt.Errorf("Deployment %q has unexpected local-registry arguments", artifactsDeploymentName)
	}
	filteredArgs = append(filteredArgs,
		"--registry="+controllerConfig.InternalRegistryHost+"/"+namespace,
		"--registry_username=token",
		"--registry_password_file="+artifactsRegistryPasswordFile,
	)
	target["args"] = filteredArgs
	env, found, err := unstructured.NestedSlice(target, "env")
	if err != nil {
		return fmt.Errorf("Deployment %q has malformed environment variables", artifactsDeploymentName)
	}
	if !found {
		env = []interface{}{}
	}
	for _, raw := range env {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Deployment %q has a malformed environment variable", artifactsDeploymentName)
		}
		if entry["name"] == "SSL_CERT_FILE" {
			return fmt.Errorf("Deployment %q already defines SSL_CERT_FILE", artifactsDeploymentName)
		}
	}
	env = append(env, map[string]interface{}{"name": "SSL_CERT_FILE", "value": artifactsRegistryCAMount + "/service-ca.crt"})
	if err := unstructured.SetNestedSlice(target, env, "env"); err != nil {
		return err
	}
	target["startupProbe"] = map[string]interface{}{
		"tcpSocket":        map[string]interface{}{"port": int64(8080)},
		"periodSeconds":    int64(2),
		"timeoutSeconds":   int64(1),
		"failureThreshold": int64(30),
	}
	target["readinessProbe"] = map[string]interface{}{
		"tcpSocket":        map[string]interface{}{"port": int64(8080)},
		"periodSeconds":    int64(3),
		"timeoutSeconds":   int64(1),
		"failureThreshold": int64(3),
	}

	ports, found, err := unstructured.NestedSlice(target, "ports")
	if err != nil || !found {
		return fmt.Errorf("Deployment %q has no container ports", artifactsDeploymentName)
	}
	filteredPorts := make([]interface{}, 0, len(ports))
	removedRegistryPort = 0
	for _, raw := range ports {
		port, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Deployment %q has a malformed container port", artifactsDeploymentName)
		}
		if port["name"] == "http-registry" {
			if port["containerPort"] != int64(9090) || port["hostPort"] != int64(17127) {
				return fmt.Errorf("Deployment %q has an unexpected local-registry port", artifactsDeploymentName)
			}
			removedRegistryPort++
			continue
		}
		if _, hasHostPort := port["hostPort"]; hasHostPort {
			return fmt.Errorf("Deployment %q has an unexpected hostPort", artifactsDeploymentName)
		}
		filteredPorts = append(filteredPorts, port)
	}
	if removedRegistryPort != 1 {
		return fmt.Errorf("Deployment %q has no expected local-registry port", artifactsDeploymentName)
	}
	if len(filteredPorts) == 0 {
		unstructured.RemoveNestedField(target, "ports")
	} else if err := unstructured.SetNestedSlice(target, filteredPorts, "ports"); err != nil {
		return err
	}

	volumeMounts, found, err := unstructured.NestedSlice(target, "volumeMounts")
	if err != nil || !found {
		return fmt.Errorf("Deployment %q has no volume mounts", artifactsDeploymentName)
	}
	filteredMounts := make([]interface{}, 0, len(volumeMounts))
	removedContainerdMount := 0
	for _, raw := range volumeMounts {
		mount, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Deployment %q has a malformed volume mount", artifactsDeploymentName)
		}
		if mount["name"] == artifactsUpstreamContainerdVolume {
			if mount["mountPath"] != artifactsUpstreamContainerdMount {
				return fmt.Errorf("Deployment %q has an unexpected containerd socket mount", artifactsDeploymentName)
			}
			removedContainerdMount++
			continue
		}
		filteredMounts = append(filteredMounts, mount)
	}
	if removedContainerdMount != 1 {
		return fmt.Errorf("Deployment %q has no expected containerd socket mount", artifactsDeploymentName)
	}
	filteredMounts = append(filteredMounts, map[string]interface{}{
		"name":      artifactsRegistryCAVolume,
		"mountPath": artifactsRegistryCAMount,
		"readOnly":  true,
	})
	if err := unstructured.SetNestedSlice(target, filteredMounts, "volumeMounts"); err != nil {
		return err
	}
	if err := unstructured.SetNestedSlice(resource.Object, containers, "spec", "template", "spec", "containers"); err != nil {
		return err
	}

	volumes, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found {
		return fmt.Errorf("Deployment %q has no pod volumes", artifactsDeploymentName)
	}
	filteredVolumes := make([]interface{}, 0, len(volumes))
	removedContainerdVolume := 0
	for _, raw := range volumes {
		volume, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Deployment %q has a malformed volume", artifactsDeploymentName)
		}
		if volume["name"] == artifactsUpstreamContainerdVolume {
			hostPath, _ := volume["hostPath"].(map[string]interface{})
			if hostPath["path"] != artifactsUpstreamContainerdHost {
				return fmt.Errorf("Deployment %q has an unexpected containerd hostPath", artifactsDeploymentName)
			}
			removedContainerdVolume++
			continue
		}
		filteredVolumes = append(filteredVolumes, volume)
	}
	if removedContainerdVolume != 1 {
		return fmt.Errorf("Deployment %q has no expected containerd hostPath", artifactsDeploymentName)
	}
	filteredVolumes = append(filteredVolumes, map[string]interface{}{
		"name": artifactsRegistryCAVolume,
		"configMap": map[string]interface{}{
			"name":  artifactsRegistryCAConfigMap,
			"items": []interface{}{map[string]interface{}{"key": "service-ca.crt", "path": "service-ca.crt"}},
		},
	})
	if err := unstructured.SetNestedSlice(resource.Object, filteredVolumes, "spec", "template", "spec", "volumes"); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(resource.Object, artifactsRegistryServiceAccount, "spec", "template", "spec", "serviceAccountName"); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(resource.Object, true, "spec", "template", "spec", "automountServiceAccountToken"); err != nil {
		return err
	}
	annotations, found, err := unstructured.NestedMap(resource.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return fmt.Errorf("Deployment %q has malformed pod-template annotations", artifactsDeploymentName)
	}
	if !found {
		annotations = map[string]interface{}{}
	}
	if _, exists := annotations["traffic.sidecar.istio.io/excludeOutboundPorts"]; exists {
		return fmt.Errorf("Deployment %q already defines mesh port exclusions", artifactsDeploymentName)
	}
	annotations["traffic.sidecar.istio.io/excludeOutboundPorts"] = "5000"
	if err := unstructured.SetNestedMap(resource.Object, annotations, "spec", "template", "metadata", "annotations"); err != nil {
		return err
	}
	return unstructured.SetNestedSlice(target, filteredMounts, "volumeMounts")
}
