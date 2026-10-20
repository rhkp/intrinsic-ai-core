# This project was developed with assistance from AI tools.
"""Bounded probes sent over stdin to the existing workspace or wrapper.

No credentials, raw SDK output, or raw application logs leave the remote process.
This file has no startup side effects; validate_demo_gates supplies the request.
"""

from __future__ import annotations

import base64
import contextlib
import hashlib
import io
import json
import os
import signal
import ssl
import subprocess
import sys
import urllib.request
from pathlib import Path

MARKER = "INTRINSIC_DEMO_GATE="
WORKSPACE = Path("/workspace/source/intrinsic-omts")
CELL = "configs/your-workcell/"
RESET_FILES = [
    CELL + name
    for name in (
        "ur_module.attachments.updates.pbtxt",
        "scene.updates.pbtxt",
        "align_robot.updates.pbtxt",
        "orbbec_gemini.updates.pbtxt",
    )
]
MODELS = (
    "ai.intrinsic.ioc_pose_estimation.segmentor.rfdetr",
    "ai.intrinsic.ioc_pose_estimation.pose_estimator.foundationpose",
)


def registry_probe(request: dict) -> dict:
    """Authenticate in memory with the mounted SA token; verify an existing upload."""
    mounted = Path("/var/run/secrets/kubernetes.io/serviceaccount")
    if (mounted / "namespace").read_text().strip() != request["namespace"]:
        return {"ok": False}
    token = (mounted / "token").read_text().strip()
    if not token or any(char.isspace() for char in token):
        return {"ok": False}
    context = ssl.create_default_context()
    context.load_verify_locations("/var/run/registry-ca/service-ca.crt")
    auth = base64.b64encode(("openshift-token:" + token).encode()).decode()
    url = (
        "https://"
        + request["registry"]
        + "/v2/"
        + request["namespace"]
        + "/"
        + request["image_name"]
        + "/manifests/"
        + request["digest"]
    )
    query = urllib.request.Request(
        url,
        method="HEAD",
        headers={
            "Authorization": "Basic " + auth,
            "Accept": "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
        },
    )
    with urllib.request.urlopen(query, context=context, timeout=8) as response:
        return {
            "ok": response.status == 200
            and response.headers.get("Docker-Content-Digest") == request["digest"]
        }


def icon_probe(request: dict) -> dict:
    url = "http://rs-icon." + request["namespace"] + ".svc.cluster.local:9091"
    with urllib.request.urlopen(url + "/v1/operationalStatus", timeout=8) as response:
        data = json.load(response)
    operational = data.get("operationalStatus", {}).get("state") == "ENABLED"
    hardware = data.get("cellControlHardwareStatus", {}).get("state") == "ENABLED"
    return {
        "ok": operational and hardware,
        "operational_enabled": operational,
        "hardware_enabled": hardware,
    }


def rhoai_probe(request: dict) -> dict:
    runfiles = Path(
        "/intrinsic_inference/assets/inference_service/inference_service_main.runfiles"
    )
    for path in runfiles.glob("*/site-packages"):
        sys.path.insert(0, str(path))
    import tritonclient.grpc as triton

    # The projected token stays in memory and RPC metadata, never argv or output.
    token = Path("/var/run/secrets/intrinsic-rhoai/token").read_text().strip()
    endpoint = os.environ.get("INTRINSIC_RHOAI_TRITON_ENDPOINT")
    if not token or endpoint != request["rhoai_endpoint"]:
        return {"ok": False}
    headers = {"authorization": "Bearer " + token}
    with triton.InferenceServerClient(url=endpoint) as remote:
        ready = remote.is_server_ready(headers=headers, client_timeout=8)
        ready = ready and all(
            remote.is_model_ready(name, headers=headers, client_timeout=8)
            for name in MODELS
        )
    with triton.InferenceServerClient(url="localhost:9090") as wrapper:
        for name in MODELS:
            metadata = wrapper.get_model_metadata(name, client_timeout=8)
            ready = ready and bool(metadata.inputs) and bool(metadata.outputs)
    return {"ok": bool(ready)}


def sdk_action(request: dict) -> dict:
    """Runs only after the existing Bazel launcher initializes its SDK imports."""
    import grpc
    from intrinsic.frontend.solution_service.proto import (
        solution_service_pb2,
        solution_service_pb2_grpc,
    )
    from intrinsic.frontend.solution_service.proto import (
        status_pb2 as solution_status_pb2,
    )

    with grpc.insecure_channel(request["gateway"]) as channel:
        status = solution_service_pb2_grpc.SolutionServiceStub(channel).GetStatus(
            solution_service_pb2.GetStatusRequest(),
            timeout=10,
        )
    healthy = status.state == solution_status_pb2.Status.State.READY
    if request["action"] == "gateway":
        # Connectivity is a lower dependency than solution readiness (phase 4).
        return {"ok": True, "backend_ready": healthy}
    if request["action"] == "solution_health":
        return {"ok": healthy}
    if not healthy:
        return {"ok": False}

    from intrinsic.solutions import deployments

    solution = deployments.connect(address=request["gateway"])
    if request["action"] == "idle":
        executive = solution.executive
        has_operation = executive.has_operation
        if callable(has_operation):
            has_operation = has_operation()
        idle = not has_operation or executive.operation.done
        return {"ok": bool(idle)}
    if request["action"] == "scene":
        import numpy as np

        world = solution.world
        enclosure = world.get_object("enclosure")
        robot = world.get_object("ur_module")
        transform = world.get_transform(enclosure, robot)
        aligned = bool(
            np.allclose(transform.translation, [0.32, 0.685, 0.658], atol=0.002)
        )
        aligned = aligned and bool(
            np.allclose(transform.rotation.matrix3x3(), np.eye(3), atol=0.002)
        )
        attached = all(
            world.get_object(name).parent.name == "ur_module"
            for name in ("gripper", "orbbec_camera")
        )
        frames = all(
            world.get_frame(name, object_name="root") is not None
            for name in ("grasp", "pre_grasp", "place_vise", "pre_place_vise", "view")
        )
        return {
            "ok": aligned and attached and frames,
            "robot_aligned": aligned,
            "attachments_correct": attached,
            "frames_present": frames,
        }
    return {"ok": False}


