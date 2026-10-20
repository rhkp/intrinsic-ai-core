// This project was developed with assistance from AI tools.
package chartassignment

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func TestAdaptOpenShiftResourcesScopesRBAC(t *testing.T) {
	role := object("ClusterRole", "resource-registry", map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"apiGroups": []interface{}{""},
				"resources": []interface{}{"configmaps"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{""},
				"resources": []interface{}{"namespaces"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"registry.cloudrobotics.com"},
				"resources": []interface{}{"robots", "robots/status"},
				"verbs":     []interface{}{"get", "list", "watch"},
			},
		},
	})
	binding := object("ClusterRoleBinding", "resource-registry", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "resource-registry"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "resource-registry", "namespace": "default"}},
	})
	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{role, binding}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	if got[0].GetKind() != "Role" || got[0].GetNamespace() != pilotNamespace {
		t.Fatalf("role scope = %s/%s, want Role/%s", got[0].GetKind(), got[0].GetNamespace(), pilotNamespace)
	}
	rules, _, _ := unstructured.NestedSlice(got[0].Object, "rules")
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want only the project-scoped ConfigMap rule", len(rules))
	}
	resources, _, _ := unstructured.NestedStringSlice(rules[0].(map[string]interface{}), "resources")
	if len(resources) != 1 || resources[0] != "configmaps" {
		t.Fatalf("remaining role resources = %v", resources)
	}
	if got[1].GetKind() != "RoleBinding" || got[1].GetNamespace() != pilotNamespace {
		t.Fatalf("binding scope = %s/%s, want RoleBinding/%s", got[1].GetKind(), got[1].GetNamespace(), pilotNamespace)
	}
	kind, _, _ := unstructured.NestedString(got[1].Object, "roleRef", "kind")
	if kind != "Role" {
		t.Fatalf("roleRef.kind = %q, want Role", kind)
	}
	subjects, _, _ := unstructured.NestedSlice(got[1].Object, "subjects")
	subjectNS, _, _ := unstructured.NestedString(subjects[0].(map[string]interface{}), "namespace")
	if subjectNS != pilotNamespace {
		t.Fatalf("ServiceAccount subject namespace = %q", subjectNS)
	}
}

func TestAdaptOpenShiftResourcesPreprovisionsWorkcellRBAC(t *testing.T) {
	rules := []interface{}{
		map[string]interface{}{
			"apiGroups": []interface{}{"apps.cloudrobotics.com"},
			"resources": []interface{}{"chartassignments"},
			"verbs":     []interface{}{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"services", "configmaps"},
			"verbs":     []interface{}{"get", "list", "watch"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"pods"},
			"verbs":     []interface{}{"get", "list", "watch", "delete"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{"apps"},
			"resources": []interface{}{"deployments"},
			"verbs":     []interface{}{"get", "list", "watch"},
		},
		map[string]interface{}{
			"apiGroups": []interface{}{"batch"},
			"resources": []interface{}{"jobs"},
			"verbs":     []interface{}{"get", "list", "watch"},
		},
	}
	clusterRole := object("ClusterRole", "workcell-cluster-service", map[string]interface{}{"rules": rules})
	clusterRoleBinding := object("ClusterRoleBinding", "workcell-cluster-service", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "workcell-cluster-service"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "workcell-cluster-service", "namespace": "default"}},
	})
	projectRole := object("Role", "workcell-cluster-service", map[string]interface{}{"rules": rules})
	projectRoleBinding := object("RoleBinding", "workcell-cluster-service", map[string]interface{}{
		"roleRef":  map[string]interface{}{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "workcell-cluster-service"},
		"subjects": []interface{}{map[string]interface{}{"kind": "ServiceAccount", "name": "workcell-cluster-service", "namespace": "default"}},
	})
	keep := object("ConfigMap", "unrelated-resource", map[string]interface{}{})

	got, err := adaptOpenShiftResources([]*unstructured.Unstructured{clusterRole, clusterRoleBinding, projectRole, projectRoleBinding, keep}, pilotNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GetKind() != "ConfigMap" || got[0].GetName() != "unrelated-resource" {
		t.Fatalf("adapted resources = %v, want only unrelated ConfigMap", kinds(got))
	}
}

func TestAdaptOpenShiftResourcesRejectsUnapprovedPreprovisionedWorkcellRBAC(t *testing.T) {
	role := object("Role", "workcell-cluster-service", map[string]interface{}{
		"rules": []interface{}{map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"pods"},
			"verbs":     []interface{}{"create"},
		}},
	})
	if _, err := adaptOpenShiftResources([]*unstructured.Unstructured{role}, pilotNamespace); err == nil {
		t.Fatal("unapproved Workcell RBAC rule was accepted")
	}
}
