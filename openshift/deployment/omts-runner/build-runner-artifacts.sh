#!/usr/bin/env bash
set -euo pipefail

readonly runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${runner_dir}/../../.." && pwd)"
source_dir="${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"

if ! command -v bazel >/dev/null 2>&1; then
  echo "Bazel is required in the x86-64 Linux build environment." >&2
  exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
  echo "OMTS runner artifacts must be built in an x86-64 Linux environment." >&2
  exit 1
fi

"${runner_dir}/prepare-omts-source.sh" "${source_dir}"

build_tmp="${TMPDIR:-${HOME}/tmp}"
mkdir -p "${build_tmp}" "${HOME}/intrinsic-distfiles"
cd "${source_dir}"
TMPDIR="${build_tmp}" bazel --batch build \
  --jobs="${OMTS_BUILD_JOBS:-4}" \
  --distdir="${HOME}/intrinsic-distfiles" \
  --spawn_strategy=local \
  --genrule_strategy=local \
  --build_python_zip=true \
  //src:omts_app \
  //tools/pose_estimation:register_using_train_service

python3 "${runner_dir}/audit_runner_archives.py" \
  --config "${repo_root}/openshift/deployment/intrinsic-omts/configs/lab_bb_01/app_config.yaml" \
  "${source_dir}/bazel-bin/src/omts_app.zip" \
  "${source_dir}/bazel-bin/tools/pose_estimation/register_using_train_service.zip"
