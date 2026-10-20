// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

func adaptService(resource *unstructured.Unstructured) error {
	if err := exposeGazeboSimulationGRPCPorts(resource); err != nil {
		return err
	}
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

// exposeGazeboSimulationGRPCPorts declares the upstream Gazebo listeners on
// its headless Service so Istio can apply the project's strict mTLS policy to
// resource clients connecting to those ports. Upstream leaves these ports
// undeclared because Kubernetes does not require Service ports for headless
// DNS; OpenShift's mesh does require service-port metadata for mTLS origination.
func exposeGazeboSimulationGRPCPorts(resource *unstructured.Unstructured) error {
	if resource.GetName() != "simulation-server" {
		return nil
	}
	selector, found, err := unstructured.NestedStringMap(resource.Object, "spec", "selector")
	if err != nil {
		return err
	}
	if !found || selector["app"] != "gzserver" {
		return fmt.Errorf("Service %q does not select the upstream gzserver workload", resource.GetName())
	}
	want := []interface{}{
		map[string]interface{}{"name": "grpc-health", "port": int64(12377), "targetPort": int64(12377), "protocol": "TCP"},
		map[string]interface{}{"name": "grpc-gripper", "port": int64(12393), "targetPort": int64(12393), "protocol": "TCP"},
		map[string]interface{}{"name": "grpc-gpio", "port": int64(12394), "targetPort": int64(12394), "protocol": "TCP"},
		map[string]interface{}{"name": "grpc-spawner", "port": int64(12395), "targetPort": int64(12395), "protocol": "TCP"},
		map[string]interface{}{"name": "grpc-outfeed", "port": int64(12396), "targetPort": int64(12396), "protocol": "TCP"},
		map[string]interface{}{"name": "grpc-inputs", "port": int64(12476), "targetPort": int64(12476), "protocol": "TCP"},
		map[string]interface{}{"name": "grpc-camera", "port": int64(15333), "targetPort": int64(15333), "protocol": "TCP"},
	}
	ports, found, err := unstructured.NestedSlice(resource.Object, "spec", "ports")
	if err != nil {
		return err
	}
	if found && len(ports) != 0 {
		if fmt.Sprint(ports) == fmt.Sprint(want) {
			return nil
		}
		return fmt.Errorf("Service %q has unexpected ports; refusing to replace them", resource.GetName())
	}
	return unstructured.SetNestedSlice(resource.Object, want, "spec", "ports")
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

const gzserverDeploymentName = "gzserver"

const gzserverContainerName = "gzserver"

const gzserverImageRepository = "gzserver_insrc_3xcbw2p75tkkh7r6"

const gzserverMainBinary = "/intrinsic/simulation/gazebo/grpc_proxy/proxy_server_main"

const simulationServiceAddressFlag = "--simulation_service_address="

const simulationServicePort = "8088"

const gazeboSimulatorName = "rs-gazebo-simulator"

const gazeboSimulatorContainerName = "rs-gazebo-simulator"

const gazeboSimulatorImageRepository = "ai.intrinsic.gazebo_simulator.asset-gzserver-image"

const gazeboSimulatorMainBinary = "/intrinsic/simulation/gazebo/asset/asset_simulation_server_main"

const assetInstancesServiceAddressFlag = "--asset_instances_service_address"

const assetInstancesServiceAddressPort = "8080"

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

// adaptGazeboAssetInstancesServiceAddress overrides the simulator image's
// upstream Asset Instances default with the Service in the target project.
// Match the exact StatefulSet, container, image repository, and entrypoint so
// unrelated resources remain unchanged and shape drift fails closed.
func adaptGazeboAssetInstancesServiceAddress(resource *unstructured.Unstructured, namespace string, containers []map[string]interface{}) error {
	if resource.GetKind() != "StatefulSet" || resource.GetName() != gazeboSimulatorName {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}

	var target map[string]interface{}
	for _, container := range containers {
		if container["name"] != gazeboSimulatorContainerName {
			continue
		}
		if target != nil {
			return fmt.Errorf("StatefulSet %q has duplicate %q containers", gazeboSimulatorName, gazeboSimulatorContainerName)
		}
		target = container
	}
	if target == nil {
		return fmt.Errorf("StatefulSet %q has no %q container", gazeboSimulatorName, gazeboSimulatorContainerName)
	}
	image, _ := target["image"].(string)
	if imageRepositoryName(image) != gazeboSimulatorImageRepository {
		return fmt.Errorf("StatefulSet %q container %q has unexpected image repository %q", gazeboSimulatorName, gazeboSimulatorContainerName, imageRepositoryName(image))
	}
	args, ok := target["args"].([]interface{})
	if !ok || len(args) == 0 {
		return fmt.Errorf("StatefulSet %q container %q has no command arguments", gazeboSimulatorName, gazeboSimulatorContainerName)
	}
	binary, ok := args[0].(string)
	if !ok || binary != gazeboSimulatorMainBinary {
		return fmt.Errorf("StatefulSet %q container %q has unexpected entrypoint", gazeboSimulatorName, gazeboSimulatorContainerName)
	}

	address := assetInstancesServiceAddressFlag + "=" + "asset-instances-v1." + namespace + ".svc.cluster.local:" + assetInstancesServiceAddressPort
	updated := make([]interface{}, 0, len(args)+1)
	updated = append(updated, args[0])
	foundAddress := false
	for i := 1; i < len(args); i++ {
		arg, ok := args[i].(string)
		if !ok {
			return fmt.Errorf("StatefulSet %q container %q has a malformed argument", gazeboSimulatorName, gazeboSimulatorContainerName)
		}
		if arg == assetInstancesServiceAddressFlag {
			if foundAddress || i+1 >= len(args) {
				return fmt.Errorf("StatefulSet %q container %q has a malformed Asset Instances service argument", gazeboSimulatorName, gazeboSimulatorContainerName)
			}
			value, ok := args[i+1].(string)
			if !ok || strings.HasPrefix(value, "--") {
				return fmt.Errorf("StatefulSet %q container %q has a malformed Asset Instances service argument", gazeboSimulatorName, gazeboSimulatorContainerName)
			}
			updated = append(updated, address)
			foundAddress = true
			i++
			continue
		}
		if strings.HasPrefix(arg, assetInstancesServiceAddressFlag+"=") {
			if foundAddress {
				return fmt.Errorf("StatefulSet %q container %q has duplicate Asset Instances service arguments", gazeboSimulatorName, gazeboSimulatorContainerName)
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
