# This project was developed with assistance from AI tools.
"""Offline contracts for E5 evidence and the bounded application patch."""

import copy
import hashlib
import io
import json
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import patch

import apply_demo_clearance as apply
import collision_probe as probe
import collision_trace as trace


def snapshot():
    pose = {"translation_m": [0.25, 0.25, 0.8], "quaternion_xyzw": [1.0, 0.0, 0.0, 0.0]}
    return {
        "schema_version": 1,
        "operation_done": True,
        "cycle_completed": False,
        "operation_state": "FAILED",
        "stages": {
            "pick": "SUCCEEDED",
            "load": "FAILED",
            "machining": "ACCEPTED",
            "unload": "ACCEPTED",
            "return": "ACCEPTED",
        },
        "tasks": {"load_exit": "FAILED"},
        "failure": probe.collision_facts(
            "gripper.gripper_finger2 raw_stock_2x3x5.base_link; planning in segment 0"
        ),
        "contact_commands": {
            "load_contact": {
                "direction_tool": [0.0, 0.0, 1.0],
                "force_newtons": 8.0,
                "timeout_seconds": 20.0,
            }
        },
        "collision_geometry_sha256": {"gripper": ["a" * 64], "workpiece": ["b" * 64]},
        "belief_world": {
            "robot_joints_rad": [0.0] * 6,
            "gripper_joints_m": [0.0, 0.0],
            "tool": copy.deepcopy(pose),
            "workpiece": copy.deepcopy(pose),
            "workpiece_attached_to_gripper": False,
            "frames": {name: copy.deepcopy(pose) for name in probe.FRAMES},
        },
        "simulator_joint_feedback_observed": False,
        "contact_points_observed": False,
    }


def report(value):
    return {
        "schema_version": 1,
        "observed_at": "2026-10-20T00:00:00+00:00",
        "image_digests": {
            label: "sha256:" + "a" * 64 for label in trace.IMAGE_WORKLOADS
        },
        "snapshot": value,
    }


