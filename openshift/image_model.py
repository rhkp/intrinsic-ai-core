# This project was developed with assistance from AI tools.
"""Compose image destinations from one target and the portable digest lock."""

from __future__ import annotations

import copy
import re

DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
NAME = re.compile(r"[a-z0-9][a-z0-9._-]*")


def normalize_lock(lock: dict, target: dict) -> dict:
    """Return a derived view; full destination references never live in the lock."""
    if lock.get("schema_version") != 2 or "registry_prefix" in lock:
        raise ValueError("image lock must use portable schema version 2")
    result = copy.deepcopy(lock)
    registry = target["registry"]
    result["registry_prefix"] = registry["quay_prefix"]
    project = f"{registry['internal_host']}/{target['namespace']}"
    tokens = {
        "{namespace}": target["namespace"],
        "{internal_host}": registry["internal_host"],
        "{quay_prefix}": registry["quay_prefix"],
    }
    for collection, key_field, digest_field in (
        ("images", "chart_image_key", "quay_digest"),
        ("project_images", "name", "digest"),
    ):
        entries = result.get(collection)
        if not isinstance(entries, list):
            raise ValueError("image lock collections must be lists")
        seen = set()
        for image in entries:
            if not isinstance(image, dict):
                raise ValueError("image lock entry must be an object")
            if set(image) & {
                "repository",
                "quay_repository",
                "openshift_image_reference",
            }:
                raise ValueError("image lock contains a repeated destination reference")
            key = image.get(key_field, "")
            basename = image.get("chart_repository_basename", key)
            if not NAME.fullmatch(key) or not NAME.fullmatch(basename) or key in seen:
                raise ValueError("image lock key or basename is invalid or duplicated")
            seen.add(key)
            if not DIGEST.fullmatch(image.get(digest_field, "")):
                raise ValueError("image lock digest is invalid")
            if collection == "images":
                image["quay_repository"] = f"{registry['quay_prefix']}/{basename}"
            else:
                image["repository"] = f"{project}/{key}"
            override = image.get("openshift_image")
            if override is not None:
                if (
                    not isinstance(override, dict)
                    or set(override) != {"name", "digest"}
                    or not NAME.fullmatch(override.get("name", ""))
                    or not DIGEST.fullmatch(override.get("digest", ""))
                ):
                    raise ValueError("image lock OpenShift destination is invalid")
                image["openshift_image_reference"] = (
                    f"{project}/{override['name']}@{override['digest']}"
                )
            for field in ("source_image", "chart_source_image"):
                if field in image:
                    for token, value in tokens.items():
                        image[field] = image[field].replace(token, value)
                    if "{" in image[field] or "}" in image[field]:
                        raise ValueError(
                            "image source contains an unknown template token"
                        )
            if image.get("controller_override") and not (
                image.get("chart_source_image") or image.get("source_image")
            ):
                raise ValueError("controller override has no source image")
    return result
