// This project was developed with assistance from AI tools.
package chartassignment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	controllerConfig     *ControllerConfig
	hostPortPattern      = regexp.MustCompile(`^[a-z0-9.-]+:[0-9]+$`)
	gatewayPattern       = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?/[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
	imageBasenamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

var placeholderMarkers = []string{"your-", "replace-with-", "example.invalid"}

type ControllerConfig struct {
	Namespace            string
	MeshNamespace        string
	MeshRevision         string
	InternalRegistryHost string
	BlockStorageClass    string
	SharedStorageClass   string
	GPUTaintKey          string
	IngressAddress       string
	IngressGateway       string
	IngressPodSelector   map[string]string
	ZenohRouterEndpoint  string
	RHOAITritonEndpoint  string
	ImageModel           *ImageModel
	ImageOverrides       map[string]string
}

type ImageModel struct {
	RegistryPrefix string
	ChartImages    map[string]NormalizedImage
	ProjectImages  map[string]NormalizedImage
}

type NormalizedImage struct {
	Key                  string
	RepositoryBasename   string
	Digest               string
	SourceImage          string
	Destination          string
	OpenShiftDestination string
	ControllerOverride   bool
}

type targetConfigDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Local         localConfig      `json:"local"`
	Target        targetConfigSpec `json:"target"`
}

type localConfig struct {
	Context      string `json:"context"`
	APIServerURL string `json:"api_server_url"`
}

type targetConfigSpec struct {
	Namespace  string           `json:"namespace"`
	Registry   registryConfig   `json:"registry"`
	Storage    storageConfig    `json:"storage"`
	Scheduling schedulingConfig `json:"scheduling"`
	Networking networkingConfig `json:"networking"`
	Images     imagesConfig     `json:"images"`
}

type registryConfig struct {
	QuayPrefix   string `json:"quay_prefix"`
	QuayOrg      string `json:"quay_org"`
	InternalHost string `json:"internal_host"`
}

type storageConfig struct {
	BlockClass            string `json:"block_class"`
	SharedFilesystemClass string `json:"shared_filesystem_class"`
	PVCSize               string `json:"pvc_size"`
}

type schedulingConfig struct {
	GPUTaintKey string `json:"gpu_taint_key"`
}

type networkingConfig struct {
	MeshNamespace       string            `json:"mesh_namespace"`
	MeshRevision        string            `json:"mesh_revision"`
	IngressAddress      string            `json:"ingress_address"`
	IngressGateway      string            `json:"ingress_gateway"`
	IngressPodSelector  map[string]string `json:"ingress_pod_selector"`
	ZenohRouterEndpoint string            `json:"zenoh_router_endpoint"`
	RHOAITritonEndpoint string            `json:"rhoai_triton_endpoint"`
}

type imagesConfig struct {
	Lock string `json:"lock"`
}

type imageLockDocument struct {
	SchemaVersion  int            `json:"schema_version"`
	RegistryPrefix string         `json:"-"`
	Images         []lockImage    `json:"images"`
	ProjectImages  []projectImage `json:"project_images"`
}

type lockImage struct {
	UpstreamRelease         string `json:"upstream_release"`
	ChartRepositoryBasename string `json:"chart_repository_basename"`
	QuayRepository          string `json:"-"`
	QuayDigest              string `json:"quay_digest"`
	ChartImageKey           string `json:"chart_image_key"`
	SourceImage             string `json:"source_image"`
	SourceImageDigest       string `json:"source_image_digest"`
	ChartSourceImage        string `json:"chart_source_image"`
	ControllerOverride      bool   `json:"controller_override"`
	OpenShiftImageReference string `json:"-"`
	OpenShiftImage          struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	} `json:"openshift_image"`
}

type projectImage struct {
	Name               string `json:"name"`
	Repository         string `json:"-"`
	Digest             string `json:"digest"`
	SourceImage        string `json:"source_image"`
	ControllerOverride bool   `json:"controller_override"`
}

func LoadControllerConfig(configPath, lockPath string) (*ControllerConfig, error) {
	return loadControllerConfig(configPath, lockPath, false)
}

