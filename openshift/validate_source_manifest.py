#!/usr/bin/env python3
"""Verify the pinned upstream-copy and adaptation SHA-256 inventories."""

from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEPLOYMENT = ROOT / "openshift/deployment"
DIGEST_RE = re.compile(r"[0-9a-f]{64}")


class ValidationError(Exception):
    pass


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def checked_path(base: Path, relative: object) -> Path:
    if not isinstance(relative, str) or not relative:
        raise ValidationError("manifest path is missing")
    path = (base / relative).resolve()
    try:
        path.relative_to(base.resolve())
    except ValueError as exc:
        raise ValidationError("manifest path escapes the repository") from exc
    return path


def verify_file(path: Path, expected: object, label: str, base: Path) -> None:
    try:
        relative = path.relative_to(base).as_posix()
    except ValueError:
        relative = "<outside-repository>"
    if not isinstance(expected, str) or not DIGEST_RE.fullmatch(expected):
        raise ValidationError(f"{label}: malformed expected digest for {relative}")
    try:
        actual = sha256(path)
    except OSError as exc:
        raise ValidationError(f"{label}: missing file {relative}") from exc
    if actual != expected:
        raise ValidationError(f"{label}: digest mismatch for {relative}")


def verify() -> tuple[int, int]:
    try:
        source_manifest = json.loads((DEPLOYMENT / "SOURCE_MANIFEST.json").read_text())
        adaptation_manifest = json.loads((DEPLOYMENT / "ADAPTATIONS.json").read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise ValidationError("could not read source/adaptation manifests") from exc

    source_files = source_manifest.get("files")
    adaptations = adaptation_manifest.get("adaptations")
    if not isinstance(source_files, list) or not isinstance(adaptations, list):
        raise ValidationError("manifest file/adaptation lists are malformed")

    for item in source_files:
        if not isinstance(item, dict):
            raise ValidationError("source manifest contains a malformed entry")
        path = checked_path(DEPLOYMENT, item.get("destination_path"))
        verify_file(path, item.get("current_sha256"), "source copy", DEPLOYMENT)

    verified_adaptations = 0
    for adaptation in adaptations:
        if not isinstance(adaptation, dict):
            raise ValidationError("adaptation manifest contains a malformed entry")
        patch_path = adaptation.get("patch_path")
        if patch_path:
            path = checked_path(ROOT, patch_path)
            verify_file(path, adaptation.get("patch_sha256"), "adaptation patch", ROOT)
            verified_adaptations += 1
        for field, digest_key in (("changed_upstream_files", "current_sha256"), ("added_adaptation_files", "sha256")):
            entries = adaptation.get(field, [])
            if not isinstance(entries, list):
                raise ValidationError(f"{field} must be a list")
            for item in entries:
                if not isinstance(item, dict):
                    raise ValidationError(f"{field} contains a malformed entry")
                path = checked_path(ROOT, item.get("path"))
                verify_file(path, item.get(digest_key), "adaptation", ROOT)
                verified_adaptations += 1

    return len(source_files), verified_adaptations


if __name__ == "__main__":
    try:
        source_count, adaptation_count = verify()
    except ValidationError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
    print(f"OK: {source_count} upstream copies and {adaptation_count} adaptation files verified.")
