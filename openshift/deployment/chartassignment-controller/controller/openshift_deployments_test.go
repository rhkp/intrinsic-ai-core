// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestAdaptOpenShiftResourcesConfiguresJupyterWritableHome(t *testing.T) {
	jupyter := object("Deployment", "code-execution", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{"name": "code-execution-service", "image": "example.invalid/code-execution"},
				map[string]interface{}{
					"name":  "jupyter-server",
					"image": "example.invalid/jupyter",
					"command": []interface{}{
						"jupyter", "server", "--ip=all-interfaces",
					},
					"args": []interface{}{
						"--ip=all-interfaces",
						"--port=8888",
						"--ip", "all-interfaces",
					},
					"env": []interface{}{
						map[string]interface{}{"name": "HOME", "value": "/"},
						map[string]interface{}{"name": "PATH", "value": "/usr/bin"},
						map[string]interface{}{"name": "HOME", "value": "/root"},
						map[string]interface{}{"name": "JUPYTER_RUNTIME_DIR", "value": "/.local/share/jupyter/runtime"},
					},
					"volumeMounts": []interface{}{map[string]interface{}{
						"name":      "jupyter-home",
						"mountPath": jupyterHomeDir,
					}},
				},
			},
			"volumes": []interface{}{map[string]interface{}{
				"name":     "jupyter-home",
				"emptyDir": map[string]interface{}{"sizeLimit": "2Gi"},
			}},
		}}},
	})

	assertJupyterHome := func(resources []*unstructured.Unstructured) {
		t.Helper()
		var deployment *unstructured.Unstructured
		for _, resource := range resources {
			if resource.GetKind() == "Deployment" && resource.GetName() == "code-execution" {
				deployment = resource
				break
			}
		}
		if deployment == nil {
			t.Fatal("code-execution Deployment is missing")
		}
		containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
		if err != nil || !found || len(containers) != 2 {
			t.Fatalf("code-execution containers = %#v, found = %t, err = %v", containers, found, err)
		}
		var jupyterContainer map[string]interface{}
		for _, item := range containers {
			container := item.(map[string]interface{})
			if container["name"] == "jupyter-server" {
				jupyterContainer = container
			}
		}
		if jupyterContainer == nil {
			t.Fatal("jupyter-server container is missing")
		}
		command, found, err := unstructured.NestedStringSlice(jupyterContainer, "command")
		if err != nil || !found || strings.Join(command, " ") != "jupyter server" {
			t.Fatalf("Jupyter command = %#v, found = %t, err = %v; want [jupyter server]", command, found, err)
		}
		args, found, err := unstructured.NestedStringSlice(jupyterContainer, "args")
		if err != nil || !found {
			t.Fatalf("Jupyter arguments = %#v, found = %t, err = %v", args, found, err)
		}
		wantArgs := []string{
			"--ip=::1",
			"--port=8888",
			"--IdentityProvider.token=''",
			"--ServerApp.disable_check_xsrf=True",
			"--notebook-dir=" + jupyterHomeDir,
		}
		if strings.Join(args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Errorf("Jupyter args = %v, want %v", args, wantArgs)
		}
		env, found, err := unstructured.NestedSlice(jupyterContainer, "env")
		if err != nil || !found {
			t.Fatalf("Jupyter environment = %#v, found = %t, err = %v", env, found, err)
		}
		wantEnv := map[string]string{"HOME": jupyterHomeDir, "JUPYTER_RUNTIME_DIR": jupyterRuntimeDir, "PATH": "/usr/bin"}
		counts := make(map[string]int)
		for _, item := range env {
			entry := item.(map[string]interface{})
			name, _ := entry["name"].(string)
			counts[name]++
			if want, ok := wantEnv[name]; ok && entry["value"] != want {
				t.Errorf("Jupyter env %s = %v, want %q", name, entry["value"], want)
			}
		}
		for name := range wantEnv {
			if counts[name] != 1 {
				t.Errorf("Jupyter env %s appears %d times, want exactly once", name, counts[name])
			}
		}

		mounts, found, err := unstructured.NestedSlice(jupyterContainer, "volumeMounts")
		if err != nil || !found || len(mounts) != 1 || mounts[0].(map[string]interface{})["mountPath"] != jupyterHomeDir {
			t.Fatalf("Jupyter home mount = %#v, found = %t, err = %v", mounts, found, err)
		}
	}

	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{jupyter}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertJupyterHome(adapted)

	adaptedAgain, err := adaptOpenShiftResources(adapted, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertJupyterHome(adaptedAgain)
}

func TestAdaptOpenShiftResourcesRequiresWritableJupyterHome(t *testing.T) {
	for _, test := range []struct {
		name       string
		mountPath  string
		readOnly   bool
		wantErrMsg string
	}{
		{name: "missing home mount", mountPath: "/home/other", wantErrMsg: "expected writable emptyDir mount"},
		{name: "read-only home mount", mountPath: jupyterHomeDir, readOnly: true, wantErrMsg: "is read-only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			jupyter := object("Deployment", "code-execution", map[string]interface{}{
				"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{
						"name": "jupyter-server",
						"volumeMounts": []interface{}{map[string]interface{}{
							"name":      "jupyter-home",
							"mountPath": test.mountPath,
							"readOnly":  test.readOnly,
						}},
					}},
					"volumes": []interface{}{map[string]interface{}{
						"name":     "jupyter-home",
						"emptyDir": map[string]interface{}{},
					}},
				}}},
			})
			_, err := adaptOpenShiftResources([]*unstructured.Unstructured{jupyter}, pilotNamespace)
			if err == nil || !strings.Contains(err.Error(), test.wantErrMsg) {
				t.Fatalf("adaptOpenShiftResources() error = %v, want substring %q", err, test.wantErrMsg)
			}
		})
	}
}
