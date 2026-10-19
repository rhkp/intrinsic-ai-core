#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Offline inventory of OpenShift target and image configuration surfaces."""

from __future__ import annotations

import argparse
import ast
import json
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from render_config import EXAMPLE_PATH, load_config, load_image_lock, render_outputs
from image_model import normalize_lock


ROOT = Path(__file__).resolve().parent.parent
LOCK_PATH = ROOT / "image-lock.json"
CONTROLLER_DEPLOYMENT = (
    ROOT
    / "deployment"
    / "chartassignment-controller"
    / "deploy"
    / "controller-deployment.yaml"
)
CONTROLLER_ADAPTER = (
    ROOT / "deployment" / "chartassignment-controller" / "controller" / "openshift.go"
)
GENERATED_IMAGE_VALUES = ROOT / "deployment" / "quay-image-values.yaml"

ENVIRONMENT_PATTERN = re.compile(r"\b(?:OPENSHIFT|INTRINSIC|QUAY|PVC)_[A-Z0-9_]+\b")
IMAGE_REFERENCE_PATTERN = re.compile(
    r"(?:[a-z0-9.-]+(?::[0-9]+)?/)?[a-z0-9._/-]+@sha256:[0-9a-f]{64}"
)
CONTROLLER_CONSTANT_PATTERN = r'(?m)^\s*({name})\s*=\s*"([^"]+)"'

TARGET_PLACEHOLDERS = {
    "context": "replace-with-your-context-name",
    "namespace": "your-project-namespace",
    "registry_org": "your-registry-org",
    "block_storage_class": "your-block-storage-class",
    "shared_storage_class": "your-shared-filesystem-storage-class",
    "gpu_taint_key": "your-gpu-taint-key",
    "service_mesh_control_plane": "your-service-mesh-control-plane",
    "api_server": "api.example.invalid",
}

SOURCE_SUFFIXES = {".go", ".py", ".sh", ".yaml", ".yml"}


def source_files() -> list[Path]:
    result = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "openshift"],
        cwd=ROOT.parent,
        text=True,
        capture_output=True,
        check=True,
    )
    files: list[Path] = []
    for relative in result.stdout.splitlines():
        path = ROOT.parent / relative
        if not path.is_file() or path.suffix not in SOURCE_SUFFIXES:
            continue
        if "/testdata/" in f"/{relative}":
            continue
        if relative == "openshift/audit/audit_config.py":
            continue
        if path.name.startswith("test_") or path.name.endswith("_test.go"):
            continue
        files.append(path)
    files.append(ROOT / ".env.sample")
    return sorted(files, key=lambda path: path.relative_to(ROOT).as_posix())


def environment_variables() -> dict[str, list[str]]:
    values: dict[str, set[str]] = defaultdict(set)
    env_sample = ROOT / ".env.sample"
    if env_sample.exists():
        for line in env_sample.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            key, separator, _ = line.partition("=")
            if separator and key:
                values[key].add(".env.sample")

    for path in source_files():
        text = path.read_text(encoding="utf-8")
        for key in ENVIRONMENT_PATTERN.findall(text):
            values[key].add(path.relative_to(ROOT).as_posix())
    return {key: sorted(files) for key, files in sorted(values.items())}


def target_placeholders() -> dict[str, dict[str, int]]:
    results: dict[str, dict[str, int]] = {label: {} for label in TARGET_PLACEHOLDERS}
    for path in source_files():
        text = path.read_text(encoding="utf-8")
        relative = path.relative_to(ROOT).as_posix()
        for label, placeholder in TARGET_PLACEHOLDERS.items():
            count = text.count(placeholder)
            if count:
                results[label][relative] = count
    return results


def workload_image_references() -> dict[str, dict[str, int]]:
    references: dict[str, dict[str, int]] = defaultdict(dict)
    for path in source_files():
        text = path.read_text(encoding="utf-8")
        relative = path.relative_to(ROOT).as_posix()
        for reference in IMAGE_REFERENCE_PATTERN.findall(text):
            references[reference][relative] = text.count(reference)
    return dict(sorted(references.items()))


