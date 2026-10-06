#!/usr/bin/env bash
set -euo pipefail

source_dir="${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"
gateway_address="${INTRINSIC_GATEWAY_ADDRESS:-}"
runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
registry_host="${INTRINSIC_REGISTRY_HOST:-image-registry.openshift-image-registry.svc:5000}"
openshift_namespace="${OPENSHIFT_NAMESPACE:-arhkp-intrinsic}"
internal_registry="${registry_host}/${openshift_namespace}"

if [[ -z "${gateway_address}" ]]; then
  echo "Set INTRINSIC_GATEWAY_ADDRESS to the project Gateway Service DNS name and internal HTTP/2 port 80." >&2
  exit 1
fi
if [[ ! "${gateway_address}" =~ :80$ ]]; then
  echo "INTRINSIC_GATEWAY_ADDRESS must use the dedicated Gateway's internal HTTP/2 port 80." >&2
  exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
  echo "The pinned OMTS deployer must run in an x86-64 Linux environment." >&2
  exit 1
fi

"${runner_dir}/prepare-omts-source.sh" "${source_dir}"
solution_file="${source_dir}/bazel-bin/omts_solution.local_solution.binpb"
runfiles_manifest="${source_dir}/bazel-bin/omts_solution.runfiles_manifest"
solution_cli="${source_dir}/bazel-bin/omts_solution"
if [[ ! -s "${solution_file}" || ! -s "${runfiles_manifest}" || ! -x "${solution_cli}" ]]; then
  echo "Build the OMTS deployer first with build-solution-cli.sh." >&2
  exit 1
fi
python3 "${runner_dir}/audit_solution_artifacts.py" "${solution_file}" "${runfiles_manifest}"

if [[ -n "${INTRINSIC_REGISTRY:-}" && "${INTRINSIC_REGISTRY}" != "${internal_registry}" ]]; then
  echo "Refusing a registry outside the arhkp-intrinsic OpenShift project." >&2
  exit 1
fi

export DOCKER_CONFIG="${DOCKER_CONFIG:-/tmp/omts-registry-auth}"
if [[ "${DOCKER_CONFIG}" != "/tmp/omts-registry-auth" ]]; then
  echo "DOCKER_CONFIG must use the workspace's ephemeral /tmp path." >&2
  exit 1
fi
registry_ca_bundle=""
trap 'rm -f "${DOCKER_CONFIG}/config.json"; [[ -z "${registry_ca_bundle}" ]] || rm -f "${registry_ca_bundle}"' EXIT
registry_ca_bundle="$(
  OPENSHIFT_NAMESPACE="${openshift_namespace}" \
  INTRINSIC_REGISTRY_HOST="${registry_host}" \
  "${runner_dir}/prepare-internal-registry-auth.py"
)"
export SSL_CERT_FILE="${registry_ca_bundle}"
export INTRINSIC_SKIP_DIRECT_UPLOAD=true
export INTRINSIC_REGISTRY="${internal_registry}"
cd "${source_dir}"

"${solution_cli}" \
  --address="${gateway_address}" \
  --operation_mode=sim
