#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
env_file="$script_dir/.env"
project="${OPENSHIFT_PROJECT:-arhkp-intrinsic}"
secret_name="${OPENSHIFT_SECRET_NAME:-intrinsic-runtime-secrets}"

if [[ ! -f "$env_file" ]]; then
  echo "Missing $env_file; copy .env.sample to .env and fill it locally." >&2
  exit 1
fi
if ! git -C "$repo_root" check-ignore -q -- openshift/.env; then
  echo "openshift/.env is not ignored by Git; refusing to apply it." >&2
  exit 1
fi
chmod 600 "$env_file"

# Keep secret contents in the pipe; do not write generated Secret YAML to disk.
oc -n "$project" create secret generic "$secret_name" \
  --from-env-file="$env_file" \
  --dry-run=client -o yaml \
  | oc -n "$project" apply -f - >/dev/null
printf 'Applied Secret %s in project %s. Values were not printed.\n' "$secret_name" "$project"
