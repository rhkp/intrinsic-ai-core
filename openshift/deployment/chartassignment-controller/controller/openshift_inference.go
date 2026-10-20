// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strconv"
	"strings"
)

const inferenceServiceName = "rs-inference-service"

const inferenceServiceContainerName = "rs-inference-service"

const inferenceServiceImageRepository = "ai.intrinsic.inference_service.inference_service_image"

const inferenceServiceTritonContainerName = inferenceServiceContainerName + "-1"

const inferenceServiceTritonImageRepository = "ai.intrinsic.inference_service.triton_inference_server_image"

const inferenceServicePatchName = "openshift-inference-cas-address"

const inferenceServicePatchVolume = "inference-service-main-patch"

const inferenceServiceTokenVolume = "rhoai-triton-token"

const inferenceServiceTokenMountPath = "/var/run/secrets/intrinsic-rhoai"

const inferenceServiceTokenPath = inferenceServiceTokenMountPath + "/token"

const inferenceServiceMainPath = "/intrinsic_inference/assets/inference_service/inference_service_main.runfiles/intrinsic-core+/intrinsic_inference/assets/inference_service/inference_service_main.py"

const inferenceServicePython = "/intrinsic_inference/assets/inference_service/inference_service_main.runfiles/rules_python++python+python_3_11_x86_64-unknown-linux-gnu/bin/python3"

const inferenceServiceUpstreamCASAddress = "content-addressable-storage.app-intrinsic-base.svc.cluster.local:9747"

const poseEstimatorServiceName = "rs-pose-estimator-service"

const poseEstimatorContainerName = "rs-pose-estimator-service"

const poseEstimatorImageRepository = "ai.intrinsic.ioc_pose_estimator_service.ioc_pose_estimator_image"

const poseEstimatorPatchName = "openshift-pose-estimator-cas-address"

const poseEstimatorPatchVolume = "pose-estimator-service-patch"

const poseEstimatorMainPath = "/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/ioc_service_main.runfiles/intrinsic-core+/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/service/ioc_pose_estimator_service.py"

const poseEstimatorPython = "/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/ioc_service_main.runfiles/rules_python++python+python_3_11_x86_64-unknown-linux-gnu/bin/python3"

const poseEstimatorUpstreamCASAddress = "content-addressable-storage.app-intrinsic-base.svc.cluster.local:9747"

func configuredRHOAITritonEndpoint(namespace string) string {
	return controllerConfig.RHOAITritonEndpoint
}

