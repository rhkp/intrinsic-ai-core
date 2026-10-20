// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

func selectorMatches(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, expected := range selector {
		if labels[key] != expected {
			return false
		}
	}
	return true
}

func stringSlice(value interface{}) []string {
	items, _ := value.([]interface{})
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func toInterfaceSlice(items []string) []interface{} {
	result := make([]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	return result
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func setContainerStringArg(container map[string]interface{}, name, value string) error {
	args, found, err := unstructured.NestedStringSlice(container, "args")
	if err != nil {
		return fmt.Errorf("container args are malformed: %w", err)
	}
	if !found {
		args = nil
	}
	updated := make([]string, 0, len(args)+1)
	replacement := name + "=" + value
	replaced := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == name {
			if !replaced {
				updated = append(updated, replacement)
				replaced = true
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, name+"=") {
			if !replaced {
				updated = append(updated, replacement)
				replaced = true
			}
			continue
		}
		updated = append(updated, arg)
	}
	if !replaced {
		updated = append(updated, replacement)
	}
	return unstructured.SetNestedStringSlice(container, updated, "args")
}

func setContainerEnv(container map[string]interface{}, name, value string) error {
	env, found, err := unstructured.NestedSlice(container, "env")
	if err != nil {
		return fmt.Errorf("container environment is malformed: %w", err)
	}
	if !found {
		env = []interface{}{}
	}
	updated := make([]interface{}, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("container environment entry is malformed")
		}
		if entry["name"] == name {
			if !replaced {
				updated = append(updated, map[string]interface{}{"name": name, "value": value})
				replaced = true
			}
			continue
		}
		updated = append(updated, item)
	}
	if !replaced {
		updated = append(updated, map[string]interface{}{"name": name, "value": value})
	}
	return unstructured.SetNestedSlice(container, updated, "env")
}

func sliceMaps(value interface{}) []map[string]interface{} {
	items, _ := value.([]interface{})
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]interface{}); ok {
			result = append(result, object)
		}
	}
	return result
}
