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

// Package crcconfig provides read-only access to cloud-robotics deployment configuration
package crcconfig

import (
	"context"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

const (
	bucketPrefix = "-cloud-robotics-config"
	configName   = "config.sh"

	// CloudRoboticsCtx is the k8s context of the cloud robotics cluster.
	CloudRoboticsCtx = "CLOUD_ROBOTICS_CTX"
)

// Config is a map of key-value pairs from the config.sh file.
type Config map[string]string

func parseConfig(data string) (Config, error) {
	cfg := make(Config)
	for _, line := range strings.Split(data, "\n") {
		// skip comments and empty lines
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// split into key and values
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		cfg[kv[0]] = strings.Trim(kv[1], "\"'")
	}

	return cfg, nil
}

// New reads the cloud-robotics deployment config and returns the values as a map.
func New(ctx context.Context, project string, opts ...option.ClientOption) (Config, error) {
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	defer client.Close()

	rc, err := client.Bucket(project + bucketPrefix).Object(configName).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("read from gcs: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	return parseConfig(string(data))
}

// Location returns the location of the cloud-robotics cluster.
//
// This defaults to the GCP_ZONE variable, but if GKE_CLUSTER_TYPE is set to "regional", then GCP_REGION is used instead.
//
// Returns an error when the location cannot be determined from the config.
func (c Config) Location() (string, error) {
	if clusterType, ok := c["GKE_CLUSTER_TYPE"]; ok {
		// In the future, we may need to look at other possible ways of setting GKE_CLUSTER_TYPE.
		if clusterType == "regional" {
			if region, ok := c["GCP_REGION"]; ok {
				return region, nil
			} else {
				return "", fmt.Errorf("gke cluster type is regional but crc config does not specify GCP_REGION")
			}
		}
	}

	// If we get here, we use the zone.
	if zone, ok := c["GCP_ZONE"]; ok {
		return zone, nil
	}
	return "", fmt.Errorf("No GCP_ZONE specified in crc config")
}
