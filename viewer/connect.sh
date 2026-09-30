#!/usr/bin/env bash
set -euo pipefail

if (($# != 3)); then
  printf 'Usage: %s <VM_HOST> <PREVIOUS_TRUSTED_HOST> <SSH_KEY_PATH>\n' "$0" >&2
  exit 2
fi

# Keep this terminal open. Ctrl+C closes the tunnel.
exec ssh -a -N -T -i "$3" \
  -o BatchMode=yes -o StrictHostKeyChecking=yes -o "HostKeyAlias=$2" \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
  -L localhost:6080:/run/intrinsic-viewer/web.sock "ubuntu@$1"
