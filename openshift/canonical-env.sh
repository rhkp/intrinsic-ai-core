#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
# Source from workstation scripts. Python validates and shell-quotes every value.
canonical_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
canonical_settings="$(python3 "${canonical_root}/config_env.py" --config "${INTRINSIC_CONFIG:-${canonical_root}/config.local.json}")" || return 2
eval "${canonical_settings}"
unset canonical_settings
oc() { command oc --context "${OPENSHIFT_CONTEXT}" "$@"; }