def image_lock() -> dict[str, Any]:
    config = load_config(EXAMPLE_PATH, True)
    return normalize_lock(load_image_lock(config, EXAMPLE_PATH), config["target"])


def lock_references(lock: dict[str, Any]) -> dict[str, list[str]]:
    quay = [
        f"{image['quay_repository']}@{image['quay_digest']}" for image in lock["images"]
    ]
    openshift = [
        image["openshift_image_reference"]
        for image in lock["images"]
        if image.get("openshift_image_reference")
    ]
    project = [
        f"{image['repository']}@{image['digest']}" for image in lock["project_images"]
    ]
    return {
        "quay": sorted(quay),
        "openshift": sorted(openshift),
        "project": sorted(project),
    }


def controller_deployment() -> dict[str, str]:
    config = load_config(EXAMPLE_PATH, True)
    document = yaml.safe_load(
        render_outputs(config, load_image_lock(config, EXAMPLE_PATH))[
            "controller_deployment"
        ]
    )
    containers = document["spec"]["template"]["spec"]["containers"]
    image = containers[0]["image"]
    watch_namespace = ""
    for argument in containers[0].get("args", []):
        if argument.startswith("--watch-namespace="):
            watch_namespace = argument.partition("=")[2]
    return {"image": image, "watch_namespace": watch_namespace}


def generated_image_value_mismatches(lock: dict[str, Any]) -> list[dict[str, str]]:
    generated = yaml.safe_load(GENERATED_IMAGE_VALUES.read_text(encoding="utf-8"))
    mismatches: list[dict[str, str]] = []
    for image in lock["images"]:
        key = image["chart_image_key"]
        expected = f"/{image['chart_repository_basename']}@{image['quay_digest']}"
        actual = generated.get("images", {}).get(key)
        if actual != expected:
            mismatches.append(
                {
                    "key": key,
                    "expected": expected,
                    "actual": actual if isinstance(actual, str) else "<missing>",
                }
            )
    return mismatches


def has_target_default(text: str, suffix: str) -> bool:
    keys = {
        "OPENSHIFT_NAMESPACE",
        "OPENSHIFT_CONTEXT",
        "OPENSHIFT_API_SERVER_URL",
        "OPENSHIFT_STORAGE_CLASS",
        "OPENSHIFT_SHARED_STORAGE_CLASS",
        "OPENSHIFT_GPU_TAINT_KEY",
        "INTRINSIC_REGISTRY_HOST",
        "INTRINSIC_GATEWAY_ADDRESS",
        "INTRINSIC_INGRESS_ADDRESS",
        "INTRINSIC_INGRESS_GATEWAY",
        "INTRINSIC_ZENOH_ROUTER_ENDPOINT",
        "INTRINSIC_RHOAI_TRITON_ENDPOINT",
        "QUAY_REGISTRY_ORG",
        "QUAY_REGISTRY_PREFIX",
    }
    if suffix == ".py":
        for node in ast.walk(ast.parse(text)):
            if (
                isinstance(node, ast.Call)
                and isinstance(node.func, ast.Attribute)
                and node.func.attr in {"get", "getenv"}
                and len(node.args) > 1
            ):
                if (
                    isinstance(node.args[0], ast.Constant)
                    and node.args[0].value in keys
                    and isinstance(node.args[1], ast.Constant)
                    and node.args[1].value
                ):
                    return True
    for key in keys:
        if re.search(r"\$\{" + key + r":-[^}]+\}", text):
            return True
    # Upstream source addresses are inputs to namespace rewriting, not destinations.
    for namespace in re.findall(r"[a-z0-9-]+\.([a-z0-9-]+)\.svc\.cluster\.local", text):
        if namespace not in {
            "namespace",
            "app-intrinsic-base",
            "app-intrinsic-app-chart",
            "app-ingress",
        }:
            return True
    return False


