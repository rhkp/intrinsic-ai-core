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

// Package workcellspec provides support for transferring the workcell spec to a
// target cluster.
//
// When pushing from Piper (rather than from a released workcell config or
// spec), it can also transfer files from Bazel's build-out.
package workcellspec

import (
	"fmt"
	"io"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"

	apipb "intrinsic/kubernetes/workcell_spec/proto/transfer_go_proto"
)

// RegistryURL describes the base URL for a container registry (e.g. gcr.io/giza-workcells)
type RegistryURL string

// Unmarshal converts a YAML workcell spec into a proto message.
func Unmarshal(r io.Reader) (*apipb.WorkcellSpec, error) {
	dec := yaml.NewDecoder(r)
	spec := &apipb.WorkcellSpec{}
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		switch obj["kind"] {
		case nil:
			// No `kind` specified. If it's an empty document, ignore it, otherwise fail.
			if len(obj) == 0 {
				continue
			}
			return nil, fmt.Errorf("YAML object doesn't specify kind: %+v", obj)
		case "WorkcellMetadata":
			metadata, err := unmarshalWorkcellMetadata(obj)
			if err != nil {
				return nil, err
			}
			spec.Metadata = metadata
		case "ChartAssignment":
			item, err := unmarshalChartAssignment(obj)
			if err != nil {
				return nil, err
			}
			spec.Items = append(spec.GetItems(), item)
		default:
			return nil, fmt.Errorf("unrecognized kind %q", obj["kind"])
		}
	}
	return spec, nil
}

// Marshal writes a workcell spec to a YAML file.
func Marshal(spec *apipb.WorkcellSpec, w io.Writer) error {
	// As the chartAssignment is already YAML in the proto, handle it
	// separately.
	var chartAssignmentYAML []byte
	enc := yaml.NewEncoder(w)
	if spec.GetMetadata() != nil {
		o := map[string]any{
			"apiVersion": "intrinsic.io/v1",
			"kind":       "WorkcellMetadata",
			"metadata": map[string]string{
				"name": spec.GetMetadata().GetName(),
			},
		}
		if err := enc.Encode(o); err != nil {
			return err
		}
	}
	for _, i := range spec.GetItems() {
		switch i.Item.(type) {
		case *apipb.WorkcellSpecItem_ChartAssignment:
			if chartAssignmentYAML != nil {
				return fmt.Errorf("multiple chart assignments in input")
			}
			chartAssignmentYAML = i.GetChartAssignment().GetYaml()
		default:
			return fmt.Errorf("invalid item type: %d", i.Item)
		}
	}
	enc.Close()
	if chartAssignmentYAML != nil {
		if len(spec.GetItems()) > 1 || spec.GetMetadata() != nil {
			// This is not the first document.
			chartAssignmentYAML = append([]byte("---\n"), chartAssignmentYAML...)
		}
		if _, err := w.Write(chartAssignmentYAML); err != nil {
			return err
		}
	}
	return nil
}

func unmarshalWorkcellMetadata(obj map[string]any) (*apipb.Metadata, error) {
	yamlMetadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`WorkcellMetadata must contain an object property named "metadata": %+v`, obj)
	}
	name, ok := yamlMetadata["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf(`WorkcellMetadata.metadata must contain a non-empty string property named "name": %+v`, obj)
	}
	return &apipb.Metadata{
		Name: name,
	}, nil
}

func unmarshalChartAssignment(obj map[string]any) (*apipb.WorkcellSpecItem, error) {
	// As we can't easily represent the ChartAssignment structure in proto, we
	// reserialize and use a bytes field.
	bytes, err := yaml.Marshal(obj)
	if err != nil {
		return nil, errors.Wrap(err, "marshal ChartAssignment")
	}
	return &apipb.WorkcellSpecItem{
		Item: &apipb.WorkcellSpecItem_ChartAssignment{
			ChartAssignment: &apipb.ChartAssignment{
				Yaml: bytes,
			},
		},
	}, nil
}

// UnmarshalFileReference converts deserialized YAML to a WorkcellSpecItem proto
// message containing a FileReference. It ignores the apiVersion/kind/metadata
// properties, as well as any unknown properties. It fails, however, if any spec
// properties are missing.
//
// TODO(rodrigoq): if we add more item types, consider a generic YAML -> JSON ->
// proto3 conversion with the ghodss/yaml and protojson packages.
func UnmarshalFileReference(obj map[string]any) (*apipb.FileReference, error) {
	yamlMetadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`FileReference must contain an object property named "metadata": %+v`, obj)
	}
	name, ok := yamlMetadata["name"].(string)
	if !ok {
		return nil, fmt.Errorf(`FileReference.metadata must contain a string property named "name": %+v`, obj)
	}
	yamlSpec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`FileReference must contain an object property named "spec": %+v`, obj)
	}
	path, ok := yamlSpec["path"].(string)
	if !ok {
		return nil, fmt.Errorf(`FileReference.spec must contain a string property named "path": %+v`, obj)
	}
	uri, ok := yamlSpec["uri"].(string)
	if !ok {
		return nil, fmt.Errorf(`FileReference.spec must contain a string property named "uri": %+v`, obj)
	}
	digest, ok := yamlSpec["digest"].(string)
	if !ok {
		return nil, fmt.Errorf(`FileReference.spec must contain a string property named "digest": %+v`, obj)
	}
	return &apipb.FileReference{
		Metadata: &apipb.Metadata{
			Name: name,
		},
		Spec: &apipb.FileReference_Spec{
			Path:   path,
			Uri:    uri,
			Digest: digest,
		},
	}, nil
}
