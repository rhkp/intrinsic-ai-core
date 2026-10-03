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

// Package imagepublisher provides functions for publishing container images to GCR.
package imagepublisher

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"intrinsic/kubernetes/workcell_spec/imagetags"
	"intrinsic/kubernetes/workcell_spec/workcellspec"
	"intrinsic/util/go/set"

	"cloud.google.com/go/storage"
	backoff "github.com/cenkalti/backoff/v4"
	log "github.com/golang/glog"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
	"golang.org/x/sync/errgroup"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
)

var (
	remoteWrite = remote.Write // Stubbed out for testing.
	remoteImage = remote.Image // Stubbed out for testing.
	remoteGet   = remote.Get   // Stubbed out for testing.
	remoteTag   = remote.Tag   // Stubbed out for testing.
)

const (
	// Number of times to try uploading a container image if we get retriable errors.
	remoteWriteTries = 5
	// Variables that are referenced in build specs.
	buildSpecImageVar       = "_IMAGE_NAME"   // e.g. "ml_train_garbage_collector_image"
	buildSpecTargetsVar     = "_TARGETS"      // e.g. "//intrinsic:some-image.tar"
	buildSpecInsrcCommitVar = "_INSRC_COMMIT" // e.g. "0fcc7f7c31daac9a60dd9a68642998f075364d81"
	buildSpecBazelFlagsVar  = "_BAZEL_FLAGS"  // e.g. "-c opt --stamp"
	buildSpecRepoNameVar    = "_REPONAME"     // e.g. "asset_deployment_us6372uc7endslkd"
	buildSpecLFSPathsVar    = "_LFS_PATHS"    // e.g. "/path/to/foo,/path/to/bar"
	buildSpecLFSGlobs       = "_LFS_GLOBS"
	// CloudBuildEndpoint is the endpoint of the Cloud Build API.
	CloudBuildEndpoint = "us-central1-cloudbuild.googleapis.com"
)

// ImageInfo represents one line of an helm_chart's image lists file
type ImageInfo struct {
	RepositoryBasename   string // last component of repository name, e.g. "foo" for "gcr.io/project/foo:latest"
	ImageAbstractionName string // a name that is used in helm templates to refer
	// to a set of container images with the same k8s interface, but different
	// implementations.
	// For traditional google3-based Bazel built images
	Tarball string // google3-relative path of the image's tarball.
	// For external_images e.g. images built from Dockerfile or insrc.
	Digest string // image digest including hash type prefix ("sha256:")
}

// ImageReference returns the GCR reference to the image, minus the registry part.
func (i *ImageInfo) ImageReference() string {
	return fmt.Sprintf("/%s@%s", i.RepositoryBasename, i.Digest)
}

// PathInRunfiles returns the path inside the current runfiles directory.
func PathInRunfiles(file string) string {
	// TODO(b/338353462): Save rlocationpath including repository.
	return filepath.Join("_main", file)
}

// ParseImageInfo parses a line from an *image_list.txt file.
func ParseImageInfo(line string) (*ImageInfo, error) {
	parts := strings.Split(line, " ")
	var image *ImageInfo
	// TODO(alexanderfaxa): parts[2] (digest) is not used, remove it from the build.
	if len(parts) == 3 {
		image = &ImageInfo{
			RepositoryBasename: parts[0],
			Tarball:            parts[1],
		}
	} else {
		return nil, fmt.Errorf(`expected "$name $path $digest", got %q`, line)
	}
	return image, nil
}

// FindImagesToPush parses the files pointed to by `imageListPaths`, ignoring duplicate lines.
func FindImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	images := set.New[ImageInfo]()
	if len(imageListPaths) == 0 {
		return images, nil
	}
	for _, path := range imageListPaths {
		file, err := runfilesFS.Open(path)
		if err != nil {
			return images, fmt.Errorf("failed opening %q: %v", path, err)
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			return images, fmt.Errorf("failed reading %q: %v", path, err)
		}
		list := strings.Split(string(content), "\n")
		for _, line := range list {
			if line == "" {
				continue
			}
			image, err := ParseImageInfo(line)
			if err != nil {
				return images, err
			}
			images.Add(*image)
		}
	}
	return images, nil
}

// Publish pushes all tarball container images in imageListPaths in parallel using publisher.
// It returns the list of images published through the hybrid build system so that downstream
// callers can get access to the image digests (not known at build time).
func Publish(ctx context.Context, publisher Publisher, registry workcellspec.RegistryURL, imageListPaths []string, lfsPaths []string, runfilesFS fs.FS) ([]*ImageInfo, error) {
	ctx, span := trace.StartSpan(ctx, "Push Images")
	defer span.End()
	images, err := publisher.findImagesToPush(imageListPaths, runfilesFS)
	if err != nil {
		return nil, err
	}

	var g errgroup.Group
	for image := range images.All() {
		image := image
		g.Go(func() error {
			return publisher.publish(ctx, registry, runfilesFS, image)
		})
	}
	// TODO(b/329281292) Remove the return value which is always nil.
	return nil, g.Wait()
}

