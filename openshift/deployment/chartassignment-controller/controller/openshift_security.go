// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const openshiftImagePullServiceAccount = "intrinsic-runtime"

const intrinsicSimRealtimeServiceAccount = "intrinsic-sim-realtime"

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
		// DAC_OVERRIDE is needed after OpenShift resets the simulator: the UR
		// HWM recreates its shared CephFS Unix socket as UID/GID 1001690000,
		// mode 0755. ICON runs as UID 0 with all other capabilities dropped, so
		// it otherwise cannot reconnect through the group without write access.
		addCapabilities: []string{"DAC_OVERRIDE", "IPC_LOCK", "SYS_NICE"},
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
