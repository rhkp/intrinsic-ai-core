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

package instanceconversion

import (
	"fmt"

	"intrinsic/assets/deploy/render"
	"intrinsic/assets/idutils"
	"intrinsic/assets/services/inspection"

	atpb "intrinsic/assets/proto/asset_type_go_proto"
	metadatapb "intrinsic/assets/proto/metadata_go_proto"
	aigrpcpb "intrinsic/assets/proto/v1/asset_instances_go_proto"
	grpcconnectionpb "intrinsic/assets/proto/v1/grpc_connection_go_proto"
	icpb "intrinsic/assets/proto/v1/instance_config_go_proto"
	ripb "intrinsic/resources/proto/resource_instance_go_proto"
	rsdpb "intrinsic/resources/proto/resource_service_definition_go_proto"
	rtrpb "intrinsic/resources/proto/resource_type_runtime_go_proto"
)

func convertConfig(ri *ripb.ResourceInstance, assetType atpb.AssetType) *icpb.InstanceConfig {
	sceneObject := &icpb.InstanceConfig_SceneObjectInstanceConfig{
		SceneObjectConfig: ri.GetSceneObjectConfig(),
	}
	var scheduledNodeHostname *string
	if hostName := ri.GetSchedulingConfig().GetRequiredNodeHostname(); hostName != "" {
		scheduledNodeHostname = &hostName
	}
	service := &icpb.InstanceConfig_ServiceInstanceConfig{
		ServiceConfig:         ri.GetConfiguration(),
		ScheduledNodeHostname: scheduledNodeHostname,
		DataFiles:             ri.GetDataFiles().GetFiles(),
	}

	// We need to use assetType here because nothing right now enforces the
	// invariant that ri.GetConfiguration() and/or ri.GetSceneObjectConfig() are
	// set.  For example, as of this writing, intrinsic_solution will only
	// send a ServiceInstance config message.  It is not able to send other types
	// of config messages, so scene object config is always blank, and defaults
	// are not required for scene objects.  This is why we do not infer a
	// hardware device from the existance of both ri.GetConfiguration() and
	// ri.GetSceneObjectConfig(), and so on, even though that is very tempting.
	switch assetType {
	case atpb.AssetType_ASSET_TYPE_SCENE_OBJECT:
		return &icpb.InstanceConfig{
			Variant: &icpb.InstanceConfig_SceneObject{
				SceneObject: sceneObject,
			},
		}
	case atpb.AssetType_ASSET_TYPE_SERVICE:
		return &icpb.InstanceConfig{
			Variant: &icpb.InstanceConfig_Service{
				Service: service,
			},
		}
	case atpb.AssetType_ASSET_TYPE_HARDWARE_DEVICE:
		return &icpb.InstanceConfig{
			Variant: &icpb.InstanceConfig_HardwareDevice{
				HardwareDevice: &icpb.InstanceConfig_HardwareDeviceInstanceConfig{
					SceneObject: sceneObject,
					Service:     service,
				},
			},
		}
	default:
		return nil
	}
}

func requiresSchedulingConfig(rsd *rsdpb.ResourceServiceDefinition) bool {
	for _, image := range rsd.GetSimSpec().GetImage() {
		if image.GetRequiresRtpcNode() {
			return true
		}
	}
	for _, image := range rsd.GetRealSpec().GetImage() {
		if image.GetRequiresRtpcNode() {
			return true
		}
	}
	return false
}

func grpcConnection(name string, rsd *rsdpb.ResourceServiceDefinition) (*grpcconnectionpb.GrpcConnection, error) {
	if len(rsd.GetServiceProtoPrefixes()) == 0 {
		return nil, nil
	}
	address, err := render.ConfiguredIngressAddress()
	if err != nil {
		return nil, fmt.Errorf("configure gRPC connection for resource instance %q: %w", name, err)
	}
	return &grpcconnectionpb.GrpcConnection{
		Address: address,
		Metadata: []*grpcconnectionpb.GrpcConnection_Metadata{
			{
				Key:   render.AssetInstanceHeader,
				Value: name,
			},
		},
	}, nil
}

func serviceDetails(ri *ripb.ResourceInstance, rtr *rtrpb.ResourceTypeRuntime) (*aigrpcpb.AssetInstance_Details_Service, error) {
	rsd := rtr.GetServiceDef()
	if rsd == nil {
		return nil, nil
	}
	connection, err := grpcConnection(ri.GetName(), rsd)
	if err != nil {
		return nil, err
	}
	var serviceInspectionTopic *string
	if topic, supported := inspection.Topic(ri.GetName(), rtr); supported {
		serviceInspectionTopic = &topic
	}
	return &aigrpcpb.AssetInstance_Details_Service{
		GrpcConnection:           connection,
		RequiresSchedulingConfig: requiresSchedulingConfig(rsd),
		ServiceInspectionTopic:   serviceInspectionTopic,
	}, nil
}

func details(ri *ripb.ResourceInstance, rtr *rtrpb.ResourceTypeRuntime) (*aigrpcpb.AssetInstance_Details, error) {
	service, err := serviceDetails(ri, rtr)
	if err != nil {
		return nil, err
	}
	return &aigrpcpb.AssetInstance_Details{
		Service: service,
	}, nil
}

// ConvertResourceInstanceToAssetInstance converts a ResourceInstance and its Runtime into an AssetInstance.
func ConvertResourceInstanceToAssetInstance(ri *ripb.ResourceInstance, rtr *rtrpb.ResourceTypeRuntime) (*aigrpcpb.AssetInstance, error) {
	instanceDetails, err := details(ri, rtr)
	if err != nil {
		return nil, err
	}
	return &aigrpcpb.AssetInstance{
		Name:     ri.GetName(),
		Asset:    idutils.IDFromProtoUnchecked(rtr.GetMetadata().GetIdVersion().GetId()),
		Config:   convertConfig(ri, rtr.GetMetadata().GetAssetType()),
		Details:  instanceDetails,
		Metadata: rtr.GetMetadata(),
	}, nil
}

func detailsMetadata(m *metadatapb.Metadata) *metadatapb.Metadata {
	return &metadatapb.Metadata{
		AssetTag:  m.GetAssetTag(),
		AssetType: m.GetAssetType(),
		IdVersion: m.GetIdVersion(),
		Provides:  m.GetProvides(),
	}
}

// AsView filters the AssetInstance based on the requested view.
func AsView(i *aigrpcpb.AssetInstance, view aigrpcpb.AssetInstanceView) *aigrpcpb.AssetInstance {
	switch view {
	case aigrpcpb.AssetInstanceView_ASSET_INSTANCE_VIEW_BASIC:
		return &aigrpcpb.AssetInstance{
			Name:  i.GetName(),
			Asset: i.GetAsset(),
		}
	case aigrpcpb.AssetInstanceView_ASSET_INSTANCE_VIEW_DETAIL:
		return &aigrpcpb.AssetInstance{
			Name:     i.GetName(),
			Asset:    i.GetAsset(),
			Config:   i.GetConfig(),
			Details:  i.GetDetails(),
			Metadata: detailsMetadata(i.GetMetadata()),
		}
	default:
		return i // Validation and defaults are handled on entry to the RPC.
	}
}
