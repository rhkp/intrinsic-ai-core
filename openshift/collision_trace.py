# This project was developed with assistance from AI tools.
"""Export read-only collision evidence or compare two sanitized snapshots."""

from __future__ import annotations

import argparse
import json
import math
import os
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path

import collision_probe as probe
from common import PilotError
from render_config import DEFAULT_CONFIG_PATH
from runtime_config import load_runtime_target
from validate_demo_gates import Reader

ROOT = Path(__file__).resolve().parent
IMAGE_WORKLOADS = {
    "planner": "rs-motion-planner-service",
    "gazebo": "rs-gazebo-simulator",
    "icon": "rs-icon",
    "wrapper": "rs-inference-service",
}


def exact(value, keys):
    if not isinstance(value, dict) or set(value) != set(keys):
        raise ValueError("invalid_trace_structure")


def vector(value, length):
    if (
        not isinstance(value, list)
        or len(value) != length
        or any(
            type(number) not in (int, float) or not math.isfinite(number)
            for number in value
        )
    ):
        raise ValueError("invalid_trace_vector")


def validate(snapshot):
    """Reject arbitrary strings, unknown fields, nonfinite data and fake success."""
    exact(
        snapshot,
        {
            "schema_version",
            "operation_done",
            "cycle_completed",
            "operation_state",
            "stages",
            "tasks",
            "failure",
            "contact_commands",
            "collision_geometry_sha256",
            "belief_world",
            "simulator_joint_feedback_observed",
            "contact_points_observed",
        },
    )
    if type(snapshot["schema_version"]) is not int or snapshot["schema_version"] != 1:
        raise ValueError("unsupported_trace_schema")
    for key in (
        "operation_done",
        "cycle_completed",
        "simulator_joint_feedback_observed",
        "contact_points_observed",
    ):
        if type(snapshot[key]) is not bool:
            raise ValueError("invalid_trace_boolean")
    if snapshot["operation_state"] not in probe.STATES:
        raise ValueError("invalid_operation_state")
    for key, allowed in (("stages", probe.STAGES), ("tasks", probe.TASKS)):
        values = snapshot[key]
        if (
            not isinstance(values, dict)
            or not set(values).issubset(set(allowed.values()))
            or any(value not in probe.STATES for value in values.values())
        ):
            raise ValueError("invalid_task_state")
    failure = snapshot["failure"]
    exact(
        failure,
        {
            "known_pair",
            "category",
            "planner_segment",
            "invalid_robot_joints_rad",
            "pair",
        },
    )
    if type(failure["known_pair"]) is not bool or failure["category"] not in {
        "none",
        "collision",
        "other",
    }:
        raise ValueError("invalid_failure")
    allowed_pairs = (
        [
            ["gripper.gripper_finger1", "raw_stock_2x3x5.base_link"],
            ["gripper.gripper_finger2", "raw_stock_2x3x5.base_link"],
        ]
        if failure["known_pair"]
        else [[]]
    )
    if failure["pair"] not in allowed_pairs or failure["known_pair"] != (
        failure["category"] == "collision"
    ):
        raise ValueError("inconsistent_collision")
    segment = failure["planner_segment"]
    if segment is not None and (type(segment) is not int or not 0 <= segment <= 100):
        raise ValueError("invalid_segment")
    if failure["invalid_robot_joints_rad"] is not None:
        vector(failure["invalid_robot_joints_rad"], 6)
    contacts = snapshot["contact_commands"]
    geometry = snapshot["collision_geometry_sha256"]
    exact(geometry, {"gripper", "workpiece"})
    for digests in geometry.values():
        if (
            not isinstance(digests, list)
            or not digests
            or any(
                not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest)
                for digest in digests
            )
        ):
            raise ValueError("collision_geometry_unverified")
    if not isinstance(contacts, dict) or not set(contacts).issubset(
        {"pick_contact", "load_contact", "unload_contact", "return_contact"}
    ):
        raise ValueError("invalid_contact_commands")
    for command in contacts.values():
        exact(command, {"direction_tool", "force_newtons", "timeout_seconds"})
        vector(command["direction_tool"], 3)
        for key in ("force_newtons", "timeout_seconds"):
            if type(command[key]) not in (int, float) or not 0 < command[key] <= 600:
                raise ValueError("invalid_contact_parameter")
    world = snapshot["belief_world"]
    exact(
        world,
        {
            "robot_joints_rad",
            "gripper_joints_m",
            "tool",
            "workpiece",
            "workpiece_attached_to_gripper",
            "frames",
        },
    )
    vector(world["robot_joints_rad"], 6)
    vector(world["gripper_joints_m"], 2)
    if type(world["workpiece_attached_to_gripper"]) is not bool:
        raise ValueError("invalid_attachment")
    exact(world["frames"], probe.FRAMES)
    for pose in [world["tool"], world["workpiece"], *world["frames"].values()]:
        exact(pose, {"translation_m", "quaternion_xyzw"})
        vector(pose["translation_m"], 3)
        vector(pose["quaternion_xyzw"], 4)
    complete = (
        snapshot["operation_done"]
        and snapshot["operation_state"] == "SUCCEEDED"
        and set(snapshot["stages"]) == set(probe.STAGES.values())
        and all(value == "SUCCEEDED" for value in snapshot["stages"].values())
    )
    if snapshot["cycle_completed"] != complete or (
        complete and failure["category"] != "none"
    ):
        raise ValueError("inconsistent_cycle_completion")
    return snapshot


def parse(text):
    lines = [
        line[len(probe.MARKER) :]
        for line in text.splitlines()
        if line.startswith(probe.MARKER)
    ]
    if len(lines) != 1 or len(lines[0]) > 64000:
        raise ValueError("trace_response_invalid")
    return validate(json.loads(lines[0]))


