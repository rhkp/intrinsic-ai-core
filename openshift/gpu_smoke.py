#!/usr/bin/env python3
"""Bounded dev01 GPU scheduling/device smoke; always deletes its temporary pod."""

from __future__ import annotations

import json
import secrets
import sys
from decimal import Decimal

from common import PilotError, TARGET_PROJECT, can_i, get_json, resolve_context

IMAGE = "registry.access.redhat.com/ubi9/ubi-minimal:latest"
SUCCESS_MARKER = "intrinsic-gpu-device-ok"


def main() -> int:
    access = None
    created = False
    name = f"intrinsic-gpu-smoke-{secrets.token_hex(4)}"
    try:
        access = resolve_context()
        project = get_json(access, "get", "namespace", TARGET_PROJECT, missing_ok=True)
        if not project:
            raise PilotError("Target project is absent; no GPU smoke pod was created.")
        if not can_i(access, "create", "pods", TARGET_PROJECT):
            raise PilotError("Current identity cannot create a project-scoped GPU smoke pod.")

        service_account = get_json(
            access, "get", "serviceaccount", "intrinsic-runtime", "-n", TARGET_PROJECT, missing_ok=True
        )
        if not service_account or service_account.get("automountServiceAccountToken") is not False:
            raise PilotError("The expected token-disabled intrinsic-runtime ServiceAccount is unavailable.")

        nodes = get_json(access, "get", "nodes") or {"items": []}
        gpu_nodes = [
            node for node in nodes.get("items", [])
            if Decimal(str(node.get("status", {}).get("allocatable", {}).get("nvidia.com/gpu", "0"))) > 0
        ]
        if not gpu_nodes:
            raise PilotError("No allocatable GPU nodes were reported; no smoke pod was created.")

        pods = get_json(access, "get", "pods", "-A") or {"items": []}
        requested_by_node: dict[str, Decimal] = {}
        for pod in pods.get("items", []):
            node_name = pod.get("spec", {}).get("nodeName")
            if not node_name or pod.get("status", {}).get("phase") in ("Succeeded", "Failed"):
                continue
            total = Decimal(0)
            for container in pod.get("spec", {}).get("containers", []) + pod.get("spec", {}).get("initContainers", []):
                resources = container.get("resources", {})
                requests = resources.get("requests", {})
                limits = resources.get("limits", {})
                gpu = requests.get("nvidia.com/gpu", limits.get("nvidia.com/gpu", "0"))
                total += Decimal(str(gpu))
            requested_by_node[node_name] = requested_by_node.get(node_name, Decimal(0)) + total

        allocatable = sum(
            (Decimal(str(node["status"]["allocatable"].get("nvidia.com/gpu", "0"))) for node in gpu_nodes),
            Decimal(0),
        )
        requested = sum((requested_by_node.get(node["metadata"]["name"], Decimal(0)) for node in gpu_nodes), Decimal(0))
        free = allocatable - requested
        if free < 1:
            raise PilotError("No aggregate GPU request headroom remains; no smoke pod was created.")

        expected_taint = {"key": "g5-gpu", "value": "true", "effect": "NoSchedule"}
        for node in gpu_nodes:
            gpu_taints = [
                {key: taint.get(key) for key in ("key", "value", "effect")}
                for taint in node.get("spec", {}).get("taints", [])
                if taint.get("key") == "g5-gpu"
            ]
            if gpu_taints != [expected_taint]:
                raise PilotError("GPU-node taint differs from the reviewed g5-gpu=true:NoSchedule policy.")

        tolerations = [
            {"key": "g5-gpu", "operator": "Equal", "value": "true", "effect": "NoSchedule"}
        ]
        manifest = {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "name": name,
                "namespace": TARGET_PROJECT,
                "labels": {"app.kubernetes.io/part-of": "intrinsic-openshift-pilot"},
            },
            "spec": {
                "serviceAccountName": "intrinsic-runtime",
                "automountServiceAccountToken": False,
                "restartPolicy": "Never",
                "activeDeadlineSeconds": 150,
                "terminationGracePeriodSeconds": 1,
                "tolerations": tolerations,
                "securityContext": {"seccompProfile": {"type": "RuntimeDefault"}},
                "containers": [{
                    "name": "gpu-smoke",
                    "image": IMAGE,
                    "imagePullPolicy": "Always",
                    "command": ["/bin/sh", "-c", f'test -c /dev/nvidia0 && echo {SUCCESS_MARKER}'],
                    "resources": {
                        "requests": {"cpu": "10m", "memory": "16Mi", "nvidia.com/gpu": "1"},
                        "limits": {"cpu": "100m", "memory": "128Mi", "nvidia.com/gpu": "1"},
                    },
                    "securityContext": {
                        "allowPrivilegeEscalation": False,
                        "capabilities": {"drop": ["ALL"]},
                        "runAsNonRoot": True,
                        "readOnlyRootFilesystem": True,
                    },
                }],
            },
        }
        result = access.oc("create", "-f", "-", check=False, input_data=json.dumps(manifest))
        if result.returncode:
            raise PilotError("Could not create the temporary GPU smoke pod; raw CLI output was suppressed.")
        created = True
        print(f"GPU capacity snapshot: {len(gpu_nodes)} GPU nodes; {allocatable} allocatable, {requested} requested, {free} aggregate headroom.")

        wait = access.oc(
            "wait", "--for=jsonpath={.status.phase}=Succeeded", f"pod/{name}",
            "-n", TARGET_PROJECT, "--timeout=180s", check=False,
        )
        if wait.returncode:
            raise PilotError("GPU smoke did not complete; raw CLI output was suppressed and cleanup will run.")
        logs = access.oc("logs", name, "-n", TARGET_PROJECT, check=False)
        if logs.returncode or SUCCESS_MARKER not in logs.stdout:
            raise PilotError("GPU device was not confirmed inside the restricted container.")
        print("GPU scheduling, image pull, restricted admission, and /dev/nvidia0 visibility: passed.")
        return 0
    except (PilotError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    finally:
        if access and created:
            access.oc("delete", "pod", name, "-n", TARGET_PROJECT, "--wait=true", "--timeout=30s", check=False)


if __name__ == "__main__":
    sys.exit(main())