func loadControllerConfig(configPath, lockPath string, allowPlaceholders bool) (*ControllerConfig, error) {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read target config: %w", err)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read image lock: %w", err)
	}

	var document targetConfigDocument
	decoder := json.NewDecoder(bytes.NewReader(configData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode target config: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("target config must contain one JSON object")
	}

	var portable map[string]json.RawMessage
	if err := json.Unmarshal(lockData, &portable); err != nil {
		return nil, fmt.Errorf("invalid image lock")
	}
	if _, exists := portable["registry_prefix"]; exists {
		return nil, fmt.Errorf("obsolete image lock registry prefix")
	}
	for _, collection := range []string{"images", "project_images"} {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(portable[collection], &entries); err != nil {
			return nil, fmt.Errorf("invalid image collection")
		}
		for _, entry := range entries {
			for _, field := range []string{"repository", "quay_repository", "openshift_image_reference"} {
				if _, exists := entry[field]; exists {
					return nil, fmt.Errorf("obsolete repeated image identity")
				}
			}
		}
	}
	document.Target.Networking.IngressAddress = strings.ReplaceAll(document.Target.Networking.IngressAddress, "{namespace}", document.Target.Namespace)
	document.Target.Networking.IngressGateway = strings.ReplaceAll(document.Target.Networking.IngressGateway, "{namespace}", document.Target.Namespace)
	document.Target.Networking.ZenohRouterEndpoint = strings.ReplaceAll(document.Target.Networking.ZenohRouterEndpoint, "{namespace}", document.Target.Namespace)
	document.Target.Networking.RHOAITritonEndpoint = strings.ReplaceAll(document.Target.Networking.RHOAITritonEndpoint, "{namespace}", document.Target.Namespace)
	var lock imageLockDocument
	decoder = json.NewDecoder(bytes.NewReader(lockData))
	if err := decoder.Decode(&lock); err != nil {
		return nil, fmt.Errorf("decode image lock: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("image lock must contain one JSON object")
	}

	if err := validateControllerConfig(&document, &lock); err != nil {
		return nil, err
	}
	if !allowPlaceholders {
		if err := rejectTargetPlaceholders(&document); err != nil {
			return nil, err
		}
	}
	model, err := buildImageModel(&document, &lock)
	if err != nil {
		return nil, err
	}
	overrides, err := buildImageOverrides(model)
	if err != nil {
		return nil, err
	}

	return &ControllerConfig{
		Namespace:            document.Target.Namespace,
		MeshNamespace:        document.Target.Networking.MeshNamespace,
		MeshRevision:         document.Target.Networking.MeshRevision,
		InternalRegistryHost: document.Target.Registry.InternalHost,
		BlockStorageClass:    document.Target.Storage.BlockClass,
		SharedStorageClass:   document.Target.Storage.SharedFilesystemClass,
		GPUTaintKey:          document.Target.Scheduling.GPUTaintKey,
		IngressAddress:       document.Target.Networking.IngressAddress,
		IngressGateway:       document.Target.Networking.IngressGateway,
		IngressPodSelector:   document.Target.Networking.IngressPodSelector,
		ZenohRouterEndpoint:  document.Target.Networking.ZenohRouterEndpoint,
		RHOAITritonEndpoint:  document.Target.Networking.RHOAITritonEndpoint,
		ImageModel:           model,
		ImageOverrides:       overrides,
	}, nil
}

func SetControllerConfig(config *ControllerConfig) {
	controllerConfig = config
}

func (config *ControllerConfig) ValidateEnvironmentCompatibility(watchNamespace string) error {
	if config.Namespace != watchNamespace {
		return fmt.Errorf("target config namespace %q does not match watch namespace %q", config.Namespace, watchNamespace)
	}
	comparisons := []struct {
		environment string
		value       string
	}{
		{"OPENSHIFT_NAMESPACE", config.Namespace},
		{"OPENSHIFT_STORAGE_CLASS", config.BlockStorageClass},
		{"OPENSHIFT_SHARED_STORAGE_CLASS", config.SharedStorageClass},
		{"OPENSHIFT_GPU_TAINT_KEY", config.GPUTaintKey},
		{"INTRINSIC_INGRESS_ADDRESS", config.IngressAddress},
		{"INTRINSIC_INGRESS_GATEWAY", config.IngressGateway},
		{"INTRINSIC_ZENOH_ROUTER_ENDPOINT", config.ZenohRouterEndpoint},
		{"INTRINSIC_RHOAI_TRITON_ENDPOINT", config.RHOAITritonEndpoint},
	}
	for _, comparison := range comparisons {
		if actual := os.Getenv(comparison.environment); actual != "" && actual != comparison.value {
			return fmt.Errorf("%s conflicts with target config", comparison.environment)
		}
	}
	selectorJSON, err := json.Marshal(config.IngressPodSelector)
	if err != nil {
		return fmt.Errorf("encode ingress pod selector: %w", err)
	}
	if actual := os.Getenv("INTRINSIC_INGRESS_POD_SELECTOR"); actual != "" && actual != string(selectorJSON) {
		return fmt.Errorf("INTRINSIC_INGRESS_POD_SELECTOR conflicts with target config")
	}
	return nil
}

func rejectTargetPlaceholders(document *targetConfigDocument) error {
	values := []string{
		document.Local.Context,
		document.Local.APIServerURL,
		document.Target.Namespace,
		document.Target.Registry.QuayPrefix,
		document.Target.Registry.QuayOrg,
		document.Target.Registry.InternalHost,
		document.Target.Storage.BlockClass,
		document.Target.Storage.SharedFilesystemClass,
		document.Target.Storage.PVCSize,
		document.Target.Scheduling.GPUTaintKey,
		document.Target.Networking.IngressAddress,
		document.Target.Networking.IngressGateway,
		document.Target.Networking.ZenohRouterEndpoint,
		document.Target.Networking.RHOAITritonEndpoint,
		document.Target.Images.Lock,
		document.Target.Networking.MeshNamespace,
		document.Target.Networking.MeshRevision,
	}
	for _, value := range values {
		for _, marker := range placeholderMarkers {
			if strings.Contains(value, marker) {
				return fmt.Errorf("target config contains placeholder value %q", value)
			}
		}
	}
	for key, value := range document.Target.Networking.IngressPodSelector {
		for _, marker := range placeholderMarkers {
			if strings.Contains(key, marker) || strings.Contains(value, marker) {
				return fmt.Errorf("target config ingress pod selector contains placeholder value")
			}
		}
	}
	return nil
}

func validateControllerConfig(document *targetConfigDocument, lock *imageLockDocument) error {
	if document.SchemaVersion != 1 {
		return fmt.Errorf("unsupported target config schema version %d", document.SchemaVersion)
	}
	if lock.SchemaVersion != 2 {
		return fmt.Errorf("unsupported image lock schema version %d", lock.SchemaVersion)
	}
	target := document.Target
	if target.Registry.InternalHost == "" || target.Registry.QuayPrefix == "" {
		return fmt.Errorf("target registry configuration must not be empty")
	}
	if !hostPortPattern.MatchString(target.Registry.InternalHost) {
		return fmt.Errorf("internal registry host %q is invalid", target.Registry.InternalHost)
	}
	if len(validation.IsDNS1123Label(target.Namespace)) != 0 {
		return fmt.Errorf("target namespace %q is not a DNS label", target.Namespace)
	}
	if len(validation.IsDNS1123Label(target.Storage.BlockClass)) != 0 {
		return fmt.Errorf("block storage class %q is not a DNS label", target.Storage.BlockClass)
	}
	if len(validation.IsDNS1123Label(target.Storage.SharedFilesystemClass)) != 0 {
		return fmt.Errorf("shared storage class %q is not a DNS label", target.Storage.SharedFilesystemClass)
	}
	if target.Scheduling.GPUTaintKey == "" {
		return fmt.Errorf("GPU taint key must not be empty")
	}
	if !hostPortPattern.MatchString(target.Networking.ZenohRouterEndpoint) {
		return fmt.Errorf("Zenoh router endpoint %q is invalid", target.Networking.ZenohRouterEndpoint)
	}
	if !hostPortPattern.MatchString(target.Networking.RHOAITritonEndpoint) {
		return fmt.Errorf("RHOAI Triton endpoint %q is invalid", target.Networking.RHOAITritonEndpoint)
	}
	if !strings.HasSuffix(target.Networking.IngressAddress, "."+target.Namespace+".svc.cluster.local:80") {
		return fmt.Errorf("ingress address %q is invalid", target.Networking.IngressAddress)
	}
	if !gatewayPattern.MatchString(target.Networking.IngressGateway) ||
		strings.Split(target.Networking.IngressGateway, "/")[0] != target.Namespace {
		return fmt.Errorf("ingress gateway %q is invalid", target.Networking.IngressGateway)
	}
	if len(target.Networking.IngressPodSelector) == 0 {
		return fmt.Errorf("ingress pod selector must not be empty")
	}
	for _, value := range []string{target.Networking.MeshNamespace, target.Networking.MeshRevision} {
		if len(validation.IsDNS1123Label(value)) != 0 {
			return fmt.Errorf("mesh namespace or revision is invalid")
		}
	}
	return nil
}

func buildImageModel(document *targetConfigDocument, lock *imageLockDocument) (*ImageModel, error) {
	model := &ImageModel{
		RegistryPrefix: document.Target.Registry.QuayPrefix,
		ChartImages:    make(map[string]NormalizedImage),
		ProjectImages:  make(map[string]NormalizedImage),
	}
	for _, image := range lock.Images {
		if image.ChartImageKey == "" || image.ChartRepositoryBasename == "" || image.QuayDigest == "" {
			return nil, fmt.Errorf("image lock entry is incomplete")
		}
		expectedRepository := document.Target.Registry.QuayPrefix + "/" + image.ChartRepositoryBasename
		if !digestPattern.MatchString(image.QuayDigest) {
			return nil, fmt.Errorf("image lock digest for %q is invalid", image.ChartImageKey)
		}
		if _, exists := model.ChartImages[image.ChartImageKey]; exists {
			return nil, fmt.Errorf("duplicate chart image key %q", image.ChartImageKey)
		}
		source := image.ChartSourceImage
		if source == "" {
			source = image.SourceImage
		}
		source = expandImageSource(source, document)
		if image.ControllerOverride && source == "" {
			return nil, fmt.Errorf("controller override for %q has no source image", image.ChartImageKey)
		}
		destination := expectedRepository + "@" + image.QuayDigest
		openShiftDestination := ""
		if image.OpenShiftImage.Name != "" || image.OpenShiftImage.Digest != "" {
			if len(validation.IsDNS1123Subdomain(image.OpenShiftImage.Name)) != 0 || !digestPattern.MatchString(image.OpenShiftImage.Digest) {
				return nil, fmt.Errorf("invalid OpenShift image destination")
			}
			openShiftDestination = document.Target.Registry.InternalHost + "/" + document.Target.Namespace + "/" + image.OpenShiftImage.Name + "@" + image.OpenShiftImage.Digest
		}
		if !imageBasenamePattern.MatchString(image.ChartRepositoryBasename) {
			return nil, fmt.Errorf("invalid image basename")
		}
		model.ChartImages[image.ChartImageKey] = NormalizedImage{
			Key:                  image.ChartImageKey,
			RepositoryBasename:   image.ChartRepositoryBasename,
			Digest:               image.QuayDigest,
			SourceImage:          source,
			Destination:          destination,
			OpenShiftDestination: openShiftDestination,
			ControllerOverride:   image.ControllerOverride,
		}
	}
	for _, image := range lock.ProjectImages {
		if !imageBasenamePattern.MatchString(image.Name) || image.Digest == "" {
			return nil, fmt.Errorf("project image lock entry is incomplete")
		}
		expectedRepository := document.Target.Registry.InternalHost + "/" + document.Target.Namespace + "/" + image.Name
		if !digestPattern.MatchString(image.Digest) {
			return nil, fmt.Errorf("project image digest for %q is invalid", image.Name)
		}
		if _, exists := model.ProjectImages[image.Name]; exists {
			return nil, fmt.Errorf("duplicate project image name %q", image.Name)
		}
		if image.ControllerOverride && image.SourceImage == "" {
			return nil, fmt.Errorf("controller override for %q has no source image", image.Name)
		}
		destination := expectedRepository + "@" + image.Digest
		model.ProjectImages[image.Name] = NormalizedImage{
			Key:                image.Name,
			RepositoryBasename: image.Name,
			Digest:             image.Digest,
			SourceImage:        expandImageSource(image.SourceImage, document),
			Destination:        destination,
			ControllerOverride: image.ControllerOverride,
		}
	}
	return model, nil
}

func buildImageOverrides(model *ImageModel) (map[string]string, error) {
	overrides := make(map[string]string)
	for _, image := range model.ChartImages {
		if !image.ControllerOverride || image.SourceImage == "" {
			continue
		}
		if _, exists := overrides[image.SourceImage]; exists {
			return nil, fmt.Errorf("duplicate image override source %q", image.SourceImage)
		}
		overrides[image.SourceImage] = image.Destination
	}
	for _, image := range model.ProjectImages {
		if !image.ControllerOverride || image.SourceImage == "" {
			continue
		}
		if _, exists := overrides[image.SourceImage]; exists {
			return nil, fmt.Errorf("duplicate image override source %q", image.SourceImage)
		}
		overrides[image.SourceImage] = image.Destination
	}
	return overrides, nil
}

func expandImageSource(source string, document *targetConfigDocument) string {
	return strings.NewReplacer("{namespace}", document.Target.Namespace, "{internal_host}", document.Target.Registry.InternalHost, "{quay_prefix}", document.Target.Registry.QuayPrefix).Replace(source)
}
