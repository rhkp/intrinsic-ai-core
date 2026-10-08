#!/usr/bin/env bash
set -euo pipefail

readonly namespace="arhkp-intrinsic"
readonly viewer_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${viewer_dir}/../../.." && pwd)"
readonly context="$(mktemp -d "${TMPDIR:-/tmp}/intrinsic-viewer-context.XXXXXX")"
trap 'rm -rf "${context}"' EXIT

cluster_api="$(oc get infrastructure cluster -o jsonpath='{.status.apiServerURL}')"
if [[ "${cluster_api}" != *ai-dev01* ]]; then
  echo "Refusing to build: the current OpenShift target is not dev01." >&2
  exit 1
fi
oc get namespace "${namespace}" >/dev/null

cp "${viewer_dir}/Containerfile" "${context}/Containerfile"
cp "${viewer_dir}/run-viewer.sh" "${viewer_dir}/desktop-session.sh" \
  "${context}/"
cp "${repo_root}/viewer/workcell.rviz" "${context}/workcell.rviz"
chmod 0555 "${context}/run-viewer.sh" "${context}/desktop-session.sh"

oc apply -n "${namespace}" -f "${viewer_dir}/build.yaml"
oc start-build intrinsic-rviz-novnc -n "${namespace}" \
  --from-dir="${context}" --follow
