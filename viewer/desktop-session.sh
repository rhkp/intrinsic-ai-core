#!/usr/bin/env bash
set -eo pipefail
umask 077

install -d -m 700 "$HOME/.rviz2"
if [[ ! -e "$HOME/.rviz2/persistent_settings" ]]; then
  touch "$HOME/.rviz2/persistent_settings"
fi

source /opt/ros/lyrical/setup.bash
export RMW_IMPLEMENTATION=rmw_zenoh_cpp
export ZENOH_CONFIG_OVERRIDE='mode="client";connect/endpoints=["tcp/localhost:7447"]'

choom -n 1000 -- ros2 run rviz2 rviz2 \
  -d "$HOME/.config/intrinsic-viewer/workcell.rviz" \
  > "$HOME/intrinsic-install-logs/viewer-rviz.log" 2>&1 &

exec xfce4-session
