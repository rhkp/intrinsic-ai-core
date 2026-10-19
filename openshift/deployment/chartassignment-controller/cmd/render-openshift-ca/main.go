// This project was developed with assistance from AI tools.
package main

import (
	"flag"
	"fmt"
	"os"

	chartassignment "example.invalid/intrinsic-openshift/chartassignment-controller/controller"
	apps "github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"sigs.k8s.io/yaml"
)

func main() {
	configPath := flag.String("target-config", "", "Required canonical target JSON")
	lockPath := flag.String("image-lock", "", "Required portable image lock")
	flag.Parse()
	config, err := chartassignment.LoadControllerConfig(*configPath, *lockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Canonical configuration is missing or invalid")
		os.Exit(2)
	}
	chartassignment.SetControllerConfig(config)
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: render-openshift-ca <chart-assignment.yaml>")
		os.Exit(2)
	}
	input, err := os.ReadFile(flag.Arg(0))
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