// tag returns a name.Tag to use with the go-containerregistry APIs.
func tag(nameWithoutTag string) (*name.Tag, error) {
	// Use the rapid candidate name if provided or a placeholder tag otherwise.
	// For Rapid workflows, the deployed chart references the image by candidate name.
	// For dev workflows, we reference by digest.
	t, err := imagetags.DefaultTag()
	if err != nil {
		return nil, errors.Wrapf(err, "generating tag")
	}
	fullName := fmt.Sprintf("%s:%s", nameWithoutTag, t)
	res, err := name.NewTag(fullName, name.StrictValidation)
	if err != nil {
		errors.Wrapf(err, "newTag(%s)", fullName)
	}
	return &res, nil
}

// pushImage pushes the given image to a container registry.
// TODO(alexanderfaxa): Consider checking for existence before pushing.
// `remote.Get` is quite a bit quicker than `remote.Write` with an existing image.
func pushImage(ctx context.Context, image v1.Image, dstTag name.Tag, options ...remote.Option) error {
	log.InfoContextf(ctx, "Writing to %q", dstTag.String())
	b := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), remoteWriteTries)
	if err := backoff.Retry(func() error {
		err := remoteWrite(dstTag, image, options...)
		if err, ok := err.(*transport.Error); ok && err.StatusCode >= 500 {
			// Retry server errors like 504 Gateway Timeout.
			return err
		}
		if err != nil {
			// TODO - https://github.com/golang/go/issues/74578: use errors.As()
			if strings.Contains(err.Error(), "server sent GOAWAY and closed the connection") {
				return err
			}
			return backoff.Permanent(err)
		}
		return nil
	}, b); err != nil {
		return errors.Wrapf(err, "remote.Write to %q", dstTag)
	}
	log.InfoContextf(ctx, "Finished pushing %q", dstTag.String())
	return nil
}

// Publisher is the interface for an image publisher.
type Publisher interface {
	publish(ctx context.Context, registry workcellspec.RegistryURL, runfilesFS fs.FS, image ImageInfo) error
	findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error)
}

// GCRPublisher provides functionality for publishing container images to GCR.
type GCRPublisher struct {
	auth remote.Option
}

// NewGCRPublisher is a factory function for creating GCRPublisher with a remote auth.
func NewGCRPublisher(auth remote.Option, gcs *storage.Client, cb *cloudbuild.Service) *GCRPublisher {
	log.Infof("Creating a GCR publisher")
	return &GCRPublisher{
		auth,
	}
}

func (p *GCRPublisher) publish(ctx context.Context, registry workcellspec.RegistryURL, runfilesFS fs.FS, imageInfo ImageInfo) error {
	imagePath := PathInRunfiles(imageInfo.Tarball)
	var imageOpener tarball.Opener = func() (io.ReadCloser, error) { return runfilesFS.Open(imagePath) }
	dstTag, err := tag(fmt.Sprintf("%s/%s", registry, imageInfo.RepositoryBasename))
	if err != nil {
		return err
	}

	log.V(1).InfoContextf(ctx, "Reading image tarball %q", imagePath)
	image, err := tarball.Image(imageOpener, nil)
	if err != nil {
		return errors.Wrapf(err, "reading image tarball from %q", imagePath)
	}
	return pushImage(ctx, image, *dstTag, p.auth)
}

func (p *GCRPublisher) findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	return FindImagesToPush(imageListPaths, runfilesFS)
}

// Publishers for tests.

// FakeImagePublisher is a publisher than ensures the right image is pushed to the right place.
type FakeImagePublisher struct {
	Image         ImageInfo
	WantRegistry  workcellspec.RegistryURL
	WantImagePath string
	PublishCalled bool
}

func (p *FakeImagePublisher) publish(_ context.Context, registry workcellspec.RegistryURL, _ fs.FS, image ImageInfo) error {
	if registry != p.WantRegistry {
		return errors.Errorf("uploads to %q, want %q", registry, p.WantRegistry)
	}
	if p.WantImagePath != image.Tarball {
		return errors.Errorf("wrong imagePath, want %q, got %q", p.WantImagePath, image.Tarball)
	}
	p.PublishCalled = true
	return nil
}

func (p *FakeImagePublisher) findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	return set.New[ImageInfo](p.Image), nil
}

// AlwaysSucceedImagePublisher always succeeds.
type AlwaysSucceedImagePublisher struct{}

func (*AlwaysSucceedImagePublisher) publish(context.Context, workcellspec.RegistryURL, fs.FS, ImageInfo) error {
	return nil
}

func (p *AlwaysSucceedImagePublisher) findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	return set.New[ImageInfo](), nil
}

// AlwaysFailImagePublisher always fails.
type AlwaysFailImagePublisher struct{ Err error }

func (p *AlwaysFailImagePublisher) publish(context.Context, workcellspec.RegistryURL, fs.FS, ImageInfo) error {
	return p.Err
}

func (p *AlwaysFailImagePublisher) findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	return set.New[ImageInfo](), p.Err
}
