# Workcell Configurations (`configs/`)
> [!NOTE]
> This project was developed with assistance from AI tools.

> Deployment examples use generic placeholders for cluster context, project namespace, registry, storage class, and GPU settings. Replace them with approved environment values before use; scripts may read `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, and `OPENSHIFT_GPU_TAINT_KEY` from the environment.


Per-cell application YAML configurations, SBL `ObjectWorldUpdates` (`.pbtxt`),
and hardware/service configuration manifests (`.textproto`).

## Directory Structure

| Directory | Cell / Scope | Description |
| :--- | :--- | :--- |
| [`common/`](common/) | Shared across cells | Asset manifests and service configs shared by all cells (`flowstate_ros_bridge`, `hande_gripper_service`, `orbbec_gemini_driver`, `pose_estimator_config`). |
| [`omts/`](omts/) | Production OMTS Cell | UR5e arm, Robotiq Hand-E gripper, Orbbec Gemini 335Le camera, CNC enclosure, and Schunk EGP 64 pneumatic vise. Selected by default (`--//:setup=omts`). |
| [`your-workcell/`](your-workcell/) | Example cell | Example simulated-workcell configuration. Replace it with an approved local workcell configuration before use. |
| [`kr_10/`](kr_10/) | KUKA KR10 (Stub) | Placeholder ICON config for KUKA KR10; not wired into `//:omts_solution`. |

## Configuration File Types

* **`app_config.yaml`**: Runtime application configuration parsed by
  [`src/core/config.py:load_app_config`](../src/core/config.py). Defines robot
  part/frame names, gripper parameters, optional CNC machine DIO pin/joint
  mappings, vision estimator settings, scene frame names, and cycle forces/timeouts.
* **`*.updates.pbtxt`**: `ObjectWorldUpdates` protos defining kinematic tree
  attachments (e.g. mounting `gripper` and `orbbec_camera` to `ur_module/flange`),
  robot base alignment, fixture poses, and named scene frames (`view`,
  `pre_grasp`, `grasp`, `machine_approach`, `vise_pre_place`, `vise_place`).
  Can be applied live via `bazel run //tools/world:apply_scene_updates`.
* **`*.textproto`**: Service configurations for ICON realtime control
  (`icon_config.textproto`), UR hardware module (`ur_module_config.textproto`),
  and Orbbec camera driver (`gemini_device_config.textproto`).
