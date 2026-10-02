# AWS simulation demo: commands and observed results

[Deployment journal](README.md) · [Pod reference](PODS_README.md) · [OpenShift plan](OPENSHIFT_PLAN.md)

**Last run:** 2026-09-30. **Release:** `20260922.0`. **Cell:** `lab_bb_01`.
**Environment:** the existing AWS VM, with the solution deployed in simulation mode.

**Result:** arm motion, camera capture, pose estimation, and gripper/workpiece
handling were exercised. The complete cycle did **not** pass: two attempts stopped
on a workpiece/enclosure collision during unloading, including a clean-state retry.

## Upstream instructions

Use the pinned [Visualize the Solution tutorial](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/developer_resources/learn/tutorials/visualize_the_solution.md).
It registers workpiece geometry, then runs the OMTS application. The
[Jog the robot tutorial](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/developer_resources/learn/tutorials/jog_the_robot.md)
is a separate manual-control option; jogging was not needed for these demo attempts.

The commands below run **on the VM in an SSH session**. Supply connection details
privately using the existing workflow. They are intentionally absent from this file.

## 1. Preflight and viewer

Confirm the deployment is still in **simulation mode** before commanding motion.
Our preflight checked `simulated: true` in the deployed ChartAssignment values and
verified the Gazebo-backed UR adapter and Hand-E simulation driver images.
The application command's `--num_cycles=1` limits repetitions; it does not switch
a real deployment into simulation mode.

```sh
kubectl get pods -A
inctl asset instances list --address localhost:17080
inctl icon status --address localhost:17080 --instance_name icon
nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu --format=csv
systemctl is-active intrinsic-viewer
```

Expected: the relevant pods are ready, ICON is enabled, the GPU is available, and
the viewer is active. Do not interrupt another running executive operation.

