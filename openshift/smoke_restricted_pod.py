#!/usr/bin/env python3
"""Test project image pulls and restricted pod admission, then self-clean."""

from __future__ import annotations

import json
import secrets
import sys

from common import PilotError, TARGET_PROJECT, can_i, get_json, resolve_context

IMAGE = "registry.access.redhat.com/ubi9/ubi-minimal:latest"
SUCCESS_MARKER = "restricted-nonroot-ok"


def main() -> int:
    access = None
    name = f"intrinsic-scc-smoke-{secrets.token_hex(4)}"
    created = False
    try:
        access = resolve_context()
        if not get_json(access, "get", "namespace", TARGET_PROJECT, missing_ok=True):
            raise PilotError("Target project is absent; no smoke pod was created.")
        if not can_i(access, "create", "pods", TARGET_PROJECT):
            raise PilotError("Current identity cannot create a project-scoped smoke pod.")

        manifest = {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "name": name,
                "namespace": TARGET_PROJECT,
                "labels": {"app.kubernetes.io/part-of": "intrinsic-openshift-pilot"},
            },
            "spec": {
                "restartPolicy": "Never",
                "securityContext": {"seccompProfile": {"type": "RuntimeDefault"}},
                "containers": [{
                    "name": "smoke",
                    "image": IMAGE,
                    "imagePullPolicy": "Always",
                    "command": ["/bin/sh", "-c", f'test "$(id -u)" -ne 0 && echo {SUCCESS_MARKER}'],
                    "resources": {
                        "requests": {"cpu": "10m", "memory": "16Mi"},
                        "limits": {"cpu": "100m", "memory": "64Mi"},
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
        result = access.oc(
            "create", "-f", "-", check=False, input_data=json.dumps(manifest)
        )
        if result.returncode:
            raise PilotError("Could not create the temporary smoke pod; raw CLI output was suppressed.")
        created = True
        wait = access.oc(
            "wait", "--for=jsonpath={.status.phase}=Succeeded", f"pod/{name}",
            "-n", TARGET_PROJECT, "--timeout=120s", check=False,
        )
        if wait.returncode:
            raise PilotError("Smoke pod did not complete; raw CLI output was suppressed.")
        logs = access.oc("logs", name, "-n", TARGET_PROJECT, check=False)
        if logs.returncode or SUCCESS_MARKER not in logs.stdout:
            raise PilotError("Smoke pod did not confirm restricted non-root execution.")
        print("Project image-pull and restricted non-root smoke: passed.")
        return 0
    except (PilotError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    finally:
        if access and created:
            access.oc("delete", "pod", name, "-n", TARGET_PROJECT, "--wait=true", "--timeout=30s", check=False)


if __name__ == "__main__":
    sys.exit(main())