func adaptInferenceCASAddress(resource *unstructured.Unstructured, podSpec map[string]interface{}, namespace string) error {
	if resource.GetKind() != "StatefulSet" || resource.GetName() != inferenceServiceName {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}

	containers, found, err := unstructured.NestedSlice(podSpec, "containers")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("StatefulSet %q has no containers", inferenceServiceName)
	}
	if len(containers) == 0 {
		return fmt.Errorf("StatefulSet %q has no containers", inferenceServiceName)
	}
	var appContainer map[string]interface{}
	filteredContainers := make([]interface{}, 0, len(containers)-1)
	var foundTriton bool
	for _, item := range containers {
		container, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed container")
		}
		switch container["name"] {
		case inferenceServiceContainerName:
			if appContainer != nil {
				return fmt.Errorf("StatefulSet %q has duplicate %q containers", inferenceServiceName, inferenceServiceContainerName)
			}
			appContainer = container
		case inferenceServiceTritonContainerName:
			if foundTriton {
				return fmt.Errorf("StatefulSet %q has duplicate %q containers", inferenceServiceName, inferenceServiceTritonContainerName)
			}
			tritonImage, _ := container["image"].(string)
			if imageRepositoryName(tritonImage) != inferenceServiceTritonImageRepository {
				return fmt.Errorf("StatefulSet %q has unexpected Triton image repository %q", inferenceServiceName, imageRepositoryName(tritonImage))
			}
			foundTriton = true
			continue
		}
		filteredContainers = append(filteredContainers, item)
	}
	if appContainer == nil {
		return fmt.Errorf("StatefulSet %q has no %q container", inferenceServiceName, inferenceServiceContainerName)
	}
	if !foundTriton {
		return fmt.Errorf("StatefulSet %q has no native Triton container %q", inferenceServiceName, inferenceServiceTritonContainerName)
	}
	image, _ := appContainer["image"].(string)
	if imageRepositoryName(image) != inferenceServiceImageRepository {
		return fmt.Errorf("StatefulSet %q has unexpected inference image repository %q", inferenceServiceName, imageRepositoryName(image))
	}
	initContainers, found, err := unstructured.NestedSlice(podSpec, "initContainers")
	if err != nil {
		return err
	}
	if found {
		for _, item := range initContainers {
			container, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("malformed init container")
			}
			if container["name"] == inferenceServicePatchName {
				return fmt.Errorf("StatefulSet %q already contains init container %q", inferenceServiceName, inferenceServicePatchName)
			}
		}
	}

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
			if volume["name"] == inferenceServicePatchVolume {
				return fmt.Errorf("StatefulSet %q already contains volume %q", inferenceServiceName, inferenceServicePatchVolume)
			}
			if volume["name"] == inferenceServiceTokenVolume {
				return fmt.Errorf("StatefulSet %q already contains volume %q", inferenceServiceName, inferenceServiceTokenVolume)
			}
		}
	}

	interceptor := `class _RhoaiBearerInterceptor(grpc.UnaryUnaryClientInterceptor):
  def intercept_unary_unary(self, continuation, call_details, request):
    with open(TOKEN_PATH, encoding="utf-8") as token_file:
      token = token_file.read().strip()
    metadata = tuple(call_details.metadata or ()) + (("authorization", "Bearer " + token),)
    details = _RhoaiClientCallDetails(call_details, metadata)
    return continuation(details, request)

class _RhoaiClientCallDetails(grpc.ClientCallDetails):
  def __init__(self, original, metadata):
    self.method = original.method
    self.timeout = original.timeout
    self.metadata = metadata
    self.credentials = original.credentials
    self.wait_for_ready = original.wait_for_ready
    self.compression = getattr(original, "compression", None)
`
	endpointOverride := "import os\n" +
		`_INFERENCE_SERVER_URL = os.environ["INTRINSIC_RHOAI_TRITON_ENDPOINT"]` +
		"\nTOKEN_PATH = " + strconv.Quote(inferenceServiceTokenPath) + "\n\n" + interceptor
	patchScript := fmt.Sprintf(`from pathlib import Path
source = Path(%q)
target = Path('/patch/inference_service_main.py')
text = source.read_text()

def replace_once(old, new, label):
  global text
  count = text.count(old)
  assert count == 1, f"expected one {label}, found {count}"
  text = text.replace(old, new)

replace_once(%q, %q, "upstream CAS address")
replace_once(%q, %q, "local Triton endpoint")
replace_once(%q, %q, "Triton channel construction")
replace_once(%q, %q, "inference runner construction")
replace_once("use_shm=True", "use_shm=False", "shared-memory setting")
target.write_text(text)
`, inferenceServiceMainPath,
		inferenceServiceUpstreamCASAddress, "content-addressable-storage."+namespace+".svc.cluster.local:9747",
		"_INFERENCE_SERVER_URL = \"unix:///dev/shm/triton.sock:0\"",
		endpointOverride,
		"triton_channel = grpc.insecure_channel(\n      _INFERENCE_SERVER_URL,\n      options=inference_service.GRPC_OPTIONS,\n  )",
		"triton_channel = grpc.intercept_channel(\n      grpc.insecure_channel(_INFERENCE_SERVER_URL, options=inference_service.GRPC_OPTIONS),\n      _RhoaiBearerInterceptor(),\n  )",
		"  inference_runner = inference_runner_lib.InferenceRunner(",
		"  model_controller._run_triton_action = lambda *_args, **_kwargs: None\n\n  inference_runner = inference_runner_lib.InferenceRunner(")

	initContainers = append(initContainers, map[string]interface{}{
		"name":    inferenceServicePatchName,
		"image":   image,
		"command": []interface{}{inferenceServicePython},
		"args":    []interface{}{"-c", patchScript},
		"volumeMounts": []interface{}{map[string]interface{}{
			"name":      inferenceServicePatchVolume,
			"mountPath": "/patch",
		}},
	})
	if err := unstructured.SetNestedSlice(podSpec, initContainers, "initContainers"); err != nil {
		return err
	}

	volumes = append(volumes, map[string]interface{}{
		"name":     inferenceServicePatchVolume,
		"emptyDir": map[string]interface{}{},
	})
	volumes = append(volumes, map[string]interface{}{
		"name": inferenceServiceTokenVolume,
		"projected": map[string]interface{}{
			"sources": []interface{}{map[string]interface{}{
				"serviceAccountToken": map[string]interface{}{
					"audience":          "https://kubernetes.default.svc",
					"expirationSeconds": int64(3600),
					"path":              "token",
				},
			}},
		},
	})
	if err := unstructured.SetNestedSlice(podSpec, volumes, "volumes"); err != nil {
		return err
	}

	volumeMounts, found, err := unstructured.NestedSlice(appContainer, "volumeMounts")
	if err != nil {
		return err
	}
	if found {
		for _, item := range volumeMounts {
			mount, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("malformed volume mount")
			}
			if mount["mountPath"] == inferenceServiceMainPath {
				return fmt.Errorf("StatefulSet %q already mounts %q", inferenceServiceName, inferenceServiceMainPath)
			}
			if mount["mountPath"] == inferenceServiceTokenMountPath {
				return fmt.Errorf("StatefulSet %q already mounts %q", inferenceServiceName, inferenceServiceTokenMountPath)
			}
		}
	}
	volumeMounts = append(volumeMounts, map[string]interface{}{
		"name":      inferenceServicePatchVolume,
		"mountPath": inferenceServiceMainPath,
		"subPath":   "inference_service_main.py",
		"readOnly":  true,
	})
	volumeMounts = append(volumeMounts, map[string]interface{}{
		"name":      inferenceServiceTokenVolume,
		"mountPath": inferenceServiceTokenMountPath,
		"readOnly":  true,
	})
	if err := unstructured.SetNestedSlice(appContainer, volumeMounts, "volumeMounts"); err != nil {
		return err
	}
	env, found, err := unstructured.NestedSlice(appContainer, "env")
	if err != nil {
		return err
	}
	if found {
		for _, item := range env {
			entry, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("malformed container environment variable")
			}
			if entry["name"] == "INTRINSIC_RHOAI_TRITON_ENDPOINT" {
				return fmt.Errorf("StatefulSet %q already sets INTRINSIC_RHOAI_TRITON_ENDPOINT", inferenceServiceName)
			}
		}
	}
	env = append(env, map[string]interface{}{
		"name":  "INTRINSIC_RHOAI_TRITON_ENDPOINT",
		"value": configuredRHOAITritonEndpoint(namespace),
	})
	if err := unstructured.SetNestedSlice(appContainer, env, "env"); err != nil {
		return err
	}
	return unstructured.SetNestedSlice(podSpec, filteredContainers, "containers")
}