From the Mac, use the existing private SSH viewer tunnel documented in the main
README, then open [RViz](http://localhost:6080/vnc.html?autoconnect=true&resize=scale).
RViz shows the software's workcell model; Gazebo runs the physics simulation.
Keep the viewer visible before starting the process. Pauses during pose estimation
and contact motions are normal; a static scene alone cannot distinguish a pause
from a failed process, so also check the command's outcome.

## 2. Register the workpiece geometry

These are the tutorial's registration parameters. The additional Bazel options
reuse this VM's documented temporary directory/distfile workaround and limit build
concurrency. They do not change the application or model configuration.

```sh
cd ~/intrinsic-omts

# Keep public source acquisition anonymous.
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_COUNT=2
export GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0=
export GIT_CONFIG_KEY_1=http.extraHeader GIT_CONFIG_VALUE_1=

TMPDIR="$HOME/tmp" bazel --batch run \
  //tools/pose_estimation:register_using_train_service \
  --jobs=4 --distdir="$HOME/intrinsic-distfiles" -- \
  --address=localhost:17080 \
  --scene_object_id=ai.intrinsic.raw_stock_2x3x5 \
  --pose_estimator_id=ai.intrinsic.raw_stock_2x3x5_estimator \
  --refinement_iters=6 \
  --confidence_threshold=0.6 \
  --visibility_threshold=0.6
```

The expected result is confirmation that the pose estimator was saved. This step
succeeded in our run. As the tutorial explains, it registers geometry; it is not
a neural-network training job. Reregister when required after a solution restart.

## 3. Run one cycle

```sh
cd ~/intrinsic-omts
TMPDIR="$HOME/tmp" bazel --batch run //src:omts_app \
  --jobs=4 --distdir="$HOME/intrinsic-distfiles" -- \
  --address=localhost:17080 \
  --config=configs/lab_bb_01/app_config.yaml \
  --num_cycles=1
```

The `lab_bb_01` configuration already defaults to one cycle; the explicit override
records our intent. Do not use zero/negative cycle counts here: the application's
flag documentation defines those as continuous operation.

After this exact executable has built successfully, the following avoids repeating
Bazel analysis for a retry on the same checkout:

```sh
cd ~/intrinsic-omts
bazel-bin/src/omts_app \
  --address=localhost:17080 \
  --config=configs/lab_bb_01/app_config.yaml \
  --num_cycles=1
```

Rebuild after changing source or dependencies. We did not set the executive's
`--simulation_mode` override; physical-versus-simulated resources were selected
when the solution was deployed, as prescribed by the upstream tutorial.

During our retries, the executive progressed through viewing the workpiece,
RGB-D capture, 6D pose estimation, opening the gripper, approaching and gripping
the workpiece, transferring/placing it, and beginning the unload sequence.
The cell configuration has no CNC-machine subsystem, so this does not demonstrate
a physical CNC handshake or actual machining.

## 4. What happened in our run

| Attempt | Outcome | Action taken |
| --- | --- | --- |
| Registration | Successful; pose estimator saved | Continued with the tutorial |
| First cycle | Failed on a Zenoh gripper command with no reply | Confirmed the router had no matching `gripper_cmd` queryable |
| Retry after driver recovery | Perception and arm/gripper actions executed; motion planning failed during unloading | Used the tutorial's world-reset troubleshooting step |
| Clean-state retry | Reproduced the same workpiece/enclosure collision | Stopped retries; reset the simulation and verified health |

### Gripper communication recovery

The driver pod was ready, but the router reported no matching command handler.
Recreated **only** the simulated Hand-E driver:

```sh
kubectl delete pod -n app-resources rs-hande-gripper-0
```

Wait until the replacement pod exists before running:

```sh
kubectl wait -n app-resources --for=condition=Ready \
  pod/rs-hande-gripper-0 --timeout=60s
```

A wait issued before recreation can return `NotFound`; this happened during our
recovery. After replacement, readiness succeeded and router logs confirmed the
queryable registered. Subsequent gripper commands executed. The original loss of
registration has not been fully explained; pod readiness alone did not catch it.
This targeted restart was an observed recovery, not a required step for every run.

### Collision during unloading

The later failure occurred while planning a Cartesian motion after the workpiece
was reattached to the robot during the unload sequence. The reported collision was:

- Workpiece: `raw_stock_2x3x5.base_link`
- Environment: `enclosure.base_link`

The upstream tutorial describes a related limitation involving the simulated vise
not retaining the part. Our result is consistent with that class of problem, but
we have not established the exact cause. Resetting and retrying did not resolve it.
No collision exemptions, geometry changes, or motion-code changes were made.

### Reset after a failed attempt

Only after execution has stopped, and with the deployment verified as simulation:

```sh
inctl world reset --address localhost:17080
inctl icon status --address localhost:17080 --instance_name icon
```

In our run, the reset left ICON disabled. To prepare the simulated controller for
another attempt, we used:

```sh
inctl icon enable --address localhost:17080 --instance_name icon
```

A world reset changes the simulated scene; it is not a substitute for cancelling
an active process. Closing a terminal or interrupting a client does not necessarily
cancel the server-side behavior tree. The SDK provides `solution.executive.cancel()`
in an initialized Intrinsic SDK session; verify that execution has stopped before
resetting or retrying.

## 5. Final state and evidence handling

- All demo application invocations exited; none was left running.
- The simulation was reset to its initial world state.
- ICON reported enabled and all 51 pods were ready after cleanup.
- The viewer remained available through the existing private SSH tunnel.
- Registration and demo logs stayed under `~/intrinsic-install-logs/` on the VM,
  with permissions `600`; raw logs were not copied into this repository.
- No source, motion limits, collision rules, AWS inbound rules, or credentials
  were changed to make the demo run.

When capturing future logs, use `umask 077` and a private file on the VM. Share
only reviewed summaries; runtime logs can contain addresses and internal details.

## 6. What to carry into the OpenShift evaluation

The working checks are now stronger than pod readiness: simulated arm movement,
camera/pose-estimation progression, and gripper commands have been exercised.
Record those as individual parity checks, plus the gripper's functional readiness.

A **complete successful cycle remains unproven on the AWS baseline**. Investigate
the unload collision before using end-to-end completion as an OpenShift comparison.
The next diagnostic should compare the simulated part pose, world-model pose,
grasp state, and fixture support around placement/unloading, while preserving
collision checking. Do not count reproduction of the existing failure as a passing
OpenShift acceptance test.
