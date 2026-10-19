# This project was developed with assistance from AI tools.
"""Offline negative-path and action-isolation tests for E3 acceptance."""

import copy
import hashlib
import io
import json
import subprocess
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import Mock, patch

import demo_probe
import validate_demo_gates as gates
from common import PilotError
from runtime_config import RuntimeTarget
from test_render_config import realistic_config, realistic_lock


def metadata(name, uid=None, **fields):
    return {"name": name, "uid": uid or name + "-uid", "generation": 2, **fields}


def owner(kind, name):
    return {"kind": kind, "name": name, "uid": name + "-uid", "controller": True}


def fixture(kind="StatefulSet", name="demo"):
    workload = {
        "kind": kind,
        "metadata": metadata(name),
        "spec": {"replicas": 1},
        "status": {
            "replicas": 1,
            "updatedReplicas": 1,
            "readyReplicas": 1,
            "availableReplicas": 1,
            "observedGeneration": 2,
            "currentRevision": "current",
            "updateRevision": "current",
        },
    }
    pod = {
        "kind": "Pod",
        "metadata": metadata(
            "demo-0", ownerReferences=[owner(kind, name)], labels={"app": "demo"}
        ),
        "spec": {
            "containers": [
                {
                    "name": "demo",
                    "image": "example.invalid/demo",
                    "ports": [{"name": "grpc", "containerPort": 8080}],
                }
            ]
        },
        "status": {
            "phase": "Running",
            "podIP": "example-pod-address",
            "conditions": [{"type": "Ready", "status": "True"}],
            "containerStatuses": [
                {
                    "name": "demo",
                    "ready": True,
                    "state": {"running": {"startedAt": "2026-01-01T00:00:00Z"}},
                }
            ],
        },
    }
    items = [workload, pod]
    if kind == "Deployment":
        rs = {
            "kind": "ReplicaSet",
            "metadata": metadata("demo-rs", ownerReferences=[owner(kind, name)]),
        }
        pod["metadata"]["ownerReferences"] = [owner("ReplicaSet", "demo-rs")]
        items.append(rs)
    service = {
        "kind": "Service",
        "metadata": metadata("demo"),
        "spec": {
            "selector": {"app": "demo"},
            "ports": [{"port": 80, "targetPort": "grpc"}],
        },
    }
    endpoint = {
        "kind": "Endpoints",
        "metadata": metadata("demo"),
        "subsets": [
            {
                "ports": [{"port": 8080}],
                "addresses": [
                    {
                        "ip": "example-pod-address",
                        "targetRef": {
                            "kind": "Pod",
                            "name": "demo-0",
                            "uid": "demo-0-uid",
                        },
                    }
                ],
            }
        ],
    }
    items.extend([service, endpoint])
    ca = {
        "kind": "ChartAssignment",
        "metadata": metadata("demo"),
        "status": {
            "phase": "Ready",
            "observedGeneration": 2,
            "conditions": [
                {"type": "Ready", "status": "True"},
                {"type": "Settled", "status": "True"},
            ],
        },
    }
    resource_set = {
        "kind": "ResourceSet",
        "metadata": metadata("demo.v2", labels={"name": "demo"}),
        "status": {
            "phase": "Settled",
            "finishedAt": "2026-01-01T00:00:00Z",
            "applied": [
                {
                    "kind": kind,
                    "items": [{"name": name, "uid": name + "-uid", "generation": 2}],
                }
            ],
        },
    }
    return items + [ca, resource_set]


