#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
set -euo pipefail

readonly namespace="${OPENSHIFT_NAMESPACE:-your-project-namespace}"
readonly manifest_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly storage_class="${OPENSHIFT_STORAGE_CLASS:-}"
readonly pvc_size="${PVC_SIZE:-}"
: "${storage_class:?Set OPENSHIFT_STORAGE_CLASS to an approved storage class}"
: "${pvc_size:?Set PVC_SIZE to an approved PVC size such as 20Gi}"
if [[ ! "${namespace}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]]; then
  echo "OPENSHIFT_NAMESPACE must be a valid Kubernetes namespace." >&2
  exit 2
fi
if [[ ! "${storage_class}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]]; then
  echo "OPENSHIFT_STORAGE_CLASS must be a valid storage class name." >&2
  exit 2
fi
if [[ ! "${pvc_size}" =~ ^[1-9][0-9]*(Ki|Mi|Gi|Ti)$ ]]; then
  echo "PVC_SIZE must be a Kubernetes storage quantity such as 20Gi." >&2
  exit 2
fi

expected_api_server="${OPENSHIFT_API_SERVER_URL:-}"
if [[ -z "${expected_api_server}" ]]; then
  echo "Set OPENSHIFT_API_SERVER_URL to the intended cluster API URL before deploying." >&2
  exit 2
fi
cluster_api="$(oc get infrastructure cluster -o jsonpath='{.status.apiServerURL}')"
if [[ "${cluster_api}" != "${expected_api_server}" ]]; then
  echo "Refusing to deploy: the current OpenShift API does not match OPENSHIFT_API_SERVER_URL." >&2
  exit 1
fi
oc get namespace "${namespace}" >/dev/null
oc get imagestreamtag omts-build-tools:20260922.0 -n "${namespace}" >/dev/null
sed "s|\${OPENSHIFT_NAMESPACE}|${namespace}|g" \
  "${manifest_dir}/../omts-runner/registry-push-rbac.yaml" \
  | oc apply -n "${namespace}" -f -
sed \
  -e "s|\${OPENSHIFT_NAMESPACE}|${namespace}|g" \
  -e "s|\${OPENSHIFT_STORAGE_CLASS}|${storage_class}|g" \
  -e "s|\${PVC_SIZE}|${pvc_size}|g" \
  "${manifest_dir}/workspace.yaml" \
  | oc apply -n "${namespace}" -f -
oc rollout status deployment/omts-build-workspace \
  -n "${namespace}" \
  --timeout=10m

printf 'Workspace ready in project %s. Source, temporary files, and Bazel cache are on the PVC.\n' "${namespace}"
printf 'Open an interactive shell with: oc rsh -n %s deploy/omts-build-workspace\n' "${namespace}"