// adaptPoseEstimatorCASAddress redirects the pinned upstream pose-estimator
// asset client to the project-local CAS service. Upstream hard-codes the
// default app namespace, which is not present on the OpenShift pilot.
func adaptPoseEstimatorCASAddress(resource *unstructured.Unstructured, podSpec map[string]interface{}, namespace string) error {
	if resource.GetKind() != "StatefulSet" || resource.GetName() != poseEstimatorServiceName {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}

	containers := sliceMaps(podSpec["containers"])
	var appContainer map[string]interface{}
	for _, container := range containers {
		if container["name"] != poseEstimatorContainerName {
			continue
		}
		if appContainer != nil {
			return fmt.Errorf("StatefulSet %q has duplicate %q containers", poseEstimatorServiceName, poseEstimatorContainerName)
		}
		appContainer = container
	}
	if appContainer == nil {
		return fmt.Errorf("StatefulSet %q has no %q container", poseEstimatorServiceName, poseEstimatorContainerName)
	}
	image, _ := appContainer["image"].(string)
	if imageRepositoryName(image) != poseEstimatorImageRepository {
		return fmt.Errorf("StatefulSet %q has unexpected pose-estimator image repository %q", poseEstimatorServiceName, imageRepositoryName(image))
	}

	initContainers, found, err := unstructured.NestedSlice(podSpec, "initContainers")
	if err != nil {
		return err
	}
	if found {
		for _, item := range initContainers {
			container, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("malformed init container")
			}
			if container["name"] == poseEstimatorPatchName {
				return fmt.Errorf("StatefulSet %q already contains init container %q", poseEstimatorServiceName, poseEstimatorPatchName)
			}
		}
	}
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
			if volume["name"] == poseEstimatorPatchVolume {
				return fmt.Errorf("StatefulSet %q already contains volume %q", poseEstimatorServiceName, poseEstimatorPatchVolume)
			}
		}
	}

	patchScript := strings.Join([]string{
		"from pathlib import Path",
		"source = Path(" + strconv.Quote(poseEstimatorMainPath) + ")",
		"target = Path('/patch/ioc_pose_estimator_service.py')",
		"text = source.read_text()",
		"old = " + strconv.Quote(poseEstimatorUpstreamCASAddress),
		"new = " + strconv.Quote("content-addressable-storage."+namespace+".svc.cluster.local:9747"),
		"assert text.count(old) == 1, f'expected one upstream CAS address, found {text.count(old)}'",
		"target.write_text(text.replace(old, new))",
	}, "; ")
	initContainers = append(initContainers, map[string]interface{}{
		"name": poseEstimatorPatchName, "image": image,
		"command": []interface{}{poseEstimatorPython}, "args": []interface{}{"-c", patchScript},
		"volumeMounts": []interface{}{map[string]interface{}{"name": poseEstimatorPatchVolume, "mountPath": "/patch"}},
	})
	if err := unstructured.SetNestedSlice(podSpec, initContainers, "initContainers"); err != nil {
		return err
	}
	volumes = append(volumes, map[string]interface{}{"name": poseEstimatorPatchVolume, "emptyDir": map[string]interface{}{}})
	if err := unstructured.SetNestedSlice(podSpec, volumes, "volumes"); err != nil {
		return err
	}
	volumeMounts, found, err := unstructured.NestedSlice(appContainer, "volumeMounts")
	if err != nil {
		return err
	}
	if found {
		for _, item := range volumeMounts {
			mount, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("malformed volume mount")
			}
			if mount["mountPath"] == poseEstimatorMainPath {
				return fmt.Errorf("StatefulSet %q already mounts %q", poseEstimatorServiceName, poseEstimatorMainPath)
			}
		}
	}
	volumeMounts = append(volumeMounts, map[string]interface{}{
		"name": poseEstimatorPatchVolume, "mountPath": poseEstimatorMainPath,
		"subPath": "ioc_pose_estimator_service.py", "readOnly": true,
	})
	if err := unstructured.SetNestedSlice(appContainer, volumeMounts, "volumeMounts"); err != nil {
		return err
	}
	return excludeZenohPortFromIstio(resource)
}
