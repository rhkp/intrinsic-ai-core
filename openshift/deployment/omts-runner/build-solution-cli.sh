#!/usr/bin/env bash
set -euo pipefail

readonly runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source_dir="${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"

if ! command -v bazel >/dev/null 2>&1; then
  echo "Bazel is required in the x86-64 Linux build environment." >&2
  exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
  echo "The pinned OMTS solution deployer must be built in an x86-64 Linux environment." >&2
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
  --config=lab_bb_01 \
  //:omts_solution

python3 "${runner_dir}/audit_solution_artifacts.py" \
  "${source_dir}/bazel-bin/omts_solution.local_solution.binpb" \
  "${source_dir}/bazel-bin/omts_solution.runfiles_manifest"
