// This project was developed with assistance from AI tools.
package chartassignment

import (
	"encoding/binary"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

const pilotNamespace = "your-project-namespace"

func object(kind, name string, fields map[string]interface{}) *unstructured.Unstructured {
	value := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name},
	}
	for key, field := range fields {
		value[key] = field
	}
	return &unstructured.Unstructured{Object: value}
}

func useControllerConfig(t *testing.T, config *ControllerConfig) {
	t.Helper()
	previous := controllerConfig
	SetControllerConfig(config)
	t.Cleanup(func() { SetControllerConfig(previous) })
}

func protobufBytesField(number uint64, value []byte) []byte {
	var encoded [binary.MaxVarintLen64]byte
	fieldTagBytes := binary.PutUvarint(encoded[:], number<<3|2)
	result := append([]byte(nil), encoded[:fieldTagBytes]...)
	lengthBytes := binary.PutUvarint(encoded[:], uint64(len(value)))
	result = append(result, encoded[:lengthBytes]...)
	return append(result, value...)
}

func testRoutingConfig() openshiftRoutingConfig {
	return openshiftRoutingConfig{
		address:         "istio-ingressgateway.mesh-system.svc.cluster.local:80",
		gateway:         "mesh-system/gateway",
		serviceSelector: map[string]interface{}{"app": "istio-ingressgateway"},
	}
}

func kinds(resources []*unstructured.Unstructured) []string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		out = append(out, resource.GetKind()+"/"+resource.GetName())
	}
	return out
}