class InventoryTest(unittest.TestCase):
    def test_ready_requires_every_container(self):
        pod = fixture()[1]
        self.assertTrue(gates.pod_ready(pod))
        pod["status"]["containerStatuses"][0]["ready"] = False
        self.assertFalse(gates.pod_ready(pod))
        pod["status"]["containerStatuses"] = []
        self.assertFalse(gates.pod_ready(pod))

    def test_stale_rollouts_fail_even_with_running_ready_pods(self):
        for kind in ("Deployment", "StatefulSet"):
            for key, value in (
                ("observedGeneration", 1),
                ("updatedReplicas", 0),
                ("readyReplicas", 0),
                ("replicas", 2),
            ):
                with self.subTest(kind=kind, key=key):
                    items = fixture(kind)
                    self.assertTrue(gates.Inventory(items).workload_ready(kind, "demo"))
                    items[0]["status"][key] = value
                    self.assertFalse(
                        gates.Inventory(items).workload_ready(kind, "demo")
                    )

    def test_statefulset_revision_and_pod_ownership_must_match(self):
        items = fixture()
        items[0]["status"]["updateRevision"] = "new"
        self.assertFalse(gates.Inventory(items).workload_ready("StatefulSet", "demo"))
        items = fixture()
        items[1]["metadata"]["ownerReferences"][0]["uid"] = "another-controller"
        self.assertFalse(gates.Inventory(items).workload_ready("StatefulSet", "demo"))

    def test_endpoint_checks_identity_port_and_current_owner(self):
        for change in ("uid", "port", "generation", "selector"):
            with self.subTest(change=change):
                items = fixture()
                self.assertTrue(gates.Inventory(items).service_pods("demo", (80,)))
                by = gates.Inventory(items)
                if change == "uid":
                    by.get("Endpoints", "demo")["subsets"][0]["addresses"][0][
                        "targetRef"
                    ]["uid"] = "stale"
                if change == "port":
                    by.get("Endpoints", "demo")["subsets"][0]["ports"][0]["port"] = 9999
                if change == "generation":
                    items[0]["status"]["observedGeneration"] = 1
                if change == "selector":
                    by.get("Service", "demo")["spec"]["selector"] = {"app": "other"}
                self.assertFalse(by.service_pods("demo", (80,)))

    def test_latest_resourceset_must_be_settled_not_an_older_one(self):
        items = fixture()
        self.assertTrue(gates.Inventory(items).assignment_ready("demo"))
        newer = copy.deepcopy(items[-1])
        newer["metadata"]["name"] = "demo.v3"
        newer["status"]["phase"] = "Pending"
        items.append(newer)
        self.assertFalse(gates.Inventory(items).assignment_ready("demo"))

    def test_assignment_requires_generation_conditions_and_applied_identity(self):
        for change in (
            "generation",
            "condition",
            "uid",
            "applied_generation",
            "failed",
        ):
            with self.subTest(change=change):
                items = fixture()
                ca, rs = items[-2:]
                if change == "generation":
                    ca["status"]["observedGeneration"] = 1
                if change == "condition":
                    ca["status"]["conditions"][0]["status"] = "False"
                if change == "uid":
                    rs["status"]["applied"][0]["items"][0]["uid"] = "old"
                if change == "applied_generation":
                    rs["status"]["applied"][0]["items"][0]["generation"] = 1
                if change == "failed":
                    rs["status"]["failed"] = [{"error": "private error details"}]
                self.assertFalse(gates.Inventory(items).assignment_ready("demo"))


