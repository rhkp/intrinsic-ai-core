// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestAdaptOpenShiftResourcesDropsOnlyKnownSimulationSecurityRequests(t *testing.T) {
	container := func(name, image string, add []interface{}, privileged bool) map[string]interface{} {
		securityContext := map[string]interface{}{}
		if len(add) != 0 {
			securityContext["capabilities"] = map[string]interface{}{"add": add}
		}
		if privileged {
			securityContext["privileged"] = true
		}
		return map[string]interface{}{
			"name":            name,
			"image":           image,
			"securityContext": securityContext,
		}
	}
	statefulSet := func(name string, containers ...interface{}) *unstructured.Unstructured {
		return object("StatefulSet", name, map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": containers,
			}}},
		})
	}
	input := []*unstructured.Unstructured{
		statefulSet("rs-ur-module", container("rs-ur-module", "registry.example:5000/your-project-namespace/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image@sha256:abcdef", nil, false)),
		statefulSet("rs-icon", container("rs-icon", "registry.example:5000/your-project-namespace/ai.intrinsic.generic_realtime_control_service.generic_icon_machine_resource:demo", []interface{}{"SYS_NICE"}, false)),
		statefulSet("rs-motion-planner-service", container("rs-motion-planner-service", "registry.example:5000/your-project-namespace/ai.intrinsic.motion_planner_service.motion-planner-service-image:demo", []interface{}{"SYS_RAWIO"}, false)),
		statefulSet("rs-hande-gripper", container("rs-hande-gripper", "registry.example:5000/your-project-namespace/ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml:demo", nil, true)),
	}

	got, err := adaptOpenShiftResources(input, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range got {
		containers, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
		if err != nil || !found || len(containers) != 1 {
			t.Fatalf("%s containers: found=%v err=%v", resource.GetName(), found, err)
		}
		container := containers[0].(map[string]interface{})
		if privileged, _, _ := unstructured.NestedBool(container, "securityContext", "privileged"); privileged {
			t.Errorf("%s retained privileged=true", resource.GetName())
		}
		gotCapabilities, found, err := unstructured.NestedStringSlice(container, "securityContext", "capabilities", "add")
		if err != nil {
			t.Fatalf("%s capabilities.add is malformed: %v", resource.GetName(), err)
		}
		wantCapabilities := map[string][]string{
			"rs-ur-module":              {"IPC_LOCK"},
			"rs-icon":                   {"SYS_NICE", "DAC_OVERRIDE", "IPC_LOCK"},
			"rs-motion-planner-service": nil,
			"rs-hande-gripper":          nil,
		}[resource.GetName()]
		if !found && len(wantCapabilities) != 0 || found && strings.Join(gotCapabilities, ",") != strings.Join(wantCapabilities, ",") {
			t.Errorf("%s capabilities.add = %v (found=%v), want %v", resource.GetName(), gotCapabilities, found, wantCapabilities)
		}
		if allow, found, err := unstructured.NestedBool(container, "securityContext", "allowPrivilegeEscalation"); err != nil || !found || allow {
			t.Errorf("%s allowPrivilegeEscalation=%v found=%v err=%v, want false", resource.GetName(), allow, found, err)
		}
		serviceAccount, found, err := unstructured.NestedString(resource.Object, "spec", "template", "spec", "serviceAccountName")
		wantServiceAccount := "intrinsic-runtime"
		if resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon" {
			wantServiceAccount = intrinsicSimRealtimeServiceAccount
		}
		if err != nil || !found || serviceAccount != wantServiceAccount {
			t.Errorf("%s serviceAccountName=%q found=%v err=%v, want %q", resource.GetName(), serviceAccount, found, err, wantServiceAccount)
		}
		uid, uidFound, uidErr := unstructured.NestedInt64(container, "securityContext", "runAsUser")
		wantsRoot := resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon"
		if wantsRoot && (uidErr != nil || !uidFound || uid != 0) {
			t.Errorf("%s runAsUser=%d found=%v err=%v, want explicit UID 0", resource.GetName(), uid, uidFound, uidErr)
		}
		if !wantsRoot && (uidErr != nil || uidFound) {
			t.Errorf("%s runAsUser=%d found=%v err=%v, want no explicit UID", resource.GetName(), uid, uidFound, uidErr)
		}
		if resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon" {
			for _, requirement := range []struct {
				field string
				name  string
				want  string
			}{
				{field: "requests", name: "cpu", want: "1"},
				{field: "requests", name: "memory", want: "1Gi"},
				{field: "limits", name: "cpu", want: "4"},
				{field: "limits", name: "memory", want: "4Gi"},
			} {
				got, found, err := unstructured.NestedString(container, "resources", requirement.field, requirement.name)
				if err != nil || !found || got != requirement.want {
					t.Errorf("%s resource %s.%s=%q found=%v err=%v, want %q", resource.GetName(), requirement.field, requirement.name, got, found, err, requirement.want)
				}
			}
		}
		if resource.GetName() == "rs-hande-gripper" {
			env, _, _ := unstructured.NestedSlice(container, "env")
			gotEnv := map[string]string{}
			for _, item := range env {
				entry := item.(map[string]interface{})
				gotEnv[entry["name"].(string)] = entry["value"].(string)
			}
			if gotEnv["HOME"] != "/tmp" || gotEnv["ROS_HOME"] != "/tmp/.ros" {
				t.Errorf("Hand-E simulator HOME=%q ROS_HOME=%q; want /tmp and /tmp/.ros", gotEnv["HOME"], gotEnv["ROS_HOME"])
			}
		}
	}
}

func TestAdaptOpenShiftResourcesRejectsUnapprovedSimulationSecurityRequests(t *testing.T) {
	statefulSet := func(name, containerName, image string, add []interface{}, privileged bool) *unstructured.Unstructured {
		securityContext := map[string]interface{}{}
		if len(add) != 0 {
			securityContext["capabilities"] = map[string]interface{}{"add": add}
		}
		if privileged {
			securityContext["privileged"] = true
		}
		return object("StatefulSet", name, map[string]interface{}{
			"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{
					"name": containerName, "image": image, "securityContext": securityContext,
				}},
			}}},
		})
	}
	tests := []struct {
		name     string
		resource *unstructured.Unstructured
		wantErr  string
	}{
		{
			name:     "unexpected capability on a simulation image",
			resource: statefulSet("rs-ur-module", "rs-ur-module", "registry/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image:demo", []interface{}{"IPC_LOCK", "SYS_ADMIN"}, false),
			wantErr:  "SYS_ADMIN",
		},
		{
			name:     "capability on an unreviewed workload",
			resource: statefulSet("rs-other", "rs-other", "registry/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image:demo", []interface{}{"IPC_LOCK"}, false),
			wantErr:  "IPC_LOCK",
		},
		{
			name:     "capability on an unreviewed image",
			resource: statefulSet("rs-ur-module", "rs-ur-module", "registry/ai.intrinsic.other_ur_module_image:demo", []interface{}{"IPC_LOCK"}, false),
			wantErr:  "IPC_LOCK",
		},
		{
			name:     "privileged non-simulation image",
			resource: statefulSet("rs-hande-gripper", "rs-hande-gripper", "registry/ai.intrinsic.hande-gripper-hardware-driver:demo", nil, true),
			wantErr:  "privileged containers are not supported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := adaptOpenShiftResources([]*unstructured.Unstructured{test.resource}, pilotNamespace)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want to contain %q", err, test.wantErr)
			}
		})
	}
}
