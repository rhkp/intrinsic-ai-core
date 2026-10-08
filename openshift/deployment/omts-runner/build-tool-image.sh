#!/usr/bin/env bash
set -euo pipefail

readonly namespace="arhkp-intrinsic"
readonly runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${runner_dir}/../../.." && pwd)"
readonly context="$(mktemp -d "${TMPDIR:-/tmp}/omts-build-context.XXXXXX")"
trap 'rm -rf "${context}"' EXIT

mkdir -p \
  "${context}/openshift/deployment/omts-runner" \
  "${context}/openshift/deployment/intrinsic-omts/configs/lab_bb_01" \
  "${context}/patches"
for source_file in \
  Containerfile.toolchain \
  audit_runner_archives.py \
  audit_solution_artifacts.py \
  build-runner-artifacts.sh \
  build-solution-cli.sh \
  deploy-simulation.sh \
  omts-runner \
  prepare-internal-registry-auth.py \
  prepare-omts-source.sh; do
  cp "${runner_dir}/${source_file}" \
    "${context}/openshift/deployment/omts-runner/${source_file}"
done
cp "${repo_root}/openshift/deployment/intrinsic-omts/configs/lab_bb_01/app_config.yaml" \
  "${context}/openshift/deployment/intrinsic-omts/configs/lab_bb_01/app_config.yaml"
for patch_file in \
  omts-anonymous-releases.patch \
  omts-openshift-core-registry-keychain.patch \
  intrinsic-openshift-gpu-toleration.patch \
  omts-openshift-gpu-toleration.patch \
  omts-openshift-resource-registry-adaptations.patch \
  resource-registry-namespace-configmaps.patch \
  resource-registry-openshift-ingress.patch \
  omts-openshift-demo-configs.patch \
  omts-openshift-module-registry-keychain.patch \
  omts-openshift-sim-config-safety.patch \
  omts-tinygltf-bcr-override.patch \
  intrinsic-core-triton-digest-pin.patch \
  openshift-rules-ros2-build-compat.patch \
  openshift-tinygltf-bazel-headers.patch \
  openshift-glib-gmodule-header.patch \
  openshift-grpc-proto-path-order.patch \
  openshift-dumb-init-version-header.patch \
  openshift-gz-common-cdt-headers.patch \
  openshift-aravis-api-header.patch \
  openshift-libzmq-install-libdir.patch; do
  case "${patch_file}" in
    resource-registry-namespace-configmaps.patch|resource-registry-openshift-ingress.patch)
      patch_source_dir="${repo_root}/openshift/deployment/patches"
      ;;
    *)
      patch_source_dir="${repo_root}/patches"
      ;;
  esac
  cp "${patch_source_dir}/${patch_file}" "${context}/patches/${patch_file}"
done

oc apply -f "${runner_dir}/registry-push-rbac.yaml"
oc apply -f "${runner_dir}/build-tool-image.yaml"
oc start-build omts-demo-tools \
  -n "${namespace}" \
  --from-dir="${context}" \
  --follow
