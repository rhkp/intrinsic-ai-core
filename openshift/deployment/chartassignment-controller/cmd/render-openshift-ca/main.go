package main

import (
	"fmt"
	"os"

	chartassignment "example.invalid/intrinsic-openshift/chartassignment-controller/controller"
	apps "github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"sigs.k8s.io/yaml"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: render-openshift-ca <chart-assignment.yaml>")
		os.Exit(2)
	}
	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	var assignment apps.ChartAssignment
	if err := yaml.UnmarshalStrict(input, &assignment); err != nil {
		fatal(fmt.Errorf("decode ChartAssignment: %w", err))
	}
	resources, err := chartassignment.RenderOpenShiftResources(&assignment)
	if err != nil {
		fatal(err)
	}
	for _, resource := range resources {
		data, err := yaml.Marshal(resource.Object)
		if err != nil {
			fatal(err)
		}
		if _, err := os.Stdout.Write([]byte("---\n")); err != nil {
			fatal(err)
		}
		if _, err := os.Stdout.Write(data); err != nil {
			fatal(err)
		}
	}
	fmt.Fprintf(os.Stderr, "rendered and policy-checked %d namespaced resources\n", len(resources))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
