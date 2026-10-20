// This project was developed with assistance from AI tools.
package chartassignment

import (
	"fmt"

	"strings"
)

func imageOverrides() map[string]string { return controllerConfig.ImageOverrides }

func configuredDeploymentImage(name string) (string, error) {
	if controllerConfig == nil || controllerConfig.ImageModel == nil {
		return "", fmt.Errorf("canonical image model is missing for Deployment %q", name)
	}
	var image string
	switch name {
	case "resource-registry":
		image = controllerConfig.ImageModel.ChartImages["resource_registry"].OpenShiftDestination
	case "workcell-cluster-service":
		image = controllerConfig.ImageModel.ProjectImages[name].Destination
	}
	if image == "" {
		return "", fmt.Errorf("canonical image destination is missing for Deployment %q", name)
	}
	return image, nil
}

func imageRepositoryName(image string) string {
	if index := strings.IndexByte(image, '@'); index >= 0 {
		image = image[:index]
	}
	if index := strings.LastIndexByte(image, '/'); index >= 0 {
		image = image[index+1:]
	}
	if index := strings.LastIndexByte(image, ':'); index >= 0 {
		image = image[:index]
	}
	return image
}
