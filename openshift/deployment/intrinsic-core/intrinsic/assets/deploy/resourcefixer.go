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

// Package resourcefixer contains functions to fix resource instance configurations as a workaround
// for some bugs.
package resourcefixer

import (
	"fmt"
	"math/rand"
	"time"

	log "github.com/golang/glog"
	"github.com/pkg/errors"

	"intrinsic/util/go/pointer"

	ccpb_v1 "intrinsic/perception/proto/v1/camera_config_go_proto"
	cdpb_v1 "intrinsic/perception/proto/v1/camera_drivers_go_proto"
	cipb_v1 "intrinsic/perception/proto/v1/camera_identifier_go_proto"

	anypb "google.golang.org/protobuf/types/known/anypb"
)

const (
	simulatedCameraIDPrefix = "camera"
	simulatedCameraIDSuffix = "simulated"
)

var (
	timeNow   = time.Now
	randInt31 = rand.Int31
	// ErrMissingCameraIdentifier is an error returned when the configuration
	// is a camera config, but the camera does not already specify an
	// identifier, which is required to appropriately
	ErrMissingCameraIdentifier = errors.New("Camera identifier not specified in config")
)

func generateSimID() string {
	// Format time with RFC3339 to get a unique 20byte string.
	return fmt.Sprintf("%s-%s-%s",
		simulatedCameraIDPrefix,
		timeNow().UTC().Format(time.RFC3339),
		simulatedCameraIDSuffix,
	)
}

// TODO(b/242041727): Ideally this should be one single API call, pending
// whether we have a way to get the default config to begin with from runtime.
//
// Initialise the camera resource by ensuring each new camera instance has
// a unique id. This is needed in particular for simulation, so that it knows
// which camera to simulate a frame from. In hardware, the user will need to
// connect to an actual camera, and that will override this auto-generated id
// with a real one.
// intrinsic/frontend/world_viewer/core/object_world_mutator.ts;l=760;rcl=546592680
func fixCameraConfigV1(cameraConfig *ccpb_v1.CameraConfig) error {
	if cameraConfig.GetIdentifier() == nil {
		return ErrMissingCameraIdentifier
	}

	simCamID := generateSimID()
	log.Infof("Selected simCamID %v for the new v1 camera resource.", simCamID)

	switch cameraConfig.GetIdentifier().Drivers.(type) {
	case *cipb_v1.CameraIdentifier_Genicam:
		cameraConfig.Identifier = &cipb_v1.CameraIdentifier{
			Drivers: &cipb_v1.CameraIdentifier_Genicam{
				Genicam: &cdpb_v1.CameraDrivers_GenICam{
					DeviceId: simCamID,
				},
			},
		}
	case *cipb_v1.CameraIdentifier_Ros:
		cameraConfig.Identifier = &cipb_v1.CameraIdentifier{
			Drivers: &cipb_v1.CameraIdentifier_Ros{
				Ros: &cdpb_v1.CameraDrivers_Ros{
					DeviceId:   simCamID,
					DriverType: pointer.To(cameraConfig.GetIdentifier().GetRos().GetDriverType()),
				},
			},
		}
	default:
		cameraConfig.Identifier = &cipb_v1.CameraIdentifier{
			Drivers: &cipb_v1.CameraIdentifier_Genicam{
				Genicam: &cdpb_v1.CameraDrivers_GenICam{
					DeviceId: simCamID,
				},
			},
		}
	}
	return nil
}

// FixConfig updates configuration for certain resource instances like cameras
// to work well in simulation. These are temporary workarounds until the
// underlying issues have been fixed.
//
// An error is raised if an error is encountered by trying to create the new
// config.
func FixConfig(c *anypb.Any) error {
	// Only fix problems, don't create new ones.
	if c == nil {
		return nil
	}

	cameraV1 := &ccpb_v1.CameraConfig{}
	if err := c.UnmarshalTo(cameraV1); err == nil {
		if err := fixCameraConfigV1(cameraV1); err != nil {
			return errors.Wrapf(err, "failed to update camera resource config")
		}
		if err := c.MarshalFrom(cameraV1); err != nil {
			return errors.Wrapf(err, "failed to update config")
		}
		return nil
	}

	return nil
}
