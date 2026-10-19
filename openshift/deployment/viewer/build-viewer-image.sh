#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../" && pwd)/canonical-env.sh"

readonly namespace="${OPENSHIFT_NAMESPACE}"
readonly viewer_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${viewer_dir}/../../.." && pwd)"
readonly context="$(mktemp -d "${TMPDIR:-/tmp}/intrinsic-viewer-context.XXXXXX")"
trap 'rm -rf "${context}"' EXIT

oc get namespace "${namespace}" >/dev/null

cp "${viewer_dir}/Containerfile" "${context}/Containerfile"
cp "${viewer_dir}/run-viewer.sh" "${viewer_dir}/desktop-session.sh" \
  "${context}/"
cp "${repo_root}/viewer/workcell.rviz" "${context}/workcell.rviz"
chmod 0555 "${context}/run-viewer.sh" "${context}/desktop-session.sh"

python3 "${canonical_root}/render_template.py" "${viewer_dir}/build.yaml" | oc apply -n "${namespace}" -f -
oc start-build intrinsic-rviz-novnc -n "${namespace}" \
  --from-dir="${context}" --follow
