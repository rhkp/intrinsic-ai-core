# This project was developed with assistance from AI tools.
"""Run one simulation-only cached demo cycle and retain private execution evidence."""

import argparse
import datetime
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time

from capture_vm_operation import capture


def command(args, root, *, timeout=45, log=None, env=None):
    result = subprocess.run(
        args, cwd=root, capture_output=True, text=True, timeout=timeout, env=env
    )
    if log:
        log.write_text(result.stdout + result.stderr)
        log.chmod(0o600)
    return result


def main(workspace, config, evidence, probe_file):
    evidence.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.chmod(evidence, 0o700)
    spec = importlib.util.spec_from_file_location("vm_demo_probe", probe_file)
    probe = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(probe)
    probe.WORKSPACE = workspace
    source = probe_file.read_text()
    anchor = '    if request["action"] == "idle":'
    if source.count(anchor) != 1:
        raise RuntimeError("Unexpected cached SDK probe context")
    source = source.replace(
        anchor,
        '    if request["action"] == "cancel_owned_cycle":\n        solution.executive.cancel()\n        return {"ok": True}\n'
        + anchor,
    )

    def sdk(action):
        return probe.sdk_probe(
            {"action": action, "gateway": "localhost:17080", "probe_source": source}
        )

    app = json.loads(
        subprocess.check_output(
            ["kubectl", "get", "chartassignment", "intrinsic-app-chart", "-o", "json"]
        )
    )
    if app["spec"]["chart"]["values"].get("simulated") is not True:
        raise RuntimeError("Simulation mode required")
    expected = {
        "rs-ur-module": "ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image",
        "rs-hande-gripper": "ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml",
        "rs-gazebo-simulator": "ai.intrinsic.gazebo_simulator.asset-gzserver-image",
    }
    for name, image in expected.items():
        sts = json.loads(
            subprocess.check_output(
                ["kubectl", "get", "sts", name, "-n", "app-resources", "-o", "json"]
            )
        )
        if not any(
            c["image"].split("@")[0].rsplit("/", 1)[-1] == image
            for c in sts["spec"]["template"]["spec"]["containers"]
        ):
            raise RuntimeError("Unexpected simulation resource image")
    if not sdk("idle").get("ok"):
        raise RuntimeError("Executive is busy")
    entry = workspace / "bazel-bin/src"
    stage = entry / "_omts_app_stage2_bootstrap.py"
    runfiles = entry / "omts_app.runfiles"
    cached_python = next(runfiles.glob("*python_3_11*/bin/python3"))
    launch_env = dict(os.environ, RUNFILES_DIR=str(runfiles))
    report = {
        "started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "simulation_verified": True,
        "cached_runfiles_launcher": True,
        "embedded_zip_launcher": False,
        "source_build_invoked": False,
        "cycles_requested": 1,
    }
    reset = command(
        ["inctl", "world", "reset", "--address", "localhost:17080"],
        workspace,
        timeout=90,
        log=evidence / "reset.log",
    )
    if reset.returncode:
        raise RuntimeError("Simulation reset failed; private log retained")
    report["reset_passed"] = True
    enabled = command(
        [
            "inctl",
            "icon",
            "enable",
            "--address",
            "localhost:17080",
            "--instance_name",
            "icon",
        ],
        workspace,
        timeout=45,
        log=evidence / "icon-enable.log",
    )
    report["icon_enable_passed"] = enabled.returncode == 0
    if not report["icon_enable_passed"]:
        raise RuntimeError("Simulated ICON enable failed")
    end = time.monotonic() + 60
    while time.monotonic() < end:
        if sdk("solution_health").get("ok"):
            break
        time.sleep(3)
    else:
        raise RuntimeError("Solution did not recover after reset")
    report["scene"] = sdk("scene")
    if not report["scene"].get("ok"):
        raise RuntimeError("Unexpected scene after reset")
    report["cycle_started_utc"] = datetime.datetime.now(
        datetime.timezone.utc
    ).isoformat()
    print(json.dumps({"simulation_ready": True, "one_cycle_started": True}), flush=True)
    try:
        result = command(
            [
                str(cached_python),
                str(stage),
                "--address=localhost:17080",
                "--config=" + str(config),
                "--num_cycles=1",
            ],
            workspace,
            env=launch_env,
            timeout=420,
            log=evidence / "cycle.log",
        )
    except subprocess.TimeoutExpired:
        report["timed_out"] = True
        report["owned_cycle_cancel_requested"] = sdk("cancel_owned_cycle").get(
            "ok", False
        )
        (evidence / "run.json").write_text(json.dumps(report, indent=2) + "\n")
        raise RuntimeError("Bounded demo timed out; cancellation requested") from None
    text = result.stdout + result.stderr
    report.update(
        {
            "finished_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "process_exit_code": result.returncode,
            "process_passed": result.returncode == 0,
            "collision_reported": "collision" in text.lower(),
            "inference_error_reported": any(
                s in text.lower()
                for s in [
                    "unauthenticated",
                    "permission_denied",
                    "failed to infer",
                    "inference failed",
                ]
            ),
            "executive_idle_after_run": sdk("idle").get("ok", False),
        }
    )
    operation = capture(workspace, probe_file.parent, evidence / "operation.json")
    report["cycle_completed"] = operation["cycle_completed"]
    report["operation_state"] = operation["operation_state"]
    report["stages"] = operation["stages"]
    (evidence / "run.json").write_text(json.dumps(report, indent=2) + "\n")
    (evidence / "run.json").chmod(0o600)
    print(json.dumps(report), flush=True)
    return 0 if result.returncode == 0 and report["cycle_completed"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("workspace", "config", "evidence", "probe-file"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    raise SystemExit(
        main(
            args.workspace.expanduser(),
            args.config,
            args.evidence.expanduser(),
            args.probe_file.expanduser(),
        )
    )
