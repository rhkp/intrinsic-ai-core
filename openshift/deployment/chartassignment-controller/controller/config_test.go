// This project was developed with assistance from AI tools.
package chartassignment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTestConfig(t *testing.T, directory string, document map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(directory, "config.json")
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTestLock(t *testing.T, directory string, document map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(directory, "image-lock.json")
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testTargetConfig() map[string]interface{} {
	return map[string]interface{}{
		"schema_version": 1,
		"local": map[string]interface{}{
			"context":        "test-context",
			"api_server_url": "https://api.example.com:6443",
		},
		"target": map[string]interface{}{
			"namespace": "test-namespace",
			"registry": map[string]interface{}{
				"quay_prefix":   "quay.io/test-org/intrinsic",
				"quay_org":      "test-org",
				"internal_host": "registry.example.com:5000",
			},
			"storage": map[string]interface{}{
				"block_class":             "test-block",
				"shared_filesystem_class": "test-shared",
				"pvc_size":                "10Gi",
			},
			"scheduling": map[string]interface{}{
				"gpu_taint_key": "test-gpu",
			},
			"networking": map[string]interface{}{
				"ingress_address":       "gateway.test-namespace.svc.cluster.local:80",
				"ingress_gateway":       "test-namespace/gateway",
				"ingress_pod_selector":  map[string]string{"app": "gateway"},
				"zenoh_router_endpoint": "zenoh.test-namespace.svc.cluster.local:7447",
				"rhoai_triton_endpoint": "triton.test-namespace.svc.cluster.local:8000",
			},
			"images": map[string]interface{}{
				"lock": "image-lock.json",
			},
		},
	}
}

func testImageLock() map[string]interface{} {
	return map[string]interface{}{
		"schema_version":  1,
		"registry_prefix": "quay.io/test-org/intrinsic",
		"images": []map[string]interface{}{
			{
				"chart_repository_basename": "zenohd",
				"quay_repository":           "quay.io/test-org/intrinsic/zenohd",
				"quay_digest":               "sha256:" + strings.Repeat("1", 64),
				"chart_image_key":           "zenohd",
				"source_image":              "zenohd:1.7.2",
				"chart_source_image":        "mirror.example.com/zenohd:1.7.2",
				"controller_override":       true,
			},
			{
				"chart_repository_basename": "jupyter",
				"quay_repository":           "quay.io/test-org/intrinsic/jupyter",
				"quay_digest":               "sha256:" + strings.Repeat("2", 64),
				"chart_image_key":           "jupyter",
				"source_image":              "jupyter:latest",
				"controller_override":       false,
			},
		},
		"project_images": []map[string]interface{}{
			{
				"name":                "move-to-contact",
				"repository":          "registry.example.com:5000/test-namespace/move-to-contact",
				"digest":              "sha256:" + strings.Repeat("3", 64),
				"source_image":        "source.example.com/move-to-contact",
				"controller_override": true,
			},
			{
				"name":                "other",
				"repository":          "registry.example.com:5000/test-namespace/other",
				"digest":              "sha256:" + strings.Repeat("4", 64),
				"source_image":        "source.example.com/other",
				"controller_override": false,
			},
		},
	}
}

func TestLoadControllerConfigBuildsNormalizedModel(t *testing.T) {
	directory := t.TempDir()
	configPath := writeTestConfig(t, directory, testTargetConfig())
	lockPath := writeTestLock(t, directory, testImageLock())

	config, err := LoadControllerConfig(configPath, lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Namespace != "test-namespace" {
		t.Fatalf("namespace = %q", config.Namespace)
	}
	if config.BlockStorageClass != "test-block" || config.SharedStorageClass != "test-shared" {
		t.Fatalf("storage classes = %q, %q", config.BlockStorageClass, config.SharedStorageClass)
	}
	if len(config.ImageModel.ChartImages) != 2 || len(config.ImageModel.ProjectImages) != 2 {
		t.Fatalf("image model sizes = %d, %d", len(config.ImageModel.ChartImages), len(config.ImageModel.ProjectImages))
	}
	if got := config.ImageOverrides["mirror.example.com/zenohd:1.7.2"]; got != "quay.io/test-org/intrinsic/zenohd@sha256:"+strings.Repeat("1", 64) {
		t.Fatalf("zenohd override = %q", got)
	}
	if got := config.ImageOverrides["source.example.com/move-to-contact"]; got != "registry.example.com:5000/test-namespace/move-to-contact@sha256:"+strings.Repeat("3", 64) {
		t.Fatalf("move-to-contact override = %q", got)
	}
	if _, exists := config.ImageOverrides["jupyter:latest"]; exists {
		t.Fatal("non-override chart image was included")
	}
	if got := config.ImageModel.ProjectImages["move-to-contact"].RepositoryBasename; got != "move-to-contact" {
		t.Fatalf("project repository basename = %q", got)
	}
}

func TestCanonicalImageOverridesPreserveLegacyControllerOutput(t *testing.T) {
	directory := t.TempDir()
	document := testTargetConfig()
	target := document["target"].(map[string]interface{})
	target["namespace"] = "your-project-namespace"
	registry := target["registry"].(map[string]interface{})
	registry["quay_org"] = "your-registry-org"
	registry["quay_prefix"] = "quay.io/your-registry-org/intrinsic"
	registry["internal_host"] = "image-registry.openshift-image-registry.svc:5000"
	storage := target["storage"].(map[string]interface{})
	storage["block_class"] = "your-block-storage-class"
	storage["shared_filesystem_class"] = "your-shared-filesystem-storage-class"
	scheduling := target["scheduling"].(map[string]interface{})
	scheduling["gpu_taint_key"] = "your-gpu-taint-key"
	networking := target["networking"].(map[string]interface{})
	networking["ingress_address"] = "intrinsic-grpc-gateway.your-project-namespace.svc.cluster.local:80"
	networking["ingress_gateway"] = "your-project-namespace/intrinsic-grpc-internal"
	networking["zenoh_router_endpoint"] = "zenoh-router.your-project-namespace.svc.cluster.local:7447"
	networking["rhoai_triton_endpoint"] = "triton.your-project-namespace.svc.cluster.local:8000"
	configPath := writeTestConfig(t, directory, document)

	config, err := loadControllerConfig(configPath, "../../../image-lock.json", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.ImageOverrides, defaultImageOverrides) {
		t.Fatalf("canonical overrides differ from legacy output:\n got %#v\nwant %#v", config.ImageOverrides, defaultImageOverrides)
	}
}

func TestLoadControllerConfigRejectsRegistryPrefixMismatch(t *testing.T) {
	directory := t.TempDir()
	configPath := writeTestConfig(t, directory, testTargetConfig())
	lock := testImageLock()
	lock["registry_prefix"] = "quay.io/other-org/intrinsic"
	lockPath := writeTestLock(t, directory, lock)

	if _, err := LoadControllerConfig(configPath, lockPath); err == nil {
		t.Fatal("expected registry prefix mismatch")
	}
}

func TestLoadControllerConfigRejectsInvalidNamespace(t *testing.T) {
	directory := t.TempDir()
	document := testTargetConfig()
	document["target"].(map[string]interface{})["namespace"] = "invalid_namespace"
	configPath := writeTestConfig(t, directory, document)
	lockPath := writeTestLock(t, directory, testImageLock())

	if _, err := LoadControllerConfig(configPath, lockPath); err == nil {
		t.Fatal("expected invalid namespace")
	}
}

func TestLoadControllerConfigRejectsRepeatedProjectRepositoryDrift(t *testing.T) {
	directory := t.TempDir()
	configPath := writeTestConfig(t, directory, testTargetConfig())
	lock := testImageLock()
	lock["project_images"].([]map[string]interface{})[0]["repository"] = "registry.example.com:5000/other-namespace/move-to-contact"
	lockPath := writeTestLock(t, directory, lock)

	if _, err := LoadControllerConfig(configPath, lockPath); err == nil {
		t.Fatal("expected project repository drift rejection")
	}
}

func TestLoadControllerConfigRejectsIncompleteControllerOverride(t *testing.T) {
	directory := t.TempDir()
	configPath := writeTestConfig(t, directory, testTargetConfig())
	lock := testImageLock()
	lock["project_images"].([]map[string]interface{})[0]["source_image"] = ""
	lockPath := writeTestLock(t, directory, lock)

	if _, err := LoadControllerConfig(configPath, lockPath); err == nil {
		t.Fatal("expected missing override source rejection")
	}
}

func TestControllerConfigValidateEnvironmentCompatibility(t *testing.T) {
	config := &ControllerConfig{
		Namespace:           "test-namespace",
		BlockStorageClass:   "test-block",
		SharedStorageClass:  "test-shared",
		GPUTaintKey:         "test-gpu",
		IngressAddress:      "gateway.test-namespace.svc.cluster.local:80",
		IngressGateway:      "test-namespace/gateway",
		IngressPodSelector:  map[string]string{"app": "gateway"},
		ZenohRouterEndpoint: "zenoh.test-namespace.svc.cluster.local:7447",
		RHOAITritonEndpoint: "triton.test-namespace.svc.cluster.local:8000",
	}
	if err := config.ValidateEnvironmentCompatibility("test-namespace"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENSHIFT_STORAGE_CLASS", "other-block")
	for name, value := range map[string]string{
		"OPENSHIFT_NAMESPACE":             "other-namespace",
		"OPENSHIFT_STORAGE_CLASS":         "other-block",
		"OPENSHIFT_GPU_TAINT_KEY":         "other-gpu",
		"INTRINSIC_ZENOH_ROUTER_ENDPOINT": "zenoh.other.svc.cluster.local:7447",
		"INTRINSIC_RHOAI_TRITON_ENDPOINT": "triton.other.svc.cluster.local:8000",
	} {
		t.Setenv(name, value)
		if err := config.ValidateEnvironmentCompatibility("test-namespace"); err == nil {
			t.Fatalf("expected %s conflict", name)
		}
		os.Unsetenv(name)
	}
	if err := config.ValidateEnvironmentCompatibility("other-namespace"); err == nil {
		t.Fatal("expected watch namespace conflict")
	}
}

func TestLoadControllerConfigRejectsPlaceholders(t *testing.T) {
	directory := t.TempDir()
	document := testTargetConfig()
	document["target"].(map[string]interface{})["namespace"] = "your-namespace"
	configPath := writeTestConfig(t, directory, document)
	lockPath := writeTestLock(t, directory, testImageLock())
	if _, err := LoadControllerConfig(configPath, lockPath); err == nil {
		t.Fatal("expected placeholder rejection")
	}
}
