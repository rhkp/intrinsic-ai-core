#!/bin/sh
set -eu

uid="$(id -u)"
gid="$(id -g)"
case "${uid}:${gid}" in
  *[!0-9:]*|:*)
    echo "containerd requires numeric runtime UID and GID" >&2
    exit 1
    ;;
esac

mkdir -p /run/containerd /tmp/containerd-root /tmp/containerd-state
sed -e "s/__UID__/${uid}/g" -e "s/__GID__/${gid}/g" \
  /etc/containerd/containerd.toml.in > /tmp/containerd.toml
exec /usr/bin/containerd --config /tmp/containerd.toml
