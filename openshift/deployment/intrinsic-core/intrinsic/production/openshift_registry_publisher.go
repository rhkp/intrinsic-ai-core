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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"

	"intrinsic/kubernetes/workcell_spec/workcellspec"
	"intrinsic/util/go/set"

	log "github.com/golang/glog"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/pkg/errors"
)

// OpenShiftRegistryPublisher pushes to the OpenShift internal registry through
// a loopback port-forward. Image names retain the in-cluster registry hostname,
// so normal TLS hostname verification and registry-specific credentials work.
// Credentials come from authn.DefaultKeychain (DOCKER_CONFIG).
type OpenShiftRegistryPublisher struct {
	pushPrefix string
	transport  http.RoundTripper
}

// NewOpenShiftRegistryPublisher creates a publisher for a private registry
// tunnel. pushPrefix must use the registry's in-cluster DNS name; loopbackAddress
// is the local endpoint of an oc port-forward. It deliberately has no
// insecure-TLS mode.
func NewOpenShiftRegistryPublisher(pushPrefix, loopbackAddress, caFile string) (*OpenShiftRegistryPublisher, error) {
	repository, err := name.NewRepository(pushPrefix, name.StrictValidation)
	if err != nil {
		return nil, fmt.Errorf("invalid registry prefix: %w", err)
	}
	registryAddress := repository.Registry.Name()
	if _, _, err := net.SplitHostPort(registryAddress); err != nil {
		return nil, fmt.Errorf("registry address must include a port: %w", err)
	}
	localHost, _, err := net.SplitHostPort(loopbackAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid loopback registry address: %w", err)
	}
	localIP := net.ParseIP(localHost)
	if localIP == nil || !localIP.IsLoopback() {
		return nil, fmt.Errorf("registry forward address %q must be a loopback IP address", localHost)
	}
	if caFile == "" {
		return nil, fmt.Errorf("registry CA file is required")
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read registry CA bundle: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("registry CA bundle contains no valid certificates")
	}
	base, ok := remote.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unsupported default registry transport %T", remote.DefaultTransport)
	}
	transport := base.Clone()
	originalProxy := transport.Proxy
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		if request.URL.Host == registryAddress {
			return nil, nil // Always connect to the registry through the explicit local tunnel.
		}
		if originalProxy == nil {
			return nil, nil
		}
		return originalProxy(request)
	}
	originalDialContext := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == registryAddress {
			address = loopbackAddress
		}
		return originalDialContext(ctx, network, address)
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.RootCAs = roots
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	return &OpenShiftRegistryPublisher{pushPrefix: pushPrefix, transport: transport}, nil
}

func (p *OpenShiftRegistryPublisher) destinationTag(imageInfo ImageInfo) (*name.Tag, error) {
	return tag(fmt.Sprintf("%s/%s", p.pushPrefix, imageInfo.RepositoryBasename))
}

func (p *OpenShiftRegistryPublisher) publish(ctx context.Context, _ workcellspec.RegistryURL, runfilesFS fs.FS, imageInfo ImageInfo) error {
	imagePath := PathInRunfiles(imageInfo.Tarball)
	imageOpener := func() (io.ReadCloser, error) { return runfilesFS.Open(imagePath) }
	dstTag, err := p.destinationTag(imageInfo)
	if err != nil {
		return err
	}
	log.InfoContextf(ctx, "Pushing image tarball %q to the OpenShift registry", imagePath)
	image, err := tarball.Image(imageOpener, nil)
	if err != nil {
		return errors.Wrapf(err, "reading image tarball from %q", imagePath)
	}
	return pushImage(ctx, image, *dstTag,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithTransport(p.transport),
	)
}

func (p *OpenShiftRegistryPublisher) findImagesToPush(imageListPaths []string, runfilesFS fs.FS) (set.Set[ImageInfo], error) {
	return FindImagesToPush(imageListPaths, runfilesFS)
}

var _ Publisher = (*OpenShiftRegistryPublisher)(nil)
