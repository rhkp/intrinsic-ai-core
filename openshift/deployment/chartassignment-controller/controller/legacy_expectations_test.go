// This project was developed with assistance from AI tools.
package chartassignment

import (
	"os"
	"testing"
)

const (
	openshiftStorageClassDefault       = "your-block-storage-class"
	openshiftSharedStorageClassDefault = "your-shared-filesystem-storage-class"
	openshiftGPUTaintKey               = "your-gpu-taint-key"
	quayResourceRegistryImage          = "image-registry.openshift-image-registry.svc:5000/your-project-namespace/resource-registry-openshift@sha256:40744c21511b7bdfcdea20334b5b00939b40153ebbc6647b9e7ec60192eba4cb"
	workcellServiceImage               = "image-registry.openshift-image-registry.svc:5000/your-project-namespace/workcell-cluster-service@sha256:b53e963051ab87b497773934c52223a3552ee62870764207c58668578442d9e3"
	quayZenohdImage                    = "quay.io/your-registry-org/intrinsic/zenohd@sha256:1e72a172c19cf1c48279b6d939d26da2eef1e07223208e896ed5a5801c702345"
	quayJupyterServerImage             = "quay.io/your-registry-org/intrinsic/code-execution-jupyter-server@sha256:fbd8aa00879fe5976fa431a4ca4fe9b9aa7cb548c49f00c1003f0ce9641f0ed5"
	inferenceServiceProjectCASAddress  = "content-addressable-storage.your-project-namespace.svc.cluster.local:9747"
	serviceMeshControlPlaneNamespace   = "istio-system"
	serviceMeshControlPlaneRevision    = "your-service-mesh-control-plane"
	artifactsRegistryHost              = "image-registry.openshift-image-registry.svc:5000"
)

var defaultImageOverrides = map[string]string{
	"us-central1-docker.pkg.dev/intrinsic-mirror/intrinsic-build-images/zenohd:1.7.2":                                                                                                                          quayZenohdImage,
	"ghcr.io/intrinsic-ai/code-execution-jupyter-server@sha256:e14b4e15b1b8341671c372eeadc328b25663c506827dc47e2e60e0f7b7ef1f2c":                                                                               quayJupyterServerImage,
	"image-registry.openshift-image-registry.svc:5000/your-project-namespace/ai.intrinsic.move_to_contact.move_to_contact_skill_image@sha256:0cfc737f4670c9b9b486ac6e2ec137b8ac552e32a98df5479f8ad929a9898a5b": "image-registry.openshift-image-registry.svc:5000/your-project-namespace/move-to-contact-openshift@sha256:79494450d82e41ca3f856eaf89a46396816e86b96e0c3269a57bbd1bd677e520",
	"quay.io/your-registry-org/intrinsic/artifacts_service_ap6rsu7y7q2zdhlk@sha256:0976d6fd4ce4916d8388c48709604f79a19e59c7d4adb1adb5a669f9066c0b99":                                                           "image-registry.openshift-image-registry.svc:5000/your-project-namespace/artifacts-service-openshift@sha256:7e35c59a20a0000f49076f4b99bd328a846ccb576b652d0b9a96177da9f88c42",
}

func TestMain(m *testing.M) {
	config, err := loadControllerConfig("../../../config.example.json", "../../../image-lock.json", true)
	if err != nil {
		panic(err)
	}
	config.MeshNamespace = serviceMeshControlPlaneNamespace
	SetControllerConfig(config)
	os.Exit(m.Run())
}