def collect_current(config_path):
    target = load_runtime_target(config_path)
    target.connect()
    source = (ROOT / "collision_probe.py").read_text()
    gateway = target.document["target"]["networking"]["ingress_address"]
    action = (
        source
        + "\n"
        + f"""
from intrinsic.solutions import deployments
import json
result = collect(deployments.connect(address={gateway!r}))
print(MARKER + json.dumps(result, allow_nan=False))
"""
    )
    bootstrap = f"""
from pathlib import Path
import os, sys
base = Path('/workspace/source/intrinsic-omts/bazel-bin/src')
stage = base / '_omts_app_stage2_bootstrap.py'
os.environ['RUNFILES_DIR'] = str(base / 'omts_app.runfiles')
sys.argv = [str(stage)]
namespace = {{'__file__': str(stage), '__name__': 'collision_bootstrap'}}
parts = stage.read_text().rsplit('\\nmain()', 1)
if len(parts) != 2: raise RuntimeError('unsupported_cached_launcher')
exec(compile(parts[0], str(stage), 'exec'), namespace)
namespace['_run_py_path'] = lambda *a, **kw: exec({action!r}, {{'__name__': 'collision_probe'}})
namespace['main']()
"""
    launcher = (
        (ROOT / "demo_probe.py").read_text()
        + f"""
python = next((WORKSPACE / 'bazel-bin/src/omts_app.runfiles').glob('*python_3_11*/bin/python3'))
result = bounded_process([str(python), '-'], timeout=40, input_text={bootstrap!r})
sys.stdout.write(result.stdout)
sys.exit(result.returncode)
"""
    )
    result = subprocess.run(
        [
            "oc",
            "--context",
            target.document["local"]["context"],
            "exec",
            "-i",
            "deployment/omts-build-workspace",
            "-n",
            target.namespace,
            "-c",
            "workspace",
            "--",
            "python3",
            "-",
        ],
        input=launcher,
        text=True,
        capture_output=True,
        timeout=55,
    )
    if result.returncode:
        raise ValueError("collision_probe_unavailable")
    snapshot = parse(result.stdout)
    inventory = Reader(target).inventory()
    versions = {}
    for label, name in IMAGE_WORKLOADS.items():
        workload = next(
            item
            for item in inventory
            if item["kind"] == "StatefulSet" and item["metadata"]["name"] == name
        )
        container = next(
            c
            for c in workload["spec"]["template"]["spec"]["containers"]
            if c["name"] == name
        )
        digest = container["image"].partition("@")[2]
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError("unversioned_runtime_image")
        versions[label] = digest
    return {
        "schema_version": 1,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "image_digests": versions,
        "snapshot": snapshot,
    }


def load_report(path):
    value = json.loads(path.read_text())
    exact(value, {"schema_version", "observed_at", "image_digests", "snapshot"})
    if value["schema_version"] != 1:
        raise ValueError("unsupported_report_schema")
    datetime.fromisoformat(value["observed_at"])
    exact(value["image_digests"], IMAGE_WORKLOADS)
    for digest in value["image_digests"].values():
        if not isinstance(digest, str) or not re.fullmatch(
            r"sha256:[0-9a-f]{64}", digest
        ):
            raise ValueError("invalid_image_digest")
    validate(value["snapshot"])
    return value


def compare(before, after):
    left, right = before["snapshot"], after["snapshot"]
    return {
        "schema_version": 1,
        "before_cycle_completed": left["cycle_completed"],
        "after_cycle_completed": right["cycle_completed"],
        "same_planner_image": before["image_digests"]["planner"]
        == after["image_digests"]["planner"],
        "same_collision_geometry": left["collision_geometry_sha256"]
        == right["collision_geometry_sha256"],
        "changed_stages": [
            key
            for key in probe.STAGES.values()
            if left["stages"].get(key) != right["stages"].get(key)
        ],
        "changed_frames": [
            key
            for key in probe.FRAMES
            if left["belief_world"]["frames"][key]
            != right["belief_world"]["frames"][key]
        ],
        "tool_translation_delta_m": [
            b - a
            for a, b in zip(
                left["belief_world"]["tool"]["translation_m"],
                right["belief_world"]["tool"]["translation_m"],
            )
        ],
        "workpiece_translation_delta_m": [
            b - a
            for a, b in zip(
                left["belief_world"]["workpiece"]["translation_m"],
                right["belief_world"]["workpiece"]["translation_m"],
            )
        ],
        "gripper_joint_delta_m": [
            b - a
            for a, b in zip(
                left["belief_world"]["gripper_joints_m"],
                right["belief_world"]["gripper_joints_m"],
            )
        ],
        "before_failure": left["failure"],
        "after_failure": right["failure"],
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument("--compare", type=Path, nargs=2, metavar=("BEFORE", "AFTER"))
    parser.add_argument(
        "--output", type=Path, help="Create a private JSON report; refuse overwrite"
    )
    args = parser.parse_args(argv)
    try:
        report = (
            compare(*(load_report(path) for path in args.compare))
            if args.compare
            else collect_current(args.config)
        )
        text = json.dumps(report, indent=2, sort_keys=True, allow_nan=False) + "\n"
        if args.output:
            with os.fdopen(
                os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w"
            ) as file:
                file.write(text)
        else:
            print(text, end="")
        return 0
    except (
        PilotError,
        OSError,
        ValueError,
        TypeError,
        KeyError,
        StopIteration,
        subprocess.TimeoutExpired,
    ):
        print(
            json.dumps(
                {
                    "schema_version": 1,
                    "error": "collision_trace_unavailable",
                    "raw_details_suppressed": True,
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