class OrchestrationTest(unittest.TestCase):
    def validator(self, items=None):
        reader = Mock()
        reader.inventory.return_value = items or fixture()
        reader.probe.return_value = {"ok": True}
        return gates.Validator(
            RuntimeTarget(realistic_config()), reader, realistic_lock()
        ), reader

    def test_running_workspace_does_not_bypass_failed_readiness(self):
        items = fixture("Deployment", "omts-build-workspace")
        items[1]["status"]["containerStatuses"][0]["ready"] = False
        validator, reader = self.validator(items)
        report = validator.run([1, 2, 3, 4, 5])
        self.assertFalse(report["passed"])
        self.assertTrue(report["read_only"])
        self.assertEqual(
            [x["status"] for x in report["gates"]],
            ["failed", "skipped", "skipped", "skipped", "skipped"],
        )
        reader.probe.assert_not_called()

    def test_unready_prerequisite_never_resets_or_runs_application(self):
        validator, reader = self.validator()
        report = validator.run([1, 2, 3, 4, 5, 6])
        self.assertFalse(report["application_cycle_complete"])
        self.assertNotIn(
            "reset", [call.args[0] for call in reader.probe.call_args_list]
        )

    def test_busy_executive_is_not_canceled_or_reset(self):
        validator, reader = self.validator()
        reader.probe.return_value = {"ok": False}
        with patch.object(validator, "pre_action_ready", return_value=True):
            validator.application()
        self.assertFalse(validator.actions_executed)
        self.assertEqual(
            [call.args[0] for call in reader.probe.call_args_list], ["idle"]
        )

    def test_solution_image_drift_is_a_failed_gate(self):
        items = fixture("Deployment")
        items[0]["metadata"]["ownerReferences"] = [owner("ResourceSet", "demo.v2")]
        items[0]["spec"]["template"] = {
            "spec": {
                "containers": [
                    {"name": "demo", "image": "quay.io/unapproved/demo:latest"}
                ]
            }
        }
        validator, _ = self.validator(items)
        validator.solution()
        result = next(
            g for g in validator.results if g.name == "solution_image_identity"
        )
        self.assertEqual(result.status, "failed")
        self.assertEqual(result.reason, "solution_image_not_locked")

    def test_storage_is_checked_even_when_reset_fails(self):
        validator, reader = self.validator()
        images = {
            "rs-ur-module": "ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image",
            "rs-hande-gripper": "ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml",
            "rs-gazebo-simulator": "ai.intrinsic.gazebo_simulator.asset-gzserver-image",
        }
        reader.inventory.return_value = [
            {
                "kind": "StatefulSet",
                "metadata": metadata(name),
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [
                                {
                                    "image": "registry.example.com/project/"
                                    + image
                                    + "@sha256:"
                                    + "a" * 64
                                }
                            ]
                        }
                    }
                },
            }
            for name, image in images.items()
        ]
        reader.probe.side_effect = lambda action, *args, **kwargs: {
            "ok": action != "reset"
        }
        before = {"cache": ("original", {"size": "20Gi"}, "Bound")}
        after = {"cache": ("replacement", {"size": "20Gi"}, "Bound")}
        with (
            patch.object(validator, "pre_action_ready", return_value=True),
            patch.object(
                gates.Inventory, "storage_signature", side_effect=[before, after]
            ),
        ):
            validator.application()
        self.assertEqual(validator.results[-1].name, "persistent_storage_preserved")
        self.assertEqual(validator.results[-1].status, "failed")
        self.assertNotIn(
            "cycle", [call.args[0] for call in reader.probe.call_args_list]
        )

    def test_missing_rhoai_gpu_fails_without_device_exec(self):
        validator, reader = self.validator()
        pod = {
            "metadata": {
                "name": "predictor",
                "labels": {"serving.kserve.io/inferenceservice": "model"},
            },
            "spec": {
                "containers": [
                    {"name": "kserve-container", "resources": {"limits": {}}}
                ]
            },
        }
        reader.get.return_value = {
            "status": {"conditions": [{"type": "Ready", "status": "True"}]}
        }
        with (
            patch.object(gates.Inventory, "service_pods", return_value=[pod]),
            patch.object(gates.Inventory, "workload_ready", return_value=True),
        ):
            validator.runtime()
        self.assertTrue(
            any(
                g.name == "rhoai_predictor" and g.status == "failed"
                for g in validator.results
            )
        )
        reader.oc.assert_not_called()

    def test_pre_action_snapshot_cannot_be_bypassed(self):
        validator, reader = self.validator()
        validator.application()
        self.assertFalse(validator.actions_executed)
        reader.probe.assert_not_called()
        self.assertEqual(validator.results[-1].reason, "pre_action_readiness_failed")

    def test_inference_trace_is_separate_from_cycle_completion(self):
        validator, reader = self.validator()
        validator.predictors = [("example-namespace", "predictor")]
        reader.oc.return_value = subprocess.CompletedProcess(
            [],
            0,
            "FoundationPose inference completed. Time: 8.060 s, with 1 outputs.",
            "",
        )
        validator.inference_trace("2026-01-01T00:00:00Z")
        self.assertEqual(validator.results[-1].status, "passed")
        self.assertFalse(
            any(
                g.name == "bounded_cycle" and g.status == "passed"
                for g in validator.results
            )
        )
        self.assertFalse(
            gates.foundationpose_completed(
                "FoundationPose inference not completed; outputs: 0"
            )
        )
        self.assertFalse(
            gates.foundationpose_completed(
                "FoundationPose inference failed; outputs: 0"
            )
        )

    def test_query_errors_never_escape_to_report(self):
        validator, reader = self.validator()
        reader.probe.side_effect = PilotError(
            "Bearer private-token https://private.invalid"
        )
        with patch.object(validator, "workspace", side_effect=reader.probe):
            report = validator.run([1])
        self.assertNotIn("private", json.dumps(report))
        self.assertFalse(report["passed"])

    def test_known_collision_always_fails_and_is_structured(self):
        validator, reader = self.validator()
        reader.probe.return_value = {
            "ok": False,
            "known_collision": True,
            "cycle_completed": False,
            "timed_out": False,
        }
        self.assertFalse(
            validator.probe_gate(6, "bounded_cycle", "cycle", "OneCycleCompleted")
        )
        self.assertEqual(validator.results[-1].reason, "known_collision")

    def test_age_is_sanitized_numeric_or_unknown(self):
        self.assertIsNone(
            gates.condition_age(
                {
                    "status": {
                        "conditions": [
                            {"status": "True", "lastTransitionTime": "private"}
                        ]
                    }
                }
            )
        )
        age = gates.condition_age(
            {
                "status": {
                    "conditions": [
                        {"status": "True", "lastTransitionTime": "2026-01-01T00:00:00Z"}
                    ]
                }
            }
        )
        self.assertIsInstance(age, int)

    def test_sanitized_json_includes_required_evidence_fields(self):
        validator, _ = self.validator()
        with patch.object(
            validator,
            "core",
            side_effect=lambda: validator.gate(3, "core", True, "Ready", "unused"),
        ):
            report = validator.run([3])
        self.assertTrue(report["passed"])
        self.assertFalse(report["application_cycle_complete"])
        self.assertTrue(
            {
                "phase",
                "name",
                "status",
                "condition",
                "reason",
                "age_seconds",
                "observed_at",
                "evidence",
            }
            <= report["gates"][0].keys()
        )


