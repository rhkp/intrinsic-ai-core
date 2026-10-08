package chartassignment

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	flowstateRuntimeConfigMapName = "rc-flowstate-ros-bridge"
	flowstateRuntimeConfigKey     = "runtime_config.pb"
	gripperCommandSkillImageRepo  = "ai.intrinsic.gripper_cmd_skill.gripper_cmd_skill"
	moveToContactSkillImageRepo   = "ai.intrinsic.move_to_contact.move_to_contact_skill_image"
	upstreamZenohRouterEndpoint   = "tcp/zenoh-router.app-intrinsic-base.svc.cluster.local:7447"
)

// adaptConductorServiceAddress replaces upstream's headless World address for
// the Workcell conductor client with the stable project ClusterIP Service.
// Envoy resolves the upstream headless name to a pod IP; after World rolls, that
// cached endpoint can keep pointing at the terminated pod and StartSolution
// times out. The OpenShift chart exposes a dedicated conductor Service for the
// same port, so use it without changing upstream/K3S behavior.
func adaptConductorServiceAddress(resource *unstructured.Unstructured, namespace string, containers []map[string]interface{}) error {
	if resource.GetKind() != "Deployment" || resource.GetName() != "workcell-cluster-service" {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}
	var target map[string]interface{}
	for _, container := range containers {
		if container["name"] != "workcell-cluster-service" {
			continue
		}
		if target != nil {
			return fmt.Errorf("Deployment %q has duplicate workcell-cluster-service containers", resource.GetName())
		}
		target = container
	}
	if target == nil {
		return fmt.Errorf("Deployment %q has no workcell-cluster-service container", resource.GetName())
	}
	args, ok := target["args"].([]interface{})
	if !ok {
		return fmt.Errorf("Deployment %q container args are missing or malformed", resource.GetName())
	}
	address := "--conductor_service_address=conductor." + namespace + ".svc.cluster.local:8082"
	count := 0
	for i, value := range args {
		arg, ok := value.(string)
		if !ok || !strings.HasPrefix(arg, "--conductor_service_address=") {
			continue
		}
		args[i] = address
		count++
	}
	if count != 1 {
		return fmt.Errorf("Deployment %q has %d conductor_service_address args, want exactly 1", resource.GetName(), count)
	}
	target["args"] = args
	return nil
}

// adaptFlowstateRuntimeConfig rewrites the upstream Zenoh router endpoint in
// Flowstate's serialized RuntimeContext. The general manifest rewrite cannot
// see strings inside this base64-encoded protobuf ConfigMap value.
func adaptFlowstateRuntimeConfig(resource *unstructured.Unstructured, namespace string) error {
	if resource.GetKind() != "ConfigMap" || resource.GetName() != flowstateRuntimeConfigMapName {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}
	binaryData, found, err := unstructured.NestedMap(resource.Object, "binaryData")
	if err != nil {
		return fmt.Errorf("read ConfigMap binaryData: %w", err)
	}
	if !found {
		return fmt.Errorf("ConfigMap binaryData is missing")
	}
	encoded, ok := binaryData[flowstateRuntimeConfigKey].(string)
	if !ok || encoded == "" {
		return fmt.Errorf("ConfigMap binaryData[%q] is missing or malformed", flowstateRuntimeConfigKey)
	}
	runtimeConfig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode %s: %w", flowstateRuntimeConfigKey, err)
	}
	replacement := []byte("tcp/zenoh-router." + namespace + ".svc.cluster.local:7447")
	old := []byte(upstreamZenohRouterEndpoint)
	oldCount := bytes.Count(runtimeConfig, old)
	if oldCount == 0 && bytes.Count(runtimeConfig, replacement) == 2 {
		return nil // Already adapted by an earlier reconciliation.
	}
	if oldCount != 2 {
		return fmt.Errorf("runtime config has %d upstream Zenoh router endpoint(s), want 2", oldCount)
	}
	updated, replacements, err := rewriteProtobufStrings(runtimeConfig, old, replacement, 0)
	if err != nil {
		return fmt.Errorf("rewrite runtime config protobuf: %w", err)
	}
	if replacements != 2 || bytes.Count(updated, replacement) != 2 {
		return fmt.Errorf("rewrote %d upstream Zenoh router endpoint(s), want 2", replacements)
	}
	binaryData[flowstateRuntimeConfigKey] = base64.StdEncoding.EncodeToString(updated)
	return unstructured.SetNestedMap(resource.Object, binaryData, "binaryData")
}

