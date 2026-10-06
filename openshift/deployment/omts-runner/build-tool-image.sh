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
  omts-openshift-demo-configs.patch \
  omts-openshift-module-registry-keychain.patch \
  omts-openshift-sim-config-safety.patch \
  omts-tinygltf-bcr-override.patch \
  intrinsic-core-triton-digest-pin.patch; do
  cp "${repo_root}/patches/${patch_file}" "${context}/patches/${patch_file}"
done

oc apply -f "${runner_dir}/registry-push-rbac.yaml"
oc apply -f "${runner_dir}/build-tool-image.yaml"
oc start-build omts-demo-tools \
  -n "${namespace}" \
  --from-dir="${context}" \
  --follow
