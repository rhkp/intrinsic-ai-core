#!/usr/bin/env bash
set -euo pipefail

readonly namespace="arhkp-intrinsic"
readonly manifest_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cluster_api="$(oc get infrastructure cluster -o jsonpath='{.status.apiServerURL}')"
if [[ "${cluster_api}" != *ai-dev01* ]]; then
  echo "Refusing to deploy: the current OpenShift target is not dev01." >&2
  exit 1
fi
oc get namespace "${namespace}" >/dev/null
oc get imagestreamtag omts-build-tools:20260922.0 -n "${namespace}" >/dev/null
oc apply -n "${namespace}" \
  -f "${manifest_dir}/../omts-runner/registry-push-rbac.yaml"
oc apply -n "${namespace}" -f "${manifest_dir}/workspace.yaml"
oc rollout status deployment/omts-build-workspace \
  -n "${namespace}" \
  --timeout=10m

printf 'Workspace ready in project %s. Source, temporary files, and Bazel cache are on the PVC.\n' "${namespace}"
printf 'Open an interactive shell with: oc rsh -n %s deploy/omts-build-workspace\n' "${namespace}"
