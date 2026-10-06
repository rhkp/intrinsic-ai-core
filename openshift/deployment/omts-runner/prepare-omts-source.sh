#!/usr/bin/env bash
set -euo pipefail

readonly release="20260922.0"
readonly expected_commit="8253cdfdd173da9d5b7c9bb7b8cee817f66902ae"
readonly runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${runner_dir}/../../.." && pwd)"
source_dir="${1:-${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}}"

if ! command -v git >/dev/null 2>&1; then
  echo "Git is required to prepare the pinned OMTS source." >&2
  exit 1
fi

export GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_COUNT=2
export GIT_CONFIG_KEY_0=credential.helper
export GIT_CONFIG_VALUE_0=
export GIT_CONFIG_KEY_1=http.extraHeader
export GIT_CONFIG_VALUE_1=
unset GH_TOKEN GITHUB_TOKEN

if [[ ! -e "${source_dir}" ]]; then
  mkdir -p "$(dirname "${source_dir}")"
  git clone --revision="${release}" \
    https://github.com/intrinsic-ai/intrinsic-omts.git "${source_dir}"
elif ! git -C "${source_dir}" rev-parse --git-dir >/dev/null 2>&1; then
  echo "source path exists but is not a Git checkout; refusing to overwrite it" >&2
  exit 1
fi

actual_commit="$(git -C "${source_dir}" rev-parse HEAD)"
if [[ "${actual_commit}" != "${expected_commit}" ]]; then
  echo "OMTS checkout does not match the pinned release commit; refusing to build" >&2
  exit 1
fi

install -D -m 0644 \
  "${repo_root}/patches/omts-openshift-core-registry-keychain.patch" \
  "${source_dir}/bazel/patches/openshift_registry_keychain.patch"
install -D -m 0644 \
  "${repo_root}/patches/intrinsic-core-triton-digest-pin.patch" \
  "${source_dir}/bazel/patches/openshift_triton_digest_pin.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-rules-ros2-build-compat.patch" \
  "${source_dir}/bazel/patches/openshift_rules_ros2.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-tinygltf-bazel-headers.patch" \
  "${source_dir}/bazel/patches/openshift_tinygltf_bazel_headers.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-glib-gmodule-header.patch" \
  "${source_dir}/bazel/patches/openshift_glib_gmodule_header.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-grpc-proto-path-order.patch" \
  "${source_dir}/bazel/patches/openshift_grpc_proto_path_order.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-dumb-init-version-header.patch" \
  "${source_dir}/bazel/patches/openshift_dumb_init_version_header.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-gz-common-cdt-headers.patch" \
  "${source_dir}/bazel/patches/openshift_gz_common_cdt_headers.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-aravis-api-header.patch" \
  "${source_dir}/bazel/patches/openshift_aravis_api_header.patch"
install -D -m 0644 \
  "${repo_root}/patches/openshift-libzmq-install-libdir.patch" \
  "${source_dir}/bazel/patches/openshift_libzmq_install_libdir.patch"

validate_source_changes() {
  while IFS= read -r change; do
    [[ -z "${change}" ]] && continue
    case "${change:3}" in
      BUILD|MODULE.bazel|bazel/gh_release.bzl|bazel/patches/BUILD|bazel/patches/openshift_registry_keychain.patch|bazel/patches/openshift_triton_digest_pin.patch|bazel/patches/openshift_rules_ros2.patch|bazel/patches/openshift_tinygltf_bazel_headers.patch|bazel/patches/openshift_glib_gmodule_header.patch|bazel/patches/openshift_grpc_proto_path_order.patch|bazel/patches/openshift_dumb_init_version_header.patch|bazel/patches/openshift_gz_common_cdt_headers.patch|bazel/patches/openshift_aravis_api_header.patch|bazel/patches/openshift_libzmq_install_libdir.patch|configs/BUILD|src/BUILD) ;;
      *)
        echo "unexpected OMTS source modification; refusing to build" >&2
        return 1
        ;;
    esac
  done < <(git -C "${source_dir}" status --short --untracked-files=normal)
}

readonly patch_owned_tracked_files=(
  BUILD
  MODULE.bazel
  bazel/gh_release.bzl
  bazel/patches/BUILD
  configs/BUILD
  src/BUILD
)
readonly compatibility_patches=(
  "${repo_root}/patches/omts-anonymous-releases.patch"
  "${repo_root}/patches/omts-openshift-demo-configs.patch"
  "${repo_root}/patches/omts-openshift-sim-config-safety.patch"
  "${repo_root}/patches/omts-openshift-module-registry-keychain.patch"
  "${repo_root}/patches/omts-tinygltf-bcr-override.patch"
)

validate_source_changes
if ! git -C "${source_dir}" diff --cached --quiet; then
  echo "OMTS source has staged changes; refusing to rewrite its preparation files" >&2
  exit 1
fi

# Later patches can add lines adjacent to earlier ones, making reverse-apply
# checks unreliable. Rebuild the patch-owned files from the pinned commit, but
# only accept a dirty tree if it reproduces exactly from this patch series.
prepared_diff="$(mktemp)"
git -C "${source_dir}" diff --binary HEAD >"${prepared_diff}"
before_patch_hash="$(python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())' <"${prepared_diff}")"
empty_patch_hash="$(python3 -c 'import hashlib; print(hashlib.sha256(b"").hexdigest())')"
prepared_rewrite_started=false
prepared_rewrite_complete=false
restore_prepared_source_on_error() {
  local status=$?
  if [[ "${prepared_rewrite_started}" == true && "${prepared_rewrite_complete}" != true ]]; then
    git -C "${source_dir}" checkout -- "${patch_owned_tracked_files[@]}" >/dev/null 2>&1 || true
    if [[ -s "${prepared_diff}" ]]; then
      git -C "${source_dir}" apply --binary "${prepared_diff}" || true
    fi
  fi
  rm -f "${prepared_diff}"
  return "${status}"
}
trap restore_prepared_source_on_error EXIT

prepared_rewrite_started=true
git -C "${source_dir}" checkout -- "${patch_owned_tracked_files[@]}"
for patch_file in "${compatibility_patches[@]}"; do
  git -C "${source_dir}" apply "${patch_file}"
done

after_patch_hash="$(git -C "${source_dir}" diff --binary HEAD | python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')"
if [[ "${before_patch_hash}" != "${empty_patch_hash}" && "${before_patch_hash}" != "${after_patch_hash}" ]]; then
  echo "existing OMTS changes do not match the pinned OpenShift patch series; restored the original source state" >&2
  exit 1
fi

prepared_rewrite_complete=true
trap - EXIT
rm -f "${prepared_diff}"

# Module overrides touch a shared section of MODULE.bazel; fail closed on accidental duplication.
for module_name in tinygltf glib grpc dumb-init gz-common aravis libzmq; do
  count="$(grep -F -c "module_name = \"${module_name}\"" "${source_dir}/MODULE.bazel" || true)"
  if [[ "${count}" != 1 ]]; then
    echo "expected one MODULE.bazel override for ${module_name}; found ${count}" >&2
    exit 1
  fi
done

validate_source_changes

echo "Prepared pinned OMTS source with the OpenShift compatibility patches."
