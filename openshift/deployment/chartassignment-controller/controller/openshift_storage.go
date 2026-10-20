// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const openshiftStorageClassEnv = "OPENSHIFT_STORAGE_CLASS"

const dataStorePVC = "intrinsic-data-store"

const intrinsicIconPVC = "intrinsic-icon-data"

const gazeboMeshesPVC = "intrinsic-gazebo-meshes"

const openshiftSharedStorageClassEnv = "OPENSHIFT_SHARED_STORAGE_CLASS"

func configuredStorageClass(environment string) string {
	if environment == openshiftSharedStorageClassEnv {
		return controllerConfig.SharedStorageClass
	}
	return controllerConfig.BlockStorageClass
}

func sharedStoragePVC(name, namespace, size string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app.kubernetes.io/part-of":    "intrinsic-core",
				"app.kubernetes.io/managed-by": "openshift-overlay",
			},
		},
		"spec": map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteMany"},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"storage": size},
			},
			"storageClassName": configuredStorageClass(openshiftSharedStorageClassEnv),
		},
	}}
}

// adaptPersistentDeploymentStrategy prevents a RollingUpdate from starting a
// second pod while the old pod still holds a ReadWriteOnce claim. On CSI
// storage this can leave the replacement Pending with a Multi-Attach error;
// Recreate releases the old attachment before scheduling the replacement.
func adaptPersistentDeploymentStrategy(resource *unstructured.Unstructured) error {
	if resource.GetKind() != "Deployment" {
		return nil
	}
	volumes, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	usesPVC := false
	for _, value := range volumes {
		volume, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("pod template contains a malformed volume")
		}
		if _, ok := volume["persistentVolumeClaim"]; ok {
			usesPVC = true
			break
		}
	}
	if !usesPVC {
		return nil
	}
	if err := unstructured.SetNestedField(resource.Object, "Recreate", "spec", "strategy", "type"); err != nil {
		return err
	}
	unstructured.RemoveNestedField(resource.Object, "spec", "strategy", "rollingUpdate")
	return nil
}

func adaptPVC(resource *unstructured.Unstructured) error {
	if err := unstructured.SetNestedField(resource.Object, configuredStorageClass(openshiftStorageClassEnv), "spec", "storageClassName"); err != nil {
		return err
	}
	unstructured.RemoveNestedField(resource.Object, "spec", "selector")
	accessModes, found, err := unstructured.NestedStringSlice(resource.Object, "spec", "accessModes")
	if err != nil {
		return err
	}
	if found {
		for i, mode := range accessModes {
			if mode == "ReadWriteMany" {
				accessModes[i] = "ReadWriteOnce"
			}
		}
		if err := unstructured.SetNestedStringSlice(resource.Object, accessModes, "spec", "accessModes"); err != nil {
			return err
		}
	}
	return nil
}

func adaptKnownHostPath(resource *unstructured.Unstructured, volume map[string]interface{}, needsDataStorePVC, needsIntrinsicIconPVC, needsGazeboMeshesPVC *bool) error {
	name, _ := volume["name"].(string)
	hostPath, ok := volume["hostPath"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("hostPath volume %q is malformed", name)
	}
	path, _ := hostPath["path"].(string)
	claimName := ""
	switch {
	case resource.GetKind() == "Deployment" && resource.GetName() == "data-store" && name == "intrinsic-db-dir":
		claimName = dataStorePVC
		*needsDataStorePVC = true
	case resource.GetKind() == "StatefulSet" &&
		(resource.GetName() == "rs-ur-module" || resource.GetName() == "rs-icon" || resource.GetName() == "rs-gazebo-simulator") &&
		volume["name"] == "intrinsic-icon" && path == "/tmp/intrinsic_icon":
		claimName = intrinsicIconPVC
		*needsIntrinsicIconPVC = true
	case resource.GetKind() == "StatefulSet" && resource.GetName() == "rs-gazebo-simulator" &&
		name == "gzserver-meshes-volume" && path == "/tmp/service_volumes/intrinsic/gzserver-meshes":
		claimName = gazeboMeshesPVC
		*needsGazeboMeshesPVC = true
	default:
		return fmt.Errorf("hostPath volume %q at %q is not supported", name, path)
	}
	delete(volume, "hostPath")
	volume["persistentVolumeClaim"] = map[string]interface{}{"claimName": claimName}
	return nil
}

func appendRequiredStorageClaims(adapted []*unstructured.Unstructured, namespace string, needsDataStorePVC, needsIntrinsicIconPVC, needsGazeboMeshesPVC bool) []*unstructured.Unstructured {
	if needsDataStorePVC {
		adapted = append(adapted, &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "PersistentVolumeClaim",
			"metadata": map[string]interface{}{
				"name":      dataStorePVC,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/part-of": "intrinsic-core",
				},
			},
			"spec": map[string]interface{}{
				"accessModes": []interface{}{"ReadWriteOnce"},
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"storage": "2Gi"},
				},
				"storageClassName": configuredStorageClass(openshiftStorageClassEnv),
			},
		}})
	}
	if needsIntrinsicIconPVC {
		adapted = append(adapted, sharedStoragePVC(intrinsicIconPVC, namespace, "1Gi"))
	}
	if needsGazeboMeshesPVC {
		adapted = append(adapted, sharedStoragePVC(gazeboMeshesPVC, namespace, "5Gi"))
	}
	return adapted
}