def sdk_probe(request: dict) -> dict:
    """Reuse cached Bazel bootstrap without installing or rebuilding anything."""
    source = request.pop("probe_source")
    entry = WORKSPACE / "bazel-bin/src"
    stage = entry / "_omts_app_stage2_bootstrap.py"
    runfiles = entry / "omts_app.runfiles"
    python = next(runfiles.glob("*python_3_11*/bin/python3"))
    launcher = (
        source
        + "\n"
        + f"""
os.environ['RUNFILES_DIR'] = {str(runfiles)!r}
stage = Path({str(stage)!r})
sys.argv = [str(stage)]
namespace = {{'__file__': str(stage), '__name__': 'demo_gate_bootstrap'}}
bootstrap = stage.read_text().rsplit('\\nmain()', 1)
if len(bootstrap) != 2:
    raise RuntimeError('unsupported cached launcher')
exec(compile(bootstrap[0], str(stage), 'exec'), namespace)
def invoke(*args, **kwargs):
    try:
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            result = sdk_action({request!r})
    except Exception:
        result = {{'ok': False}}
    print(MARKER + json.dumps(result))
namespace['_run_py_path'] = invoke
namespace['main']()
"""
    )
    process = bounded_process([str(python), "-"], timeout=40, input_text=launcher)
    return parse_result(process.stdout) if process.returncode == 0 else {"ok": False}


def parse_result(text: str) -> dict:
    lines = [
        line[len(MARKER) :] for line in text.splitlines() if line.startswith(MARKER)
    ]
    if len(lines) != 1:
        return {"ok": False}
    try:
        result = json.loads(lines[0])
    except (TypeError, ValueError):
        return {"ok": False}
    # Only fixed boolean evidence crosses the process boundary.
    allowed = {
        "ok",
        "backend_ready",
        "operational_enabled",
        "hardware_enabled",
        "robot_aligned",
        "attachments_correct",
        "frames_present",
        "known_collision",
        "timed_out",
        "cycle_completed",
    }
    if not isinstance(result, dict) or any(
        key not in allowed or type(value) is not bool for key, value in result.items()
    ):
        return {"ok": False}
    return result


def bounded_process(
    command: list[str], *, timeout: int, input_text: str | None = None
) -> subprocess.CompletedProcess:
    """Terminate the launcher and its descendants on timeout; never emit logs."""
    with subprocess.Popen(
        command,
        cwd=WORKSPACE,
        stdin=subprocess.PIPE if input_text is not None else subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    ) as process:
        try:
            stdout, stderr = process.communicate(input=input_text, timeout=timeout)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGTERM)
                process.communicate(timeout=3)
            except (ProcessLookupError, subprocess.TimeoutExpired):
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.communicate()
            raise subprocess.TimeoutExpired("cached_demo_process", timeout) from None
        return subprocess.CompletedProcess(command, process.returncode, stdout, stderr)


def action_probe(request: dict) -> dict:
    if request.get("allow_app_actions") is not True:
        return {"ok": False}
    hashes = request.get("file_hashes", {})
    if set(hashes) != {*RESET_FILES, CELL + "app_config.yaml"} or any(
        hashlib.sha256((WORKSPACE / name).read_bytes()).hexdigest() != digest
        for name, digest in hashes.items()
    ):
        return {"ok": False}
    if request["action"] == "reset":
        command = [
            str(WORKSPACE / "bazel-bin/tools/world/apply_scene_updates"),
            "--address=" + request["gateway"],
            "--files",
            *RESET_FILES,
            "--reset_sim",
        ]
        process = bounded_process(command, timeout=90)
        return {"ok": process.returncode == 0}
    command = [
        str(WORKSPACE / "bazel-bin/src/omts_app"),
        "--address=" + request["gateway"],
        "--config=" + CELL + "app_config.yaml",
        "--num_cycles=1",
    ]
    try:
        process = bounded_process(command, timeout=request["cycle_timeout"])
    except subprocess.TimeoutExpired:
        return {
            "ok": False,
            "timed_out": True,
            "cycle_completed": False,
            "known_collision": False,
        }
    log = process.stdout + process.stderr
    collision = (
        "gripper_finger1" in log or "gripper_finger2" in log
    ) and "raw_stock_2x3x5" in log
    # A reproduced collision always fails the cycle gate, even with an exit-zero bug.
    completed = process.returncode == 0 and not collision
    return {
        "ok": completed,
        "timed_out": False,
        "cycle_completed": completed,
        "known_collision": collision,
    }


def run_request(request: dict) -> None:
    try:
        action = request["action"]
        with contextlib.redirect_stderr(io.StringIO()):
            if action == "registry":
                result = registry_probe(request)
            elif action == "icon":
                result = icon_probe(request)
            elif action == "rhoai":
                result = rhoai_probe(request)
            elif action in {"gateway", "solution_health", "idle", "scene"}:
                result = sdk_probe(request)
            elif action in {"reset", "cycle"}:
                result = action_probe(request)
            else:
                result = {"ok": False}
    except subprocess.TimeoutExpired:
        result = {"ok": False, "timed_out": True}
    except Exception:
        result = {"ok": False}
    print(MARKER + json.dumps(result))
