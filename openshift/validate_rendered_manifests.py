#!/usr/bin/env python3
"""Fail-closed validation for offline-rendered Intrinsic OpenShift manifests."""

from __future__ import annotations

import argparse
import collections
import json
import re
import sys
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parent
IMAGE_LOCK = ROOT / "image-lock.json"
PROJECT_KINDS = {
    "ConfigMap",
    "ServiceAccount",
    "Service",
    "Deployment",
    "StatefulSet",
    "DaemonSet",
    "ReplicaSet",
    "Pod",
    "Job",
    "CronJob",
    "PersistentVolumeClaim",
    "Role",
    "RoleBinding",
    "ServiceMonitor",
    "PodMonitor",
    "NetworkPolicy",
    "VirtualService",
    "ChartAssignment",
    "ResourceSet",
}
LEGACY_NAMESPACES = (
    "app-intrinsic-base",
    "app-intrinsic-app-chart",
    "app-ingress",
)
DIGEST = re.compile(r"@sha256:[0-9a-f]{64}$")


class ValidationError(ValueError):
    """A safe-to-print rendered-manifest validation failure."""


def locked_references(path: Path) -> set[str]:
    try:
        lock = json.loads(path.read_text())
        images = lock["images"]
        prefix = lock["registry_prefix"]
    except (OSError, json.JSONDecodeError, KeyError, TypeError) as exc:
        raise ValidationError("image lock is unreadable or malformed") from exc
    if not isinstance(images, list) or not isinstance(prefix, str):
        raise ValidationError("image lock schema is invalid")
    refs: set[str] = set()
    for image in images:
        if not isinstance(image, dict):
            raise ValidationError("image lock entry is invalid")
        repository = image.get("quay_repository")
        digest = image.get("quay_digest")
        if not isinstance(repository, str) or not repository.startswith(prefix + "/"):
            raise ValidationError("image lock repository is invalid")
        if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValidationError("image lock digest is invalid")
        ref = f"{repository}@{digest}"
        if ref in refs:
            raise ValidationError("duplicate image lock entry")
        refs.add(ref)
    return refs


def walk(value: Any):
    if isinstance(value, dict):
        for key, nested in value.items():
            yield key, nested
            yield from walk(nested)
    elif isinstance(value, list):
        for nested in value:
            yield from walk(nested)


def validate_virtual_service(document: dict[str, Any], namespace: str, name: str) -> None:
    if not str(document.get("apiVersion", "")).startswith("networking.istio.io/"):
        raise ValidationError(f"VirtualService/{name} has an unexpected API group")
    spec = document.get("spec")
    if not isinstance(spec, dict):
        raise ValidationError(f"VirtualService/{name} has no spec")
    gateways = spec.get("gateways")
    if not isinstance(gateways, list) or len(gateways) != 1 or not isinstance(gateways[0], str):
        raise ValidationError(f"VirtualService/{name} must use exactly one configured mesh Gateway")
    gateway_parts = gateways[0].split("/")
    if len(gateway_parts) != 2 or not all(gateway_parts):
        raise ValidationError(f"VirtualService/{name} Gateway reference is not namespaced")
    exported = spec.get("exportTo")
    if not isinstance(exported, list) or "." not in exported or gateway_parts[0] not in exported:
        raise ValidationError(f"VirtualService/{name} is not scoped to its project and mesh Gateway")
    routes = spec.get("http")
    if not isinstance(routes, list):
        raise ValidationError(f"VirtualService/{name} HTTP routes are missing")

    def check_destinations(value: Any) -> None:
        if isinstance(value, dict):
            for key, nested in value.items():
                if key in {"destination", "mirror"}:
                    if not isinstance(nested, dict) or not isinstance(nested.get("host"), str):
                        raise ValidationError(f"VirtualService/{name} contains a malformed destination")
                    host = nested["host"]
                    if "." in host and not host.endswith(f".{namespace}.svc.cluster.local"):
                        raise ValidationError(f"VirtualService/{name} destination is outside the project")
                    if any(character in host for character in ":/@ \t\n"):
                        raise ValidationError(f"VirtualService/{name} destination is not a Service name")
                check_destinations(nested)
        elif isinstance(value, list):
            for nested in value:
                check_destinations(nested)

    check_destinations(routes)


def validate_documents(documents: list[dict[str, Any]], namespace: str, locked: set[str]) -> tuple[collections.Counter[str], int]:
    kinds: collections.Counter[str] = collections.Counter()
    seen: set[tuple[str, str, str]] = set()
    image_count = 0
    for document in documents:
        if not isinstance(document, dict):
            raise ValidationError("manifest document is not an object")
        kind = document.get("kind")
        metadata = document.get("metadata")
        if not isinstance(kind, str) or not isinstance(metadata, dict):
            raise ValidationError("manifest kind or metadata is missing")
        name = metadata.get("name")
        if not isinstance(name, str) or not name:
            raise ValidationError("manifest name is missing")
        if kind not in PROJECT_KINDS:
            raise ValidationError(f"unapproved or cluster-scoped kind {kind}/{name}")
        if metadata.get("namespace") != namespace:
            raise ValidationError(f"{kind}/{name} is outside the selected project")
        identity = (document.get("apiVersion", ""), kind, name)
        if identity in seen:
            raise ValidationError(f"duplicate object {kind}/{name}")
        seen.add(identity)
        kinds[kind] += 1

        if kind == "VirtualService":
            validate_virtual_service(document, namespace, name)

        for key, value in walk(document):
            if key in {"hostPath", "hostPort", "runAsUser", "runAsGroup"}:
                raise ValidationError(f"host access or fixed UID remains in {kind}/{name}")
            if key == "privileged" and value is True:
                raise ValidationError(f"privileged container remains in {kind}/{name}")
            if isinstance(value, str) and any(old in value for old in LEGACY_NAMESPACES):
                raise ValidationError(f"legacy namespace reference remains in {kind}/{name}")
            if key == "image":
                image_count += 1
                if not isinstance(value, str) or not DIGEST.search(value) or value not in locked:
                    raise ValidationError(f"container image is not digest-locked in {kind}/{name}")
    if not documents:
        raise ValidationError("no manifest objects were rendered")
    if image_count == 0:
        raise ValidationError("rendered manifests contain no locked workload images")
    return kinds, image_count


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifests", nargs="+", type=Path, help="private files emitted by render-openshift-chart")
    parser.add_argument("--namespace", default="arhkp-intrinsic")
    parser.add_argument("--image-lock", type=Path, default=IMAGE_LOCK)
    args = parser.parse_args()

    try:
        locked = locked_references(args.image_lock)
        documents: list[dict[str, Any]] = []
        for path in args.manifests:
            try:
                parsed = list(yaml.safe_load_all(path.read_text()))
            except (OSError, yaml.YAMLError) as exc:
                raise ValidationError(f"cannot parse manifest file {path.name}") from exc
            documents.extend(document for document in parsed if document is not None)
        kinds, image_count = validate_documents(documents, args.namespace, locked)
    except ValidationError as exc:
        print(f"Validation failed: {exc}", file=sys.stderr)
        return 1

    print(f"Validated {sum(kinds.values())} objects in {args.namespace}; {image_count} image references match the digest lock.")
    print("Kinds:", ", ".join(f"{kind}={count}" for kind, count in sorted(kinds.items())))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
