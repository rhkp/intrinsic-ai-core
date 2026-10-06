#!/usr/bin/env bash
set -euo pipefail

readonly release="20260922.0"
readonly default_image="quay.io/rhkp/intrinsic/omts-demo-runner:${release}"
readonly runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${runner_dir}/../../.." && pwd)"
source_dir="${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"
image_ref="${1:-${default_image}}"

if ! command -v bazel >/dev/null 2>&1 || ! command -v podman >/dev/null 2>&1; then
  echo "Bazel and Podman are required on a Linux x86-64 build host." >&2
  exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
  echo "OMTS runner image builds must run on an x86-64 Linux host." >&2
  exit 1
fi

"${runner_dir}/prepare-omts-source.sh" "${source_dir}"

build_tmp="${TMPDIR:-${HOME}/tmp}"
mkdir -p "${build_tmp}" "${HOME}/intrinsic-distfiles"
cd "${source_dir}"
TMPDIR="${build_tmp}" bazel --batch build \
  --jobs="${OMTS_BUILD_JOBS:-4}" \
  --distdir="${HOME}/intrinsic-distfiles" \
  --build_python_zip=true \
  //src:omts_app \
  //tools/pose_estimation:register_using_train_service

app_zip="${source_dir}/bazel-bin/src/omts_app.zip"
register_zip="${source_dir}/bazel-bin/tools/pose_estimation/register_using_train_service.zip"
safe_app_config="${repo_root}/openshift/deployment/intrinsic-omts/configs/lab_bb_01/app_config.yaml"
python3 "${runner_dir}/audit_runner_archives.py" \
  --config "${safe_app_config}" \
  "${app_zip}" "${register_zip}"

stage_dir="$(mktemp -d "${build_tmp%/}/omts-runner-build.XXXXXX")"
trap 'rm -rf "${stage_dir}"' EXIT
cp "${app_zip}" "${stage_dir}/omts_app.zip"
cp "${register_zip}" "${stage_dir}/register_using_train_service.zip"
cp "${runner_dir}/Containerfile" "${stage_dir}/Containerfile"
cp "${runner_dir}/omts-runner" "${stage_dir}/omts-runner"
cp "${safe_app_config}" "${stage_dir}/app_config.yaml"
cp "${source_dir}/LICENSE" "${stage_dir}/LICENSE"
cp "${source_dir}/TRADEMARK.md" "${stage_dir}/TRADEMARK.md"

podman build \
  --platform=linux/amd64 \
  --pull=always \
  --tag "${image_ref}" \
  --file "${stage_dir}/Containerfile" \
  "${stage_dir}"

echo "Built image: ${image_ref}"
podman image inspect --format 'local-image-id={{.Id}} architecture={{.Architecture}} os={{.Os}}' "${image_ref}"
sha256sum "${app_zip}" "${register_zip}"
sha256sum "${stage_dir}/app_config.yaml"
