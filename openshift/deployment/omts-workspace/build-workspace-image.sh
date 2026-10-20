#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../" && pwd)/canonical-env.sh"

readonly namespace="${OPENSHIFT_NAMESPACE}"
readonly workspace_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${workspace_dir}/../../.." && pwd)"
readonly runner_dir="${repo_root}/openshift/deployment/omts-runner"
readonly context="$(mktemp -d "${TMPDIR:-/tmp}/omts-workspace-context.XXXXXX")"
trap 'rm -rf "${context}"' EXIT

oc get namespace "${namespace}" >/dev/null

mkdir -p \
  "${context}/openshift/deployment/omts-workspace" \
  "${context}/openshift/deployment/omts-runner" \
  "${context}/openshift/deployment/intrinsic-omts/configs/your-workcell" \
  "${context}/patches"

cp "${workspace_dir}/Containerfile" \
  "${context}/openshift/deployment/omts-workspace/Containerfile"
for source_file in \
  audit_solution_artifacts.py \
  build-solution-cli.sh \
  deploy-simulation.sh \
  prepare-internal-registry-auth.py \
  prepare-omts-source.sh; do
  cp "${runner_dir}/${source_file}" \
    "${context}/openshift/deployment/omts-runner/${source_file}"
done
chmod 0555 \
  "${context}/openshift/deployment/omts-runner/build-solution-cli.sh" \
  "${context}/openshift/deployment/omts-runner/deploy-simulation.sh" \
  "${context}/openshift/deployment/omts-runner/prepare-internal-registry-auth.py" \
  "${context}/openshift/deployment/omts-runner/prepare-omts-source.sh" \
  "${context}/openshift/deployment/omts-runner/audit_solution_artifacts.py"
cp "${repo_root}/openshift/deployment/intrinsic-omts/configs/your-workcell/app_config.yaml" \
  "${context}/openshift/deployment/intrinsic-omts/configs/your-workcell/app_config.yaml"
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
  omts-release-clearance.patch \
  intrinsic-core-triton-digest-pin.patch \
  openshift-glib-gmodule-header.patch \
  openshift-grpc-proto-path-order.patch \
  openshift-dumb-init-version-header.patch \
  openshift-gz-common-cdt-headers.patch \
  openshift-aravis-api-header.patch \
  openshift-libzmq-install-libdir.patch \
  openshift-rules-ros2-build-compat.patch \
  openshift-tinygltf-bazel-headers.patch; do
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

sed "s|\${OPENSHIFT_NAMESPACE}|${namespace}|g" \
  "${workspace_dir}/build-workspace-image.yaml" \
  | oc apply -n "${namespace}" -f -
oc start-build omts-build-tools \
  -n "${namespace}" \
  --from-dir="${context}" \
  --follow

if oc get deployment omts-build-workspace -n "${namespace}" >/dev/null 2>&1; then
  oc rollout restart deployment/omts-build-workspace -n "${namespace}"
  oc rollout status deployment/omts-build-workspace \
    -n "${namespace}" \
    --timeout=10m
fi
