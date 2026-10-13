// This project was developed with assistance from AI tools.
// Copyright 2026 Intrinsic Innovation LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0

package imagepublisher

import (
	"strings"
	"testing"
)

func TestOpenShiftRegistryDestinationKeepsProjectPrefix(t *testing.T) {
	t.Setenv("RELEASE_CANDIDATE_NAME", "openshift-test")
	publisher := &OpenShiftRegistryPublisher{
		pushPrefix: "image-registry.openshift-image-registry.svc:5000/your-project-namespace",
	}
	tag, err := publisher.destinationTag(ImageInfo{RepositoryBasename: "intrinsic-assets"})
	if err != nil {
		t.Fatalf("destinationTag() error = %v", err)
	}
	if got, wantPrefix := tag.Name(), "image-registry.openshift-image-registry.svc:5000/your-project-namespace/intrinsic-assets:"; !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("destinationTag() = %q, want prefix %q", got, wantPrefix)
	}
}

func TestNewOpenShiftRegistryPublisherRequiresTrustedCA(t *testing.T) {
	if _, err := NewOpenShiftRegistryPublisher("image-registry.openshift-image-registry.svc:5000/your-project-namespace", "127.0.0.1:5000", ""); err == nil {
		t.Fatal("NewOpenShiftRegistryPublisher() accepted an empty CA path")
	}
	if _, err := NewOpenShiftRegistryPublisher("image-registry.openshift-image-registry.svc:5000/your-project-namespace", "127.0.0.1:5000", "/missing/ca.pem"); err == nil {
		t.Fatal("NewOpenShiftRegistryPublisher() accepted a missing CA file")
	}
}

func TestNewOpenShiftRegistryPublisherRejectsNonLoopbackRegistry(t *testing.T) {
	if _, err := NewOpenShiftRegistryPublisher("image-registry.openshift-image-registry.svc:5000/your-project-namespace", "example.invalid:5000", "/unused/ca.pem"); err == nil {
		t.Fatal("NewOpenShiftRegistryPublisher() accepted a non-loopback registry forward")
	}
}
