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

// Package repositorybasename implements a function for creating unique
// container image repository basenames.
package repositorybasename

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

// Make takes image abstraction name and appends a hash to disambiguate between
// different images using the same name.  It takes one or more stable unique
// identifiers, such as the path to the tarball for internal images and/or the
// path to the build spec for external images. For an in-depth explanation, see
// go/intrinsic-unique-gcr-basenames.
// TODO(b/298660954): Reenable as soon as we have a solution for insrc.
// If We Change
func Make(imageAbstractionName string, paths ...string) string {
	h := sha256.New()
	for _, path := range paths {
		h.Write([]byte(path))
	}

	digestB := h.Sum(nil)
	// 80 bits allow us to have ~10000s of container images with the same
	// abstraction name while keeping the collision probability at ~10^-15.
	//
	// Use a multiple of 5 bytes so that we don't have padding in Base32.
	digest := base32.StdEncoding.EncodeToString(digestB[:10])
	// Don't use Base64 as Docker repositories can be two to 255 characters,
	// and can only contain lowercase letters, numbers, hyphens (-),
	// and underscores (_).
	// See https://docs.docker.com/docker-hub/repos/#creating-repositories
	return fmt.Sprintf("%s_%s", imageAbstractionName, strings.ToLower(digest))
}

// Then We Change(//incde/ml/backends/common/constants.py)
