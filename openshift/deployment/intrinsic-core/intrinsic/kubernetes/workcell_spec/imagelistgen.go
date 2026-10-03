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

// The imagelistgen command creates a single line of the "*.image_list.txt" file
// which contains the information required by the image publisher.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"intrinsic/kubernetes/workcell_spec/repositorybasename"
	"intrinsic/production/intrinsic"

	log "github.com/golang/glog"
)

var (
	flagImage   = flag.String("image", "", "Image abstraction name") // See go/intrinsic-unique-gcr-basenames.
	flagTarball = flag.String("tarball", "", "Path to the image tarball, relative to the google3 root")
	flagDigest  = flag.String("digest", "", "Path to the file containing the tarball digest")
	flagOutput  = flag.String("output", "", "Output file path")
)

func main() {
	intrinsic.Init()
	if *flagOutput == "" {
		log.Exit("usage: imagelistgen -name skills_cpp -tarball my/image.tar -digest bazel-out/k8-opt/bin/my/digest -output bazel-out/k8-opt/bin/my/path-digest-file")
	}

	digest, err := os.ReadFile(*flagDigest)
	if err != nil {
		log.Exitf("Failed to read %q: %v", *flagDigest, err)
	}

	if strings.Contains(*flagImage, ":") {
		log.Exitf("Image abstraction name %q may not contain a colon (':'), please check your BUILD file.", *flagImage)
	}

	repositoryBasename := repositorybasename.Make(*flagImage, *flagTarball)
	s := fmt.Sprintf("%s %s %s\n", repositoryBasename, *flagTarball, digest)
	if err := os.WriteFile(*flagOutput, []byte(s), 0o644); err != nil {
		log.Exitf("Failed to write %q: %v", *flagOutput, err)
	}
}
