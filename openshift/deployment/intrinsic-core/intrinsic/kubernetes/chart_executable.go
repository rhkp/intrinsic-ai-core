// Copyright 2026 Intrinsic Innovation LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This binary is used by the executable output of helm_chart build rules.
// It is not intended to be used directly.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	appstat "intrinsic/kubernetes/appstat/inspect"
	"intrinsic/kubernetes/chartassignmentrelease"
	"intrinsic/kubernetes/values"
	"intrinsic/kubernetes/workcell_spec/chartassignment"
	"intrinsic/kubernetes/workcell_spec/workcellspec"
	"intrinsic/production/imagepublisher"
	intrinsicinit "intrinsic/production/intrinsic"
	"intrinsic/tools/inctl/cmd/chartstop"
	"intrinsic/tools/inctl/cmd/lint"
	"intrinsic/tools/inctl/util/traceutil"
	"intrinsic/tools/internal/k8sclientutil"
	"intrinsic/util/path_resolver/pathresolver"

	log "github.com/golang/glog"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	apipb "intrinsic/kubernetes/workcell_spec/proto/transfer_go_proto"
)

var rootCmd = &cobra.Command{
	Use:   "chart",
	Short: "chart is the executable for helm_chart targets",
	Long:  `chart executable provides functionality to interact with a helm_chart target like starting or stopping.`,
	// Do not print usage when a command exits with an error.
	SilenceUsage: true,
}

func readWorkcellSpec(rootFS fs.FS, path string) (*apipb.WorkcellSpec, error) {
	b, err := fs.ReadFile(rootFS, path)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot read chart assignment %q", path)
	}
	spec, err := workcellspec.Unmarshal(bytes.NewBuffer(b))
	if err != nil {
		return nil, errors.Wrapf(err, "cannot unmarshal %q", path)
	}
	return spec, nil
}

// validateSpec checks that the workcell spec has exactly one chart assignment.
func validateSpec(spec *apipb.WorkcellSpec) error {
	if n := len(spec.Items); n != 1 {
		return fmt.Errorf("Got %d items, expected exactly 1 ChartAssignment", n)
	}
	if it := spec.Items[0]; it.GetChartAssignment() == nil {
		return fmt.Errorf("Workcell spec item is %v, expected ChartAssignment", it)
	}
	return nil
}

