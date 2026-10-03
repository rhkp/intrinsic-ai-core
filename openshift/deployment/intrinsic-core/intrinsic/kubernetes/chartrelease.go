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

// Package chartrelease provides functions for releasing helm chart artifacts to GCS and GAR.
package chartrelease

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"slices"

	"intrinsic/kubernetes/workcell_spec/chartassignment"
	"intrinsic/kubernetes/workcell_spec/workcellspec"

	log "github.com/golang/glog"

	"cloud.google.com/go/storage"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/registry"

	"intrinsic/kubernetes/workcell_spec/imagetags"

	"github.com/Masterminds/semver"

	apipb "intrinsic/kubernetes/workcell_spec/proto/transfer_go_proto"
)

// GARChartPush represents a chart to be pushed to a destination in GAR.
type GARChartPush struct {
	chart    []byte
	appName  string
	registry string
	tag      string
}

func (c *GARChartPush) ref() string {
	return fmt.Sprintf("%s/%s", c.registry, c.appName)
}

func (c *GARChartPush) taggedRef() string {
	return fmt.Sprintf("%s/%s:%s", c.registry, c.appName, c.tag)
}

// ReleaseBucket returns the GCS bucket name for the given project.
func ReleaseBucket(project string) string {
	return project + "-app-releases"
}

func uploadToGCS(ctx context.Context, client *storage.Client, path string, r io.Reader, project string) error {
	w := client.Bucket(ReleaseBucket(project)).Object(path).NewWriter(ctx)
	if _, err := io.Copy(w, r); err != nil {
		return errors.Wrap(err, "upload")
	}
	if err := w.Close(); err != nil {
		return errors.Wrap(err, "close upload")
	}
	return nil
}

// Check if the passed releaseName is a valid semver, if so, use it as the tag. Else use imagetags.ReleaseCandidateTag().
func createSemverTag(releaseName string) (string, error) {
	var semverReleaseTag string
	var ok bool
	version, err := semver.NewVersion(releaseName)
	if err == nil {
		semverReleaseTag = version.String()
	} else {
		fmt.Printf("failed to parse release name %q as semver: %v\n", releaseName, err)
		semverReleaseTag, ok = imagetags.ReleaseCandidateTag()
		if !ok {
			return "", fmt.Errorf("release candidate tag is empty")
		}
	}
	return semverReleaseTag, nil
}

// ReleaseHelmChartToGCS releases a helm chart to the release bucket on GCS. It returns the path
// under the release bucket used where the corresponding chart assignment can be found.
func ReleaseHelmChartToGCS(ctx context.Context, client *storage.Client, spec *apipb.WorkcellSpec, project string, releaseName string, dryRun bool) (string, error) {
	app, err := chartassignment.AppName(spec)
	if err != nil {
		return "", err
	}
	semverReleaseTag, err := createSemverTag(releaseName)
	if err != nil {
		return "", err
	}
	// The YAML is pushed to gs://<project>-app-releases/<app name>/common/<release name>/<app name>.yaml.
	// The "common" subfolder was used before for releasing workcell-agnostic helm charts. It's a pure
	// legacy thing now for compatibility.
	path := fmt.Sprintf("%s/common/%s/%s.yaml", app, semverReleaseTag, app)
	b := bytes.NewBuffer(nil)
	if err := workcellspec.Marshal(spec, b); err != nil {
		return "", errors.Wrap(err, "workcellspec.Marshal")
	}
	if dryRun {
		log.InfoContextf(ctx, "DRY RUN: Omitting push to GCS path %s", path)
		return path, nil
	}
	if err := uploadToGCS(ctx, client, path, b, project); err != nil {
		return "", err
	}
	return path, nil
}

// NewGARClient creates a new client for pushing charts to GAR.
func NewGARClient(ctx context.Context) (*registry.Client, error) {
	return registry.NewClient()
}

func extractChart(spec *apipb.WorkcellSpec) ([]byte, error) {
	cas, err := chartassignment.FromWorkcellSpec(spec)
	if err != nil {
		return nil, errors.Wrap(err, "chartassignment.FromWorkcellSpec")
	}
	ca := cas[0] // NOTE: validateSpec ensures there is exactly one chart assignment.

	gzChart, err := base64.StdEncoding.DecodeString(ca.Spec.Chart.Inline)
	if err != nil {
		return nil, fmt.Errorf("error decoding inline chart: %s", err)
	}
	return gzChart, nil
}

// Interface that matches the required methods of helm.sh/helm/v3/pkg/registry.Client.
type RegistryClient interface {
	Tags(ref string) ([]string, error)
	Push(data []byte, ref string, opts ...registry.PushOption) (*registry.PushResult, error)
}

func tagExistsInGAR(ctx context.Context, client RegistryClient, pushedChart GARChartPush) (bool, error) {
	tags, err := client.Tags(pushedChart.ref())
	if err != nil {
		return false, err
	}
	return slices.Contains(tags, pushedChart.tag), nil
}

func pushToGAR(ctx context.Context, client RegistryClient, pushedChart GARChartPush) (*registry.PushResult, error) {
	// Currently, the chart basename and version are the app name and 0.0.1 respectively for all charts when built in g3.
	// This creates a mismatch with the target in GAR which targets the full repo path and has a date-based version, so it fails strict mode check.
	pushOption := []registry.PushOption{registry.PushOptStrictMode(false)}

	info, err := client.Push(pushedChart.chart, pushedChart.taggedRef(), pushOption...)
	if err != nil {
		return nil, err
	}
	log.InfoContextf(ctx, "Pushed: %s\n", info.Ref)
	log.InfoContextf(ctx, "Digest: %s\n", info.Manifest.Digest)

	return info, nil
}

// ReleaseHelmChartToGAR releases a helm chart to the proper registry on GAR. It returns the path
// under the registry used where the corresponding chart assignment can be found.
func ReleaseHelmChartToGAR(ctx context.Context, client RegistryClient, spec *apipb.WorkcellSpec, releaseName, releaseChartRegistry string, dryRun bool) (string, error) {
	chart, err := extractChart(spec)
	if err != nil {
		return "", err
	}
	appName, err := chartassignment.AppName(spec)
	if err != nil {
		return "", err
	}
	semverReleaseTag, err := createSemverTag(releaseName)
	if err != nil {
		return "", err
	}
	pushedChart := GARChartPush{
		chart:    chart,
		appName:  appName,
		registry: releaseChartRegistry,
		tag:      semverReleaseTag,
	}

	exists, err := tagExistsInGAR(ctx, client, pushedChart)
	if err != nil {
		return "", err
	}
	if exists {
		log.WarningContextf(ctx, "Tag %s already exists in GAR, skipping push", pushedChart.taggedRef())
		return pushedChart.taggedRef(), nil
	}

	if dryRun {
		log.InfoContextf(ctx, "DRY RUN: Omitting push to GAR repo %s", pushedChart.taggedRef())
		return pushedChart.taggedRef(), nil
	}

	info, err := pushToGAR(ctx, client, pushedChart)
	if err != nil {
		return "", err
	}
	return info.Ref, nil
}
