#!/usr/bin/env bash
set -euo pipefail

readonly namespace="arhkp-intrinsic"
readonly workspace_deployment="omts-build-workspace"
readonly source_dir="/workspace/source/intrinsic-omts"
readonly image_target="@intrinsic-core//intrinsic/resources/service:resource_registry_main"
readonly binary_relative_path="bazel-bin/external/intrinsic-core+/intrinsic/resources/service/resource_registry_main"
readonly chunk_bytes=4194304
readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly context="$(mktemp -d "${TMPDIR:-/tmp}/resource-registry-build.XXXXXX")"
trap 'rm -rf "${context}"' EXIT

oc apply -f "${script_dir}/build.yaml"
oc exec -n "${namespace}" "deploy/${workspace_deployment}" -c workspace -- \
  sh -lc "cd '${source_dir}' && bazel build '${image_target}'"

remote_binary="${source_dir}/${binary_relative_path}"
binary_bytes="$(oc exec -n "${namespace}" "deploy/${workspace_deployment}" -c workspace -- stat -L -c %s "${remote_binary}")"
remote_sha256="$(oc exec -n "${namespace}" "deploy/${workspace_deployment}" -c workspace -- sha256sum "${remote_binary}" | awk '{print $1}')"
binary_path="${context}/resource_registry_main"
: >"${binary_path}"
for ((offset = 0; offset < binary_bytes; offset += chunk_bytes)); do
  oc exec -n "${namespace}" "deploy/${workspace_deployment}" -c workspace -- \
    dd if="${remote_binary}" bs="${chunk_bytes}" skip="$((offset / chunk_bytes))" count=1 2>/dev/null \
    >>"${binary_path}"
done

local_sha256="$(shasum -a 256 "${binary_path}" | awk '{print $1}')"
if [[ "${local_sha256}" != "${remote_sha256}" ]]; then
  echo "ResourceRegistry binary transfer checksum mismatch" >&2
  exit 1
fi
chmod 0755 "${binary_path}"
cp "${script_dir}/Dockerfile" "${context}/Dockerfile"

oc start-build -n "${namespace}" resource-registry-openshift --from-dir="${context}" --follow
oc get istag -n "${namespace}" resource-registry-openshift:gpu-toleration-20261007 \
  -o jsonpath='{.image.dockerImageReference}{"\n"}'
