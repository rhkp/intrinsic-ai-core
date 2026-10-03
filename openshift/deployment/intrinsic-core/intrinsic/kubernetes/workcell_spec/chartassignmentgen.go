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

// chartassignmentgen is used by the helm_chart() build rule to generate the
// ChartAssignment.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"intrinsic/production/intrinsic"
	intrinsicflag "intrinsic/util/flag"

	log "github.com/golang/glog"
)

var (
	flagBuildTarget = flag.String("build_target", "", "helm_chart() build target")
	flagChartName   = flag.String("chart_name", "", "short name of the chart, eg pick-and-place")
	flagChart       = flag.String("chart", "", "path to the Helm chart tarball")
	flagOutput      = flag.String("output", "", "output path.")
	flagImageList   = flag.String("image_list", "", "path to list of images to push")
	flagLFSPath     = intrinsicflag.MultiString("lfs_path", nil, "LFS path to include in images")
	// TODO(b/326921974): Remove once default charts have been migrated to intrinsic-base.
	flagEmbedChartList = intrinsicflag.MultiString("embed_chart_file", nil, "path to chart to embed.")
	flagVersion        = flag.String("version", "", "the version of the chart. Can be empty.")
)

func main() {
	intrinsic.Init()
	if *flagBuildTarget == "" || *flagChartName == "" || *flagChart == "" || *flagOutput == "" {
		log.Exit("usage: chartassignmentgen -output=path/to/app-name.yaml -build_target=//path/to:app-name -chart_name=app-name -chart=path/to/app-name-0.0.1.tgz")
	}

	for i, embedChart := range *flagEmbedChartList {
		// Add quotes around the chart file paths.
		(*flagEmbedChartList)[i] = fmt.Sprintf("%q", embedChart)
	}

	for i, lfsPath := range *flagLFSPath {
		// Add quotes around the LFS paths.
		(*flagLFSPath)[i] = fmt.Sprintf("%q", lfsPath)
	}

	chartData, err := os.ReadFile(*flagChart)
	if err != nil {
		log.Exitf("Failed to read chart: %v", err)
	}

	f, err := os.Create(*flagOutput)
	if err != nil {
		log.Exitf("Failed to open %q: %v", *flagOutput, err)
	}
	defer f.Close()

	if *flagVersion == "" {
		*flagVersion = "local-build"
	}

	chartEncoded := base64.StdEncoding.EncodeToString(chartData)
	// `workcell-spec-target` is overridden by workcellspecgen if building a workcell spec.
	fmt.Fprintf(f, `apiVersion: apps.cloudrobotics.com/v1alpha1
kind: ChartAssignment
metadata:
  annotations:
    workcell-spec-target: %[1]q
    version: %[2]q
  labels:
    app: %[3]q
  name: %[3]q
spec:
  chart:
    inline: %[4]s
    values:
      app_name: %[3]q
      image_lists:
        - %[5]q
      lfs_paths: [%[7]s]
      embed_chart_files: [%[6]s]
  namespaceName: "app-%[3]s"
`, *flagBuildTarget, *flagVersion, *flagChartName, chartEncoded, *flagImageList, strings.Join(*flagEmbedChartList, ","), strings.Join(*flagLFSPath, ","))
}
