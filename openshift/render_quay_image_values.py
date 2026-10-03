#!/usr/bin/env python3
"""Render immutable Helm image values from the Quay image lock."""

from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent
LOCK = ROOT / "image-lock.json"
OUTPUT = ROOT / "deployment" / "quay-image-values.yaml"


def main() -> None:
    lock = json.loads(LOCK.read_text())
    registry = lock["registry_prefix"]
    images = lock["images"]
    values: dict[str, str] = {}
    for image in images:
        key = image["chart_image_key"]
        basename = image["chart_repository_basename"]
        digest = image["quay_digest"]
        if not re.fullmatch(r"[a-z0-9_]+", key):
            raise ValueError(f"invalid chart image key: {key}")
        if image["quay_repository"] != f"{registry}/{basename}":
            raise ValueError(f"repository does not match lock prefix for {key}")
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError(f"invalid Quay digest for {key}")
        if key in values:
            raise ValueError(f"duplicate chart image key: {key}")
        values[key] = f"/{basename}@{digest}"

    lines = [f"registry: {json.dumps(registry)}", "images:"]
    lines.extend(
        f"  {key}: {json.dumps(values[key])}" for key in sorted(values)
    )
    OUTPUT.write_text("\n".join(lines) + "\n")
    print(f"Rendered {len(values)} digest-pinned image values to {OUTPUT.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
