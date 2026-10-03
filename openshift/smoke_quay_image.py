#!/usr/bin/env python3
"""Run one digest-pinned Quay image under the dev01 restricted SCC and clean up."""

from __future__ import annotations

import argparse
import json
import secrets
import sys
import time
from pathlib import Path

from common import PilotError, TARGET_PROJECT, can_i, get_json, resolve_context

ROOT = Path(__file__).resolve().parent
LOCK = ROOT / "image-lock.json"
WAIT_SECONDS = 150


def image_for(basename: str) -> str:
    lock = json.loads(LOCK.read_text())
    matches = [
        image
        for image in lock["images"]
        if image["chart_repository_basename"] == basename
    ]
    if len(matches) != 1:
        raise PilotError("Image basename must match exactly one locked Quay image.")
    image = matches[0]
    return f"{image['quay_repository']}@{image['quay_digest']}"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("basename", help="chart repository basename from image-lock.json")
    args = parser.parse_args()

    access = None
    name = f"intrinsic-image-smoke-{secrets.token_hex(4)}"
    created = False
    try:
        image = image_for(args.basename)
        access = resolve_context("dev01")
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
                "serviceAccountName": "intrinsic-runtime",
                "automountServiceAccountToken": False,
                "restartPolicy": "Never",
                "activeDeadlineSeconds": WAIT_SECONDS,
                "securityContext": {"seccompProfile": {"type": "RuntimeDefault"}},
                "containers": [
                    {
                        "name": "image",
                        "image": image,
                        "imagePullPolicy": "Always",
                        "resources": {
                            "requests": {"cpu": "25m", "memory": "64Mi"},
                            "limits": {"cpu": "250m", "memory": "256Mi"},
                        },
                        "securityContext": {
                            "allowPrivilegeEscalation": False,
                            "capabilities": {"drop": ["ALL"]},
                            "runAsNonRoot": True,
                        },
                    }
                ],
            },
        }
        result = access.oc(
            "create", "-f", "-", check=False, input_data=json.dumps(manifest)
        )
        if result.returncode:
            raise PilotError("Could not create the temporary image smoke pod; raw CLI output was suppressed.")
        created = True

        deadline = time.monotonic() + WAIT_SECONDS
        while time.monotonic() < deadline:
            pod = get_json(access, "get", "pod", name, "-n", TARGET_PROJECT)
            spec = pod.get("spec", {})
            container = (spec.get("containers") or [{}])[0]
            run_as_user = container.get("securityContext", {}).get("runAsUser")
            if run_as_user is None:
                run_as_user = spec.get("securityContext", {}).get("runAsUser")
            status = (pod.get("status", {}).get("containerStatuses") or [{}])[0]
            state = status.get("state", {})
            if state.get("waiting"):
                reason = state["waiting"].get("reason", "waiting")
                if reason in {"ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError"}:
                    raise PilotError(f"Image smoke could not start ({reason}); pod will be removed.")
            if state.get("terminated"):
                term = state["terminated"]
                raise PilotError(
                    f"Image process exited before staying ready (exit {term.get('exitCode', 'unknown')}); pod will be removed."
                )
            if state.get("running") and pod.get("status", {}).get("phase") == "Running":
                if not isinstance(run_as_user, int) or run_as_user <= 0:
                    raise PilotError("OpenShift did not assign a non-root UID; pod will be removed.")
                print("Restricted-SCC image startup smoke: passed (non-root UID assigned; pod removed).")
                return 0
            if pod.get("status", {}).get("phase") == "Failed":
                raise PilotError("Image smoke pod failed; raw pod details were suppressed.")
            time.sleep(3)

        raise PilotError("Image smoke timed out; pod will be removed.")
    except (PilotError, OSError, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    finally:
        if access and created:
            access.oc(
                "delete", "pod", name, "-n", TARGET_PROJECT,
                "--wait=true", "--timeout=30s", check=False,
            )


if __name__ == "__main__":
    sys.exit(main())