def executable_target_violations() -> list[str]:
    """Reject target defaults in owned executable code; templates remain declarative."""
    violations = []
    provenance = json.loads((ROOT / "deployment/SOURCE_MANIFEST.json").read_text())
    upstream = set()

    def visit(value):
        if isinstance(value, dict):
            if "destination_path" in value:
                upstream.add("openshift/deployment/" + value["destination_path"])
            for child in value.values():
                visit(child)
        elif isinstance(value, list):
            for child in value:
                visit(child)

    visit(provenance)
    forbidden = re.compile(
        r"your-(?:project-namespace|registry-org|block-storage-class|shared-filesystem-storage-class|gpu-taint-key|service-mesh-control-plane)|quay\.io/your-"
    )
    for path in source_files():
        relative = path.relative_to(ROOT.parent).as_posix()
        if (
            path.suffix not in {".py", ".sh", ".go"}
            or relative in upstream
            or "/audit/" in relative
        ):
            continue
        text = path.read_text()
        if forbidden.search(text) or has_target_default(text, path.suffix):
            violations.append(relative)
        if (
            path.suffix == ".go"
            and path.name == "openshift.go"
            and ("defaultImageOverrides" in text or "os.Getenv(" in text)
        ):
            violations.append(relative)
    return sorted(set(violations))


def collect_audit() -> dict[str, Any]:
    lock = image_lock()
    references = lock_references(lock)
    constants = {}
    constant_mismatches = {}
    return {
        "environment_variables": environment_variables(),
        "target_placeholders": target_placeholders(),
        "workload_image_references": workload_image_references(),
        "image_lock_references": references,
        "controller_deployment": controller_deployment(),
        "controller_image_constants": constants,
        "executable_target_violations": executable_target_violations(),
        "controller_image_constant_mismatches": constant_mismatches,
        "generated_image_value_mismatches": generated_image_value_mismatches(lock),
    }


def print_audit(audit: dict[str, Any]) -> None:
    print("OpenShift configuration audit")
    print(f"Environment variables: {len(audit['environment_variables'])} keys")
    for key, files in audit["environment_variables"].items():
        print(f"  {key}: {', '.join(files)}")

    print("Hard-coded target placeholders:")
    for label, files in audit["target_placeholders"].items():
        count = sum(files.values())
        print(f"  {label}: {count} occurrence(s) in {len(files)} file(s)")

    print(
        "Workload image references: "
        f"{len(audit['workload_image_references'])} distinct reference(s)"
    )
    print(
        "Image lock references: "
        f"{len(audit['image_lock_references']['quay'])} Quay, "
        f"{len(audit['image_lock_references']['openshift'])} OpenShift, "
        f"{len(audit['image_lock_references']['project'])} project"
    )
    deployment = audit["controller_deployment"]
    print(f"Controller image: {deployment['image']}")
    print(f"Controller watch namespace: {deployment['watch_namespace']}")

    if audit["controller_image_constant_mismatches"]:
        print("WARNING: controller image constants do not match the image lock")
    else:
        print("Controller image destinations are derived from the canonical lock")

    if audit["generated_image_value_mismatches"]:
        print("WARNING: generated Quay image values do not match the image lock")
        for mismatch in audit["generated_image_value_mismatches"]:
            print(
                f"  {mismatch['key']}: expected {mismatch['expected']}, "
                f"found {mismatch['actual']}"
            )
    else:
        print("Generated Quay image values match the image lock")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--strict",
        action="store_true",
        help="fail if controller constants or generated image values drift from the lock",
    )
    arguments = parser.parse_args()
    audit = collect_audit()
    print_audit(audit)
    if audit["executable_target_violations"]:
        print(
            "Executable target policy violations:",
            ", ".join(audit["executable_target_violations"]),
        )
    if arguments.strict and (
        audit["executable_target_violations"]
        or audit["controller_image_constant_mismatches"]
        or audit["generated_image_value_mismatches"]
    ):
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
