// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

func isProjectScopedKind(kind string) bool {
	switch kind {
	case "ConfigMap", "ServiceAccount", "Service", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod", "Job", "CronJob", "PersistentVolumeClaim", "Role", "RoleBinding", "ServiceMonitor", "PodMonitor", "NetworkPolicy", "VirtualService", "ChartAssignment", "ResourceSet":
		return true
	default:
		return false
	}
}

func rewriteProjectReferences(value interface{}, namespace string) {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if key == "namespace" {
				if current, ok := nested.(string); ok && (current == "default" || current == "app-intrinsic-base" || current == "app-intrinsic-app-chart") {
					item[key] = namespace
					continue
				}
			}
			rewriteProjectReferences(nested, namespace)
		}
	case []interface{}:
		for i := range item {
			rewriteProjectReferences(item[i], namespace)
		}
	case string:
		_ = item
	}
	// Replace hard-coded upstream namespace names in arguments, selectors, and
	// embedded configuration while preserving all other string values.
	rewriteStrings(value, namespace)
}

func rejectOpenShiftIncompatibleReferences(resource *unstructured.Unstructured) error {
	for _, reference := range []string{
		"app-intrinsic-base",
		"app-intrinsic-app-chart",
		"app-ingress",
		upstreamIngressAddress,
	} {
		if containsStringValue(resource.Object, reference) {
			return fmt.Errorf("unsupported upstream namespace or service reference %q remains", reference)
		}
	}
	return nil
}

func containsStringValue(value interface{}, needle string) bool {
	switch item := value.(type) {
	case map[string]interface{}:
		for _, nested := range item {
			if containsStringValue(nested, needle) {
				return true
			}
		}
	case []interface{}:
		for _, nested := range item {
			if containsStringValue(nested, needle) {
				return true
			}
		}
	case string:
		return strings.Contains(item, needle)
	}
	return false
}

func rewriteStrings(value interface{}, namespace string) {
	switch item := value.(type) {
	case map[string]interface{}:
		for key, nested := range item {
			if text, ok := nested.(string); ok {
				text = strings.ReplaceAll(text, "app-intrinsic-base", namespace)
				text = strings.ReplaceAll(text, "app-intrinsic-app-chart", namespace)
				item[key] = text
			} else {
				rewriteStrings(nested, namespace)
			}
		}
	case []interface{}:
		for i := range item {
			if text, ok := item[i].(string); ok {
				text = strings.ReplaceAll(text, "app-intrinsic-base", namespace)
				text = strings.ReplaceAll(text, "app-intrinsic-app-chart", namespace)
				item[i] = text
			} else {
				rewriteStrings(item[i], namespace)
			}
		}
	}
}
