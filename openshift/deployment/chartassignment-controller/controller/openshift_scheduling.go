// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

const nvidiaGPUResource = "nvidia.com/gpu"

func configuredGPUTaintKey() string { return controllerConfig.GPUTaintKey }

// ensureNvidiaGPUToleration lets workloads that already request an NVIDIA GPU
// schedule on configured GPU nodes. It leaves all other workloads unchanged.
func ensureNvidiaGPUToleration(podSpec map[string]interface{}) error {
	containers := append(sliceMaps(podSpec["containers"]), sliceMaps(podSpec["initContainers"])...)
	requiresGPU := requiresGPUCoLocation(podSpec)
	for _, container := range containers {
		resources, _ := container["resources"].(map[string]interface{})
		for _, field := range []string{"requests", "limits"} {
			quantities, _ := resources[field].(map[string]interface{})
			value, found := quantities[nvidiaGPUResource]
			if !found {
				continue
			}
			quantity := strings.TrimSpace(fmt.Sprint(value))
			if quantity != "" && quantity != "0" && quantity != "0m" {
				requiresGPU = true
				break
			}
		}
		if requiresGPU {
			break
		}
	}
	if !requiresGPU {
		return nil
	}

	tolerations, found, err := unstructured.NestedSlice(podSpec, "tolerations")
	if err != nil {
		return err
	}
	for _, item := range tolerations {
		toleration, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if toleration["key"] == configuredGPUTaintKey() && toleration["operator"] == "Equal" && toleration["value"] == "true" && toleration["effect"] == "NoSchedule" {
			return nil
		}
	}
	if !found {
		tolerations = []interface{}{}
	}
	tolerations = append(tolerations, map[string]interface{}{
		"key":      configuredGPUTaintKey(),
		"operator": "Equal",
		"value":    "true",
		"effect":   "NoSchedule",
	})
	return unstructured.SetNestedSlice(podSpec, tolerations, "tolerations")
}

func requiresGPUCoLocation(podSpec map[string]interface{}) bool {
	affinity, _, _ := unstructured.NestedMap(podSpec, "affinity")
	podAffinity, _, _ := unstructured.NestedMap(affinity, "podAffinity")
	required, _, _ := unstructured.NestedSlice(podAffinity, "requiredDuringSchedulingIgnoredDuringExecution")
	for _, item := range required {
		rule, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		matchLabels, _, _ := unstructured.NestedMap(rule, "labelSelector", "matchLabels")
		app, _ := matchLabels["app"].(string)
		if app == gazeboSimulatorName || app == "rs-icon" {
			return true
		}
	}
	return false
}

func adaptICONPodAffinity(resource *unstructured.Unstructured, podSpec map[string]interface{}, namespace string) error {
	if resource.GetKind() != "StatefulSet" {
		return nil
	}

	// ICON connects to the Gazebo HWM over a Unix socket on their shared PVC.
	// The UR HWM server creates that socket too, so it must share ICON's node.
	var targetApp string
	switch resource.GetName() {
	case "rs-icon":
		targetApp = gazeboSimulatorName
	case "rs-ur-module":
		targetApp = "rs-icon"
	default:
		return nil
	}

	affinity, found, err := unstructured.NestedMap(podSpec, "affinity")
	if err != nil {
		return err
	}
	if !found {
		affinity = map[string]interface{}{}
	}
	podAffinity, found, err := unstructured.NestedMap(affinity, "podAffinity")
	if err != nil {
		return err
	}
	if !found {
		podAffinity = map[string]interface{}{}
	}
	required, found, err := unstructured.NestedSlice(podAffinity, "requiredDuringSchedulingIgnoredDuringExecution")
	if err != nil {
		return err
	}
	if !found {
		required = []interface{}{}
	}
	for _, item := range required {
		rule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed required pod affinity rule")
		}
		matchLabels, _, err := unstructured.NestedMap(rule, "labelSelector", "matchLabels")
		if err != nil {
			return err
		}
		app, _ := matchLabels["app"].(string)
		topologyKey, _, err := unstructured.NestedString(rule, "topologyKey")
		if err != nil {
			return err
		}
		namespaces, _, err := unstructured.NestedStringSlice(rule, "namespaces")
		if err != nil {
			return err
		}
		if app == targetApp && topologyKey == "kubernetes.io/hostname" && len(namespaces) == 1 && namespaces[0] == namespace {
			return nil
		}
	}

	required = append(required, map[string]interface{}{
		"labelSelector": map[string]interface{}{
			"matchLabels": map[string]interface{}{"app": targetApp},
		},
		"namespaces":  []interface{}{namespace},
		"topologyKey": "kubernetes.io/hostname",
	})
	if err := unstructured.SetNestedSlice(podAffinity, required, "requiredDuringSchedulingIgnoredDuringExecution"); err != nil {
		return err
	}
	if err := unstructured.SetNestedMap(affinity, podAffinity, "podAffinity"); err != nil {
		return err
	}
	return unstructured.SetNestedMap(podSpec, affinity, "affinity")
}
