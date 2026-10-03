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

// Package chartassignmentrelease contains utilities for working with chart assignments.
package chartassignmentrelease

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"regexp"

	"intrinsic/kubernetes/values"
	"intrinsic/kubernetes/workcell_spec/chartassignment"
	"intrinsic/kubernetes/workcell_spec/workcellspec"
	"intrinsic/production/imagepublisher"

	log "github.com/golang/glog"
	"github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"github.com/pkg/errors"
	kubeyaml "sigs.k8s.io/yaml"
)

// fileSystem must be rooted at the runfiles directory
// TODO(b/326921974): Remove once default charts have been migrated to intrinsic-base.
func processEmbeddedCharts(ctx context.Context, fileSystem fs.FS, params *FinalizeParams) error {
	embeddedSpecFiles := values.EmbeddedChartFiles(params.ChartAssignment)
	for _, embeddedSpecFile := range embeddedSpecFiles {
		embeddedSpecFileReader, err := fileSystem.Open(imagepublisher.PathInRunfiles(embeddedSpecFile))
		if err != nil {
			return errors.Wrapf(err, "cannot open embedded chart assignment %q", embeddedSpecFile)
		}
		embeddedSpec, err := workcellspec.Unmarshal(embeddedSpecFileReader)
		if err != nil {
			return errors.Wrapf(err, "cannot unmarshal embedded chart assignment %q", embeddedSpecFile)
		}
		if len(embeddedSpec.GetItems()) != 1 {
			return fmt.Errorf("embedded WorkcellSpec must contain exactly one ChartAssignment %s, got %d", embeddedSpec.GetMetadata().GetName(), len(embeddedSpec.GetItems()))
		}
		caPB := embeddedSpec.GetItems()[0].GetChartAssignment()
		embeddedCa := &v1alpha1.ChartAssignment{}
		if err := kubeyaml.Unmarshal(caPB.GetYaml(), embeddedCa); err != nil {
			return errors.Wrap(err, "unmarshal ChartAssignment")
		}

		newParams := &FinalizeParams{
			ImgPub:          params.ImgPub,
			Project:         params.Project,
			Registry:        params.Registry,
			FeatureOptions:  params.FeatureOptions,
			RunfilesFS:      params.RunfilesFS,
			ChartAssignment: embeddedCa,
			Values:          embeddedCa.Spec.Chart.Values,
		}
		if err := Finalize(ctx, newParams); err != nil {
			return fmt.Errorf("process embedded chart assignment %s: %w", embeddedSpec.GetMetadata().GetName(), err)
		}

		b, err := kubeyaml.Marshal(embeddedCa)
		if err != nil {
			return errors.Wrap(err, "marshall ChartAssignment")
		}
		caPB.Yaml = b
		embeddedSpecYaml := bytes.NewBuffer(nil)
		if err := workcellspec.Marshal(embeddedSpec, embeddedSpecYaml); err != nil {
			return errors.Wrap(err, "workcellspec.Marshal")
		}
		values.AddEmbeddedChart(embeddedCa.Name, embeddedSpecYaml.Bytes(), &params.ChartAssignment.Spec.Chart.Values)
	}
	values.ClearEmbeddedChartFiles(params.ChartAssignment)

	return nil
}

// addImageValues adds a list of images to values inside an inlined ChartAssignment, along with the
// registry that contains them, modifying "ca" in-place.
func addImageValues(registry workcellspec.RegistryURL, images []*imagepublisher.ImageInfo, ca *v1alpha1.ChartAssignment) error {
	// A Helm chart has the values.yaml in the top-level directory.
	valuesPath := regexp.MustCompile("^[^/]*/values.yaml$")
	modifyValues := func(content []byte) ([]byte, error) {
		values := make(v1alpha1.ConfigValues)
		if err := kubeyaml.Unmarshal(content, &values); err != nil {
			return nil, fmt.Errorf("unmarshal values: %w", err)
		}
		values["registry"] = string(registry)
		if values["images"] == nil {
			values["images"] = make(map[string]any)
		}
		for _, image := range images {
			values["images"].(map[string]any)[image.ImageAbstractionName] = image.ImageReference()
		}
		return kubeyaml.Marshal(values)
	}

	if err := chartassignment.ModifyInlineChart(ca, valuesPath, modifyValues); err != nil {
		return err
	}
	return nil
}

// FinalizeParams wraps the parameters for finalizing a chart assignment before deployment or release
type FinalizeParams struct {
	ImgPub         imagepublisher.Publisher
	Project        string
	Registry       workcellspec.RegistryURL
	FeatureOptions string
	RunfilesFS     fs.FS

	// ChartAssignment and Values will be modified in-place by Finalize().
	ChartAssignment *v1alpha1.ChartAssignment
	Values          v1alpha1.ConfigValues
}

// Finalize finalizes the chart assignment by setting feature options and pushing images.
func Finalize(ctx context.Context, params *FinalizeParams) error {
	if err := values.SetFeatureOptions(params.Project, params.FeatureOptions, params.Values); err != nil {
		return fmt.Errorf("set feature options %q: %w", params.FeatureOptions, err)
	}

	images := values.ImageLists(params.ChartAssignment)
	if len(images) == 0 {
		return nil
	}
	if params.Registry == "" {
		params.Registry = workcellspec.RegistryURL("gcr.io/" + params.Project)
	}
	lfsPaths := values.LFSPaths(params.ChartAssignment)
	log.InfoContextf(ctx, "Pushing images to %s", params.Registry)
	newImages, err := imagepublisher.Publish(ctx, params.ImgPub, params.Registry, images, lfsPaths, params.RunfilesFS)
	if err != nil {
		return fmt.Errorf("upload images: %w", err)
	}
	if err := addImageValues(params.Registry, newImages, params.ChartAssignment); err != nil {
		return fmt.Errorf("add image values: %w", err)
	}
	values.ClearImageLists(params.ChartAssignment)
	values.ClearLFSPaths(params.ChartAssignment)

	if err := processEmbeddedCharts(ctx, params.RunfilesFS, params); err != nil {
		return fmt.Errorf("process embedded charts: %w", err)
	}

	return nil
}
