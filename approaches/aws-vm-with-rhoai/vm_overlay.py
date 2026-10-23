# This project was developed with assistance from AI tools.
"""Run on the VM: guarded apply/rollback of the inference ChartAssignment overlay."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def sha(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def kubectl(*args, data=None):
    result = subprocess.run(
        ["kubectl", *args], input=data, capture_output=True, text=True, timeout=120
    )
    if result.returncode:
        raise RuntimeError("Kubernetes operation failed; raw details suppressed")
    return result.stdout


def transition(plan_file, mode, apply):
    plan = json.loads(plan_file.read_text())
    live = json.loads(kubectl("get", "chartassignment", "resources", "-o", "json"))
    if live["metadata"]["uid"] != plan["baseline_uid"]:
        raise RuntimeError("Owning deployment was replaced")
    old, new = (
        ("baseline", "candidate") if mode == "apply" else ("candidate", "baseline")
    )
    current = sha(live["spec"])
    if current == plan[new + "_spec_sha256"]:
        print(json.dumps({"mode": mode, "changed": False, "already_at_target": True}))
        return
    if current != plan[old + "_spec_sha256"]:
        raise RuntimeError(
            "Owner configuration drifted; refusing to overwrite unrelated changes"
        )
    patch = [
        {
            "op": "test",
            "path": "/metadata/resourceVersion",
            "value": live["metadata"]["resourceVersion"],
        },
        {"op": "test", "path": "/spec", "value": plan[old + "_spec"]},
        {"op": "replace", "path": "/spec", "value": plan[new + "_spec"]},
    ]
    if apply and mode == "apply":
        kubectl("apply", "-f", "-", data=json.dumps(plan["configmap"]))
    args = [
        "patch",
        "chartassignment",
        "resources",
        "--type=json",
        "--patch-file=/dev/stdin",
        "-o",
        "json",
    ]
    if not apply:
        args.append("--dry-run=server")
    result = json.loads(kubectl(*args, data=json.dumps(patch)))
    if sha(result["spec"]) != plan[new + "_spec_sha256"]:
        raise RuntimeError("Server did not preserve the intended owner configuration")
    print(
        json.dumps(
            {
                "mode": mode,
                "changed": apply,
                "server_validated": True,
                "generation": result["metadata"].get("generation"),
            }
        )
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["apply", "rollback"])
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    transition(args.plan, args.mode, args.execute)
