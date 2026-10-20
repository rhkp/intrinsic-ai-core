// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

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
	if resource.GetKind() == "Deployment" && resource.GetName() == artifactsDeploymentName && serviceAccount == artifactsRegistryServiceAccount {
		automount = true
	}
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
	if err := adaptICONPodAffinity(resource, podSpec, namespace); err != nil {
		return fmt.Errorf("co-locate ICON and UR hardware module: %w", err)
	}
	if err := ensureNvidiaGPUToleration(podSpec); err != nil {
		return fmt.Errorf("adapt NVIDIA GPU scheduling for %s/%s: %w", resource.GetKind(), resource.GetName(), err)
	}
	if err := adaptInferenceCASAddress(resource, podSpec, namespace); err != nil {
		return fmt.Errorf("configure inference CAS service address: %w", err)
	}
	if err := adaptPoseEstimatorCASAddress(resource, podSpec, namespace); err != nil {
		return fmt.Errorf("configure pose estimator CAS service address: %w", err)
	}

	containers := append(sliceMaps(podSpec["containers"]), sliceMaps(podSpec["initContainers"])...)
	if err := adaptConductorServiceAddress(resource, namespace, containers); err != nil {
		return fmt.Errorf("configure stable Conductor Service for %s/%s: %w", resource.GetKind(), resource.GetName(), err)
	}
	if err := adaptGazeboSimulationServiceAddress(resource, namespace, containers); err != nil {
		return fmt.Errorf("configure Gazebo simulation service address: %w", err)
	}
	if err := adaptGazeboAssetInstancesServiceAddress(resource, namespace, sliceMaps(podSpec["containers"])); err != nil {
		return fmt.Errorf("configure Gazebo Asset Instances service address: %w", err)
	}
	if err := adaptZenohClient(resource, namespace, sliceMaps(podSpec["containers"])); err != nil {
		return fmt.Errorf("configure project Zenoh client for %s/%s: %w", resource.GetKind(), resource.GetName(), err)
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
			if strings.HasPrefix(image, "localhost:17127/") {
				return fmt.Errorf("runtime image %q still targets the node-local ArtifactService registry; set the OpenShift registry endpoint in the workspace", image)
			}
			if lockedImage, found := imageOverrides()[image]; found {
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
	if zenohConfigured {
		if err := excludeZenohPortFromIstio(resource); err != nil {
			return fmt.Errorf("exclude Zenoh TCP from Service Mesh for %s/%s: %w", resource.GetKind(), resource.GetName(), err)
		}
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