// rewriteProtobufStrings rewrites an exact string in protobuf wire data while
// preserving wire types and updating enclosing length-delimited fields.
func rewriteProtobufStrings(message, old, replacement []byte, depth int) ([]byte, int, error) {
	if depth > 64 {
		return nil, 0, fmt.Errorf("protobuf nesting exceeds 64 levels")
	}
	updated := make([]byte, 0, len(message))
	replacements := 0
	for offset := 0; offset < len(message); {
		fieldStart := offset
		tag, tagBytes := binary.Uvarint(message[offset:])
		if tagBytes <= 0 || tag>>3 == 0 {
			return nil, 0, fmt.Errorf("invalid protobuf tag at byte %d", offset)
		}
		offset += tagBytes
		updated = append(updated, message[fieldStart:offset]...)
		switch wireType := tag & 7; wireType {
		case 0:
			_, valueBytes := binary.Uvarint(message[offset:])
			if valueBytes <= 0 {
				return nil, 0, fmt.Errorf("invalid varint at byte %d", offset)
			}
			updated = append(updated, message[offset:offset+valueBytes]...)
			offset += valueBytes
		case 1, 5:
			valueBytes := 8
			if wireType == 5 {
				valueBytes = 4
			}
			if len(message)-offset < valueBytes {
				return nil, 0, fmt.Errorf("truncated fixed-width field at byte %d", offset)
			}
			updated = append(updated, message[offset:offset+valueBytes]...)
			offset += valueBytes
		case 2:
			length, lengthBytes := binary.Uvarint(message[offset:])
			if lengthBytes <= 0 || length > uint64(len(message)-offset-lengthBytes) {
				return nil, 0, fmt.Errorf("invalid length-delimited field at byte %d", offset)
			}
			lengthStart := offset
			offset += lengthBytes
			payload := message[offset : offset+int(length)]
			offset += int(length)

			updatedPayload := payload
			fieldReplacements := 0
			if nested, count, nestedErr := rewriteProtobufStrings(payload, old, replacement, depth+1); nestedErr == nil && count > 0 {
				updatedPayload = nested
				fieldReplacements = count
			} else if bytes.Contains(payload, old) {
				fieldReplacements = bytes.Count(payload, old)
				updatedPayload = bytes.ReplaceAll(payload, old, replacement)
			}
			replacements += fieldReplacements
			if fieldReplacements == 0 {
				updated = append(updated, message[lengthStart:offset-int(length)]...)
				updated = append(updated, payload...)
				continue
			}
			var encodedLength [binary.MaxVarintLen64]byte
			encodedLengthBytes := binary.PutUvarint(encodedLength[:], uint64(len(updatedPayload)))
			updated = append(updated, encodedLength[:encodedLengthBytes]...)
			updated = append(updated, updatedPayload...)
		case 3, 4:
			return nil, 0, fmt.Errorf("protobuf groups are unsupported at byte %d", fieldStart)
		default:
			return nil, 0, fmt.Errorf("unsupported protobuf wire type %d at byte %d", tag&7, fieldStart)
		}
	}
	return updated, replacements, nil
}

