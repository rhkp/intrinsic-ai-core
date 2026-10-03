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

// Package uriutils implements utils for handling URI validation and mapping to Firestore paths.
package uriutils

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	firestoreBytesLimit = 1500
)

var (
	reservedUnderscoresRE = regexp.MustCompile(`^__.*__$`)
	whitespacesOnlyRE     = regexp.MustCompile(`^\s*$`)
)

// validatePathElement checks the validity of a single path element. We check for elements that
// contain exclusively whitespaces and for the conditions imposed by Firestore:
// https://firebase.google.com/docs/firestore/quotas#collections_documents_and_fields
// In the future, we might make path elements more restrictive.
func validatePathElement(el string) error {
	if len(el) == 0 {
		return fmt.Errorf("got %q, want non-empty path element", el)
	}
	if len(el) > firestoreBytesLimit {
		return fmt.Errorf("path element exceeds the Firestore path limit of %d bytes", firestoreBytesLimit)
	}
	if el == "." || el == ".." {
		return errors.New("'.' or '..' are not allowed as path elements")
	}
	if reservedUnderscoresRE.MatchString(el) {
		return fmt.Errorf("%q is a reserved key", el)
	}
	if whitespacesOnlyRE.MatchString(el) {
		return fmt.Errorf("%q contains only whitespaces", el)
	}
	// Do not check for "/" since we split by this separator.
	return nil
}

// ParseURIForFirestore takes a URI and converts it into a list of path
// elements. It validates that this path is mappable to the current Firestore
// document-collection hierarchy. The returned path elements list is guaranteed
// to be at least two elements long.
func ParseURIForFirestore(uri string) ([]string, error) {
	parts := strings.Split(uri, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid URI: got %q, want at least two elements", uri)
	}
	if pathLen := len(parts); pathLen%2 != 0 {
		return nil, fmt.Errorf("invalid path: got %q with %d path elements, want an even number of path elements", uri, pathLen)
	}
	for _, el := range parts {
		if err := validatePathElement(el); err != nil {
			return nil, fmt.Errorf("%q contains an invalid path element: %w", uri, err)
		}
	}
	return parts, nil
}
