// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestCanonicalImageOverridesSubstituteWorkloadImages(t *testing.T) {
	source := "source.example.com/skill@sha256:" + strings.Repeat("a", 64)
	destination := "destination.example.com/skill@sha256:" + strings.Repeat("b", 64)
	config := *controllerConfig
	config.ImageOverrides = map[string]string{source: destination}
	useControllerConfig(t, &config)
	resource := object("Deployment", "override-smoke", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "override-smoke",
				"image": source,
			}},
		}}},
	})

	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{resource}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(adapted[0].Object, "spec", "template", "spec", "containers")
	if len(containers) != 1 || containers[0].(map[string]interface{})["image"] != destination {
		t.Fatalf("adapted image = %#v, want %q", containers, destination)
	}
}

func TestCanonicalDeploymentImagesUseImageModel(t *testing.T) {
	registryImage := "registry.example.com:5000/test-project/registry@sha256:" + strings.Repeat("a", 64)
	workcellImage := "registry.example.com:5000/test-project/workcell@sha256:" + strings.Repeat("b", 64)
	useControllerConfig(t, &ControllerConfig{ImageModel: &ImageModel{
		ChartImages: map[string]NormalizedImage{
			"resource_registry": {OpenShiftDestination: registryImage},
		},
		ProjectImages: map[string]NormalizedImage{
			"workcell-cluster-service": {Destination: workcellImage},
		},
	}})
	for name, expected := range map[string]string{"resource-registry": registryImage, "workcell-cluster-service": workcellImage} {
		t.Run(name, func(t *testing.T) {
			resource := object("Deployment", name, nil)
			podSpec := map[string]interface{}{"containers": []interface{}{map[string]interface{}{
				"name": name, "image": "source.example.com/upstream:original",
			}}}
			if err := adaptDeploymentForOpenShift(resource, podSpec, "test-project"); err != nil {
				t.Fatal(err)
			}
			if got := podSpec["containers"].([]interface{})[0].(map[string]interface{})["image"]; got != expected {
				t.Fatalf("image = %v, want %q", got, expected)
			}
		})
	}
}

func TestCanonicalDeploymentImagesRejectMissingDestination(t *testing.T) {
	for _, model := range []*ImageModel{nil, {}} {
		useControllerConfig(t, &ControllerConfig{ImageModel: model})
		for _, name := range []string{"resource-registry", "workcell-cluster-service"} {
			if _, err := configuredDeploymentImage(name); err == nil {
				t.Fatalf("missing canonical image for %q silently accepted", name)
			}
		}
	}
}

func TestAdaptOpenShiftResourcesRewritesDirectImagesToLockedQuayDigests(t *testing.T) {
	deployment := object("Deployment", "direct-image-check", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{
					"name":  "zenohd",
					"image": "us-central1-docker.pkg.dev/intrinsic-mirror/intrinsic-build-images/zenohd:1.7.2",
				},
				map[string]interface{}{
					"name":  "jupyter-server",
					"image": "ghcr.io/intrinsic-ai/code-execution-jupyter-server@sha256:e14b4e15b1b8341671c372eeadc328b25663c506827dc47e2e60e0f7b7ef1f2c",
				},
			},
		}}},
	})
	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{deployment}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(got[0].Object, "spec", "template", "spec", "containers")
	for i, want := range []string{quayZenohdImage, quayJupyterServerImage} {
		image, _, _ := unstructured.NestedString(containers[i].(map[string]interface{}), "image")
		if image != want {
			t.Errorf("container %d image = %q, want locked Quay digest %q", i, image, want)
		}
	}
}

func TestAdaptOpenShiftResourcesPinsNamespaceScopedServiceImages(t *testing.T) {
	registry := object("Deployment", "resource-registry", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "resource-registry",
				"image": "upstream.example/resource-registry:stale",
				"args": []interface{}{
					"--port=8080",
					"--configmap_watch_namespace", "$(POD_NAMESPACE)",
					"--configmap_watch_namespace=stale",
				},
			}},
		}}},
	})
	workcell := object("Deployment", "workcell-cluster-service", map[string]interface{}{
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "workcell-cluster-service",
				"image": "upstream.example/workcell-service:stale",
				"args":  []interface{}{"--conductor_service_address=conductor.app-intrinsic-base.svc.cluster.local:8082"},
			}},
		}}},
	})

	assertAdapted := func(resources []*unstructured.Unstructured) {
		t.Helper()
		for _, test := range []struct {
			name      string
			image     string
			watchFlag bool
		}{
			{name: "resource-registry", image: quayResourceRegistryImage, watchFlag: true},
			{name: "workcell-cluster-service", image: workcellServiceImage},
		} {
			var deployment *unstructured.Unstructured
			for _, resource := range resources {
				if resource.GetName() == test.name {
					deployment = resource
					break
				}
			}
			if deployment == nil {
				t.Fatalf("Deployment %q is missing", test.name)
			}
			containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
			if err != nil || !found || len(containers) != 1 {
				t.Fatalf("Deployment %q containers = %#v, found = %t, err = %v", test.name, containers, found, err)
			}
			container := containers[0].(map[string]interface{})
			image, _, _ := unstructured.NestedString(container, "image")
			if image != test.image {
				t.Errorf("Deployment %q image = %q, want %q", test.name, image, test.image)
			}
			if test.watchFlag {
				args, _, err := unstructured.NestedStringSlice(container, "args")
				if err != nil {
					t.Fatalf("Deployment %q args: %v", test.name, err)
				}
				count := 0
				for _, arg := range args {
					if strings.HasPrefix(arg, "--configmap_watch_namespace") {
						count++
						if arg != "--configmap_watch_namespace="+pilotNamespace {
							t.Errorf("unexpected ConfigMap watch argument %q", arg)
						}
					}
				}
				if count != 1 {
					t.Errorf("ConfigMap namespace argument count = %d, want exactly 1; args = %v", count, args)
				}
			}
		}
	}

	adapted, err := adaptOpenShiftResources([]*unstructured.Unstructured{registry, workcell}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertAdapted(adapted)

	adaptedAgain, err := adaptOpenShiftResources(adapted, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	assertAdapted(adaptedAgain)
}

func TestOpenShiftImageConfigurationMatchesImageLock(t *testing.T) {
	model := controllerConfig.ImageModel
	authorized := map[string]bool{}
	for _, image := range model.ChartImages {
		authorized[image.Destination] = true
		if image.OpenShiftDestination != "" {
			authorized[image.OpenShiftDestination] = true
		}
	}
	for _, image := range model.ProjectImages {
		authorized[image.Destination] = true
	}
	for _, image := range imageOverrides() {
		if !authorized[image] {
			t.Fatal("override destination is outside the canonical lock")
		}
	}
	if len(imageOverrides()) != 4 {
		t.Fatal("override count changed")
	}
}