func adaptZenohClient(resource *unstructured.Unstructured, namespace string, containers []map[string]interface{}) error {
	containerName := ""
	useFlag := false
	configureMoveToContact := false
	var target map[string]interface{}
	switch resource.GetKind() + "/" + resource.GetName() {
	case "StatefulSet/rs-flowstate-ros-bridge":
		containerName = "rs-flowstate-ros-bridge"
	case "StatefulSet/rs-orbbec-gemini-driver":
		containerName = "rs-orbbec-gemini-driver"
	case "StatefulSet/rs-hande-gripper":
		containerName = "rs-hande-gripper"
	case "StatefulSet/rs-gazebo-simulator":
		containerName = "rs-gazebo-simulator"
		useFlag = true
	case "Deployment/kvstore-service":
		containerName = "kvstore-service"
		useFlag = true
	default:
		for _, container := range containers {
			image, _ := container["image"].(string)
			imageRepository := imageRepositoryName(image)
			if imageRepository != gripperCommandSkillImageRepo && imageRepository != moveToContactSkillImageRepo {
				continue
			}
			if imageRepository == moveToContactSkillImageRepo {
				// The derived MTC image reads this endpoint and passes an explicit
				// Zenoh config to PubSub. The upstream image ignores the ROS-only
				// ZENOH_CONFIG_OVERRIDE variable and defaults to app-intrinsic-base.
				configureMoveToContact = true
			}
			if target != nil {
				return fmt.Errorf("%s %q contains multiple Zenoh-dependent skill containers", resource.GetKind(), resource.GetName())
			}
			target = container
		}
		if target == nil {
			return nil
		}
		containerName, _ = target["name"].(string)
	}
	if namespace == "" {
		return fmt.Errorf("target namespace is required")
	}
	if target == nil {
		for _, container := range containers {
			if container["name"] != containerName {
				continue
			}
			if target != nil {
				return fmt.Errorf("%s %q has duplicate %q containers", resource.GetKind(), resource.GetName(), containerName)
			}
			target = container
		}
	}
	if target == nil {
		return fmt.Errorf("%s %q has no %q container", resource.GetKind(), resource.GetName(), containerName)
	}
	endpoint := "tcp/zenoh-router." + namespace + ".svc.cluster.local:7447"
	if configureMoveToContact {
		if err := setContainerEnv(target, "INTRINSIC_ZENOH_ROUTER_ENDPOINT", endpoint); err != nil {
			return err
		}
		return excludeZenohPortFromIstio(resource)
	}
	if useFlag {
		if err := ensureProjectZenohRouter(target, namespace); err != nil {
			return err
		}
		return excludeZenohPortFromIstio(resource)
	}
	if err := setContainerEnv(target, "ZENOH_CONFIG_OVERRIDE", "mode=\"client\";connect/endpoints=[\""+endpoint+"\"]"); err != nil {
		return err
	}
	if err := setContainerEnv(target, "ZENOH_ROUTER_CHECK_ATTEMPTS", "-1"); err != nil {
		return err
	}
	return excludeZenohPortFromIstio(resource)
}

// excludeZenohPortFromIstio lets raw Zenoh TCP reach the project router
// directly. The upstream router is not a mesh member, so applying mesh mTLS
// to its port breaks the Zenoh handshake. Preserve any existing exclusions.
func excludeZenohPortFromIstio(resource *unstructured.Unstructured) error {
	var metadataPath []string
	switch resource.GetKind() {
	case "Pod":
		metadataPath = []string{"metadata"}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		metadataPath = []string{"spec", "template", "metadata"}
	case "CronJob":
		metadataPath = []string{"spec", "jobTemplate", "spec", "template", "metadata"}
	default:
		return nil
	}
	metadata, _, err := unstructured.NestedMap(resource.Object, metadataPath...)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	annotations, _, err := unstructured.NestedStringMap(metadata, "annotations")
	if err != nil {
		return err
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	const annotation = "traffic.sidecar.istio.io/excludeOutboundPorts"
	ports := strings.FieldsFunc(annotations[annotation], func(r rune) bool { return r == ',' || r == ' ' })
	for _, port := range ports {
		if port == "7447" {
			return nil
		}
	}
	ports = append(ports, "7447")
	annotations[annotation] = strings.Join(ports, ",")
	if err := unstructured.SetNestedStringMap(metadata, annotations, "annotations"); err != nil {
		return err
	}
	return unstructured.SetNestedMap(resource.Object, metadata, metadataPath...)
}