func chartStartCommand() *cobra.Command {
	var (
		flagDryRun          bool
		flagFeatureOptions  string
		flagValues          string
		flagSkipAppstat     bool
		flagSkipLinting     bool
		flagRegistry        string
		flagRegistryForward string
		flagRegistryCA      string
	)
	cmd := &cobra.Command{
		Use:   "start [chart name]",
		Short: "Deploy a chart",
		Long:  "Deploy a chart to a Kubernetes cluster.",
		Args:  cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) != 3 {
				return fmt.Errorf("accepts 3 arg(s), received %v", len(args))
			}

			chartAssignmentRlocationpath := args[1]

			var err error
			var imagePub imagepublisher.Publisher
			openshiftProject := os.Getenv("OPENSHIFT_PROJECT")
			if flagRegistryForward != "" {
				if os.Getenv("RELEASE_CANDIDATE_NAME") == "" {
					return fmt.Errorf("RELEASE_CANDIDATE_NAME must be set for OpenShift publishing; refusing to derive an image tag from the local username")
				}
				if flagRegistry == "" {
					return fmt.Errorf("--registry is required with --registry_forward so workloads use the in-cluster registry")
				}
				imagePub, err = imagepublisher.NewOpenShiftRegistryPublisher(flagRegistry, flagRegistryForward, flagRegistryCA)
			} else {
				if openshiftProject != "" {
					return fmt.Errorf("OPENSHIFT_PROJECT is set but --registry_forward is missing; refusing to use the K3s containerd publisher")
				}
				imagePub, err = imagepublisher.NewContainerDPublisher(imagepublisher.WithContainerDAddress("/run/k3s/containerd/containerd.sock"))
			}
			if err != nil {
				return fmt.Errorf("cannot create image publisher: %w", err)
			}
			restConfig, err := k8sclientutil.KubernetesConfig(ctx, "")
			if err != nil {
				return fmt.Errorf("get rest config: %w", err)
			}
			kubeCS, err := kubernetes.NewForConfig(restConfig)
			if err != nil {
				return fmt.Errorf("get kube clientset: %w", err)
			}
			dynamicClient, err := dynamic.NewForConfig(restConfig)
			if err != nil {
				return fmt.Errorf("get dynamic client: %w", err)
			}

			clusterName := os.Getenv("OPENSHIFT_CLUSTER_NAME")
			project := openshiftProject
			if clusterName == "" {
				clusterName = "local"
			}
			if project == "" {
				project = "local"
			}
			location := "local"
			crcConfig := map[string]string{}

			runfilesFS, err := pathresolver.ResolveRunfilesFsRoot()
			if err != nil {
				return errors.Wrap(err, "failed to resolve runfiles fs root")
			}
			spec, err := readWorkcellSpec(runfilesFS, chartAssignmentRlocationpath)
			if err != nil {
				return errors.Wrapf(err, "cannot read workcell spec %q", chartAssignmentRlocationpath)
			}
			if err := validateSpec(spec); err != nil {
				return errors.Wrap(err, "invalid workcell spec")
			}

			cas, err := chartassignment.FromWorkcellSpec(spec)
			if err != nil {
				return errors.Wrap(err, "chartassignment.FromWorkcellSpec")
			}
			ca := cas[0] // NOTE: validateSpec ensures there is exactly one chart assignment.
			runtimeValues, err := values.BuildValues(project, clusterName, location, flagValues, crcConfig)
			if err != nil {
				return fmt.Errorf("unable to build runtime values: %w", err)
			}
			if err := values.ValidateFlagValuesCA(ca, runtimeValues); err != nil {
				return fmt.Errorf("unable to validate the flag values: %w", err)
			}

			startPushImages := time.Now()
			params := &chartassignmentrelease.FinalizeParams{
				ImgPub:          imagePub,
				Project:         project,
				Registry:        workcellspec.RegistryURL(flagRegistry),
				FeatureOptions:  flagFeatureOptions,
				RunfilesFS:      runfilesFS,
				ChartAssignment: ca,
				Values:          runtimeValues,
			}
			if err := chartassignmentrelease.Finalize(ctx, params); err != nil {
				return fmt.Errorf("unable to finalize the chart assignment: %w", err)
			}
			dPushImages := time.Since(startPushImages)
			fmt.Printf("Timing: %.2f seconds to push container images.\n", dPushImages.Seconds())

			if !flagSkipLinting {
				if err := lint.Lint(ctx, clusterName, ca, runtimeValues); err != nil {
					return fmt.Errorf("linting the chart failed: %w", err)
				}
			}

			log.InfoContextf(ctx, "Deploying to cluster with name %q", clusterName)
			startInstallChart := time.Now()
			installIdentity := os.Getenv("USER")
			if openshiftProject != "" {
				installIdentity = "openshift-pilot"
			}
			if err := chartassignment.Install(ctx, clusterName, installIdentity, ca, runtimeValues, dynamicClient, flagDryRun); err != nil {
				return fmt.Errorf("installing the chartassignment failed: %w", err)
			}
			if flagDryRun {
				log.InfoContextf(ctx, "DRY RUN: Not waiting for %s to settle", ca.Name)
				return nil
			}
			var appstatClient *appstat.Client
			if !flagSkipAppstat {
				appstatClient = &appstat.Client{kubeCS, dynamicClient}
			}
			if err := chartassignment.WaitForHealthy(ctx, dynamicClient, ca.Name, appstatClient, os.Stdout); err != nil {
				return fmt.Errorf("wait for helm chart: %w", err)
			}
			dInstallChart := time.Since(startInstallChart)

			fmt.Printf("Timing: %.2f seconds to install Workcell Spec.\n", dInstallChart.Seconds())
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagDryRun, "dry_run", false, "Do not push apps to Firestore or charts to GCS. Do push dependent data such as worlds or images.")
	cmd.Flags().StringVar(&flagFeatureOptions, "feature_options", "", "Comma-separated values to override project-specific feature options. Example: 'has_apiserver: true'")
	cmd.Flags().StringVarP(&flagValues, "values", "v", "", "Comma-separated helm values, e.g. 'flag: true,  parameter: 50'.")
	cmd.Flags().BoolVar(&flagSkipAppstat, "skip_appstat", false, "Skip running appstat after workcell spec has been deployed.")
	cmd.Flags().BoolVar(&flagSkipLinting, "skip_linting", false, "Skip running linting tools.")
	cmd.Flags().StringVar(&flagRegistry, "registry", "", "Logical registry reference used by deployed workloads.")
	cmd.Flags().StringVar(&flagRegistryForward, "registry_forward", "", "Loopback address of an oc port-forward to the internal registry, e.g. 127.0.0.1:5000.")
	cmd.Flags().StringVar(&flagRegistryCA, "registry_ca_file", "", "PEM CA bundle used to verify the registry certificate.")
	return cmd
}

func init() {
	rootCmd.AddCommand(chartStartCommand())
	rootCmd.AddCommand(chartstop.Command())
}

func main() {
	ctx := context.Background()
	intrinsicinit.Init()

	flush := traceutil.SetUpTracing(ctx, flag.Args())
	defer flush()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
