// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func isPreprovisionedWorkcellRBAC(kind, name string) bool {
	return name == "workcell-cluster-service" && (kind == "Role" || kind == "RoleBinding")
}

func isPilotRole(name string) bool {
	switch name {
	case "resource-registry", "skill-registry", "workcell-cluster-service":
		return true
	default:
		return false
	}
}

func validatePilotRoleRules(role *unstructured.Unstructured) error {
	allowed := map[string]map[string]map[string]map[string]bool{
		"resource-registry": {
			"": {"configmaps": {"get": true, "list": true, "watch": true}},
		},
		"skill-registry": {
			"": {"configmaps": {"get": true, "list": true, "watch": true}},
		},
		"workcell-cluster-service": {
			"apps.cloudrobotics.com": {"chartassignments": {"get": true, "list": true, "watch": true, "create": true, "update": true, "patch": true, "delete": true}},
			"":                       {"services": {"get": true, "list": true, "watch": true}, "configmaps": {"get": true, "list": true, "watch": true}, "pods": {"get": true, "list": true, "watch": true, "delete": true}},
			"apps":                   {"deployments": {"get": true, "list": true, "watch": true}},
			"batch":                  {"jobs": {"get": true, "list": true, "watch": true}},
		},
	}
	rules, _, err := unstructured.NestedSlice(role.Object, "rules")
	if err != nil {
		return err
	}
	for _, item := range rules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed RBAC rule")
		}
		for _, group := range stringSlice(rule["apiGroups"]) {
			for _, resource := range stringSlice(rule["resources"]) {
				verbs, ok := allowed[role.GetName()][group][resource]
				if !ok {
					return fmt.Errorf("permission for %s/%s is not approved", group, resource)
				}
				for _, verb := range stringSlice(rule["verbs"]) {
					if !verbs[verb] {
						return fmt.Errorf("verb %s on %s/%s is not approved", verb, group, resource)
					}
				}
			}
		}
	}
	return nil
}

func normalizeRoleBinding(binding *unstructured.Unstructured, namespace string) error {
	roleRef, found, err := unstructured.NestedMap(binding.Object, "roleRef")
	if err != nil || !found {
		return fmt.Errorf("roleRef is missing or malformed")
	}
	if roleRef["kind"] != "Role" || roleRef["name"] != binding.GetName() {
		return fmt.Errorf("binding must refer to same-name project Role")
	}
	subjects, found, err := unstructured.NestedSlice(binding.Object, "subjects")
	if err != nil || !found || len(subjects) != 1 {
		return fmt.Errorf("exactly one ServiceAccount subject is required")
	}
	subject, ok := subjects[0].(map[string]interface{})
	if !ok || subject["kind"] != "ServiceAccount" || subject["name"] != binding.GetName() {
		return fmt.Errorf("binding subject must be the same-name ServiceAccount")
	}
	subject["namespace"] = namespace
	return unstructured.SetNestedSlice(binding.Object, subjects, "subjects")
}

func scopeRoleRulesToProject(role *unstructured.Unstructured) error {
	rules, found, err := unstructured.NestedSlice(role.Object, "rules")
	if err != nil || !found {
		return fmt.Errorf("rules are missing or malformed")
	}
	projectRules := make([]interface{}, 0, len(rules))
	for _, item := range rules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed RBAC rule")
		}
		if urls, found := rule["nonResourceURLs"]; found && len(stringSlice(urls)) != 0 {
			return fmt.Errorf("non-resource URL permissions cannot be represented by a namespaced Role")
		}
		groups := stringSlice(rule["apiGroups"])
		resources := stringSlice(rule["resources"])
		if contains(groups, "registry.cloudrobotics.com") {
			// The upstream Robot registry is a separate cluster service and is
			// not installed by this namespace-scoped pilot.
			continue
		}
		filtered := resources[:0]
		for _, resource := range resources {
			if resource == "*" {
				return fmt.Errorf("wildcard resource permissions are not accepted")
			}
			if isClusterScopedRBACResource(resource) {
				continue
			}
			filtered = append(filtered, resource)
		}
		if len(filtered) == 0 {
			continue
		}
		rule["resources"] = toInterfaceSlice(filtered)
		projectRules = append(projectRules, rule)
	}
	if len(projectRules) == 0 {
		return fmt.Errorf("no project-scoped permissions remain after filtering")
	}
	return unstructured.SetNestedSlice(role.Object, projectRules, "rules")
}

func isClusterScopedRBACResource(resource string) bool {
	switch resource {
	case "*", "namespaces", "nodes", "persistentvolumes", "storageclasses", "customresourcedefinitions", "clusterroles", "clusterrolebindings", "apiservices", "priorityclasses", "volumeattachments", "csidrivers", "csinodes":
		return true
	default:
		return false
	}
}

func adaptProjectRBAC(resource *unstructured.Unstructured, namespace string) error {
	kind, name := resource.GetKind(), resource.GetName()
	switch kind {
	case "ClusterRole":
		if !isPilotRole(name) {
			return fmt.Errorf("ClusterRole %q is not in the reviewed project-role allowlist", name)
		}
		if err := scopeRoleRulesToProject(resource); err != nil {
			return fmt.Errorf("scope ClusterRole %q: %w", name, err)
		}
		resource.SetKind("Role")
		if err := validatePilotRoleRules(resource); err != nil {
			return fmt.Errorf("validate Role %q: %w", name, err)
		}
	case "Role":
		if !isPilotRole(name) {
			return fmt.Errorf("Role %q is not in the reviewed project-role allowlist", name)
		}
		if err := scopeRoleRulesToProject(resource); err != nil {
			return fmt.Errorf("validate Role %q: %w", name, err)
		}
		if err := validatePilotRoleRules(resource); err != nil {
			return fmt.Errorf("validate Role %q: %w", name, err)
		}
	case "ClusterRoleBinding":
		if !isPilotRole(name) {
			return fmt.Errorf("ClusterRoleBinding %q is not in the reviewed project-binding allowlist", name)
		}
		resource.SetKind("RoleBinding")
		roleRef, found, err := unstructured.NestedMap(resource.Object, "roleRef")
		if err != nil || !found {
			return fmt.Errorf("ClusterRoleBinding %q has no roleRef", name)
		}
		if roleRef["kind"] == "ClusterRole" {
			roleRef["kind"] = "Role"
		}
		if err := unstructured.SetNestedMap(resource.Object, roleRef, "roleRef"); err != nil {
			return fmt.Errorf("update roleRef for %q: %w", name, err)
		}
		if err := normalizeRoleBinding(resource, namespace); err != nil {
			return fmt.Errorf("validate RoleBinding %q: %w", name, err)
		}
	case "RoleBinding":
		if !isPilotRole(name) {
			return fmt.Errorf("RoleBinding %q is not in the reviewed project-binding allowlist", name)
		}
		if err := normalizeRoleBinding(resource, namespace); err != nil {
			return fmt.Errorf("validate RoleBinding %q: %w", name, err)
		}
	}
	return nil
}
