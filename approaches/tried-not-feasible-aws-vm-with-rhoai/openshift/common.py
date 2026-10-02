"""Shared, deliberately low-output helpers for the dev01 pilot scripts."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from dataclasses import dataclass
from typing import Any

TARGET_PROJECT = "arhkp-intrinsic"


class PilotError(RuntimeError):
    """An error safe to show without leaking command output or cluster identity."""


@dataclass(frozen=True)
class ClusterAccess:
    context: str

    def oc(self, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            ["oc", "--context", self.context, *args],
            text=True,
            capture_output=True,
            check=False,
        )
        if check and result.returncode != 0:
            raise PilotError("OpenShift query failed; raw CLI output was suppressed.")
        return result


def resolve_context(fragment: str) -> ClusterAccess:
    """Select a configured context without displaying its identity."""
    if not shutil.which("oc"):
        raise PilotError("The OpenShift CLI (oc) is required.")
    result = subprocess.run(
        ["oc", "config", "view", "-o", "json"],
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        raise PilotError("Could not read the local OpenShift context configuration.")
    try:
        config = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise PilotError("The local OpenShift context configuration is unreadable.") from exc

    matches = [
        entry
        for entry in config.get("contexts", [])
        if fragment.casefold() in entry.get("name", "").casefold()
    ]
    identities = {
        (entry.get("context", {}).get("cluster"), entry.get("context", {}).get("user"))
        for entry in matches
    }
    if not matches:
        raise PilotError("No configured context matched the requested cluster hint.")
    if len(identities) != 1:
        raise PilotError(
            "The cluster hint matches more than one cluster or identity; choose a narrower hint."
        )
    # Duplicate contexts can differ only by default namespace. Every API call
    # uses this explicit context; current-context is never changed.
    return ClusterAccess(matches[0]["name"])


def get_json(access: ClusterAccess, *args: str, missing_ok: bool = False) -> Any:
    result = access.oc(*args, "-o", "json", check=False)
    if result.returncode != 0:
        if missing_ok and any(
            marker in result.stderr.casefold() for marker in ("notfound", "not found")
        ):
            return None
        raise PilotError("OpenShift query failed or the resource is unavailable; raw output was suppressed.")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise PilotError("OpenShift returned an unexpected response; output was suppressed.") from exc


def main_error(exc: Exception) -> int:
    print(f"ERROR: {exc}", file=sys.stderr)
    return 2
