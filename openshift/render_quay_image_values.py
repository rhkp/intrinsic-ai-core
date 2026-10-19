#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Render immutable Helm image values from the Quay image lock."""

from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def render_values(lock: dict) -> str:
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

    lines = [
        "# This project was developed with assistance from AI tools.",
        f"registry: {json.dumps(registry)}",
        "images:",
    ]
    lines.extend(f"  {key}: {json.dumps(values[key])}" for key in sorted(values))
    return "\n".join(lines) + "\n"


def main() -> None:
    import argparse
    from render_config import (
        DEFAULT_CONFIG_PATH,
        ConfigError,
        load_config,
        load_image_lock,
        validate_config,
    )
    from image_model import normalize_lock

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument(
        "--output",
        type=Path,
        default=ROOT / ".generated/deployment/quay-image-values.yaml",
    )
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        lock = load_image_lock(config, args.config)
        validate_config(config, lock)
        rendered = render_values(normalize_lock(lock, config["target"]))
    except (ConfigError, ValueError, OSError):
        parser.exit(2, "Canonical configuration or image lock is invalid.\n")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(rendered)
    args.output.chmod(0o600)
    print("Rendered canonical digest-pinned image values.")


if __name__ == "__main__":
    main()
