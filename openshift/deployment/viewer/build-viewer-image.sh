#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
set -euo pipefail

readonly namespace="${OPENSHIFT_NAMESPACE:-your-project-namespace}"
readonly viewer_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${viewer_dir}/../../.." && pwd)"
readonly context="$(mktemp -d "${TMPDIR:-/tmp}/intrinsic-viewer-context.XXXXXX")"
trap 'rm -rf "${context}"' EXIT

expected_api_server="${OPENSHIFT_API_SERVER_URL:-}"
if [[ -z "${expected_api_server}" ]]; then
  echo "Set OPENSHIFT_API_SERVER_URL to the intended cluster API URL before building." >&2
  exit 2
fi
cluster_api="$(oc get infrastructure cluster -o jsonpath='{.status.apiServerURL}')"
if [[ "${cluster_api}" != "${expected_api_server}" ]]; then
  echo "Refusing to build: the current OpenShift API does not match OPENSHIFT_API_SERVER_URL." >&2
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
