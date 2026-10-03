# Copyright 2026 Intrinsic Innovation LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

load("//bazel:container.bzl", "container_image")
load("//bazel:go_macros.bzl", "go_binary", "go_library")

filegroup(
    name = "resource-registry-template",
    srcs = [
        "resource-registry.yaml",
    ],
    visibility = [
        "//intrinsic:intrinsic_internal",
    ],
)

go_binary(
    name = "noop",
    srcs = ["noop.go"],
    importpath = "intrinsic/resources/noop",
    deps = [
        "//intrinsic/kubernetes:intrinsic",
        "//intrinsic/util/proto:protoio",
        "@com_github_golang_glog//:go_default_library",
        "@intrinsic_apis//intrinsic/assets/services/proto/v1:dynamic_reconfiguration_go_proto",
        "@intrinsic_apis//intrinsic/resources/proto:runtime_context_go_proto",
        "@org_golang_google_grpc//:go_default_library",
    ],
)

container_image(
    name = "noop_image",
    base = "//intrinsic/kubernetes:base-image-oci",
    cmd = [
        "intrinsic/resources/noop",
    ],
    data_path = "/",
    files = [":noop"],
    visibility = ["//intrinsic:public_api_users"],
)

go_library(
    name = "resourcefix",
    srcs = ["resourcefix.go"],
    importpath = "intrinsic/resources/resourcefix",
    visibility = ["//intrinsic:internal_api_users"],
    deps = [
        "//intrinsic/assets/data:datafix",
        "//intrinsic/assets/processes:processfix",
        "//intrinsic/assets/proto:installation_origin_go_proto",
        "//intrinsic/resources/proto:resource_type_runtime_go_proto",
        "//intrinsic/skills:skillfix",
        "//intrinsic/util/proto:descriptorcompatibility",
        "@intrinsic_apis//intrinsic/assets/proto:metadata_go_proto",
        "@intrinsic_apis//intrinsic/assets/services/proto/v1:dynamic_reconfiguration_go_proto",
        "@intrinsic_apis//intrinsic/assets/services/proto/v1:service_state_go_proto",
        "@intrinsic_apis//intrinsic/resources/proto:resource_service_definition_go_proto",
        "@org_golang_google_protobuf//types/descriptorpb:go_default_library",
    ],
)

go_library(
    name = "resourcevalidate",
    srcs = ["resourcevalidate.go"],
    importpath = "intrinsic/resources/resourcevalidate",
    visibility = ["//intrinsic:internal_api_users"],
    deps = [
        "//intrinsic/assets:assetvalidate",
        "//intrinsic/assets:metadatautils",
        "//intrinsic/assets/conversion:runtime",
        "//intrinsic/assets/data:datavalidate",
        "//intrinsic/assets/errors:report",
        "//intrinsic/assets/hardware_devices:hardwaredevicevalidate",
        "//intrinsic/assets/processes:processvalidate",
        "//intrinsic/assets/proto/v1:asset_go_proto",
        "//intrinsic/assets/scene_objects:sceneobjectvalidate",
        "//intrinsic/assets/services:servicevalidate",
        "//intrinsic/resources/proto:resource_type_runtime_go_proto",
        "//intrinsic/skills:skillvalidate",
    ],
)
