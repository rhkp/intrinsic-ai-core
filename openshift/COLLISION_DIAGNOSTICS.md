# Demo collision diagnosis and correction

> [!NOTE]
> This project was developed with assistance from AI tools.

## Verified result — 2026-10-20

Two consecutive, independently reset runs completed the configured simulated
cycle: picking, loading, the configured machining stage, unloading, and returning
to the view pose. Both observed FoundationPose completion in RHOAI. The repeat
passed all **52 acceptance gates**; an independent SDK snapshot confirmed terminal
`SUCCEEDED` and all five subtrees `SUCCEEDED`. This cell has no CNC machine
adapter; this establishes the existing simulated demo, not physical machining.

All 10 PVCs remained Bound with original identities/specifications. Workload
specifications and application Pod identities were unchanged. Existing
executables/caches were reused without image builds, controller changes,
resource replacement, or application reinstall.

## What actually failed

Pre-change executive metadata identified **Load Machine → Retract Arm to Machine
Entry** as the failing task. Picking, perception, compliant pickup, grasping,
placement, and detachment had already succeeded. The planner rejected segment 0:
the lateral retreat intersected `gripper.gripper_finger2` /
`raw_stock_2x3x5.base_link`.

Returning to an absolute approach frame after force-controlled placement did
not establish sufficient vertical clearance from the released stock. Raising
the vise approach frame by 6 cm shifted the release position by the same amount
and reproduced the failure. That scene edit was reverted.

An explicit vertical lift after release cleared the loading failure. The next
run completed loading, machining, and unloading, then exposed the same omission
at **Return to Infeed → Return to View Pose**, involving `gripper.gripper_finger1`.
The same release-clearance correction there allowed two complete cycles.

The [application patch](../patches/omts-release-clearance.patch) adds one relative
tool −Z lift at each release exit, using `cycle.approach_offset_z` (0.08 m in the
approved simulated cell). Only the gripper/workpiece pair is excluded during
these contact-escape segments, following the existing release-retreat convention.
Environment collision checks remain active; subsequent transit uses ordinary
collision checking. Scene geometry and force thresholds are unchanged.

Closed gripper joints were also observed in the belief world after an open
command. Actual simulator jaw feedback was not collected, so a synchronization
defect is **unverified**; no jaw-state change was made.

## Reproduce using the existing workspace

Run from the repository root with the approved ignored canonical configuration:

```sh
mkdir -p openshift/.generated

# Read-only source check; exit 1 means the fix is absent or unverified.
python3 openshift/apply_demo_clearance.py

# Read-only snapshot of the last operation and current belief world.
python3 openshift/collision_trace.py \
  --output openshift/.generated/collision-before.json

# Explicitly patch only the two pinned application files, if needed.
python3 openshift/apply_demo_clearance.py --apply

# Explicit simulated reset and one bounded full cycle.
python3 openshift/validate_demo_gates.py \
  --through-phase 6 --allow-app-actions --json

python3 openshift/collision_trace.py \
  --output openshift/.generated/collision-after.json

# Offline comparison; no cluster calls.
python3 openshift/collision_trace.py --compare \
  openshift/.generated/collision-before.json \
  openshift/.generated/collision-after.json
```

Output files must be new: the exporter refuses overwrites and creates files
with mode 0600. A successful export means evidence was collected; inspect
`snapshot.cycle_completed` for the operation's result. It does not start a run.

[apply_demo_clearance.py](apply_demo_clearance.py) defaults to checking only.
`--apply` requires a Ready workspace, exact Ready simulation workloads, an idle
executive, and exact original/patched source SHA-256 identities. Unexpected or
partially patched source fails before writing. Applying an already verified
patch is a no-op. Other workspace source and caches are preserved.

New workspace/toolchain builds package the patch, and
[prepare-omts-source.sh](deployment/omts-runner/prepare-omts-source.sh) includes
both files in its strict reproducible patch series. The running tools image was
not rebuilt. Its older source-preparation script does not know this patch;
use the helper for the existing cache and build the updated tools image before
invoking source preparation there. A previously prepared checkout must first
receive the exact patch to match the updated series. Unrelated edits remain rejected.

## Evidence and comparison contract

[collision_trace.py](collision_trace.py) reuses the cached SDK and emits:

- fixed stage/task labels and executive completion state;
- collision pair, planner segment, and reported invalid robot joint vector;
- the four configured `move_to_contact` direction/force/timeout requests;
- numeric robot/gripper belief state, tool/workpiece poses and relevant frames;
- collision-geometry hashes and immutable planner/runtime image digests.

Reports exclude status prose, raw logs, resource identifiers, endpoints,
identities, and credentials. Unknown/malformed fields fail closed. The SDK
[collect(solution)](collision_probe.py) accepts a connected deployment on either
topology. AWS evidence can use the same snapshot structure with verified runtime
image digests; `--compare` reads both reports offline. No fresh AWS run was performed.

The pre-change comparison replays SDK metadata and scene state captured before
editing, avoiding another failed motion run. Before/after planner and collision
geometry hashes match. Static machine frames are unchanged; perception updated
the dynamic grasp/pre-grasp frames normally.

`simulator_joint_feedback_observed` and `contact_points_observed` are explicitly
false: world state/model identity do not establish measured jaw feedback or
physical contact points. Reports contain configured force-skill requests and
task outcomes, not a sampled force/command telemetry stream.

Sanitized run, trace, comparison, and preservation reports remain locally under
ignored `openshift/.generated/e5/`. Full diagnostic inputs remain private. Offline
regressions cover both collision fingers, false success, source drift, packaging,
malformed evidence, safe errors, numeric deltas, permissions, and read-only defaults.