class CLITest(unittest.TestCase):
    def test_phase_six_requires_explicit_action_flag_before_cluster_access(self):
        for option in ("--phase", "--through-phase"):
            with (
                patch.object(gates, "load_runtime_target") as load,
                redirect_stderr(io.StringIO()),
                self.assertRaises(SystemExit) as error,
            ):
                gates.main([option, "6", "--json"])
            self.assertEqual(error.exception.code, 2)
            load.assert_not_called()

    def test_missing_config_is_nonzero_and_safe_json(self):
        with (
            tempfile.TemporaryDirectory() as directory,
            redirect_stdout(io.StringIO()) as output,
        ):
            code = gates.main(
                ["--config", str(Path(directory) / "missing.json"), "--json"]
            )
        self.assertEqual(code, 1)
        report = json.loads(output.getvalue())
        self.assertFalse(report["passed"])
        self.assertNotIn(directory, output.getvalue())

    def test_default_is_read_only_through_five(self):
        target = RuntimeTarget(realistic_config())
        validator = Mock()
        validator.run.return_value = {
            "passed": True,
            "gates": [],
            "application_cycle_complete": False,
        }
        with (
            patch.object(gates, "load_runtime_target", return_value=target),
            patch.object(RuntimeTarget, "connect"),
            patch.object(gates, "load_image_lock", return_value=realistic_lock()),
            patch.object(gates, "Validator", return_value=validator),
            redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(gates.main(["--json"]), 0)
        validator.run.assert_called_once_with([1, 2, 3, 4, 5])

    def test_phase_six_always_runs_prerequisites(self):
        target = RuntimeTarget(realistic_config())
        validator = Mock()
        validator.run.return_value = {
            "passed": False,
            "gates": [],
            "application_cycle_complete": False,
        }
        with (
            patch.object(gates, "load_runtime_target", return_value=target),
            patch.object(RuntimeTarget, "connect"),
            patch.object(gates, "load_image_lock", return_value=realistic_lock()),
            patch.object(gates, "Validator", return_value=validator),
            redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(
                gates.main(["--phase", "6", "--allow-app-actions", "--json"]), 1
            )
        validator.run.assert_called_once_with([1, 2, 3, 4, 5, 6])


class ProbeTest(unittest.TestCase):
    def request(self, directory, action):
        hashes = {}
        for name in [*demo_probe.RESET_FILES, demo_probe.CELL + "app_config.yaml"]:
            path = Path(directory) / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("reviewed fixture")
            hashes[name] = hashlib.sha256(path.read_bytes()).hexdigest()
        return {
            "action": action,
            "allow_app_actions": True,
            "file_hashes": hashes,
            "gateway": "gateway.example.com:80",
            "cycle_timeout": 30,
        }

    def test_probe_boundary_only_accepts_known_boolean_fields(self):
        for text in (
            "",
            demo_probe.MARKER + "not json",
            demo_probe.MARKER + '{"ok":true,"token":"private"}',
            demo_probe.MARKER + '{"ok":"true"}',
            demo_probe.MARKER + '{"ok":true}\n' + demo_probe.MARKER + '{"ok":true}',
        ):
            self.assertEqual(demo_probe.parse_result(text), {"ok": False})
        self.assertEqual(
            demo_probe.parse_result(
                "private raw logs\n" + demo_probe.MARKER + '{"ok":true}'
            ),
            {"ok": True},
        )

    def test_remote_action_rechecks_explicit_flag(self):
        with patch.object(demo_probe, "bounded_process") as run:
            self.assertFalse(demo_probe.action_probe({"action": "reset"})["ok"])
        run.assert_not_called()

    def test_reset_uses_only_the_four_reviewed_files_and_no_build(self):
        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(demo_probe, "WORKSPACE", Path(directory)),
            patch.object(
                demo_probe,
                "bounded_process",
                return_value=subprocess.CompletedProcess([], 0, "", ""),
            ) as run,
        ):
            request = self.request(directory, "reset")
            self.assertTrue(demo_probe.action_probe(request)["ok"])
            command = run.call_args.args[0]
        self.assertEqual(
            command[command.index("--files") + 1 : -1], demo_probe.RESET_FILES
        )
        self.assertNotIn("bazel", command)
        self.assertIn("--reset_sim", command)

    def test_changed_scene_file_fails_before_reset(self):
        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(demo_probe, "WORKSPACE", Path(directory)),
            patch.object(demo_probe, "bounded_process") as run,
        ):
            request = self.request(directory, "reset")
            (Path(directory) / demo_probe.RESET_FILES[0]).write_text("drift")
            self.assertFalse(demo_probe.action_probe(request)["ok"])
        run.assert_not_called()

    def test_collision_is_not_a_successful_cycle_even_if_exit_zero(self):
        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(demo_probe, "WORKSPACE", Path(directory)),
            patch.object(
                demo_probe,
                "bounded_process",
                return_value=subprocess.CompletedProcess(
                    [], 0, "gripper.gripper_finger2 raw_stock_2x3x5.base_link", ""
                ),
            ) as run,
        ):
            result = demo_probe.action_probe(self.request(directory, "cycle"))
            self.assertIn("--num_cycles=1", run.call_args.args[0])
        self.assertFalse(result["ok"])
        self.assertFalse(result["cycle_completed"])
        self.assertTrue(result["known_collision"])

    def test_sdk_pending_backend_is_a_connectivity_pass_and_health_failure(self):
        from types import SimpleNamespace
        from unittest.mock import MagicMock

        proto = SimpleNamespace(
            solution_service_pb2=SimpleNamespace(GetStatusRequest=lambda: None),
            solution_service_pb2_grpc=SimpleNamespace(
                SolutionServiceStub=lambda channel: SimpleNamespace(
                    GetStatus=lambda *args, **kwargs: SimpleNamespace(state=2)
                )
            ),
            status_pb2=SimpleNamespace(
                Status=SimpleNamespace(State=SimpleNamespace(READY=1))
            ),
        )
        modules = {
            "grpc": SimpleNamespace(insecure_channel=lambda address: MagicMock()),
            "intrinsic.frontend.solution_service.proto": proto,
        }
        with patch.dict("sys.modules", modules):
            self.assertEqual(
                demo_probe.sdk_action(
                    {"gateway": "example.invalid:80", "action": "gateway"}
                ),
                {"ok": True, "backend_ready": False},
            )
            self.assertFalse(
                demo_probe.sdk_action(
                    {"gateway": "example.invalid:80", "action": "solution_health"}
                )["ok"]
            )

    def test_timeout_terminates_launcher_process_group_without_raw_output(self):
        process = Mock()
        process.pid = 12345
        process.communicate.side_effect = [
            subprocess.TimeoutExpired("private", 1),
            ("private logs", ""),
        ]
        context = Mock()
        context.__enter__ = Mock(return_value=process)
        context.__exit__ = Mock(return_value=False)
        with (
            patch.object(demo_probe.subprocess, "Popen", return_value=context),
            patch.object(demo_probe.os, "killpg") as kill,
            self.assertRaises(subprocess.TimeoutExpired) as error,
        ):
            demo_probe.bounded_process(["cached-demo"], timeout=1)
        self.assertEqual(error.exception.cmd, "cached_demo_process")
        kill.assert_called_once_with(12345, demo_probe.signal.SIGTERM)

    def test_cycle_timeout_is_a_failed_gate(self):
        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(demo_probe, "WORKSPACE", Path(directory)),
            patch.object(
                demo_probe,
                "bounded_process",
                side_effect=subprocess.TimeoutExpired("private executable", 30),
            ),
        ):
            result = demo_probe.action_probe(self.request(directory, "cycle"))
        self.assertTrue(result["timed_out"])
        self.assertFalse(result["cycle_completed"])

    def test_remote_exceptions_never_print_raw_details(self):
        with (
            patch.object(
                demo_probe,
                "registry_probe",
                side_effect=RuntimeError("Bearer secret private host"),
            ),
            redirect_stdout(io.StringIO()) as output,
        ):
            demo_probe.run_request({"action": "registry"})
        self.assertEqual(demo_probe.parse_result(output.getvalue()), {"ok": False})
        self.assertNotIn("secret", output.getvalue())


if __name__ == "__main__":
    unittest.main()
