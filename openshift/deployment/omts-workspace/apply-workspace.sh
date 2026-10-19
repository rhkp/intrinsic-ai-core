#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../" && pwd)/canonical-env.sh"

readonly namespace="${OPENSHIFT_NAMESPACE}"
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

oc get namespace "${namespace}" >/dev/null
oc get imagestreamtag omts-build-tools:20260922.0 -n "${namespace}" >/dev/null
python3 "${canonical_root}/render_template.py" "${manifest_dir}/../omts-runner/registry-push-rbac.yaml" | oc apply -n "${namespace}" -f -
if oc get pvc omts-build-workspace -n "${namespace}" >/dev/null 2>&1; then
  python3 "${canonical_root}/render_template.py" --exclude-pvc "${manifest_dir}/workspace.yaml" | oc apply -n "${namespace}" -f -
else
  python3 "${canonical_root}/render_template.py" "${manifest_dir}/workspace.yaml" | oc apply -n "${namespace}" -f -
fi
oc rollout status deployment/omts-build-workspace \
  -n "${namespace}" \
  --timeout=10m

printf 'Workspace ready in project %s. Source, temporary files, and Bazel cache are on the PVC.\n' "${namespace}"
printf 'Open an interactive shell with: oc rsh -n %s deploy/omts-build-workspace\n' "${namespace}"
