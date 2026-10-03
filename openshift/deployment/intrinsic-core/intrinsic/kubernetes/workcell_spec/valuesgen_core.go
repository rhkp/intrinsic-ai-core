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

// valuesgen builds a single values.yaml file from multiple helm value sources with proper merging.
package main

import (
	"flag"
	"os"

	log "github.com/golang/glog"
	"github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"gopkg.in/yaml.v3"

	"intrinsic/kubernetes/values"
	"intrinsic/production/intrinsic"
	intrinsicflag "intrinsic/util/flag"
)

var (
	flagCommonValues = flag.String("common_values", "", "Path to a file with common values, e.g. common-values.yaml.")
	flagValues       = intrinsicflag.MultiString("values", nil, "Path to files with chart specific values")
	flagImages       = intrinsicflag.MultiString("image", nil, "Image of the form image_name%path_to_digest_file%path_to_image_tarball.")
	flagOutput       = flag.String("output", "", "output path.")
)

func main() {
	intrinsic.Init()
	if *flagOutput == "" {
		log.Exit("usage: valuesgen [-common_values=/path/to/common_values.yaml,/path/to/cloudbuild_images_values.yaml -values=/path/to/values.yaml] -output=path/to/values.yaml")
	}

	v := make(v1alpha1.ConfigValues)
	err := values.MergeFromFile(*flagCommonValues, &v)
	if err != nil {
		log.Exitf("%v", err)
	}
	for _, valuesPath := range *flagValues {
		if err := values.MergeFromFile(valuesPath, &v); err != nil {
			log.Exitf("%v", err)
		}
	}

	if v["images"] == nil {
		v["images"] = make(map[string]any)
	}
	if len(*flagImages) > 0 {
		imageValues, err := values.AssembleImageValues(*flagImages)
		if err != nil {
			log.Exitf("%v", err)
		}
		for key, value := range imageValues {
			v["images"].(map[string]any)[key] = value
		}
	}

	// Add project feature options to the values map. This lets us use the same chart for all
	// projects.
	v["project_feature_options"] = make(map[string]any)

	// Add generic feature options to the values map. These are feature-presets
	// that can serve as a fallback for new projects.
	v["generic_feature_options"] = make(map[string]any)

	b, err := yaml.Marshal(v)
	if err != nil {
		log.Exitf("marshal values: %v", err)
	}
	if err := os.WriteFile(*flagOutput, b, 0o644); err != nil {
		log.Exitf("Failed to write %q: %v", *flagOutput, err)
	}
}
