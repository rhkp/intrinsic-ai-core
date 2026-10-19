# This project was developed with assistance from AI tools.
"""Low-output helpers for an OpenShift pilot.

These helpers keep kubeconfig identities, API addresses, Secrets, and raw
resource specifications out of command output and repository artifacts.
"""

from __future__ import annotations

import json
import subprocess
from dataclasses import dataclass
from typing import Any


class PilotError(RuntimeError):
    """An error safe to print without exposing cluster response details."""


@dataclass(frozen=True)
class ClusterAccess:
    context: str

    def oc(
        self,
        *args: str,
        check: bool = True,
        input_data: str | None = None,
    ) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            ["oc", "--context", self.context, *args],
            text=True,
            capture_output=True,
            check=False,
            input=input_data,
        )
        if check and result.returncode:
            raise PilotError("OpenShift query failed; raw CLI output was suppressed.")
        return result


def get_json(access: ClusterAccess, *args: str, missing_ok: bool = False) -> Any:
    """Read JSON into memory without exposing unfiltered CLI output."""
    result = access.oc(*args, "-o", "json", check=False)
    if result.returncode:
        if missing_ok and any(
            marker in result.stderr.casefold() for marker in ("notfound", "not found")
        ):
            return None
        raise PilotError(
            "OpenShift query failed or is unavailable; raw output was suppressed."
        )
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise PilotError(
            "OpenShift returned an unexpected response; output was suppressed."
        ) from exc


def can_i(
    access: ClusterAccess, verb: str, resource: str, namespace: str | None = None
) -> bool:
    args = ["auth", "can-i", verb, resource]
    if namespace:
        args.extend(["-n", namespace])
    result = access.oc(*args, check=False)
    return result.returncode == 0 and result.stdout.strip().casefold() == "yes"
