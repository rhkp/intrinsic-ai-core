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

package imagepublisher

import (
	"context"
	"fmt"
	"io"
	"io/fs"

	"intrinsic/kubernetes/workcell_spec/workcellspec"
	"intrinsic/util/go/set"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	log "github.com/golang/glog"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const (
	defaultContainerdNamespace = "k8s.io"
	defaultContainerdAddress   = "/run/containerd/containerd.sock"
)

// ContainerDPublisher provides functionality for publishing container images to a local containerd daemon.
type ContainerDPublisher struct {
	client    *containerd.Client
	namespace string
}

type containerdOptions struct {
	address    string
	namespace  string
	clientOpts []containerd.ClientOpt
}

// ContainerDOption is an interface that applies options to ContainerDPublisher.
type ContainerDOption interface {
	apply(opts *containerdOptions)
}

type containerdOption func(*containerdOptions)

func (f containerdOption) apply(opts *containerdOptions) {
	f(opts)
}

// WithContainerDAddress sets the containerd socket address.
func WithContainerDAddress(address string) ContainerDOption {
	return containerdOption(func(opts *containerdOptions) {
		opts.address = address
	})
}

// WithContainerDNamespace sets the containerd namespace.
func WithContainerDNamespace(namespace string) ContainerDOption {
	return containerdOption(func(opts *containerdOptions) {
		opts.namespace = namespace
	})
}

// WithContainerDClientOpts adds custom containerd.ClientOpt options.
func WithContainerDClientOpts(clientOpts ...containerd.ClientOpt) ContainerDOption {
	return containerdOption(func(opts *containerdOptions) {
		opts.clientOpts = append(opts.clientOpts, clientOpts...)
	})
}

// NewContainerDPublisher creates a new ContainerDPublisher with optional configuration.
func NewContainerDPublisher(opts ...ContainerDOption) (Publisher, error) {
	o := containerdOptions{
		address:   defaultContainerdAddress,
		namespace: defaultContainerdNamespace,
	}
	for _, opt := range opts {
		opt.apply(&o)
	}

	clientOpts := append([]containerd.ClientOpt{containerd.WithDefaultNamespace(o.namespace)}, o.clientOpts...)
	client, err := containerd.New(o.address, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("cannot create containerd client at %q: %w", o.address, err)
	}
	return &ContainerDPublisher{
		client:    client,
		namespace: o.namespace,
	}, nil
}

func (p *ContainerDPublisher) publish(ctx context.Context, registry workcellspec.RegistryURL, runfilesFS fs.FS, imageInfo ImageInfo) error {
	imagePath := PathInRunfiles(imageInfo.Tarball)
	file, err := runfilesFS.Open(imagePath)
	if err != nil {
		return fmt.Errorf("failed opening image tarball %q: %w", imagePath, err)
	}
	defer file.Close()

	ctx = namespaces.WithNamespace(ctx, p.namespace)
	dstTag, err := tag(fmt.Sprintf("%s/%s", registry, imageInfo.RepositoryBasename))
	if err != nil {
		return fmt.Errorf("failed creating image tag: %w", err)
	}

	log.V(1).InfoContextf(ctx, "Importing image tarball %q to containerd namespace %s", imagePath, p.namespace)
	importedImages, err := p.client.Import(ctx, file,
		containerd.WithIndexName(dstTag.Name()),
		containerd.WithImportPlatform(platforms.DefaultStrict()))
	if err != nil {
		return fmt.Errorf("cannot import image to runtime: %w", err)
	}

	for _, img := range importedImages {
		log.V(1).InfoContextf(ctx, "image imported to containerd: %s > %s", img.Name, img.Target.Digest)

		// and now finally, unpack image, making it ready for containers
		clientImage := containerd.NewImage(p.client, img)

		if err = clientImage.Unpack(ctx, containerd.DefaultSnapshotter, containerd.WithSnapshotterPlatformCheck()); err != nil {
			return fmt.Errorf("cannot unpack image %q: %w", img.Name, err)
		}

		imageRef, err := tarball.Image(func() (io.ReadCloser, error) {
			return runfilesFS.Open(imagePath)
		}, nil)
		if err != nil {
			return fmt.Errorf("loading %s as docker image: %v", imagePath, err)
		}
		d, err := imageRef.Digest()
		if err != nil {
			return fmt.Errorf("getting digest for %s: %v", imagePath, err)
		}

		hashedTag := fmt.Sprintf("%s/%s@%s", registry, imageInfo.RepositoryBasename, d)

		newImgRecord := images.Image{
			Name:   hashedTag,
			Target: img.Target, // Points to the exact same manifest/blobs
			Labels: img.Labels, // Optional: copy existing labels
		}

		if _, err := p.client.ImageService().Create(ctx, newImgRecord); err != nil {
			// Note: If the tag already exists, this will return an error.
			// You would need to use imageService.Update() or delete it first.
			log.InfoContextf(ctx, "Warning: Failed to tag image as %s: %v", hashedTag, err)
			continue
		}

		log.InfoContextf(ctx, "Successfully tagged image %s > %s", img.Name, hashedTag)
	}
	return nil
}

func (p *ContainerDPublisher) findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	return FindImagesToPush(imageListPaths, runfilesFS)
}
