// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const jupyterHomeDir = "/home/defaultuser"

const jupyterRuntimeDir = jupyterHomeDir + "/.local/share/jupyter/runtime"

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
		watchConfigMaps = true
	case "workcell-cluster-service":
		containerName = "workcell-cluster-service"
	case "code-execution":
		containerName = "jupyter-server"
		configureJupyter = true
	default:
		return nil
	}
	if !configureJupyter {
		var err error
		image, err = configuredDeploymentImage(resource.GetName())
		if err != nil {
			return err
		}
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
