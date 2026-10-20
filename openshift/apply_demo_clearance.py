# This project was developed with assistance from AI tools.
"""Check or apply the pinned application-only release-clearance patch."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from common import PilotError
from render_config import DEFAULT_CONFIG_PATH
from runtime_config import load_runtime_target
from validate_demo_gates import Inventory, Reader

ROOT = Path(__file__).resolve().parent.parent
SOURCE_DIGESTS = {
    "src/behaviors/load_machine.py": (
        "db0d0e7ae92f5934f79ca7a2b11ca1a965f207254be590ba793a24bcbb0f429f",
        "66c68f92a7de817379665fb09203a9f6c2c1925433b40a0239de45c1a261d416",
    ),
    "src/behaviors/return_infeed.py": (
        "33f5297339527bd4a13a8f96004d4c59d626c7f3641a3afa5f02177a3fb1da8b",
        "9d8db523bcc1479920f5cf59c528b176b68327d4ef48fc33b8cb5b4cb4020757",
    ),
}
SIMULATED_IMAGES = {
    "rs-ur-module": "ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image",
    "rs-hande-gripper": "ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml",
    "rs-gazebo-simulator": "ai.intrinsic.gazebo_simulator.asset-gzserver-image",
}


def remote_script(patch: str, apply: bool) -> str:
    return f"""
from pathlib import Path
import hashlib, json, subprocess
root = Path('/workspace/source/intrinsic-omts')
expected = {SOURCE_DIGESTS!r}
current = {{name: hashlib.sha256((root/name).read_bytes()).hexdigest() for name in expected}}
original = all(current[name] == digests[0] for name, digests in expected.items())
patched = all(current[name] == digests[1] for name, digests in expected.items())
if not original and not patched:
    raise RuntimeError('unexpected_application_source')
if {apply!r} and original:
    patch = {patch!r}
    command = ['git', 'apply', '--check', '-']
    check = subprocess.run(command, input=patch, text=True, capture_output=True, cwd=root, timeout=10)
    if check.returncode: raise RuntimeError('patch_context_changed')
    result = subprocess.run(['git','apply','-'], input=patch, text=True, capture_output=True, cwd=root, timeout=10)
    if result.returncode: raise RuntimeError('patch_failed')
    patched = all(hashlib.sha256((root/name).read_bytes()).hexdigest() == digests[1] for name,digests in expected.items())
    if not patched: raise RuntimeError('patched_digest_mismatch')
print(json.dumps({{'patch_applied': patched, 'source_identity_verified': True}}))
"""


def run(config_path: Path, *, apply: bool) -> dict:
    target = load_runtime_target(config_path)
    target.connect()
    reader = Reader(target)
    inventory = Inventory(reader.inventory())
    if apply:
        if not inventory.workload_ready("Deployment", "omts-build-workspace"):
            raise ValueError("ready_workspace_required")
        if not all(
            inventory.workload_ready("StatefulSet", name)
            and any(
                c.get("image", "").split("@", 1)[0].rsplit("/", 1)[-1] == image
                for c in inventory.get("StatefulSet", name)
                .get("spec", {})
                .get("template", {})
                .get("spec", {})
                .get("containers", [])
            )
            for name, image in SIMULATED_IMAGES.items()
        ):
            raise ValueError("ready_simulation_required")
        idle = reader.probe(
            "idle",
            {"gateway": target.document["target"]["networking"]["ingress_address"]},
        )
        if idle.get("ok") is not True:
            raise ValueError("idle_executive_required")
    patch = (ROOT / "patches/omts-release-clearance.patch").read_text()
    result = reader.oc(
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
        input_data=remote_script(patch, apply),
    )
    report = json.loads(result.stdout)
    if set(report) != {"patch_applied", "source_identity_verified"} or any(
        type(value) is not bool for value in report.values()
    ):
        raise ValueError("unexpected_patch_response")
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument(
        "--apply",
        action="store_true",
        help="Modify only the two pinned workspace application files",
    )
    args = parser.parse_args(argv)
    try:
        report = run(args.config, apply=args.apply)
        print(json.dumps(report))
        return 0 if report["patch_applied"] else 1
    except (PilotError, OSError, ValueError, TypeError, KeyError):
        print(
            json.dumps(
                {
                    "patch_applied": False,
                    "error": "source_check_or_apply_failed",
                    "raw_details_suppressed": True,
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
