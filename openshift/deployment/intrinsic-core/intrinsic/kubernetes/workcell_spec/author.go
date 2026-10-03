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

// Package author provides methods for adding/updating files in the workcell spec.
// It is intended for use by inctl subcommands, but might be useful for the
// frontend server in the future.
package author

import (
	"bytes"
	"encoding/hex"
	"hash"
	"io"
	"net/url"

	"github.com/minio/highwayhash"

	apipb "intrinsic/kubernetes/workcell_spec/proto/transfer_go_proto"
)

// highwayHashKey is required when calculating the HighwayHash-128 for uploaded files.
var highwayHashKey = bytes.Repeat([]byte{0}, 32)

const (
	// Data URLs are of the form: data:[<mediatype>][;base64],<data>
	// See https://datatracker.ietf.org/doc/html/rfc2397 for more details.
	dataPrefix = "data:,"
)

// NewHash128 creates the hash.Hash calculating HighwayHash-128 checksum
func NewHash128() (hash.Hash, error) {
	return highwayhash.New128(highwayHashKey)
}

// InlineFileReferenceFromBytes builds a FileReference that carries the data inside its URI field.
func InlineFileReferenceFromBytes(name, path string, data []byte) *apipb.FileReference {
	return &apipb.FileReference{
		Metadata: &apipb.Metadata{
			Name: name,
		},
		Spec: &apipb.FileReference_Spec{
			Path:   path,
			Uri:    dataPrefix + url.QueryEscape(string(data)),
			Digest: digestBytes(data),
		},
	}
}

func digestBytes(data []byte) string {
	d := highwayhash.Sum128(data, highwayHashKey)
	return hex.EncodeToString(d[:])
}

// Digest creates a digest from a file
func Digest(reader io.Reader) (string, error) {
	h, err := NewHash128()
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(h, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
