#!/usr/bin/env python3
"""Prepare pinned upstream ChartAssignments for local OpenShift render checks.

This is a preflight helper only. Deployment uses the adapted chart executable,
which publishes images and creates ChartAssignments in the target project.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path

import yaml


DEFAULT_PROJECT = "arhkp-intrinsic"
DEFAULT_REGISTRY = "image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic"
CLUSTER_NAME = "openshift-pilot"


def read_assignment(path: Path) -> dict:
    value = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or value.get("kind") != "ChartAssignment":
        raise ValueError(f"{path.name} is not a ChartAssignment document")
    if not isinstance(value.get("spec"), dict) or not isinstance(value["spec"].get("chart"), dict):
        raise ValueError(f"{path.name} has no chart specification")
    return value


def set_target(assignment: dict, project: str, registry: str) -> None:
    assignment.setdefault("metadata", {})["namespace"] = project
    spec = assignment["spec"]
    spec["namespaceName"] = project
    spec["clusterName"] = CLUSTER_NAME
    values = spec["chart"].setdefault("values", {})
    if not isinstance(values, dict):
        raise ValueError("ChartAssignment chart.values must be a mapping")
    values["project"] = project
    values["registry"] = registry.rstrip("/") + "/"
    values["domain"] = "example.invalid"
    values["feature_options_overrides"] = json.dumps(
        {"disable_cluster_doc_backup": True, "disable_zenoh_storage": True},
        separators=(",", ":"),
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True, type=Path, help="pinned intrinsic-base-0.0.1.yaml")
    parser.add_argument("--app", required=True, type=Path, help="pinned intrinsic-app-chart-0.0.1.yaml")
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--project", default=DEFAULT_PROJECT)
    parser.add_argument("--registry", default=DEFAULT_REGISTRY)
    args = parser.parse_args()

    base = read_assignment(args.base)
    app = read_assignment(args.app)
    app_inline = app["spec"]["chart"].get("inline")
    if not isinstance(app_inline, str) or not app_inline:
        raise ValueError("app chart inline package is missing")

    for assignment in (base, app):
        set_target(assignment, args.project, args.registry)
    base["spec"]["chart"]["values"].setdefault("embed_chart", {})[
        "intrinsic_app_chart"
    ] = app_inline

    args.output_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    for stem, assignment in (("intrinsic-base", base), ("intrinsic-app-chart", app)):
        output = args.output_dir / f"{stem}.preflight.yaml"
        flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(output, flags, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            stream.write(yaml.safe_dump(assignment, sort_keys=False))
        print(f"prepared {output.name}; no credential values were added")


if __name__ == "__main__":
    main()