class TraceTest(unittest.TestCase):
    def test_known_fingers_have_same_failure_category(self):
        for finger in ("1", "2"):
            value = probe.collision_facts(
                "gripper.gripper_finger"
                + finger
                + " raw_stock_2x3x5.base_link; segment 0"
            )
            self.assertTrue(value["known_pair"])
            self.assertEqual(value["pair"][0], "gripper.gripper_finger" + finger)

    def test_status_prose_and_endpoints_are_never_copied(self):
        value = probe.collision_facts(
            "Bearer fake-secret https://private.example.invalid Configuration [1,2,3,4,5,6] (in rad); gripper.gripper_finger2 raw_stock_2x3x5.base_link; segment 0"
        )
        text = json.dumps(value)
        self.assertNotIn("fake-secret", text)
        self.assertNotIn("private.example", text)
        self.assertEqual(value["invalid_robot_joints_rad"], [1, 2, 3, 4, 5, 6])

    def test_unknown_collision_is_not_reported_as_known(self):
        self.assertEqual(
            probe.collision_facts("another collision")["category"], "other"
        )

    def test_invalid_joint_vector_is_discarded(self):
        for text in ("1,2", "nan,2,3,4,5,6", "bad,2,3,4,5,6"):
            self.assertIsNone(
                probe.collision_facts("Configuration [" + text + "] (in rad)")[
                    "invalid_robot_joints_rad"
                ]
            )

    def test_valid_report_and_marked_output(self):
        value = snapshot()
        self.assertEqual(trace.validate(value), value)
        self.assertEqual(
            trace.parse("ignored SDK output\n" + probe.MARKER + json.dumps(value)),
            value,
        )

    def test_multiple_markers_fail_closed(self):
        marked = probe.MARKER + json.dumps(snapshot())
        with self.assertRaises(ValueError):
            trace.parse(marked + "\n" + marked)

    def test_unknown_fields_cannot_export_strings(self):
        for place in ("root", "world", "pose"):
            value = snapshot()
            target = (
                value
                if place == "root"
                else value["belief_world"]
                if place == "world"
                else value["belief_world"]["tool"]
            )
            target["token"] = "fake-secret"
            with self.assertRaises(ValueError):
                trace.validate(value)

    def test_nonfinite_and_boolean_coordinates_are_rejected(self):
        for coordinate in (float("nan"), float("inf"), True, "fake-secret"):
            value = snapshot()
            value["belief_world"]["robot_joints_rad"][0] = coordinate
            with self.assertRaises(ValueError):
                trace.validate(value)

    def test_inconsistent_collision_pair_is_rejected(self):
        value = snapshot()
        value["failure"]["pair"][0] = "arbitrary-private-name"
        with self.assertRaises(ValueError):
            trace.validate(value)

    def test_cycle_requires_all_stages_and_terminal_success(self):
        value = snapshot()
        value["cycle_completed"] = True
        with self.assertRaises(ValueError):
            trace.validate(value)
        value["operation_state"] = "SUCCEEDED"
        value["stages"] = dict.fromkeys(probe.STAGES.values(), "SUCCEEDED")
        value["failure"] = probe.collision_facts("")
        self.assertTrue(trace.validate(value)["cycle_completed"])
        value["stages"].pop("return")
        with self.assertRaises(ValueError):
            trace.validate(value)

    def test_completed_stages_cannot_hide_terminal_failure(self):
        value = snapshot()
        value["cycle_completed"] = True
        value["stages"] = dict.fromkeys(probe.STAGES.values(), "SUCCEEDED")
        value["failure"] = probe.collision_facts("")
        with self.assertRaises(ValueError):
            trace.validate(value)

    def test_offline_comparison_has_numeric_deltas_and_no_cluster_calls(self):
        before = report(snapshot())
        after = copy.deepcopy(before)
        after["snapshot"]["belief_world"]["tool"]["translation_m"][2] += 0.08
        with tempfile.TemporaryDirectory() as directory:
            paths = [Path(directory) / name for name in ("before.json", "after.json")]
            for path, value in zip(paths, (before, after)):
                path.write_text(json.dumps(value))
            output = io.StringIO()
            with (
                patch.object(
                    trace,
                    "collect_current",
                    side_effect=AssertionError("cluster access"),
                ),
                redirect_stdout(output),
            ):
                self.assertEqual(trace.main(["--compare", *map(str, paths)]), 0)
            result = json.loads(output.getvalue())
            self.assertAlmostEqual(result["tool_translation_delta_m"][2], 0.08)
            self.assertTrue(result["same_planner_image"])

    def test_existing_output_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text("keep")
            with (
                patch.object(trace, "collect_current", return_value=report(snapshot())),
                redirect_stdout(io.StringIO()),
            ):
                self.assertEqual(trace.main(["--output", str(path)]), 1)
            self.assertEqual(path.read_text(), "keep")

    def test_private_report_permissions(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            with patch.object(
                trace, "collect_current", return_value=report(snapshot())
            ):
                self.assertEqual(trace.main(["--output", str(path)]), 0)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_query_errors_do_not_escape(self):
        output = io.StringIO()
        with (
            patch.object(
                trace, "collect_current", side_effect=ValueError("fake-secret")
            ),
            redirect_stdout(output),
        ):
            self.assertEqual(trace.main([]), 1)
        self.assertNotIn("fake-secret", output.getvalue())


class PatchTest(unittest.TestCase):
    def test_patch_adds_only_two_release_lifts(self):
        text = (apply.ROOT / "patches/omts-release-clearance.patch").read_text()
        self.assertEqual(text.count("+      create_relative_retract_task("), 2)
        self.assertEqual(
            text.count("+        distance_meters=config.cycle.approach_offset_z,"), 2
        )
        self.assertNotIn("disable_collision_checking=True", text)
        self.assertIn(
            '+        task_name="Lift Clear of Released Part (-Z Tool)"', text
        )
        self.assertIn(
            '+        task_name="Lift Clear of Returned Part (-Z Tool)"', text
        )

    def test_both_build_paths_package_patch(self):
        for name in (
            "openshift/deployment/omts-runner/build-tool-image.sh",
            "openshift/deployment/omts-workspace/build-workspace-image.sh",
            "openshift/deployment/omts-runner/Containerfile.toolchain",
            "openshift/deployment/omts-runner/prepare-omts-source.sh",
        ):
            self.assertIn(
                "omts-release-clearance.patch", (apply.ROOT / name).read_text()
            )

    def test_remote_check_never_applies_and_rejects_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / "app.py"
            file.write_text("original\n")
            digest = hashlib.sha256(file.read_bytes()).hexdigest()
            with patch.object(apply, "SOURCE_DIGESTS", {"app.py": (digest, "b" * 64)}):
                code = apply.remote_script("unused patch", False).replace(
                    "/workspace/source/intrinsic-omts", directory
                )
                output = io.StringIO()
                with (
                    redirect_stdout(output),
                    patch("subprocess.run", side_effect=AssertionError("mutation")),
                ):
                    exec(compile(code, "remote_check", "exec"), {})
                self.assertFalse(json.loads(output.getvalue())["patch_applied"])
                file.write_text("unexpected")
                with self.assertRaises(RuntimeError):
                    exec(compile(code, "remote_check", "exec"), {})

    def test_cli_does_not_apply_without_flag(self):
        with (
            patch.object(
                apply,
                "run",
                return_value={"patch_applied": True, "source_identity_verified": True},
            ) as run,
            redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(apply.main([]), 0)
        self.assertFalse(run.call_args.kwargs["apply"])


if __name__ == "__main__":
    unittest.main()
