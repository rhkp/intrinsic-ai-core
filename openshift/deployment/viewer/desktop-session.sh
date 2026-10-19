#!/usr/bin/env bash
# This project was developed with assistance from AI tools.
set -eo pipefail

source /opt/ros/lyrical/setup.bash
export RMW_IMPLEMENTATION=rmw_zenoh_cpp
: "${ZENOH_CONFIG_OVERRIDE:?Canonical Zenoh runtime configuration is required}"
export ZENOH_CONFIG_OVERRIDE

ros2 run rviz2 rviz2 -d "$HOME/.config/intrinsic-viewer/workcell.rviz" &
exec xfce4-session
