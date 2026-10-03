// render-openshift-chart renders an inline ChartAssignment through the same
// OpenShift policy path used by the pilot controller. It never contacts a
// Kubernetes API or applies resources.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	controller "example.invalid/intrinsic-openshift/chartassignment-controller/controller"
	apps "github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"sigs.k8s.io/yaml"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("read ChartAssignment: %w", err)
	}
	var assignment apps.ChartAssignment
	if err := yaml.UnmarshalStrict(data, &assignment); err != nil {
		return fmt.Errorf("parse ChartAssignment: %w", err)
	}
	if assignment.Spec.Chart.Inline == "" {
		return fmt.Errorf("ChartAssignment %q must include an inline chart; remote chart retrieval is disabled", assignment.Name)
	}
	resources, err := controller.RenderOpenShiftResources(&assignment)
	if err != nil {
		return fmt.Errorf("render ChartAssignment %q: %w", assignment.Name, err)
	}

	var rendered bytes.Buffer
	for i, resource := range resources {
		if i != 0 {
			rendered.WriteString("---\n")
		}
		manifest, err := yaml.Marshal(resource.Object)
		if err != nil {
			return fmt.Errorf("encode %s/%s: %w", resource.GetKind(), resource.GetName(), err)
		}
		rendered.Write(manifest)
	}
	if _, err := output.Write(rendered.Bytes()); err != nil {
		return fmt.Errorf("write rendered manifests: %w", err)
	}
	return nil
}
