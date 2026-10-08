#!/usr/bin/env bash
set -eo pipefail

source /opt/ros/lyrical/setup.bash
export RMW_IMPLEMENTATION=rmw_zenoh_cpp
export ZENOH_CONFIG_OVERRIDE='mode="client";connect/endpoints=["tcp/zenoh-router.arhkp-intrinsic.svc.cluster.local:7447"]'

ros2 run rviz2 rviz2 -d "$HOME/.config/intrinsic-viewer/workcell.rviz" &
exec xfce4-session
