#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Render an operational manifest using validated canonical settings."""

import argparse
import os
from pathlib import Path

import yaml

from render_config import (
    DEFAULT_CONFIG_PATH,
    ConfigError,
    load_config,
    load_image_lock,
    render_template,
    validate_config,
)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("template", type=Path)
    parser.add_argument(
        "--config",
        type=Path,
        default=Path(os.environ.get("INTRINSIC_CONFIG", DEFAULT_CONFIG_PATH)),
    )
    parser.add_argument("--exclude-pvc", action="store_true")
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        lock = load_image_lock(config, args.config)
        validate_config(config, lock)
        text = render_template(args.template.read_text(), config, lock)
        if args.exclude_pvc:
            text = yaml.safe_dump_all(
                doc
                for doc in yaml.safe_load_all(text)
                if doc and doc.get("kind") != "PersistentVolumeClaim"
            )
    except (ConfigError, ValueError, OSError):
        parser.exit(2, "Canonical configuration or template is invalid.\n")
    print(text, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
